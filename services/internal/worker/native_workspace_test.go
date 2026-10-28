package worker

import (
	"context"
	"encoding/json"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/config"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeWorkspaceReadinessRequiresUsablePersistedSession(t *testing.T) {
	for _, scenario := range []string{"ready", "session-unavailable", "no-session", "wrong-workspace", "files-unavailable", "rpc-error", "wrong-rpc", "missing-entries"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Host != "runtime.harness.test" || r.Header.Get("Origin") != "https://runtime.harness.test" || r.Header.Get("Cookie") != "native=synthetic" {
					t.Error("invalid native authorization")
				}
				var request struct {
					RPCID   string `json:"rpcId"`
					Method  string `json:"method"`
					Payload struct {
						Args map[string]any `json:"args"`
					} `json:"payload"`
				}
				if json.NewDecoder(r.Body).Decode(&request) != nil {
					t.Error("bad RPC")
					return
				}
				var value any
				switch request.Method {
				case "session/list":
					if scenario == "session-unavailable" {
						w.WriteHeader(503)
						return
					}
					items := []map[string]string{{"sessionId": "persisted-session", "cwd": "/data/workspace"}}
					if scenario == "no-session" {
						items = nil
					}
					if scenario == "wrong-workspace" {
						items[0]["cwd"] = "/other"
					}
					value = map[string]any{"items": items}
				case "workspaceFiles/list":
					if request.Payload.Args["workspaceFileScopeId"] != "persisted-session" || request.Payload.Args["path"] != "." {
						t.Error("wrong workspace scope")
					}
					if scenario == "files-unavailable" {
						w.WriteHeader(502)
						return
					}
					value = map[string]any{"path": "", "entries": []any{}, "truncated": false}
					if scenario == "missing-entries" {
						value = map[string]any{"path": ""}
					}
				default:
					t.Error("unexpected native operation")
				}
				if scenario == "wrong-rpc" {
					request.RPCID = "foreign"
				}
				json.NewEncoder(w).Encode(map[string]any{"type": "server-response", "rpcId": request.RPCID, "result": map[string]any{"ok": scenario != "rpc-error", "value": value}})
			}))
			defer server.Close()
			got := checkNativeWorkspace(context.Background(), server.URL, "runtime.harness.test", []byte("native=synthetic"))
			if got != (scenario == "ready") {
				t.Fatal("incorrect workspace readiness", got)
			}
			if scenario == "ready" && calls != 2 {
				t.Fatal("native reads missing or replayed", calls)
			}
		})
	}
}

func TestConfigurationUpdatesPersistedSessionSelections(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "updates", true: "rejects-failure"}[fail], func(t *testing.T) {
			selections := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					RPCID   string `json:"rpcId"`
					Method  string `json:"method"`
					Payload struct {
						Args map[string]json.RawMessage `json:"args"`
					} `json:"payload"`
				}
				if json.NewDecoder(r.Body).Decode(&request) != nil {
					t.Fatal("invalid RPC")
				}
				var value any
				ok := true
				switch request.Method {
				case "session/list":
					value = map[string]any{"items": []any{map[string]any{"sessionId": "old", "cwd": "/data/workspace", "agentAvailable": false}, map[string]any{"sessionId": "current", "cwd": "/data/workspace", "agentAvailable": true}, map[string]any{"sessionId": "subagent", "cwd": "/data/workspace", "agentAvailable": false}}}
				case "session/projections":
					var input struct {
						SessionID string `json:"sessionId"`
					}
					if json.Unmarshal(request.Payload.Args["request"], &input) != nil {
						t.Fatal("invalid projection request")
					}
					model := "old-model"
					if input.SessionID == "current" {
						model = "new-model"
					}
					var subagent any
					if input.SessionID == "subagent" {
						subagent = map[string]any{"owner": "parent"}
					}
					value = map[string]any{"kind": "sequenced", "values": map[string]any{"subagent": subagent, "modelSelection": map[string]any{"next": map[string]string{"provider": "platform-models", "model": model}}}}
				case "session/selectModel":
					selections++
					var selected struct {
						SessionID string `json:"sessionId"`
						Provider  string `json:"provider"`
						Model     string `json:"model"`
					}
					if json.Unmarshal(request.Payload.Args["request"], &selected) != nil || selected.SessionID != "old" || selected.Provider != "platform-models" || selected.Model != "new-model" {
						t.Error("wrong selection")
					}
					ok = !fail
					value = map[string]any{"selected": map[string]string{"provider": selected.Provider, "model": selected.Model}}
				case "workspaceFiles/list":
					value = map[string]any{"path": "", "entries": []any{}}
				default:
					t.Error("unexpected operation")
				}
				json.NewEncoder(w).Encode(map[string]any{"type": "server-response", "rpcId": request.RPCID, "result": map[string]any{"ok": ok, "value": value}})
			}))
			defer server.Close()
			if checkNativeWorkspace(context.Background(), server.URL, "runtime.harness.test", []byte("native=synthetic"), "platform-models", "new-model") == fail {
				t.Fatal("incorrect configuration readiness")
			}
			if selections != 1 {
				t.Fatal("selection repeated or unrelated session changed", selections)
			}
		})
	}
}

func TestNativeSessionSyncFixture(t *testing.T) {
	path := os.Getenv("HC_NATIVE_SYNC_FIXTURE")
	if path == "" {
		t.Skip("isolated runtime fixture required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(raw)
	var fixture struct{ Endpoint, Host, Cookie, Provider, Model string }
	if json.Unmarshal(raw, &fixture) != nil {
		t.Fatal("invalid fixture")
	}
	if !checkNativeWorkspace(context.Background(), fixture.Endpoint, fixture.Host, []byte(fixture.Cookie), fixture.Provider, fixture.Model) {
		t.Fatal("real native session synchronization failed")
	}
}

// Opt-in read-only check of the pinned runtime with its real native RPC stack.
func TestNativeWorkspaceReadinessOnRunningRuntime(t *testing.T) {
	instance := os.Getenv("HC_NATIVE_WORKSPACE_TEST_INSTANCE")
	if instance == "" {
		t.Skip("explicit running native runtime required")
	}
	if !uuidPattern.MatchString(instance) {
		t.Fatal("invalid contract instance")
	}
	options, err := LoadOptions(os.Getenv("HC_WORKER_CONFIG_FILE"))
	if err != nil {
		t.Fatal("private worker options unavailable")
	}
	host, err := NewLocalHost(options.Host)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := config.ReadPrivateFile(filepath.Join(options.Host.RuntimeRoot, instance, "native.json"))
	if err != nil {
		t.Fatal("native channel unavailable")
	}
	defer clear(raw)
	var channel nativeChannel
	if json.Unmarshal(raw, &channel) != nil || channel.InstanceID != instance {
		t.Fatal("native identity mismatch")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	address, err := dockerCommand(ctx, nil, "inspect", "--format", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", "hc-"+instance)
	if err != nil {
		t.Fatal("native container unavailable")
	}
	endpoint := "http://" + strings.TrimSpace(string(address)) + ":3080"
	cookie, _, err := exchangeNative(ctx, endpoint, host.authority(instance), channel.Token)
	channel.Token = ""
	if err != nil {
		t.Fatal("native exchange failed")
	}
	defer clear(cookie)
	if !checkNativeWorkspace(ctx, endpoint, host.authority(instance), cookie) {
		t.Fatal("native workspace RPC readiness failed")
	}
}
