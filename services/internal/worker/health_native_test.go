package worker

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNativeHealthRequiresAnonymousDenialAndOriginalAuthorityCookie(t *testing.T) {
	host := "i-test.harness.test"
	cookie := []byte(nativeCookieName(host) + "=v1.synthetic.signature")
	for _, name := range []string{"valid", "anonymous-open", "cookie-rejected", "redirect", "sets-cookie", "foreign-cookie", "cancelled", "ws-rejected", "ws-anonymous-open", "ws-wrong-origin", "ws-invalid-accept"} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Host != host || (r.URL.Path != "/" && r.URL.Path != "/api/remote.mux") || r.URL.RawQuery != "" {
					t.Error("native health used an incorrect authority or private launch query")
				}
				if r.URL.Path == "/api/remote.mux" {
					if r.Header.Get("Upgrade") != "websocket" || r.Header.Get("Sec-WebSocket-Version") != "13" {
						t.Error("native health omitted the WebSocket handshake")
					}
					if r.Header.Get("Cookie") == "" && name != "ws-anonymous-open" {
						w.WriteHeader(401)
						return
					}
					if r.Header.Get("Origin") != "https://"+host && name != "ws-wrong-origin" {
						w.WriteHeader(403)
						return
					}
					if name == "ws-rejected" {
						w.WriteHeader(403)
						return
					}
					sum := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
					accept := base64.StdEncoding.EncodeToString(sum[:])
					if name == "ws-invalid-accept" {
						accept = "foreign"
					}
					w.Header().Set("Upgrade", "websocket")
					w.Header().Set("Connection", "Upgrade")
					w.Header().Set("Sec-WebSocket-Accept", accept)
					w.WriteHeader(101)
					return
				}
				if name == "sets-cookie" {
					w.Header().Set("Set-Cookie", "foreign=value")
				}
				if r.Header.Get("Cookie") == "" {
					if name == "anonymous-open" {
						w.WriteHeader(200)
					} else {
						w.WriteHeader(401)
					}
					return
				}
				if r.Header.Get("Cookie") != string(cookie) {
					t.Error("unexpected private credential")
				}
				switch name {
				case "cookie-rejected":
					w.WriteHeader(401)
				case "redirect":
					w.Header().Set("Location", "https://foreign.test")
					w.WriteHeader(303)
				default:
					w.WriteHeader(200)
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if name == "cancelled" {
				cancel()
			}
			supplied := cookie
			if name == "foreign-cookie" {
				supplied = []byte("dsh-auth-foreign=v1.synthetic.signature")
			}
			app, access := checkNativeHealth(ctx, server.URL, host, supplied)
			if name == "valid" {
				if !app || !access {
					t.Fatal("authenticated native runtime was rejected")
				}
			} else if access {
				t.Fatal("unsafe native access was reported healthy")
			}
		})
	}
}
