package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/devlocal"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type testProvider struct{ subject, state, nonce string }

func (p *testProvider) Authorize(state, nonce, verifier string) string {
	p.state = state
	p.nonce = nonce
	return "https://idp.example/auth"
}
func (p *testProvider) Exchange(context.Context, string, string) (identity.VerifiedIdentity, error) {
	return identity.VerifiedIdentity{Issuer: "https://idp.example", Subject: p.subject, Nonce: p.nonce}, nil
}
func problemIs(err error, code string) bool {
	var p *webapi.Problem
	return errors.As(err, &p) && p.Code == code
}

func TestMCPConnectionsWithPostgres(t *testing.T) {
	filename := os.Getenv("HC_TEST_DB_DSN_FILE")
	if filename == "" {
		t.Skip("isolated PostgreSQL required")
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal("cannot read connection file")
	}
	u, err := url.Parse(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal("invalid connection file")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	u.Path = "/postgres"
	admin, err := pgx.Connect(ctx, u.String())
	if err != nil {
		t.Fatal("database administrator unavailable")
	}
	database := "hc_test_mcp_" + strings.ReplaceAll(webapi.ID(), "-", "")
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{database}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	var root, app *pgxpool.Pool
	defer func() {
		if app != nil {
			app.Close()
		}
		if root != nil {
			root.Close()
		}
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, _ = admin.Exec(cleanup, "DROP DATABASE "+pgx.Identifier{database}.Sanitize())
		_ = admin.Close(cleanup)
	}()
	u.Path = "/" + database
	if err = db.Apply(ctx, u.String()); err != nil {
		t.Fatal(err)
	}
	root, err = pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	tx, err := root.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = devlocal.GrantAPI(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(u.String())
	if err != nil {
		t.Fatal(err)
	}
	config.AfterConnect = func(ctx context.Context, c *pgx.Conn) error { _, e := c.Exec(ctx, "SET ROLE hc_api"); return e }
	app, err = pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	org := webapi.ID()
	if err = identity.ApplyMapping(ctx, root, identity.Mapping{SchemaVersion: 1, OrganizationID: org, Members: []identity.MemberMapping{{Issuer: "https://idp.example", Subject: "owner", Role: "MEMBER", WorkspaceOwnerID: webapi.ID()}, {Issuer: "https://idp.example", Subject: "admin", Role: "ADMIN", WorkspaceOwnerID: webapi.ID()}}}, "skills-test"); err != nil {
		t.Fatal(err)
	}
	cipher, _ := security.NewSecretCipher("test-key", bytes.Repeat([]byte{7}, 32))
	s := NewService(app, cipher, "test", false, "")
	login := func(subject string) identity.Principal {
		t.Helper()
		provider := &testProvider{subject: subject}
		auth := identity.NewService(app, provider, cipher, "test", org, "https://portal.example")
		l, e := auth.Login(ctx, "", "/app/skills")
		if e != nil {
			t.Fatal(e)
		}
		result, e := auth.Callback(ctx, provider.state, l.BrowserSecret, provider.state, "")
		if e != nil {
			t.Fatal(e)
		}
		p, e := auth.Authenticate(ctx, result.SessionSecret)
		if e != nil {
			t.Fatal(e)
		}
		return p
	}

	owner, other := login("owner"), login("admin")

	for _, principal := range []identity.Principal{owner, other} {
		for _, permission := range []string{"connectors:read", "connectors:manage", "connectors:invoke"} {
			if !principal.Has(permission) {
				t.Fatal("default permission missing", permission)
			}
		}
	}
	remote, endpoint, remoteCalls := testMCP(t)
	endpoint.CatalogID = "notion"
	s.policy = func() (Policy, error) { return Policy{Endpoints: []Endpoint{endpoint}}, nil }
	listed, err := s.List(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	list := listed.(map[string]any)
	if len(list["catalog"].(Catalog).Entries) != 29 || list["endpoints"].([]Endpoint)[0].CatalogID != "notion" {
		t.Fatal("catalog metadata or approved binding missing")
	}
	// Each account can configure a public service without a platform binding.
	public := Input{Name: "个人远程服务", URL: "https://mcp.example.com/mcp", Protocol: "2026-07-28", Auth: "bearer", Credential: "owner-only-secret", Enabled: true}
	personal, err := s.Save(ctx, owner, "", public, "")
	if err != nil || personal.EndpointID != "personal" || personal.URL != public.URL {
		t.Fatal("personal HTTPS endpoint rejected", err)
	}
	public.Credential = "other-owner-secret"
	otherPersonal, err := s.Save(ctx, other, "", public, "")
	if err != nil || otherPersonal.ID == personal.ID {
		t.Fatal("independent personal endpoint rejected", err)
	}
	otherList, err := s.List(ctx, other)
	if err != nil || len(otherList.(map[string]any)["items"].([]Connection)) != 1 {
		t.Fatal("other owner list isolation", err)
	}
	public.ExpectedVersion = personal.Version
	public.URL, public.Credential = "https://other.example.com/mcp", ""
	if _, err = s.Save(ctx, owner, personal.ID, public, ""); !problemIs(err, "CONNECTOR_INVALID") {
		t.Fatal("credential reused for another address", err)
	}
	if _, err = s.Save(ctx, other, personal.ID, public, ""); !problemIs(err, "NOT_FOUND") {
		t.Fatal("cross-owner configuration allowed", err)
	}
	if err = s.Delete(ctx, other, personal.ID, Input{ExpectedVersion: personal.Version, ConfirmedName: personal.Name}, ""); !problemIs(err, "NOT_FOUND") {
		t.Fatal("cross-owner deletion allowed", err)
	}
	public.URL, public.Credential = personal.URL, ""
	personal, err = s.Save(ctx, owner, personal.ID, public, "")
	if err != nil {
		t.Fatal("unchanged address cannot preserve credential", err)
	}
	decrypted, err := cipher.Decrypt(personal.envelope, s.aad(owner, personal.ID))
	if err != nil || string(decrypted) != "owner-only-secret" {
		t.Fatal("owner credential changed")
	}
	clear(decrypted)
	if err = s.Delete(ctx, owner, personal.ID, Input{ExpectedVersion: personal.Version, ConfirmedName: personal.Name}, ""); err != nil {
		t.Fatal(err)
	}
	// The exact platform-approved local fixture is also usable as a personal URL.
	input := Input{Name: "测试连接器", URL: endpoint.URL, CatalogID: "notion", Protocol: "2026-07-28", Auth: "bearer", Credential: "private-test-token", Enabled: true}
	conn, err := s.Save(ctx, owner, "", input, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Sync(ctx, other, conn.ID, conn.Version, ""); !problemIs(err, "NOT_FOUND") {
		t.Fatal("cross-owner admin access", err)
	}
	conn, err = s.Sync(ctx, owner, conn.ID, conn.Version, "")
	if err != nil || conn.Status != "READY" || len(conn.Tools) != 1 || conn.Tools[0].Enabled || remoteCalls.Load() != 0 {
		t.Fatalf("discover: %#v %v", conn, err)
	}
	invoke := InvokeInput{ExpectedVersion: conn.Version, CatalogVersion: conn.CatalogVersion, Tool: "echo", Arguments: map[string]any{"text": "私有参数不要存库"}, IdempotencyKey: webapi.ID(), Confirmed: true}
	if _, err = s.Invoke(ctx, owner, conn.ID, invoke, ""); !problemIs(err, "CONNECTOR_INVALID") {
		t.Fatal("disabled tool callable", err)
	}
	conn, err = s.SelectTools(ctx, owner, conn.ID, conn.Version, []string{"echo"}, "")
	if err != nil {
		t.Fatal(err)
	}
	invoke.ExpectedVersion = conn.Version
	first, err := s.Invoke(ctx, owner, conn.ID, invoke, "")
	if err != nil || first.Status != "SUCCEEDED" || first.Result == nil || remoteCalls.Load() != 1 {
		t.Fatalf("invoke: %#v %v count=%d", first, err, remoteCalls.Load())
	}
	replay, err := s.Invoke(ctx, owner, conn.ID, invoke, "")
	if err != nil || !replay.Replayed || replay.ID != first.ID || remoteCalls.Load() != 1 || replay.Result != nil {
		t.Fatal("replayed a side effect", replay, err)
	}
	invoke.Arguments["text"] = "different"
	if _, err = s.Invoke(ctx, owner, conn.ID, invoke, ""); !problemIs(err, "IDEMPOTENCY_CONFLICT") {
		t.Fatal("idempotency mismatch", err)
	}
	invoke.IdempotencyKey = webapi.ID()
	invoke.Arguments = map[string]any{"text": 12}
	if _, err = s.Invoke(ctx, owner, conn.ID, invoke, ""); !problemIs(err, "CONNECTOR_INVALID") {
		t.Fatal("bad schema accepted", err)
	}
	if _, err = s.GetCall(ctx, other, first.ID); !problemIs(err, "NOT_FOUND") {
		t.Fatal("cross-owner call leak", err)
	}
	var body string
	if err = root.QueryRow(ctx, `SELECT row_to_json(c)::text FROM mcp_calls c WHERE id=$1`, first.ID).Scan(&body); err != nil || strings.Contains(body, "私有参数") || strings.Contains(body, "private-test-token") {
		t.Fatal("sensitive call data stored", err)
	}
	var ciphertext []byte
	if err = root.QueryRow(ctx, `SELECT ciphertext FROM mcp_connectors WHERE id=$1`, conn.ID).Scan(&ciphertext); err != nil || bytes.Contains(ciphertext, []byte("private-test-token")) {
		t.Fatal("plaintext credential", err)
	}
	// Changes observed just before dispatch reject the invocation. A later sync
	// publishes the new definition with its previous authorization removed.
	remote.AddTool(&mcp.Tool{Name: "echo", Description: "Changed definition", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		remoteCalls.Add(1)
		return &mcp.CallToolResult{}, nil
	})
	invoke.Arguments = map[string]any{"text": "changed"}
	invoke.IdempotencyKey = webapi.ID()
	rejected, err := s.Invoke(ctx, owner, conn.ID, invoke, "")
	if err != nil || rejected.Status != "REJECTED" || remoteCalls.Load() != 1 {
		t.Fatal("changed definition dispatched", rejected, err)
	}
	conn, err = s.Sync(ctx, owner, conn.ID, conn.Version, "")
	if err != nil || conn.Tools[0].Enabled || conn.Tools[0].Change != "changed" {
		t.Fatal("definition change retained grant", conn, err)
	}
	// Timeout happens after a synthetic side effect; retries must only read status.
	entered := make(chan struct{}, 1)
	remote.AddTool(&mcp.Tool{Name: "slow", Description: "Synthetic external write", InputSchema: map[string]any{"type": "object"}}, func(callCtx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		remoteCalls.Add(1)
		entered <- struct{}{}
		select {
		case <-callCtx.Done():
		case <-time.After(2 * time.Second):
		}
		return &mcp.CallToolResult{}, nil
	})
	conn, err = s.Sync(ctx, owner, conn.ID, conn.Version, "")
	if err != nil {
		t.Fatal(err)
	}
	conn, err = s.SelectTools(ctx, owner, conn.ID, conn.Version, []string{"slow"}, "")
	if err != nil {
		t.Fatal(err)
	}
	invoke = InvokeInput{ExpectedVersion: conn.Version, CatalogVersion: conn.CatalogVersion, Tool: "slow", Arguments: map[string]any{}, IdempotencyKey: webapi.ID(), Confirmed: true, Timeout: 1}
	if _, err = s.Invoke(ctx, owner, conn.ID, invoke, ""); !problemIs(err, "CONNECTOR_INVALID") {
		t.Fatal("risk confirmation missing", err)
	}
	invoke.RiskConfirmed = true
	unknown, err := s.Invoke(ctx, owner, conn.ID, invoke, "")
	if err != nil || unknown.Status != "UNKNOWN" {
		t.Fatal("timeout not unknown", unknown, err)
	}
	count := remoteCalls.Load()
	if _, err = s.Invoke(ctx, owner, conn.ID, invoke, ""); err != nil || remoteCalls.Load() != count {
		t.Fatal("timeout retried", err)
	}
	<-entered
	invoke.Timeout = 20
	invoke.IdempotencyKey = webapi.ID()
	finished := make(chan Call, 1)
	failures := make(chan error, 1)
	go func() { result, e := s.Invoke(ctx, owner, conn.ID, invoke, ""); finished <- result; failures <- e }()
	<-entered
	invoke.IdempotencyKey = webapi.ID()
	if _, err = s.Invoke(ctx, owner, conn.ID, invoke, ""); !problemIs(err, "CONNECTOR_BUSY") {
		t.Fatal("same connection concurrent calls", err)
	}
	var running string
	if err = root.QueryRow(ctx, `SELECT id::text FROM mcp_calls WHERE connector_id=$1 AND status='RUNNING' ORDER BY created_at DESC LIMIT 1`, conn.ID).Scan(&running); err != nil {
		t.Fatal(err)
	}
	if err = s.Cancel(ctx, owner, running, ""); err != nil {
		t.Fatal(err)
	}
	cancelled := <-finished
	if e := <-failures; e != nil || cancelled.Status != "UNKNOWN" {
		t.Fatal("cancel claimed rollback", cancelled, e)
	}
	// Business failures are distinct from transport uncertainty.
	remote.AddTool(&mcp.Tool{Name: "fail", InputSchema: map[string]any{"type": "object"}, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: ptr(false)}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "synthetic business failure"}}}, nil
	})
	conn, err = s.Sync(ctx, owner, conn.ID, conn.Version, "")
	if err != nil {
		t.Fatal(err)
	}
	conn, err = s.SelectTools(ctx, owner, conn.ID, conn.Version, []string{"fail"}, "")
	if err != nil {
		t.Fatal(err)
	}
	invoke = InvokeInput{ExpectedVersion: conn.Version, CatalogVersion: conn.CatalogVersion, Tool: "fail", Arguments: map[string]any{}, IdempotencyKey: webapi.ID(), Confirmed: true}
	failed, err := s.Invoke(ctx, owner, conn.ID, invoke, "")
	if err != nil || failed.Status != "TOOL_ERROR" || failed.Result == nil || !failed.Result.IsError {
		t.Fatal("business failure misreported", failed, err)
	}
	pkg, err := s.Export(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(pkg)
	if strings.Contains(string(raw), "private-test-token") {
		t.Fatal("export leaked token")
	}
	pkg.Connections[0].Name = "导入测试"
	raw, _ = json.Marshal(pkg)
	preview, err := s.Import(ctx, owner, raw, false, "")
	if err != nil || len(preview.Items) != 1 || preview.Imported != 0 {
		t.Fatal(preview, err)
	}
	saved, err := s.Import(ctx, owner, raw, true, "")
	if err != nil || saved.Imported != 1 {
		t.Fatal(saved, err)
	}
	saved, err = s.Import(ctx, owner, raw, true, "")
	if err != nil || saved.Imported != 0 || !saved.Items[0].Conflict {
		t.Fatal("same name import replaced", saved, err)
	}
	publicPackage, _ := json.Marshal(Package{Format: "hc-mcp-v1", Connections: []Config{{Name: "公开服务导入", URL: "https://mcp.example.com/public", Protocol: "2026-07-28", Auth: "none"}}})
	if preview, err = s.Import(ctx, owner, publicPackage, false, ""); err != nil || preview.Imported != 0 || preview.Items[0].EndpointID != "personal" {
		t.Fatal("public URL import preview rejected", err)
	}
	if saved, err = s.Import(ctx, owner, publicPackage, true, ""); err != nil || saved.Imported != 1 {
		t.Fatal("public URL import rejected", err)
	}
	t.Run("OAuth authorization, isolation and refresh", func(t *testing.T) { testOAuthFlow(t, ctx, s, root, owner, other, endpoint) })
	// Withdrawing the local exception blocks personal URLs too.
	s.policy = func() (Policy, error) { return Policy{Endpoints: []Endpoint{}}, nil }
	if _, err = s.Sync(ctx, owner, conn.ID, conn.Version, ""); !problemIs(err, "CONNECTOR_INVALID") {
		t.Fatal("private URL exception revocation ignored", err)
	}
	s.policy = func() (Policy, error) { return Policy{Endpoints: []Endpoint{endpoint}}, nil }
	// A stale Principal cannot continue invoking after role revocation.
	_, err = root.Exec(ctx, `DELETE FROM role_permissions WHERE role_id=(SELECT id FROM roles WHERE org_id=$1 AND code='MEMBER') AND permission_id='connectors:invoke'`, org)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Invoke(ctx, owner, conn.ID, invoke, ""); !errors.Is(err, identity.ErrForbidden) {
		t.Fatal("revoked permission accepted", err)
	}
	if conn, err = s.SelectTools(ctx, owner, conn.ID, conn.Version, []string{}, ""); err != nil {
		t.Fatal("manage wrongly depends on invoke", err)
	}

	// Configuration remains manageable without invoke, and invalid credentials
	// produce a safe actionable status rather than leaking the upstream response.
	input.ExpectedVersion = conn.Version
	input.Credential = "wrong-test-credential"
	conn, err = s.Save(ctx, owner, conn.ID, input, "")
	if err != nil {
		t.Fatal(err)
	}
	conn, err = s.Sync(ctx, owner, conn.ID, conn.Version, "")
	if err != nil || conn.Status != "AUTH_REQUIRED" || !strings.Contains(string(conn.Server), "verification_error") || strings.Contains(string(conn.Server), "wrong-test-credential") {
		t.Fatal("unsafe authentication feedback", conn.Status, err)
	}
	input.ExpectedVersion = conn.Version
	input.Enabled = false
	conn, err = s.Save(ctx, owner, conn.ID, input, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Sync(ctx, owner, conn.ID, conn.Version, ""); !problemIs(err, "CONNECTOR_INVALID") {
		t.Fatal("disabled connection accepted", err)
	}
	if err = s.Delete(ctx, owner, conn.ID, Input{ExpectedVersion: conn.Version, ConfirmedName: conn.Name}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Sync(ctx, owner, conn.ID, conn.Version, ""); !problemIs(err, "NOT_FOUND") {
		t.Fatal("deleted connection accessible", err)
	}
	if _, err = s.GetCall(ctx, owner, first.ID); err != nil {
		t.Fatal("deletion removed durable call status", err)
	}
	var erased security.Envelope
	if err = root.QueryRow(ctx, `SELECT key_id,nonce,ciphertext FROM mcp_connectors WHERE id=$1`, conn.ID).Scan(&erased.KeyID, &erased.Nonce, &erased.Ciphertext); err != nil {
		t.Fatal(err)
	}
	remaining, err := cipher.Decrypt(erased, s.aad(owner, conn.ID))
	if err != nil || string(remaining) != "-" {
		t.Fatal("deleted credential retained", err)
	}

}
