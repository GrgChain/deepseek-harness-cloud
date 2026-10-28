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

func TestWorkspaceRuntimeMCPUsesFrozenToolAuthorizationAndStableReceipts(t *testing.T) {
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
	request := extensionRequest{ID: webapi.ID(), Kind: "connector", SourceID: source, Tool: "echo", Arguments: map[string]any{"text": "runtime echo"}, Confirmed: true}
	first, e := broker.invoke(f.ctx, instance, request)
	if e != nil || remoteCalls != 1 || !strings.Contains(string(marshalForTest(first)), "runtime echo") {
		t.Fatal("runtime tool failed", e, remoteCalls)
	}
	replay, e := broker.invoke(f.ctx, instance, request)
	if e != nil || remoteCalls != 1 || !replay.(connectors.Call).Replayed {
		t.Fatal("duplicate effect on replay", e, remoteCalls)
	}
	request.Tool = "unselected"
	request.ID = webapi.ID()
	if _, e = broker.invoke(f.ctx, instance, request); e == nil || remoteCalls != 1 {
		t.Fatal("unselected tool invoked", e)
	}
	f.exec(t, `UPDATE mcp_connectors SET enabled=false WHERE id=$1`, source)
	request.Tool = "echo"
	if _, e = broker.invoke(f.ctx, instance, request); e == nil || remoteCalls != 1 {
		t.Fatal("disabled connector invoked", e)
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
