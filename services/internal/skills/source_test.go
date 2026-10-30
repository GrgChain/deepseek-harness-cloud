package skills

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestSourceDownloadTransport(t *testing.T) {
	for _, scenario := range []string{"download", "redirect", "private-redirect", "dns-rebind", "mixed-dns", "dns-error", "tls-untrusted", "tls-hostname", "redirect-loop", "oversize", "oversize-chunked", "not-found", "cancel", "deadline"} {
		t.Run(scenario, func(t *testing.T) {
			requests, resolutions, dials := 0, 0, 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				switch {
				case scenario == "deadline":
					<-r.Context().Done()
				case scenario == "not-found":
					http.NotFound(w, r)
				case scenario == "oversize":
					w.Header().Set("Content-Length", fmt.Sprint(MaxUpload+1))
					_, _ = w.Write([]byte(strings.Repeat("x", MaxUpload+1)))
				case scenario == "oversize-chunked":
					w.(http.Flusher).Flush()
					_, _ = w.Write([]byte(strings.Repeat("x", MaxUpload+1)))
				case scenario == "private-redirect":
					http.Redirect(w, r, "https://127.0.0.1/private.md", http.StatusFound)
				case scenario == "redirect-loop":
					http.Redirect(w, r, "/skill.md", http.StatusFound)
				case r.URL.Path == "/skill.md" && (scenario == "redirect" || scenario == "dns-rebind"):
					http.Redirect(w, r, "/final.md", http.StatusFound)
				default:
					_, _ = w.Write([]byte("# skill\n"))
				}
			}))
			defer server.Close()
			transport := sourceTransport(func(ctx context.Context, network, host string) ([]netip.Addr, error) {
				resolutions++
				if host != "example.com" || network != "ip" {
					t.Errorf("unexpected resolution %s %s", network, host)
				}
				if scenario == "dns-error" {
					return nil, errors.New("DNS unavailable")
				}
				addresses := []netip.Addr{netip.MustParseAddr("1.1.1.1")}
				if scenario == "mixed-dns" || scenario == "dns-rebind" && resolutions > 1 {
					addresses = append(addresses, netip.MustParseAddr("127.0.0.1"))
				}
				return addresses, nil
			}, func(ctx context.Context, network, authority string) (net.Conn, error) {
				dials++
				if authority != "1.1.1.1:443" {
					t.Errorf("dial was not pinned to approved address: %s", authority)
				}
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			})
			if transport.Proxy != nil {
				t.Fatal("download must not use environment proxy")
			}
			if scenario != "tls-untrusted" {
				roots := x509.NewCertPool()
				roots.AddCert(server.Certificate())
				transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
				if scenario == "tls-hostname" {
					transport.TLSClientConfig.ServerName = "wrong.example"
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
			defer cancel()
			if scenario == "deadline" {
				var end context.CancelFunc
				ctx, end = context.WithTimeout(ctx, 50*time.Millisecond)
				defer end()
			}
			if scenario == "cancel" {
				cancel()
			}
			body, final, err := fetchSourceWithTransport(ctx, "https://example.com/skill.md", transport)
			if scenario == "download" || scenario == "redirect" {
				if err != nil || string(body) != "# skill\n" || !strings.HasPrefix(final, "https://example.com/") {
					t.Fatal("valid TLS download failed", err)
				}
				if scenario == "redirect" && (final != "https://example.com/final.md" || resolutions != 2 || dials != 2) {
					t.Fatal("redirect did not independently resolve and pin its target", final, resolutions, dials)
				}
			} else if scenario == "oversize" || scenario == "oversize-chunked" {
				if err != nil || len(body) != MaxUpload+1 {
					t.Fatal("large download rejected", err)
				}
			} else if err == nil || len(body) != 0 || final != "" {
				t.Fatal("unsafe or unavailable download succeeded")
			}
			if (scenario == "mixed-dns" || scenario == "dns-error") && dials != 0 {
				t.Fatal("connection attempted before all DNS answers were approved")
			}
			if (scenario == "private-redirect" || scenario == "dns-rebind") && requests != 1 {
				t.Fatal("redirect reached blocked target", requests)
			}
			if scenario == "redirect-loop" && requests != 4 {
				t.Fatal("redirect budget not enforced", requests)
			}
			if (scenario == "tls-untrusted" || scenario == "tls-hostname") && requests != 0 {
				t.Fatal("request sent before TLS trust verification")
			}
		})
	}
}
