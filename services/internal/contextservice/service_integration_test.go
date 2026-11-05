package contextservice

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	t.Run("manual memory backends forward bounded search conditions", func(t *testing.T) {
		for _, backend := range []string{"mem0", "graphiti"} {
			t.Run(backend, func(t *testing.T) {
				keyFile := filepath.Join(t.TempDir(), "bridge-key")
				if err := os.WriteFile(keyFile, []byte("memory-search-secret-32-characters-long"), 0600); err != nil {
					t.Fatal(err)
				}
				var received Action
				var namespace, sessionNamespace, resolvedScope string
				var calls int
				responseBody, responseStatus := `{"items":[]}`, http.StatusOK
				remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var input struct {
						Namespace        string `json:"namespace"`
						SessionNamespace string `json:"session_namespace"`
						Scope            string `json:"scope"`
						Action
					}
					if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
						t.Error(err)
					}
					calls++
					received, namespace, sessionNamespace, resolvedScope = input.Action, input.Namespace, input.SessionNamespace, input.Scope
					if r.URL.Path == "/v1/verify" {
						fmt.Fprintf(w, `{"contract":"hc-context-v1","backend":%q,"ready":true,"capabilities":{"search":true}}`, backend)
					} else {
						w.WriteHeader(responseStatus)
						io.WriteString(w, responseBody)
					}
				}))
				defer remote.Close()
				service := NewService(app, cipher, "test", false, "")
				service.policy = func() (Policy, error) {
					return Policy{[]Endpoint{{ID: "search-" + backend, Name: backend, Backend: backend, URL: remote.URL, AllowPrivateHTTP: true, DataUse: "memory", ProxyKeyFile: keyFile}}}, nil
				}
				connection, err := service.Save(ctx, owner, "memory", "", Input{Name: backend + " search", EndpointID: "search-" + backend, Credential: "test-memory-credential"}, "")
				if err != nil {
					t.Fatal(err)
				}
				if _, err = service.Call(ctx, owner, "memory", connection.ID, "verify", Action{ExpectedVersion: connection.Version}, ""); err != nil {
					t.Fatal(err)
				}
				for index, minimum := range []float64{-1, 0, 0.75, 1} {
					limit := []int{1, 5, 10, 20}[index]
					if _, err = service.Call(ctx, owner, "memory", connection.ID, "search", Action{ExpectedVersion: connection.Version, Query: "公司叫什么", Limit: limit, MinSimilarity: &minimum}, ""); err != nil {
						t.Fatal("memory search conditions rejected", minimum, limit, err)
					}
					if received.MinSimilarity == nil || *received.MinSimilarity != minimum || received.Limit != limit || received.MemorySource != "" || len(namespace) != 64 || sessionNamespace != "" || resolvedScope != "personal" {
						t.Fatal("memory search conditions were not forwarded safely", received, resolvedScope)
					}
				}
				if _, err = service.Call(ctx, owner, "memory", connection.ID, "search", Action{ExpectedVersion: connection.Version, Query: "公司叫什么"}, ""); err != nil || received.Limit != 5 || received.MinSimilarity != nil {
					t.Fatal("memory search defaults changed", received, err)
				}
				searchNamespace := namespace
				request := &SessionRequest{Method: "POST", Path: "/sessions/messages", Body: `{"session_id":"` + strings.Repeat("a", 64) + `","messages":[{"id":"` + strings.Repeat("b", 64) + `","role":"user","content":"周三发布"}]}`}
				if _, err = service.Session(ctx, owner, connection.ID, connection.Version, "personal", request); err != nil {
					t.Fatal("managed session sync rejected", backend, err)
				}
				if namespace != searchNamespace || sessionNamespace != "" || resolvedScope != "personal" || received.SessionRequest == nil || received.SessionRequest.Path != request.Path {
					t.Fatal("managed session sync used a different corpus or lost its request", received)
				}
				if _, err = service.Session(ctx, other, connection.ID, connection.Version, "personal", request); !problemIs(err, "NOT_FOUND") {
					t.Fatal("foreign memory session sync accepted", err)
				}
				for _, source := range []string{"manual", "automatic"} {
					_, err = service.Call(ctx, owner, "memory", connection.ID, "search", Action{ExpectedVersion: connection.Version, Query: "公司叫什么", MemorySource: source}, "")
					if err != nil || received.MemorySource != source {
						t.Fatal("generated/manual source filter was lost", backend, source, err)
					}
				}
				entries := make([]map[string]any, 40)
				for index := range entries {
					entries[index] = map[string]any{"id": fmt.Sprintf("large-memory-%d", index), "content": strings.Repeat("x", 128<<10)}
				}
				large, err := json.Marshal(map[string]any{"schema": "hc-memory-v1", "backend": backend, "exported_at": "2026-11-03T00:00:00Z", "entries": entries, "sha256": "test-transport-checksum"})
				if err != nil {
					t.Fatal(err)
				}
				responseBody = string(large)
				for _, action := range []string{"preview", "export"} {
					input := Action{ExpectedVersion: connection.Version}
					if action == "preview" {
						input.Package = large
					}
					result, err := service.Call(ctx, owner, "memory", connection.ID, action, input, "")
					if err != nil || !bytes.Equal(result, large) {
						t.Fatal("manual memory archive retained a legacy request/response size cap", action, len(result), err)
					}
				}
				responseBody, responseStatus = `{}`, http.StatusConflict
				if _, err := service.Call(ctx, owner, "memory", connection.ID, "export", Action{ExpectedVersion: connection.Version}, ""); !problemIs(err, "VERSION_CONFLICT") {
					t.Fatal("incomplete memory archive was not rejected", err)
				}
				responseStatus = http.StatusRequestEntityTooLarge
				if _, err := service.Call(ctx, owner, "memory", connection.ID, "preview", Action{ExpectedVersion: connection.Version, Package: large}, ""); !problemIs(err, "PAYLOAD_TOO_LARGE") {
					t.Fatal("oversize memory archive guard was not preserved", err)
				}
			})
		}
	})
	t.Run("memory viewer reads the plugin tenant with fixed owner scope", func(t *testing.T) {
		keyFile := filepath.Join(t.TempDir(), "bridge-key")
		if err := os.WriteFile(keyFile, []byte("viking-bridge-secret-32-characters-long"), 0600); err != nil {
			t.Fatal(err)
		}
		var namespace, resolvedScope, sessionNamespace, manualReadNamespace, lastPath string
		var searchOptions Action
		remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			lastPath = r.URL.Path
			var input struct {
				Namespace        string `json:"namespace"`
				Scope            string `json:"scope"`
				SessionNamespace string `json:"session_namespace"`
				ManualNamespace  string `json:"manual_namespace"`
				Action
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			namespace, resolvedScope = input.Namespace, input.Scope
			sessionNamespace = input.SessionNamespace
			manualReadNamespace = input.ManualNamespace
			searchOptions = input.Action
			if r.URL.Path == "/v1/verify" {
				io.WriteString(w, `{"contract":"hc-context-v1","backend":"openviking","ready":true,"capabilities":{"search":true}}`)
			} else {
				io.WriteString(w, `{"items":[]}`)
			}
		}))
		defer remote.Close()
		service := NewService(app, cipher, "test", false, "")
		service.policy = func() (Policy, error) {
			return Policy{[]Endpoint{{ID: "viking-viewer", Name: "OpenViking", Backend: "openviking", URL: remote.URL, AllowPrivateHTTP: true, DataUse: "memory", ProxyKeyFile: keyFile, ServiceKeyFile: keyFile}}}, nil
		}
		connection, err := service.Save(ctx, owner, "memory", "", Input{Name: "DSH memory", EndpointID: "viking-viewer", ServiceURL: "https://viking.example", Credential: "admin-key"}, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = service.Call(ctx, owner, "memory", connection.ID, "verify", Action{ExpectedVersion: connection.Version}, ""); err != nil {
			t.Fatal(err)
		}
		manualNamespace := namespace
		if sessionNamespace != "" {
			t.Fatal("verification unnecessarily exposed the native corpus")
		}
		pluginDigest := sha256.Sum256([]byte(owner.OrganizationID + ":" + owner.UserID + ":" + connection.ID + ":dsh-session:personal"))
		if _, err = service.Session(ctx, owner, connection.ID, connection.Version, "personal", &SessionRequest{Method: "POST", Path: "/api/v1/search/find", Body: `{"query":"项目发布约定"}`}); err != nil {
			t.Fatal(err)
		}
		if namespace != hex.EncodeToString(pluginDigest[:]) || manualReadNamespace != manualNamespace || sessionNamespace != "" {
			t.Fatal("official plugin cannot read the owner's platform memories")
		}
		if _, err = service.Session(ctx, other, connection.ID, connection.Version, "personal", &SessionRequest{Method: "GET", Path: "/health"}); !problemIs(err, "NOT_FOUND") {
			t.Fatal("another owner accessed the plugin's combined memory corpus", err)
		}
		for _, action := range []string{"session-list", "session-get"} {
			if _, err = service.Call(ctx, owner, "memory", connection.ID, action, Action{ExpectedVersion: connection.Version, Scope: "workspace:" + webapi.ID(), RecordID: "viking://user/owner/memories/profile.md"}, ""); err != nil {
				t.Fatal(err)
			}
			if namespace != hex.EncodeToString(pluginDigest[:]) || namespace == manualNamespace || resolvedScope != "personal" || manualReadNamespace != "" {
				t.Fatal("viewer used a different corpus than the plugin")
			}
			if _, err = service.Call(ctx, other, "memory", connection.ID, action, Action{ExpectedVersion: connection.Version}, ""); !problemIs(err, "NOT_FOUND") {
				t.Fatal("another owner accessed session memory", err)
			}
		}
		if _, err = service.Call(ctx, owner, "memory", connection.ID, "session-list", Action{ExpectedVersion: connection.Version, SessionRequest: &SessionRequest{Method: "GET", Path: "/health"}}, ""); !problemIs(err, "CONTEXT_INVALID") {
			t.Fatal("public viewer accepted native proxy input", err)
		}
		for _, action := range []string{"search", "export", "preview", "apply"} {
			input := Action{ExpectedVersion: connection.Version, Scope: "workspace:" + webapi.ID()}
			if action == "search" {
				input.Query = "项目约定"
				minimum := 0.75
				input.MemorySource, input.MinSimilarity, input.Limit = "automatic", &minimum, 20
			}
			if action == "apply" {
				input.Confirmed, input.IdempotencyKey, input.Token = true, "archive-apply-test", "archive-preview"
			}
			if _, err = service.Call(ctx, owner, "memory", connection.ID, action, input, ""); err != nil {
				t.Fatal(err)
			}
			if namespace != manualNamespace || sessionNamespace != hex.EncodeToString(pluginDigest[:]) || resolvedScope != "personal" {
				t.Fatal("memory operation did not address both server-resolved corpora")
			}
			if action == "search" && (searchOptions.MemorySource != "automatic" || searchOptions.MinSimilarity == nil || *searchOptions.MinSimilarity != 0.75 || searchOptions.Limit != 20) {
				t.Fatal("OpenViking search conditions were not forwarded")
			}
			if _, err = service.Call(ctx, other, "memory", connection.ID, action, input, ""); !problemIs(err, "NOT_FOUND") {
				t.Fatal("another owner accessed a complete memory archive", err)
			}
		}
		// The existing runtime tool follows every search hit with generic get.
		// Native URIs may exceed the old 128-byte manual record-ID limit.
		uri := "viking://user/owner/memories/preferences/" + strings.Repeat("long-name-", 14) + ".md"
		if _, err = service.Call(ctx, owner, "memory", connection.ID, "get", Action{ExpectedVersion: connection.Version, RecordID: uri}, ""); err != nil {
			t.Fatal("generic reader could not open an automatic search result", err)
		}
		if lastPath != "/v1/session-get" || namespace != hex.EncodeToString(pluginDigest[:]) || sessionNamespace != "" {
			t.Fatal("generic reader did not use the constrained native reader")
		}
		if _, err = service.Call(ctx, other, "memory", connection.ID, "get", Action{ExpectedVersion: connection.Version, RecordID: uri}, ""); !problemIs(err, "NOT_FOUND") {
			t.Fatal("generic reader leaked another owner's native search result", err)
		}
	})
	t.Run("custom Graphiti connection routes credentials through its approved bridge", func(t *testing.T) {
		const bridgeKey = "graphiti-bridge-secret-32-characters-long"
		keyFile := filepath.Join(t.TempDir(), "service-key")
		if err := os.WriteFile(keyFile, []byte(bridgeKey), 0600); err != nil {
			t.Fatal(err)
		}
		var target milvusService
		remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req struct {
				Service milvusService `json:"service"`
			}
			if json.NewDecoder(r.Body).Decode(&req) != nil || r.Header.Get("Authorization") != "Bearer "+bridgeKey {
				t.Error("invalid bridge request")
			}
			target = req.Service
			io.WriteString(w, `{"contract":"hc-context-v1","backend":"graphiti","ready":true,"capabilities":{"search":true}}`)
		}))
		defer remote.Close()
		custom := NewService(app, cipher, "test", false, "")
		custom.policy = func() (Policy, error) {
			return Policy{[]Endpoint{{ID: "graphiti-bridge", Name: "Graphiti", Backend: "graphiti", URL: remote.URL, AllowPrivateHTTP: true, DataUse: "graph", ProxyKeyFile: keyFile, ServiceKeyFile: keyFile}}}, nil
		}
		input := Input{Name: "Graphiti", EndpointID: "graphiti-bridge", ServiceURL: "bolt+s://neo4j.example:7687", Credential: "neo4j:secret"}
		v, err := custom.Save(ctx, owner, "memory", "", input, "")
		if err != nil || v.ServiceURL != input.ServiceURL {
			t.Fatalf("save: %v", err)
		}
		if _, err = custom.Call(ctx, owner, "memory", v.ID, "verify", Action{ExpectedVersion: v.Version}, ""); err != nil || target.URL != input.ServiceURL || target.Token != input.Credential {
			t.Fatalf("verify: %v target=%+v", err, target)
		}
		input.ExpectedVersion = v.Version
		input.ServiceURL = ""
		input.Credential = "bad-credential"
		if _, err = custom.Save(ctx, owner, "memory", v.ID, input, ""); !problemIs(err, "CONTEXT_INVALID") {
			t.Fatalf("invalid credential accepted: %v", err)
		}
	})
	t.Run("custom Milvus connections retain and isolate their own targets and encrypted tokens", func(t *testing.T) {
		const bridgeKey = "platform-bridge-secret-32-characters-long"
		keyFile := filepath.Join(t.TempDir(), "service-key")
		if err := os.WriteFile(keyFile, []byte(bridgeKey), 0600); err != nil {
			t.Fatal(err)
		}
		var target milvusService
		var embedding *EmbeddingConfig
		var resolvedScope, namespace string
		remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req struct {
				Service   milvusService    `json:"service"`
				Embedding *EmbeddingConfig `json:"embedding"`
				Scope     string           `json:"scope"`
				Namespace string           `json:"namespace"`
			}
			if json.NewDecoder(r.Body).Decode(&req) != nil || r.Header.Get("Authorization") != "Bearer "+bridgeKey {
				t.Error("native token used as companion credential")
			}
			target = req.Service
			resolvedScope, namespace = req.Scope, req.Namespace
			embedding = req.Embedding
			io.WriteString(w, `{"contract":"hc-context-v1","backend":"milvus","ready":true,"capabilities":{"search":true}}`)
		}))
		defer remote.Close()
		custom := NewService(app, cipher, "test", false, "")
		custom.policy = func() (Policy, error) {
			return Policy{[]Endpoint{{ID: "milvus-bridge", Name: "Milvus", Backend: "milvus", URL: remote.URL, AllowPrivateHTTP: true, DataUse: "user Milvus", ProxyKeyFile: keyFile, ServiceKeyFile: keyFile}}}, nil
		}
		external := EmbeddingConfig{Provider: "openai_compatible", Model: "custom-embedding-model", APIBase: "https://embedding.example/v1", Dimensions: 768, APIKey: "embedding-secret"}
		local := defaultEmbedding()
		for _, config := range []*EmbeddingConfig{nil, &local} {
			if _, err := custom.Save(ctx, owner, "knowledge", "", Input{Name: "未配置 API", EndpointID: "milvus-bridge", ServiceURL: "https://first.example:19530", Embedding: config}, ""); !problemIs(err, "CONTEXT_INVALID") {
				t.Fatal("new knowledge accepted an implicit or built-in embedding model", err)
			}
		}
		first, err := custom.Save(ctx, owner, "knowledge", "", Input{Name: "自有 Milvus", EndpointID: "milvus-bridge", ServiceURL: "https://first.example:19530/", Credential: "root:secret", Embedding: &external}, "")
		if err != nil || first.ServiceURL != "https://first.example:19530" {
			t.Fatal("target not saved", err)
		}
		sharedNamespace := ""
		for _, requestedScope := range []string{"", "personal", "workspace:" + webapi.ID(), "workspace:" + webapi.ID()} {
			if _, err := custom.Call(ctx, owner, "knowledge", first.ID, "verify", Action{ExpectedVersion: first.Version, Scope: requestedScope}, ""); err != nil {
				t.Fatal("shared knowledge scope rejected", err)
			}
			if resolvedScope != "personal" || namespace == "" || sharedNamespace != "" && namespace != sharedNamespace {
				t.Fatal("knowledge split by workspace", resolvedScope, namespace)
			}
			sharedNamespace = namespace
			if _, err := custom.Call(ctx, owner, "knowledge", first.ID, "search", Action{ExpectedVersion: first.Version, Scope: requestedScope, Query: "shared document"}, ""); err != nil || resolvedScope != "personal" || namespace != sharedNamespace {
				t.Fatal("knowledge retrieval split by workspace", err)
			}
		}
		selfHosted := EmbeddingConfig{Provider: "openai_compatible", Model: "self-hosted-model", APIBase: "http://embedding.internal:11434/v1", Dimensions: 384}
		second, err := custom.Save(ctx, owner, "knowledge", "", Input{Name: "另一 Milvus", EndpointID: "milvus-bridge", ServiceURL: "http://second.internal:19530", Embedding: &selfHosted}, "")
		if err != nil {
			t.Fatal("unauthenticated Milvus not supported", err)
		}
		for _, item := range []Connection{first, second} {
			if _, err := custom.Call(ctx, owner, "knowledge", item.ID, "verify", Action{ExpectedVersion: item.Version}, ""); err != nil || target.URL != item.ServiceURL {
				t.Fatal("target leaked across connections", err)
			}
			if item.ID != first.ID && namespace == sharedNamespace {
				t.Fatal("different knowledge connections shared a corpus")
			}
			if item.ID == first.ID && target.Token != "root:secret" || item.ID == second.ID && target.Token != "" {
				t.Fatal("wrong native token")
			}
			if item.ID == first.ID && (embedding == nil || *embedding != external) || item.ID == second.ID && (embedding == nil || *embedding != selfHosted) {
				t.Fatal("embedding configuration leaked across connections")
			}
		}
		// Simulate a connection saved before per-connection API models were required.
		legacyJSON, _ := json.Marshal(connectionSettings{ServiceURL: second.ServiceURL})
		legacySecret, err := cipher.Encrypt(append([]byte(settingsSecretPrefix), legacyJSON...), custom.aad(owner, second.ID, "context-credential"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = root.Exec(ctx, `UPDATE context_connections SET key_id=$2,nonce=$3,ciphertext=$4 WHERE id=$1`, second.ID, legacySecret.KeyID, legacySecret.Nonce, legacySecret.Ciphertext); err != nil {
			t.Fatal(err)
		}
		legacy, err := custom.Save(ctx, owner, "knowledge", second.ID, Input{Name: second.Name, EndpointID: second.EndpointID, ExpectedVersion: second.Version, Embedding: &local}, "")
		if err != nil || legacy.Embedding == nil || *legacy.Embedding != local || legacy.ServiceURL != second.ServiceURL {
			t.Fatal("existing local knowledge settings cannot be retained", err)
		}
		var encrypted []byte
		if err := root.QueryRow(ctx, "SELECT ciphertext FROM context_connections WHERE id=$1", first.ID).Scan(&encrypted); err != nil || bytes.Contains(encrypted, []byte("root:secret")) || bytes.Contains(encrypted, []byte(first.ServiceURL)) || bytes.Contains(encrypted, []byte(external.APIKey)) {
			t.Fatal("native config not encrypted", err)
		}
		listing, err := custom.List(ctx, owner, "knowledge")
		raw, _ := json.Marshal(listing)
		if err != nil || !bytes.Contains(raw, []byte(first.ServiceURL)) || bytes.Contains(raw, []byte("root:secret")) || bytes.Contains(raw, []byte(external.APIKey)) || !bytes.Contains(raw, []byte(`"embedding_key_configured":true`)) || bytes.Contains(raw, []byte("service_key_file")) || !bytes.Contains(raw, []byte(`"custom_service":true`)) {
			t.Fatal("wrong public connection view", err)
		}
		otherList, err := custom.List(ctx, other, "knowledge")
		otherRaw, _ := json.Marshal(otherList)
		if err != nil || bytes.Contains(otherRaw, []byte(first.ServiceURL)) {
			t.Fatal("cross owner target disclosure", err)
		}
		if _, err := custom.Call(ctx, other, "knowledge", first.ID, "verify", Action{ExpectedVersion: first.Version}, ""); !problemIs(err, "NOT_FOUND") {
			t.Fatal("cross owner call", err)
		}
		updated, err := custom.Save(ctx, owner, "knowledge", first.ID, Input{Name: first.Name, EndpointID: first.EndpointID, ExpectedVersion: first.Version}, "")
		if err != nil || updated.ServiceURL != first.ServiceURL {
			t.Fatal("blank edit erased service", err)
		}
		if _, err := custom.Call(ctx, owner, "knowledge", first.ID, "verify", Action{ExpectedVersion: updated.Version}, ""); err != nil || target.Token != "root:secret" || embedding == nil || embedding.APIKey != external.APIKey {
			t.Fatal("blank edit erased token", err)
		}
		if _, err := custom.Save(ctx, owner, "knowledge", first.ID, Input{Name: first.Name, EndpointID: first.EndpointID, ExpectedVersion: updated.Version, ServiceURL: second.ServiceURL}, ""); !problemIs(err, "CONTEXT_INVALID") {
			t.Fatal("silently moved existing records to another server", err)
		}
		rotated, err := custom.Save(ctx, owner, "knowledge", first.ID, Input{Name: first.Name, EndpointID: first.EndpointID, ExpectedVersion: updated.Version, ServiceURL: first.ServiceURL, Credential: "new-token"}, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = custom.Call(ctx, owner, "knowledge", first.ID, "verify", Action{ExpectedVersion: rotated.Version}, ""); err != nil || target.Token != "new-token" {
			t.Fatal("token rotation failed", err)
		}
		for _, change := range []func(*EmbeddingConfig){func(e *EmbeddingConfig) { e.Model = "other" }, func(e *EmbeddingConfig) { e.Dimensions = 512 }, func(e *EmbeddingConfig) { e.APIBase = "https://other.example/v1" }, func(e *EmbeddingConfig) { *e = defaultEmbedding() }} {
			changed := external
			change(&changed)
			if _, err := custom.Save(ctx, owner, "knowledge", first.ID, Input{Name: first.Name, EndpointID: first.EndpointID, ExpectedVersion: rotated.Version, Embedding: &changed}, ""); !problemIs(err, "CONTEXT_INVALID") {
				t.Fatal("allowed mixed vector models", err)
			}
		}
		emptyKey := external
		emptyKey.APIKey = ""
		kept, err := custom.Save(ctx, owner, "knowledge", first.ID, Input{Name: first.Name, EndpointID: first.EndpointID, ExpectedVersion: rotated.Version, Embedding: &emptyKey}, "")
		if err != nil || kept.Embedding == nil || kept.Embedding.APIKey != "" || !kept.EmbeddingKeyConfigured {
			t.Fatal("embedding secret exposed or erased", err)
		}
		if _, err = custom.Call(ctx, owner, "knowledge", first.ID, "verify", Action{ExpectedVersion: kept.Version}, ""); err != nil || embedding.APIKey != external.APIKey {
			t.Fatal("blank key not retained", err)
		}
		newKey := external
		newKey.APIKey = "rotated-embedding-key"
		changedKey, err := custom.Save(ctx, owner, "knowledge", first.ID, Input{Name: first.Name, EndpointID: first.EndpointID, ExpectedVersion: kept.Version, Embedding: &newKey}, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = custom.Call(ctx, owner, "knowledge", first.ID, "verify", Action{ExpectedVersion: changedKey.Version}, ""); err != nil || embedding.APIKey != newKey.APIKey {
			t.Fatal("embedding key rotation failed", err)
		}
	})
	for _, backend := range []string{"weaviate", "qdrant"} {
		t.Run("custom "+backend+" target and credentials", func(t *testing.T) {
			keyFile := filepath.Join(t.TempDir(), "service-key")
			const bridgeKey = "platform-bridge-secret-32-characters-long"
			if err := os.WriteFile(keyFile, []byte(bridgeKey), 0600); err != nil {
				t.Fatal(err)
			}
			var target milvusService
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Service milvusService `json:"service"`
				}
				if json.NewDecoder(r.Body).Decode(&req) != nil || r.Header.Get("Authorization") != "Bearer "+bridgeKey {
					t.Error("invalid bridge authentication")
				}
				target = req.Service
				fmt.Fprintf(w, `{"contract":"hc-context-v1","backend":%q,"ready":true,"capabilities":{"search":true}}`, backend)
			}))
			defer remote.Close()
			custom := NewService(app, cipher, "test", false, "")
			custom.policy = func() (Policy, error) {
				return Policy{[]Endpoint{{ID: "own-" + backend, Name: backend, Backend: backend, URL: remote.URL, AllowPrivateHTTP: true, DataUse: "test", ProxyKeyFile: keyFile, ServiceKeyFile: keyFile}}}, nil
			}
			embedding := EmbeddingConfig{Provider: "openai_compatible", Model: "test-model", APIBase: "https://embedding.example/v1", Dimensions: 512}
			v, err := custom.Save(ctx, owner, "knowledge", "", Input{Name: "own " + backend, EndpointID: "own-" + backend, ServiceURL: "http://service.internal:8080", Credential: "native-key", Embedding: &embedding}, "")
			if err != nil || v.ServiceURL != "http://service.internal:8080" {
				t.Fatal("save failed", err)
			}
			if _, err = custom.Call(ctx, owner, "knowledge", v.ID, "verify", Action{ExpectedVersion: v.Version}, ""); err != nil || target.URL != v.ServiceURL || target.Token != "native-key" {
				t.Fatal("wrong target or credential", err)
			}
			v, err = custom.Save(ctx, owner, "knowledge", v.ID, Input{Name: v.Name, EndpointID: v.EndpointID, ExpectedVersion: v.Version}, "")
			if err != nil {
				t.Fatal("edit failed", err)
			}
			if _, err = custom.Call(ctx, owner, "knowledge", v.ID, "verify", Action{ExpectedVersion: v.Version}, ""); err != nil || target.Token != "native-key" {
				t.Fatal("credential not retained", err)
			}
			if _, err = custom.Save(ctx, owner, "knowledge", v.ID, Input{Name: v.Name, EndpointID: v.EndpointID, ExpectedVersion: v.Version, ServiceURL: "https://other.example"}, ""); !problemIs(err, "CONTEXT_INVALID") {
				t.Fatal("address unexpectedly changed", err)
			}
		})
	}
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
	var memoryScope, memoryNamespace string
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
		memoryScope, memoryNamespace = req["scope"].(string), req["namespace"].(string)
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
	sharedMemoryNamespace := memoryNamespace
	for _, requestedScope := range []string{"", "personal", "workspace:" + webapi.ID(), "workspace:" + webapi.ID()} {
		if _, err := s.Call(ctx, owner, "memory", v.ID, "search", Action{ExpectedVersion: v.Version, Scope: requestedScope, Query: "shared memory"}, ""); err != nil || memoryScope != "personal" || memoryNamespace != sharedMemoryNamespace {
			t.Fatal("memory split by workspace", err, memoryScope, memoryNamespace)
		}
	}
	separate, err := s.Save(ctx, owner, "memory", "", Input{Name: "Separate memory", EndpointID: "test", Credential: "remote-secret-value"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Call(ctx, owner, "memory", separate.ID, "verify", Action{ExpectedVersion: separate.Version}, ""); err != nil || memoryNamespace == sharedMemoryNamespace {
		t.Fatal("different memory connections shared a corpus", err)
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
