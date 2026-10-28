package connectors

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type echoArgs struct {
	Text string `json:"text"`
}

func testMCP(t *testing.T, versions ...string) (*mcp.Server, Endpoint, *atomic.Int32) {
	t.Helper()
	calls := &atomic.Int32{}
	server := mcp.NewServer(&mcp.Implementation{Name: "connector-fixture", Version: "1"}, &mcp.ServerOptions{PageSize: 1, SupportedProtocolVersions: versions})
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "Return the supplied text", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: ptr(false)}}, func(ctx context.Context, req *mcp.CallToolRequest, in echoArgs) (*mcp.CallToolResult, any, error) {
		calls.Add(1)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: in.Text}}}, nil, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true})
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-test-token" {
			http.Error(w, "denied", 401)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(httpServer.Close)
	return server, Endpoint{ID: "fixture", Name: "MCP fixture", URL: httpServer.URL, AllowPrivate: true, AllowHTTP: true}, calls
}
func ptr[T any](v T) *T { return &v }
func TestOfficialMCPInteroperability(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(version, func(t *testing.T) {
			_, endpoint, calls := testMCP(t)
			ctx, stop := context.WithTimeout(context.Background(), 8*time.Second)
			defer stop()
			session, err := openSession(ctx, endpoint, "private-test-token", version)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			tools, err := discover(ctx, session)
			if err != nil || len(tools) != 1 || tools[0].Name != "echo" || tools[0].Enabled || calls.Load() != 0 {
				t.Fatalf("discovery %v %v calls=%d", tools, err, calls.Load())
			}
			result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "echo", Arguments: map[string]any{"text": "中文测试"}})
			if err != nil || result.IsError || calls.Load() != 1 {
				t.Fatalf("call %v %v", result, err)
			}
			raw, _ := json.Marshal(result)
			if !strings.Contains(string(raw), "中文测试") {
				t.Fatal("content not preserved")
			}
			_, err = openSession(ctx, endpoint, "wrong", version)
			if !errors.Is(err, errAuth) {
				t.Fatal("authentication not identified", err)
			}
		})
	}
}
func TestAutomaticProtocolCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name, configured, negotiated string
		versions                     []string
	}{
		{"prefer-modern", "2026-07-28", "2026-07-28", []string{"2026-07-28", "2025-11-25"}},
		{"modern-only", "2026-07-28", "2026-07-28", []string{"2026-07-28"}},
		{"legacy-fallback", "2026-07-28", "2025-11-25", []string{"2025-11-25"}},
		{"preserve-legacy", "2025-11-25", "2025-11-25", []string{"2026-07-28", "2025-11-25"}},
		{"unsupported-version", "2026-07-28", "", []string{"2025-06-18"}},
		{"legacy-does-not-upgrade", "2025-11-25", "", []string{"2026-07-28"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, endpoint, calls := testMCP(t, tc.versions...)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			session, err := openSession(ctx, endpoint, "private-test-token", tc.configured)
			if tc.negotiated == "" {
				if err == nil {
					session.Close()
					t.Fatal("unsupported protocol accepted")
				}
				if calls.Load() != 0 {
					t.Fatal("negotiation executed a tool")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			if got := session.InitializeResult().ProtocolVersion; got != tc.negotiated {
				t.Fatalf("negotiated %s, want %s", got, tc.negotiated)
			}
			if _, err := discover(ctx, session); err != nil || calls.Load() != 0 {
				t.Fatalf("discovery failed or executed a tool: %v", err)
			}
			result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "echo", Arguments: map[string]any{"text": "compatibility"}})
			if err != nil || result.IsError || calls.Load() != 1 {
				t.Fatalf("tool call failed or repeated: %v, calls=%d", err, calls.Load())
			}
		})
	}
}
func TestConnectorAddressAndSchemaBoundaries(t *testing.T) {
	for _, address := range []string{"169.254.169.254", "::ffff:169.254.169.254", "0.0.0.0", "224.0.0.1", "100.64.0.1", "198.18.0.1", "2001:db8::1"} {
		for _, private := range []bool{false, true} {
			if allowedIP(netip.MustParseAddr(address), private) {
				t.Fatal("unsafe IP allowed", address)
			}
		}
	}
	for _, address := range []string{"127.0.0.1", "10.0.0.1", "::1", "fc00::1"} {
		if allowedIP(netip.MustParseAddr(address), false) || !allowedIP(netip.MustParseAddr(address), true) {
			t.Fatal("private policy not enforced", address)
		}
	}
	for _, raw := range []string{`{"type":"object","$ref":"http://127.0.0.1/schema"}`, `{"type":"object","$ref":"#"}`, `{"type":"array"}`, `{"type":"object","properties":{"x":{"type":"unknown"}}}`} {
		if _, err := schemaFor([]byte(raw)); err == nil {
			t.Fatal("unsafe schema accepted", raw)
		}
	}
	schema, err := schemaFor([]byte(`{"type":"object","properties":{"n":{"type":"integer","minimum":1}},"required":["n"],"additionalProperties":false}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []map[string]any{{}, {"n": "wrong"}, {"n": 0}, {"n": 2, "extra": true}} {
		if schema.Validate(v) == nil {
			t.Fatal("invalid arguments accepted", v)
		}
	}
	if schema.Validate(map[string]any{"n": 2}) != nil {
		t.Fatal("valid arguments rejected")
	}
}
func TestPolicyAndConfigImport(t *testing.T) {
	file := filepath.Join(t.TempDir(), "policy.json")
	raw := `{"endpoints":[{"id":"local","name":"Local","url":"http://127.0.0.1:8080/mcp","allow_private":true,"allow_http":true}]}`
	os.WriteFile(file, []byte(raw), 0600)
	if _, err := LoadPolicy(file); err != nil {
		t.Fatal(err)
	}
	os.Chmod(file, 0644)
	if _, err := LoadPolicy(file); err == nil {
		t.Fatal("public policy accepted")
	}
	for _, raw := range []string{`{"mcpServers":{"x":{"command":"npx","args":["unsafe"]}}}`, `{"format":"hc-mcp-v1","connections":[],"token":"secret"}`, `{"mcpServers":{"x":{"url":"https://example.com","type":"sse"}}}`} {
		if _, _, err := parsePackage([]byte(raw)); err == nil {
			t.Fatal("unsupported import accepted")
		}
	}
	pkg, warnings, err := parsePackage([]byte(`{"mcpServers":{"x":{"url":"https://example.com/mcp","type":"http","headers":{"Authorization":"Bearer imported-secret"}}}}`))
	if err != nil || len(pkg.Connections) != 1 || pkg.Connections[0].Auth != "bearer" || len(warnings) < 2 {
		t.Fatal(pkg, warnings, err)
	}
	out, _ := json.Marshal(pkg)
	if strings.Contains(string(out), "imported-secret") {
		t.Fatal("import preserved credential")
	}
}

func TestHTTPBoundaryRejectsRedirectsUntrustedTLSAndOversize(t *testing.T) {
	for _, mode := range []string{"redirect", "oversize", "stream-limit", "auth"} {
		t.Run(mode, func(t *testing.T) {
			var received atomic.Int32
			target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				received.Add(1)
				if r.Header.Get("Authorization") != "Bearer scoped-secret" || r.Header.Get("Cookie") != "" {
					t.Error("credential boundary")
				}
				switch mode {
				case "redirect":
					w.Header().Set("Location", "http://169.254.169.254/latest")
					w.WriteHeader(302)
				case "oversize":
					w.Header().Set("Content-Length", "2000000")
				case "stream-limit":
					w.(http.Flusher).Flush()
					io.WriteString(w, strings.Repeat("x", maxBytes+1))
				case "auth":
					w.WriteHeader(401)
				}
			}))
			defer target.Close()
			endpoint := Endpoint{ID: "tls", Name: "TLS", URL: target.URL, AllowPrivate: true}
			client, err := endpointClient(context.Background(), endpoint, "scoped-secret")
			if err != nil {
				t.Fatal(err)
			}
			req, _ := http.NewRequest("POST", target.URL, strings.NewReader("{}"))
			req.Header.Set("Cookie", "must-not-forward")
			if _, err = client.Do(req); err == nil {
				t.Fatal("untrusted certificate accepted")
			}
			transport := client.Transport.(*boundedTransport).base.(*http.Transport)
			roots := x509.NewCertPool()
			roots.AddCert(target.Certificate())
			transport.TLSClientConfig.RootCAs = roots
			response, err := client.Do(req)
			if mode == "stream-limit" {
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				if _, err = io.ReadAll(response.Body); err == nil {
					t.Fatal("unbounded response accepted")
				}
			} else if err == nil {
				response.Body.Close()
				t.Fatal("unsafe response accepted")
			}
			if received.Load() != 1 {
				t.Fatal("redirect followed or request retried")
			}
		})
	}
}

// Exercise real paginated protocol exchanges, including hostile server responses.
func TestDiscoveryPublishesOnlyCompleteSafeCatalogs(t *testing.T) {
	for _, mode := range []string{"complete", "loop", "unsafe-schema"} {
		t.Run(mode, func(t *testing.T) {
			server, endpoint, calls := testMCP(t)
			for _, name := range []string{"second", "third"} {
				server.AddTool(&mcp.Tool{Name: name, InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
					calls.Add(1)
					return &mcp.CallToolResult{}, nil
				})
			}
			pages := 0
			server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
				return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
					result, err := next(ctx, method, request)
					if method == "tools/list" && err == nil {
						pages++
						list := result.(*mcp.ListToolsResult)
						if mode == "loop" {
							list.NextCursor = "repeated"
						}
						if mode == "unsafe-schema" {
							list.Tools = []*mcp.Tool{{Name: "bad", InputSchema: map[string]any{"type": "object", "$ref": "http://169.254.169.254/schema"}}}
						}
					}
					return result, err
				}
			})
			ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			session, err := openSession(ctx, endpoint, "private-test-token", "2026-07-28")
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			tools, err := discover(ctx, session)
			if mode == "complete" {
				if err != nil || len(tools) != 3 || pages != 3 {
					t.Fatalf("incomplete catalog: tools=%d pages=%d err=%v", len(tools), pages, err)
				}
			} else if err == nil || tools != nil {
				t.Fatal("unsafe partial catalog published")
			}
			if calls.Load() != 0 {
				t.Fatal("discovery executed a tool")
			}
		})
	}
}
