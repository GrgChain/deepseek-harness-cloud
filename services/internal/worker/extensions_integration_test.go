package worker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/connectors"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/contextservice"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/workspaceextensions"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestWorkspaceRuntimeRetrievalAndRevocation(t *testing.T) {
	f := newMonitorFixture(t)
	instance, owner := f.instance(t)
	source := webapi.ID()
	requests := 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid request")
		}
		if strings.Contains(string(marshalForTest(body)), "credential") {
			t.Error("credential in body")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{{"id": "proof", "title": "Source", "content": "authorized result", "source": "fixture"}}})
	}))
	defer remote.Close()
	directory := t.TempDir()
	proxyKey := filepath.Join(directory, "proxy-key")
	if os.WriteFile(proxyKey, []byte(strings.Repeat("k", 32)), 0600) != nil {
		t.Fatal("key")
	}
	policy := filepath.Join(directory, "context.json")
	raw, _ := json.Marshal(map[string]any{"endpoints": []map[string]any{{"id": "fixture", "name": "Fixture", "backend": "mem0", "url": remote.URL, "allow_private_http": true, "data_use": "Test retrieval", "proxy_key_file": proxyKey}}})
	if os.WriteFile(policy, raw, 0600) != nil {
		t.Fatal("policy")
	}
	t.Setenv("HC_CONTEXT_ENDPOINTS_FILE", policy)
	envelope, e := f.cipher.Encrypt([]byte("synthetic-context-key"), security.Context{Environment: "test", OrganizationID: f.org, RecordID: source, Purpose: "context-credential"})
	if e != nil {
		t.Fatal(e)
	}
	f.exec(t, `INSERT INTO context_connections(id,org_id,owner_id,kind,backend,endpoint_id,name,status,key_id,nonce,ciphertext) VALUES($1,$2,$3,'memory','mem0','fixture','Memory','READY',$4,$5,$6)`, source, f.org, owner, envelope.KeyID, envelope.Nonce, envelope.Ciphertext)
	snapshot := workspaceextensions.Snapshot{Items: []workspaceextensions.Item{{Kind: "memory", ID: source, Name: "Memory", Version: 1, Scopes: []string{"personal"}}}}
	f.exec(t, `INSERT INTO workspace_extensions(instance_id,org_id,owner_id,auth_generation,configuration) VALUES($1,$2,$3,1,$4)`, instance, f.org, owner, marshalForTest(snapshot))
	f.exec(t, `INSERT INTO context_bindings(instance_id,org_id,owner_id,connection_id,scopes) VALUES($1,$2,$3,$4,'["personal"]')`, instance, f.org, owner, source)
	broker := NewExtensionBroker(f.pool, directory, f.cipher, "test", false)
	request := extensionRequest{ID: webapi.ID(), Kind: "memory", SourceID: source, Query: "test", Scope: "personal"}
	result, e := broker.invoke(f.ctx, instance, request)
	if e != nil || !strings.Contains(string(marshalForTest(result)), "authorized result") || requests != 1 {
		t.Fatalf("retrieval failed: %v, requests %d", e, requests)
	}
	request.Action, request.RecordID = "get", "proof"
	if result, e = broker.invoke(f.ctx, instance, request); e != nil || !strings.Contains(string(marshalForTest(result)), "authorized result") || requests != 2 {
		t.Fatal("authorized source retrieval failed", e)
	}
	request.Action = "apply"
	if _, e = broker.invoke(f.ctx, instance, request); e == nil || requests != 2 {
		t.Fatal("runtime was allowed to write context", e)
	}
	request.Action = "search"
	request.Scope = "workspace:" + instance
	if _, e = broker.invoke(f.ctx, instance, request); e == nil || requests != 2 {
		t.Fatal("scope expanded", e)
	}
	request.Scope = "personal"
	f.exec(t, `UPDATE context_bindings SET enabled=false WHERE instance_id=$1`, instance)
	if _, e = broker.invoke(f.ctx, instance, request); e == nil || requests != 2 {
		t.Fatal("disabled binding used", e)
	}
	f.exec(t, `UPDATE context_bindings SET enabled=true WHERE instance_id=$1`, instance)
	f.exec(t, `UPDATE memberships SET auth_generation=auth_generation+1 WHERE org_id=$1 AND user_id=$2`, f.org, owner)
	if _, e = broker.invoke(f.ctx, instance, request); e == nil || requests != 2 {
		t.Fatal("revoked runtime authority used", e)
	}
	tx, e := f.pool.Begin(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(context.Background())
	if _, e = identity.LockPrincipal(f.ctx, tx, identity.Principal{WorkspaceID: instance, OrganizationID: f.org, UserID: owner, Generation: 1}); e == nil {
		t.Fatal("old generation survived")
	}
}

func marshalForTest(value any) []byte { raw, _ := json.Marshal(value); return raw }

func TestWorkspaceKnowledgeSubmissionRequiresConfirmationAndReplays(t *testing.T) {
	f := newMonitorFixture(t)
	instance, owner := f.instance(t)
	f.exec(t, `INSERT INTO role_permissions(role_id,permission_id) SELECT id,'knowledge:manage' FROM roles WHERE org_id=$1 AND builtin ON CONFLICT DO NOTHING`, f.org)
	source := webapi.ID()
	previews, writes, deletes := 0, 0, 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body contextservice.Action
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid request")
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/get"):
			_, _ = w.Write([]byte(`{"id":"document","title":"Project","content":"Approved knowledge","version":1}`))
		case strings.HasSuffix(r.URL.Path, "/delete"):
			deletes++
			if !body.Confirmed || body.RecordID != "document" || body.RecordVersion != 1 {
				t.Error("unsafe delete")
			}
			_, _ = w.Write([]byte(`{"deleted":true}`))
		case strings.HasSuffix(r.URL.Path, "/preview"):
			previews++
			if body.Title != "Project" || body.Content != "Approved knowledge" || body.Type != "document" {
				t.Error("document changed")
			}
			_, _ = w.Write([]byte(`{"token":"staged","items":[],"warnings":[]}`))
		case strings.HasSuffix(r.URL.Path, "/apply"):
			writes++
			if !body.Confirmed || body.Token != "staged" || body.Mode != "skip" {
				t.Error("unsafe apply")
			}
			_, _ = w.Write([]byte(`{"applied":["document"],"skipped":[]}`))
		default:
			t.Error("unexpected action", r.URL.Path)
		}
	}))
	defer remote.Close()
	directory := t.TempDir()
	keyFile, policy := filepath.Join(directory, "proxy-key"), filepath.Join(directory, "context.json")
	if err := os.WriteFile(keyFile, []byte(strings.Repeat("k", 32)), 0600); err != nil {
		t.Fatal(err)
	}
	raw := marshalForTest(map[string]any{"endpoints": []map[string]any{{"id": "fixture", "name": "Fixture", "backend": "qdrant", "url": remote.URL, "allow_private_http": true, "data_use": "Test", "proxy_key_file": keyFile}}})
	if err := os.WriteFile(policy, raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HC_CONTEXT_ENDPOINTS_FILE", policy)
	envelope, err := f.cipher.Encrypt([]byte("synthetic-key"), security.Context{Environment: "test", OrganizationID: f.org, RecordID: source, Purpose: "context-credential"})
	if err != nil {
		t.Fatal(err)
	}
	f.exec(t, `INSERT INTO context_connections(id,org_id,owner_id,kind,backend,endpoint_id,name,status,key_id,nonce,ciphertext) VALUES($1,$2,$3,'knowledge','qdrant','fixture','Knowledge','READY',$4,$5,$6)`, source, f.org, owner, envelope.KeyID, envelope.Nonce, envelope.Ciphertext)
	snapshot := workspaceextensions.Snapshot{Items: []workspaceextensions.Item{{Kind: "knowledge", ID: source, Name: "Knowledge", Version: 1, Scopes: []string{"personal"}}}}
	f.exec(t, `INSERT INTO workspace_extensions(instance_id,org_id,owner_id,auth_generation,configuration) VALUES($1,$2,$3,1,$4)`, instance, f.org, owner, marshalForTest(snapshot))
	f.exec(t, `INSERT INTO context_bindings(instance_id,org_id,owner_id,connection_id,scopes) VALUES($1,$2,$3,$4,'["personal"]')`, instance, f.org, owner, source)
	broker := NewExtensionBroker(f.pool, directory, f.cipher, "test", false)
	request := extensionRequest{ID: webapi.ID(), Kind: "knowledge", SourceID: source, Action: "preview", Scope: "personal", Title: "Project", Content: "Approved knowledge"}
	if _, err = broker.invoke(f.ctx, instance, request); err == nil || previews != 0 {
		t.Fatal("legacy read-only permission expanded", err)
	}
	snapshot.Items[0].Permissions = []string{"read", "write", "delete"}
	f.exec(t, `UPDATE workspace_extensions SET configuration=$2 WHERE instance_id=$1`, instance, marshalForTest(snapshot))
	if _, err = broker.invoke(f.ctx, instance, request); err != nil || previews != 1 || writes != 0 {
		t.Fatal("preview failed", err, previews, writes)
	}
	request.ID, request.Action, request.Token = webapi.ID(), "apply", "staged"
	if _, err = broker.invoke(f.ctx, instance, request); err == nil || writes != 0 {
		t.Fatal("unconfirmed write", err)
	}
	request.Confirmed = true
	if _, err = broker.invoke(f.ctx, instance, request); err != nil || writes != 1 {
		t.Fatal("write failed", err, writes)
	}
	if _, err = broker.invoke(f.ctx, instance, request); err != nil || writes != 1 {
		t.Fatal("duplicate write", err, writes)
	}
	deletion := extensionRequest{ID: webapi.ID(), Kind: "knowledge", SourceID: source, Action: "delete", Scope: "personal", RecordID: "document", RecordVersion: 1}
	if _, err = broker.invoke(f.ctx, instance, deletion); err == nil || deletes != 0 {
		t.Fatal("unconfirmed deletion", err)
	}
	deletion.Confirmed = true
	snapshot.Items[0].Permissions = []string{"write"}
	f.exec(t, `UPDATE workspace_extensions SET configuration=$2 WHERE instance_id=$1`, instance, marshalForTest(snapshot))
	if _, err = broker.invoke(f.ctx, instance, deletion); err == nil || deletes != 0 {
		t.Fatal("delete exceeded frozen permission", err)
	}
	read := extensionRequest{ID: webapi.ID(), Kind: "knowledge", SourceID: source, Action: "get", Scope: "personal", RecordID: "document"}
	if _, err = broker.invoke(f.ctx, instance, read); err == nil {
		t.Fatal("read exceeded frozen permission")
	}
	snapshot.Items[0].Permissions = []string{"read", "write", "delete"}
	f.exec(t, `UPDATE workspace_extensions SET configuration=$2 WHERE instance_id=$1`, instance, marshalForTest(snapshot))
	if _, err = broker.invoke(f.ctx, instance, deletion); err != nil || deletes != 1 {
		t.Fatal("delete failed", err, deletes)
	}
	if _, err = broker.invoke(f.ctx, instance, deletion); err != nil || deletes != 1 {
		t.Fatal("duplicate delete", err, deletes)
	}
	request.ID, request.Scope = webapi.ID(), "workspace:"+instance
	if _, err = broker.invoke(f.ctx, instance, request); err == nil || writes != 1 {
		t.Fatal("scope expanded", err)
	}
	request.Scope = "personal"
	f.exec(t, `DELETE FROM role_permissions WHERE role_id=(SELECT r.id FROM memberships m JOIN roles r ON r.org_id=m.org_id AND r.code=m.role WHERE m.org_id=$1 AND m.user_id=$2) AND permission_id='knowledge:manage'`, f.org, owner)
	if _, err = broker.invoke(f.ctx, instance, request); err == nil || writes != 1 {
		t.Fatal("write permission was not checked", err)
	}
	f.exec(t, `INSERT INTO role_permissions(role_id,permission_id) SELECT r.id,'knowledge:manage' FROM memberships m JOIN roles r ON r.org_id=m.org_id AND r.code=m.role WHERE m.org_id=$1 AND m.user_id=$2`, f.org, owner)
	f.exec(t, `UPDATE context_bindings SET enabled=false WHERE instance_id=$1`, instance)
	if _, err = broker.invoke(f.ctx, instance, request); err == nil || writes != 1 {
		t.Fatal("revoked write", err)
	}
}

func TestExtensionChannelRejectsSymlinksAndOversizedFiles(t *testing.T) {
	directory := t.TempDir()
	root, e := os.OpenRoot(directory)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	outside := filepath.Join(t.TempDir(), "secret")
	if os.WriteFile(outside, []byte("secret"), 0600) != nil {
		t.Fatal("fixture")
	}
	if e = os.Symlink(outside, filepath.Join(directory, "link")); e != nil {
		t.Fatal(e)
	}
	if _, e = privateChannelRead(root, "link"); e == nil {
		t.Fatal("symlink followed")
	}
	if e = os.WriteFile(filepath.Join(directory, "large"), make([]byte, (1<<20)+1), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = privateChannelRead(root, "large"); e == nil {
		t.Fatal("oversized file read")
	}
}

func TestWorkspaceRuntimeMCPFollowsBoundServersAndStableReceipts(t *testing.T) {
	f := newMonitorFixture(t)
	instance, owner := f.instance(t)
	source := webapi.ID()
	remoteCalls := 0
	remote := mcp.NewServer(&mcp.Implementation{Name: "runtime-fixture", Version: "1"}, nil)
	readonly := false
	mcp.AddTool(remote, &mcp.Tool{Name: "echo", Description: "Return text", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: &readonly}}, func(_ context.Context, _ *mcp.CallToolRequest, in struct {
		Text string `json:"text"`
	}) (*mcp.CallToolResult, any, error) {
		remoteCalls++
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: in.Text}}}, nil, nil
	})
	server := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return remote }, &mcp.StreamableHTTPOptions{Stateless: true}))
	defer server.Close()
	directory := t.TempDir()
	policy := filepath.Join(directory, "connectors.json")
	raw, _ := json.Marshal(map[string]any{"endpoints": []map[string]any{{"id": "fixture", "name": "Fixture", "url": server.URL, "allow_private": true, "allow_http": true}}})
	if os.WriteFile(policy, raw, 0600) != nil {
		t.Fatal("policy")
	}
	t.Setenv("HC_CONNECTOR_ENDPOINTS_FILE", policy)
	envelope, e := f.cipher.Encrypt([]byte("workspace-test-token"), security.Context{Environment: "test", OrganizationID: f.org, RecordID: source, Purpose: "connector-credential"})
	if e != nil {
		t.Fatal(e)
	}
	f.exec(t, `INSERT INTO mcp_connectors(id,org_id,owner_id,name,endpoint_id,protocol_version,auth_type,key_id,nonce,ciphertext) VALUES($1,$2,$3,'MCP','fixture','2026-07-28','bearer',$4,$5,$6)`, source, f.org, owner, envelope.KeyID, envelope.Nonce, envelope.Ciphertext)
	f.exec(t, `INSERT INTO workspace_extensions(instance_id,org_id,owner_id,auth_generation,configuration) VALUES($1,$2,$3,1,'{"items":[]}')`, instance, f.org, owner)
	broker := NewExtensionBroker(f.pool, directory, f.cipher, "test", false)
	principal := identity.Principal{WorkspaceID: instance, OrganizationID: f.org, UserID: owner, Generation: 1}
	connection, e := broker.connectors.Sync(f.ctx, principal, source, 1, "")
	if e != nil {
		t.Fatal(e)
	}
	connection, e = broker.connectors.SelectTools(f.ctx, principal, source, connection.Version, []string{"echo"}, "")
	if e != nil {
		t.Fatal(e)
	}
	snapshot := workspaceextensions.Snapshot{Items: []workspaceextensions.Item{{Kind: "connector", ID: source, Name: "MCP", Version: connection.Version, CatalogVersion: connection.CatalogVersion, Tools: []string{"echo"}}}}
	f.exec(t, `UPDATE workspace_extensions SET configuration=$2 WHERE instance_id=$1`, instance, marshalForTest(snapshot))
	request := extensionRequest{ID: webapi.ID(), Kind: "connector", SourceID: source, ExpectedVersion: connection.Version, CatalogVersion: connection.CatalogVersion, Tool: "echo", Arguments: map[string]any{"text": "runtime echo"}, Confirmed: true}
	first, e := broker.invoke(f.ctx, instance, request)
	if e != nil || remoteCalls != 1 || !strings.Contains(string(marshalForTest(first)), "runtime echo") {
		t.Fatal("runtime tool failed", e, remoteCalls)
	}
	replay, e := broker.invoke(f.ctx, instance, request)
	if e != nil || remoteCalls != 1 || !replay.(connectors.Call).Replayed {
		t.Fatal("duplicate effect on replay", e, remoteCalls)
	}
	servers, e := broker.mcpServers(f.ctx, instance)
	if e != nil || len(servers) != 1 || len(servers[0].Tools) != 1 || !strings.Contains(string(servers[0].Tools[0].Schema), "text") {
		t.Fatal("native catalog unavailable", servers, e)
	}
	if strings.Contains(string(marshalForTest(servers)), "workspace-test-token") {
		t.Fatal("catalog leaked credential")
	}
	if servers[0].Version != connection.Version || servers[0].CatalogVersion != connection.CatalogVersion {
		t.Fatal("native catalog missing confirmation versions", servers[0])
	}
	// New tools appear in the existing binding after the background sweep.
	mcp.AddTool(remote, &mcp.Tool{Name: "new_echo", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: &readonly}}, func(_ context.Context, _ *mcp.CallToolRequest, in struct {
		Text string `json:"text"`
	}) (*mcp.CallToolResult, any, error) {
		remoteCalls++
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: in.Text}}}, nil, nil
	})
	broker.refreshMCP(f.ctx)
	servers, e = broker.mcpServers(f.ctx, instance)
	if e != nil || len(servers) != 1 || len(servers[0].Tools) != 2 {
		t.Fatal("new tool not discovered", servers, e)
	}
	// A confirmation for an old definition cannot authorize the refreshed catalog.
	request.ID = webapi.ID()
	_, e = broker.invoke(f.ctx, instance, request)
	var problem *webapi.Problem
	if !errors.As(e, &problem) || problem.Status != 409 || remoteCalls != 1 {
		t.Fatal("stale catalog confirmation used", e, remoteCalls)
	}
	request.ExpectedVersion, request.CatalogVersion = servers[0].Version, servers[0].CatalogVersion
	request.ID, request.Tool = webapi.ID(), "new_echo"
	if _, e = broker.invoke(f.ctx, instance, request); e != nil || remoteCalls != 2 {
		t.Fatal("new tool not callable", e)
	}
	// Toggling the service retains the binding and tool preferences.
	var version int64
	if e = f.admin.QueryRow(f.ctx, `SELECT version FROM mcp_connectors WHERE id=$1`, source).Scan(&version); e != nil {
		t.Fatal(e)
	}
	connection, e = broker.connectors.SetEnabled(f.ctx, principal, source, version, false, "")
	if e != nil {
		t.Fatal(e)
	}
	servers, e = broker.mcpServers(f.ctx, instance)
	if e != nil || len(servers) != 0 {
		t.Fatal("disabled server advertised", servers, e)
	}
	connection, e = broker.connectors.SetEnabled(f.ctx, principal, source, connection.Version, true, "")
	if e != nil || !connection.Enabled || connection.Status != "READY" || len(connection.Tools) != 2 {
		t.Fatal("service did not reconnect", e)
	}
	request.ID = webapi.ID()
	_, e = broker.invoke(f.ctx, instance, request)
	if !errors.As(e, &problem) || problem.Status != 409 || remoteCalls != 2 {
		t.Fatal("stale service confirmation used after reconnect", e, remoteCalls)
	}
	request.ExpectedVersion, request.CatalogVersion = connection.Version, connection.CatalogVersion
	if _, e = broker.invoke(f.ctx, instance, request); e != nil || remoteCalls != 3 {
		t.Fatal("binding lost on reconnect", e)
	}
	request.Tool = "unselected"
	request.ID = webapi.ID()
	if _, e = broker.invoke(f.ctx, instance, request); e == nil || remoteCalls != 3 {
		t.Fatal("unselected tool invoked", e)
	}
	f.exec(t, `UPDATE mcp_connectors SET enabled=false WHERE id=$1`, source)
	request.Tool = "echo"
	if _, e = broker.invoke(f.ctx, instance, request); e == nil || remoteCalls != 3 {
		t.Fatal("disabled connector invoked", e)
	}
	f.exec(t, `UPDATE mcp_connectors SET enabled=true WHERE id=$1`, source)
	f.exec(t, `UPDATE memberships SET auth_generation=2 WHERE org_id=$1 AND user_id=$2`, f.org, owner)
	if _, e = broker.mcpServers(f.ctx, instance); e == nil {
		t.Fatal("catalog survived revoked binding authority")
	}
}

func TestExtensionCreateCheckpointRechecksAcceptedAuthority(t *testing.T) {
	f := newMonitorFixture(t)
	instance, owner := f.instance(t)
	snapshot := workspaceextensions.Snapshot{Items: []workspaceextensions.Item{{Kind: "plugin", ID: "session-search", Name: "Search", Package: "@deepseek-ai/dsh-experimental-session-search", PackageVersion: "0.2.1-alpha.2"}}}
	f.exec(t, `INSERT INTO workspace_extensions(instance_id,org_id,owner_id,auth_generation,configuration) VALUES($1,$2,$3,1,$4)`, instance, f.org, owner, marshalForTest(snapshot))
	operation := webapi.ID()
	intent := Intent{SchemaVersion: 1, ExpectedVersion: 1, Desired: "RUNNING", Reason: "USER_REQUEST", RequestID: "extension-checkpoint", AcceptedGeneration: ptrInt64(1), Target: mustSnapshot(t, f, instance)}
	f.exec(t, `INSERT INTO operations(id,instance_id,actor_id,type,status,stage,intent,trace_id,lease_owner,lease_token,lease_expires_at) VALUES($1,$2,$3,'CREATE','RUNNING','QUEUED',$4,'extension-checkpoint','extensions-worker',1,clock_timestamp()+interval '1 minute')`, operation, instance, owner, marshalForTest(intent))
	f.exec(t, `INSERT INTO outbox_events(id,operation_id,status) VALUES($1,$2,'PENDING')`, webapi.ID(), operation)
	lease := Lease{ID: operation, InstanceID: instance, Owner: "extensions-worker", Token: 1}
	store := NewExecutionStore(f.pool, f.cipher, "test")
	plan, err := store.Checkpoint(f.ctx, lease)
	if err != nil || len(plan.extensions.Items) != 1 {
		t.Fatal("restricted Worker could not apply accepted extensions", err)
	}
	plan.Destroy()
	f.exec(t, `UPDATE memberships SET auth_generation=2 WHERE org_id=$1 AND user_id=$2`, f.org, owner)
	if _, err = store.Checkpoint(f.ctx, lease); !errors.Is(err, ErrExecutionRevoked) {
		t.Fatal("accepted extension authority survived revocation", err)
	}
}

func TestWorkspaceNativeSessionMemoryOptInAndRevocation(t *testing.T) {
	for _, backend := range []string{"openviking", "mem0", "graphiti"} {
		t.Run(backend, func(t *testing.T) {
			f := newMonitorFixture(t)
			instance, owner := f.instance(t)
			source := webapi.ID()
			f.exec(t, `INSERT INTO role_permissions(role_id,permission_id) SELECT id,'memory:manage' FROM roles WHERE org_id=$1 AND builtin ON CONFLICT DO NOTHING`, f.org)
			namespaces := []string{}
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var input struct {
					Namespace string `json:"namespace"`
					contextservice.Action
				}
				if json.NewDecoder(r.Body).Decode(&input) != nil || input.SessionRequest == nil {
					t.Error("missing native request")
				}
				if input.Limit < 1 || input.Limit > 20 {
					t.Error("native request violates the bridge envelope limit")
				}
				namespaces = append(namespaces, input.Namespace)
				if !strings.HasSuffix(r.URL.Path, "/session") {
					t.Error("wrong native transport")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"status":200,"body":"{\"status\":\"ok\"}","headers":{"content-type":"application/json"}}`))
			}))
			defer remote.Close()
			directory := t.TempDir()
			proxyKey := filepath.Join(directory, "proxy-key")
			if err := os.WriteFile(proxyKey, []byte(strings.Repeat("k", 32)), 0600); err != nil {
				t.Fatal(err)
			}
			policy := filepath.Join(directory, "context.json")
			raw, _ := json.Marshal(map[string]any{"endpoints": []map[string]any{{"id": "native", "name": "Native", "backend": backend, "url": remote.URL, "allow_private_http": true, "data_use": "Native session fixture", "proxy_key_file": proxyKey}}})
			if err := os.WriteFile(policy, raw, 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HC_CONTEXT_ENDPOINTS_FILE", policy)
			envelope, err := f.cipher.Encrypt([]byte("native-credential"), security.Context{Environment: "test", OrganizationID: f.org, RecordID: source, Purpose: "context-credential"})
			if err != nil {
				t.Fatal(err)
			}
			f.exec(t, `INSERT INTO context_connections(id,org_id,owner_id,kind,backend,endpoint_id,name,status,key_id,nonce,ciphertext) VALUES($1,$2,$3,'memory',$7,'native','Native','READY',$4,$5,$6)`, source, f.org, owner, envelope.KeyID, envelope.Nonce, envelope.Ciphertext, backend)
			item := workspaceextensions.Item{Kind: "memory", ID: source, Name: "Native", Backend: backend, Version: 1, Scopes: []string{"personal", "workspace:" + instance}}
			snapshot := workspaceextensions.Snapshot{Items: []workspaceextensions.Item{item}}
			f.exec(t, `INSERT INTO workspace_extensions(instance_id,org_id,owner_id,auth_generation,configuration) VALUES($1,$2,$3,1,$4)`, instance, f.org, owner, marshalForTest(snapshot))
			f.exec(t, `INSERT INTO context_bindings(instance_id,org_id,owner_id,connection_id,scopes) VALUES($1,$2,$3,$4,$5)`, instance, f.org, owner, source, marshalForTest(item.Scopes))
			broker := NewExtensionBroker(f.pool, directory, f.cipher, "test", false)
			request := extensionRequest{ID: webapi.ID(), Kind: "memory", SourceID: source, Scope: "personal", Action: "session", SessionRequest: &contextservice.SessionRequest{Method: "POST", Path: "/api/v1/sessions", Body: `{"session_id":"dsh-first"}`}}
			if backend != "openviking" {
				request.SessionRequest = &contextservice.SessionRequest{Method: "POST", Path: "/sessions/messages", Body: `{}`}
			}
			if _, err = broker.invoke(f.ctx, instance, request); err == nil || len(namespaces) != 0 {
				t.Fatal("legacy selection gained capture", err)
			}
			item.SessionMemory = true
			item.SessionScope = "personal"
			snapshot.Items[0] = item
			f.exec(t, `UPDATE workspace_extensions SET configuration=$2 WHERE instance_id=$1`, instance, marshalForTest(snapshot))
			if _, err = broker.invoke(f.ctx, instance, request); err != nil || len(namespaces) != 1 {
				t.Fatal("native capture failed", err)
			}
			personal := namespaces[0]
			request.Scope = "workspace:" + instance
			if _, err = broker.invoke(f.ctx, instance, request); err == nil || len(namespaces) != 1 {
				t.Fatal("native scope expanded", err)
			}
			item.SessionScope = request.Scope
			snapshot.Items[0] = item
			f.exec(t, `UPDATE workspace_extensions SET configuration=$2 WHERE instance_id=$1`, instance, marshalForTest(snapshot))
			if _, err = broker.invoke(f.ctx, instance, request); err != nil || len(namespaces) != 2 || namespaces[1] != personal {
				t.Fatal("legacy session scope split the shared memory corpus", err)
			}
			secondInstance, _ := f.instance(t, owner)
			item.Scopes, item.SessionScope = []string{"personal"}, "personal"
			snapshot.Items[0] = item
			f.exec(t, `INSERT INTO workspace_extensions(instance_id,org_id,owner_id,auth_generation,configuration) VALUES($1,$2,$3,1,$4)`, secondInstance, f.org, owner, marshalForTest(snapshot))
			f.exec(t, `INSERT INTO context_bindings(instance_id,org_id,owner_id,connection_id,scopes) VALUES($1,$2,$3,$4,$5)`, secondInstance, f.org, owner, source, marshalForTest(item.Scopes))
			request.Scope = "personal"
			if _, err = broker.invoke(f.ctx, secondInstance, request); err != nil || len(namespaces) != 3 || namespaces[2] != personal {
				t.Fatal("two authorized workspaces did not share session memory", err)
			}
			request.Scope = "workspace:" + instance
			f.exec(t, `UPDATE context_bindings SET enabled=false WHERE instance_id=$1`, instance)
			if _, err = broker.invoke(f.ctx, instance, request); err == nil || len(namespaces) != 3 {
				t.Fatal("revoked capture continued", err)
			}
			request.Scope = "personal"
			if _, err = broker.invoke(f.ctx, secondInstance, request); err != nil || len(namespaces) != 4 || namespaces[3] != personal {
				t.Fatal("revoking one binding affected another workspace", err)
			}

		})
	}
}
