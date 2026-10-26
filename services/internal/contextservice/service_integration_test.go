package contextservice

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/devlocal"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
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

func TestContextConnectionsWithPostgres(t *testing.T) {
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
	database := "hc_test_context_" + strings.ReplaceAll(webapi.ID(), "-", "")
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
	for _, p := range []identity.Principal{owner, other} {
		for _, permission := range []string{"memory:read", "memory:manage", "knowledge:read", "knowledge:manage"} {
			if !p.Has(permission) {
				t.Fatal("built-in role is missing personal workbench permission", permission)
			}
		}
	}

	key := []byte("platform-transport-key-private-value-32")
	keyFile := filepath.Join(t.TempDir(), "proxy-key")
	if err = os.WriteFile(keyFile, key, 0600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	fail := false
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mac := hmac.New(sha256.New, key)
		mac.Write([]byte(r.Header.Get("X-Context-Timestamp") + "\n" + r.URL.EscapedPath() + "\n"))
		mac.Write(body)
		if r.Header.Get("X-Context-Signature") != hex.EncodeToString(mac.Sum(nil)) || r.Header.Get("Authorization") != "Bearer remote-secret-value" {
			t.Error("missing boundary authentication")
			w.WriteHeader(401)
			return
		}
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		if len(req["namespace"].(string)) != 64 {
			t.Error("unscoped external call")
		}
		calls.Add(1)
		if fail {
			w.WriteHeader(503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/verify") {
			io.WriteString(w, `{"contract":"hc-context-v1","backend":"mem0","ready":true,"capabilities":{"search":true}}`)
		} else {
			io.WriteString(w, `{"items":[],"receipt":"private-body-token"}`)
		}
	}))
	defer remote.Close()
	approved := true
	s.policy = func() (Policy, error) {
		if !approved {
			return Policy{Endpoints: []Endpoint{}}, nil
		}
		return Policy{Endpoints: []Endpoint{{ID: "test", Name: "test", Backend: "mem0", URL: remote.URL, AllowPrivateHTTP: true, DataUse: "local test", ProxyKeyFile: keyFile}}}, nil
	}
	v, err := s.Save(ctx, owner, "memory", "", Input{Name: "个人记忆", EndpointID: "test", Credential: "remote-secret-value"}, "")
	if err != nil {
		t.Fatal(err)
	}
	var stored []byte
	if err = root.QueryRow(ctx, "SELECT ciphertext FROM context_connections WHERE id=$1", v.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte("remote-secret-value")) {
		t.Fatal("plaintext credential")
	}
	if _, err = s.Save(ctx, owner, "memory", v.ID, Input{Name: v.Name, EndpointID: "test", ExpectedVersion: 99}, ""); !problemIs(err, "VERSION_CONFLICT") {
		t.Fatal("stale version accepted", err)
	}
	if _, err = s.Call(ctx, other, "memory", v.ID, "verify", Action{ExpectedVersion: v.Version}, ""); !problemIs(err, "NOT_FOUND") {
		t.Fatal("cross-owner admin access", err)
	}
	if _, err = s.Call(ctx, owner, "memory", v.ID, "verify", Action{ExpectedVersion: v.Version}, ""); err != nil {
		t.Fatal(err)
	}
	in := Action{ExpectedVersion: v.Version, Confirmed: true, IdempotencyKey: "first-delete", RecordID: "example"}
	if _, err = s.Call(ctx, owner, "memory", v.ID, "delete", in, ""); err != nil {
		t.Fatal(err)
	}
	before := calls.Load()
	if _, err = s.Call(ctx, owner, "memory", v.ID, "delete", in, ""); err != nil || calls.Load() != before {
		t.Fatal("idempotency replay forwarded", err)
	}
	in.RecordID = "changed"
	if _, err = s.Call(ctx, owner, "memory", v.ID, "delete", in, ""); !problemIs(err, "VERSION_CONFLICT") {
		t.Fatal("idempotency key reused for different input", err)
	}
	fail = true
	in.IdempotencyKey = "timed-out"
	if _, err = s.Call(ctx, owner, "memory", v.ID, "delete", in, ""); !problemIs(err, "CONTEXT_PENDING") {
		t.Fatal(err)
	}
	if _, err = s.Call(ctx, owner, "memory", v.ID, "search", Action{ExpectedVersion: 1, Query: "secret"}, ""); !problemIs(err, "CONTEXT_PENDING") {
		t.Fatal("unknown deletion still searchable", err)
	}
	fail = false
	if _, err = s.Reconcile(ctx, owner, "memory", v.ID, ""); err != nil {
		t.Fatal(err)
	}
	fail = true
	applyInput := Action{ExpectedVersion: v.Version, Confirmed: true, IdempotencyKey: "unknown-apply", Token: "preview-token", Mode: "skip"}
	if _, err = s.Call(ctx, owner, "memory", v.ID, "apply", applyInput, ""); !problemIs(err, "CONTEXT_PENDING") {
		t.Fatal("unknown indexing result", err)
	}
	fail = false
	if _, err = s.Call(ctx, owner, "memory", v.ID, "search", Action{ExpectedVersion: v.Version, Query: "previous usable version"}, ""); err != nil {
		t.Fatal("indexing failure blocked old usable content", err)
	}
	if _, err = s.Reconcile(ctx, owner, "memory", v.ID, ""); err != nil {
		t.Fatal(err)
	}
	approved = false
	if _, err = s.Call(ctx, owner, "memory", v.ID, "search", Action{ExpectedVersion: 1, Query: "secret"}, ""); !problemIs(err, "ENDPOINT_NOT_APPROVED") {
		t.Fatal("revoked endpoint was called", err)
	}
	approved = true
	if _, err = root.Exec(ctx, `DELETE FROM role_permissions WHERE role_id=(SELECT id FROM roles WHERE org_id=$1 AND code='MEMBER') AND permission_id='memory:read'`, org); err != nil {
		t.Fatal(err)
	}
	if _, err = s.List(ctx, owner, "memory"); !errors.Is(err, identity.ErrForbidden) {
		t.Fatal("stale principal retained permission", err)
	}
}
