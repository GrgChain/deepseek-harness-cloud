package worker

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNativeStartupRequiresFullHTTPAndWebsocketReadiness(t *testing.T) {
	const host = "i-test.harness.test:42013"
	for _, scenario := range []string{"ready", "websocket-not-ready", "origin-not-enforced", "http-lost-after-exchange", "empty-catalog", "wrong-default", "missing-model", "catalog-error"} {
		t.Run(scenario, func(t *testing.T) {
			authenticatedRoots := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Host != host {
					t.Error("startup probe changed the public authority")
				}
				if r.URL.Query().Get("token") != "" {
					http.SetCookie(w, &http.Cookie{Name: nativeCookieName(host), Value: "v1.synthetic.signature", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 3600, Expires: time.Now().Add(time.Hour)})
					w.Header().Set("Location", "./")
					w.WriteHeader(303)
					return
				}
				if r.Header.Get("Cookie") == "" {
					w.WriteHeader(401)
					return
				}
				if r.URL.Path == "/api/session/modelCatalog" {
					if r.Method != "POST" || r.Header.Get("Origin") != "https://"+host {
						t.Error("invalid catalog request")
					}
					var request struct {
						RPCID  string `json:"rpcId"`
						Method string `json:"method"`
					}
					if json.NewDecoder(r.Body).Decode(&request) != nil || request.Method != "session/modelCatalog" {
						t.Error("unexpected RPC")
					}
					if scenario == "catalog-error" {
						w.WriteHeader(503)
						return
					}
					selected := "approved-model"
					if scenario == "wrong-default" {
						selected = "another-model"
					}
					models := []map[string]string{{"id": "approved-model"}}
					if scenario == "missing-model" {
						models = nil
					}
					groups := []any{map[string]any{"id": "platform-models", "models": models}}
					if scenario == "empty-catalog" {
						groups = nil
					}
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(map[string]any{"type": "server-response", "rpcId": request.RPCID, "result": map[string]any{"ok": true, "value": map[string]any{"default": map[string]string{"provider": "platform-models", "model": selected}, "routableProviders": []string{"platform-models"}, "groups": groups}}})
					return
				}
				if r.URL.Path == "/" {
					authenticatedRoots++
					if scenario == "http-lost-after-exchange" && authenticatedRoots > 1 {
						w.WriteHeader(503)
					}
					return
				}
				if scenario == "websocket-not-ready" {
					w.WriteHeader(503)
					return
				}
				if r.Header.Get("Origin") != "https://"+host && scenario != "origin-not-enforced" {
					w.WriteHeader(403)
					return
				}
				sum := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
				w.Header().Set("Upgrade", "websocket")
				w.Header().Set("Connection", "Upgrade")
				w.Header().Set("Sec-WebSocket-Accept", base64.StdEncoding.EncodeToString(sum[:]))
				w.WriteHeader(101)
			}))
			defer server.Close()
			cookie, expires, err := exchangeReadyNative(context.Background(), server.URL, host, "synthetic-launch-token", "platform-models", "approved-model")
			if scenario == "ready" {
				if err != nil || len(cookie) == 0 || !expires.After(time.Now()) {
					t.Fatal("ready runtime rejected")
				}
			} else {
				expected := ErrNativeUnavailable
				if scenario == "empty-catalog" || scenario == "wrong-default" || scenario == "missing-model" || scenario == "catalog-error" {
					expected = ErrNativeModelUnavailable
				}
				if err != expected || len(cookie) != 0 || !expires.IsZero() {
					t.Fatal("startup published incomplete readiness")
				}
			}
		})
	}
}

func TestNativeCatalogRejectsUntrustedOrIncompleteResponses(t *testing.T) {
	const host = "i-test.harness.test:42013"
	cookie := []byte(nativeCookieName(host) + "=v1.synthetic.signature")
	for _, scenario := range []string{"malformed", "oversized", "redirect", "cookie-change", "canceled", "wrong-rpc", "rpc-error", "not-routable"} {
		t.Run(scenario, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/session/modelCatalog" {
					t.Error("probe called an unexpected endpoint")
					w.WriteHeader(404)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				switch scenario {
				case "oversized":
					w.Write([]byte(strings.Repeat(" ", 128<<10) + "{}"))
				case "redirect":
					w.Header().Set("Location", "/inference")
					w.WriteHeader(302)
				case "cookie-change":
					w.Header().Set("Set-Cookie", "other=value")
					w.Write([]byte(`{}`))
				case "wrong-rpc":
					w.Write([]byte(`{"type":"server-response","rpcId":"another","result":{"ok":true}}`))
				case "rpc-error":
					w.Write([]byte(`{"type":"server-response","rpcId":"harness-model-readiness","result":{"ok":false}}`))
				case "not-routable":
					w.Write([]byte(`{"type":"server-response","rpcId":"harness-model-readiness","result":{"ok":true,"value":{"default":{"provider":"platform-models","model":"approved-model"},"groups":[{"id":"platform-models","models":[{"id":"approved-model"}]}],"routableProviders":[]}}}`))
				default:
					w.Write([]byte(`{`))
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if scenario == "canceled" {
				cancel()
			}
			if checkNativeModel(ctx, server.URL, host, cookie, "platform-models", "approved-model") {
				t.Fatal("invalid catalog passed readiness")
			}
		})
	}
	if checkNativeModel(context.Background(), "https://untrusted.invalid", host, cookie, "platform-models", "approved-model") {
		t.Fatal("uninspected endpoint accepted")
	}
	if failureCode(ErrNativeModelUnavailable) != "MODEL_UNAVAILABLE" || !validFailure("MODEL_UNAVAILABLE") {
		t.Fatal("model readiness failure lost its safe diagnostic")
	}
}
