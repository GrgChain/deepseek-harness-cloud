package skills

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestSourceProxyConfiguration(t *testing.T) {
	for _, body := range []string{`{"url":"http://127.0.0.1:20172"}`, `{"url":"http://[::1]:20172"}`, `{"url":"http://192.168.1.1:20172"}`, `{"url":"http://localhost:20172"}`, `{"url":"https://127.0.0.1:20172"}`, `{"url":"http://u:p@127.0.0.1:20172"}`, `{"url":"http://127.0.0.1:20172/path"}`, `{"url":"http://127.0.0.1:20172?x=y"}`, `{"url":"http://127.0.0.1:20172","extra":true}`} {
		t.Run(body, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "proxy.json")
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HC_SKILL_SOURCE_PROXY_FILE", path)
			_, err := configuredSourceDialer()
			valid := body == `{"url":"http://127.0.0.1:20172"}` || body == `{"url":"http://[::1]:20172"}`
			if (err == nil) != valid {
				t.Fatal("unexpected proxy policy result", err)
			}
			if valid {
				if err = os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
				if _, err = configuredSourceDialer(); err == nil {
					t.Fatal("publicly readable policy accepted")
				}
			}
		})
	}
}

func TestSourceProxyRejectsUnpinnedDestinations(t *testing.T) {
	calls := 0
	dial := connectSourceDialer("127.0.0.1:20172", func(context.Context, string, string) (net.Conn, error) { calls++; return nil, io.EOF })
	for _, target := range []string{"example.com:443", "127.0.0.1:443", "10.0.0.1:443", "169.254.169.254:443", "[::ffff:127.0.0.1]:443", "1.1.1.1:80", "1.1.1.1:8443"} {
		if _, err := dial(context.Background(), "tcp", target); err == nil {
			t.Fatal("unsafe CONNECT target accepted", target)
		}
	}
	if calls != 0 {
		t.Fatal("unsafe target reached proxy")
	}
}

func TestSourceProxyPreservesPinnedIPAndTLS(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "# public skill\n") }))
	defer origin.Close()
	var calls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodConnect || r.Host != "1.1.1.1:443" || r.Header.Get("Authorization") != "" || r.Header.Get("Proxy-Authorization") != "" {
			t.Error("proxy did not receive only the pinned address")
			w.WriteHeader(403)
			return
		}
		upstream, err := net.Dial("tcp", origin.Listener.Addr().String())
		if err != nil {
			t.Error(err)
			return
		}
		downstream, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			_ = upstream.Close()
			t.Error(err)
			return
		}
		defer downstream.Close()
		defer upstream.Close()
		_, _ = io.WriteString(downstream, "HTTP/1.1 200 Connection Established\r\n\r\n")
		go func() { _, _ = io.Copy(upstream, downstream); _ = upstream.Close() }()
		_, _ = io.Copy(downstream, upstream)
	}))
	defer proxy.Close()
	dial := connectSourceDialer(proxy.Listener.Addr().String(), (&net.Dialer{Timeout: time.Second}).DialContext)
	for _, scenario := range []string{"trusted", "untrusted", "mixed DNS"} {
		t.Run(scenario, func(t *testing.T) {
			before := calls.Load()
			transport := sourceTransport(func(context.Context, string, string) ([]netip.Addr, error) {
				ips := []netip.Addr{netip.MustParseAddr("1.1.1.1")}
				if scenario == "mixed DNS" {
					ips = append(ips, netip.MustParseAddr("127.0.0.1"))
				}
				return ips, nil
			}, dial)
			if scenario != "untrusted" {
				roots := x509.NewCertPool()
				roots.AddCert(origin.Certificate())
				transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
			}
			data, _, err := fetchSourceWithTransport(context.Background(), "https://example.com/SKILL.md", transport)
			if scenario == "trusted" {
				if err != nil || string(data) != "# public skill\n" {
					t.Fatal("pinned proxy TLS download failed", err)
				}
			} else if err == nil || len(data) != 0 {
				t.Fatal("proxy bypassed DNS or TLS validation")
			}
			if scenario == "mixed DNS" && calls.Load() != before {
				t.Fatal("private DNS answer reached proxy")
			}
		})
	}
}

func TestSourceProxyRefusalIsNotAFile(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(407)
		_, _ = io.WriteString(w, "proxy private diagnostic")
	}))
	defer proxy.Close()
	dial := connectSourceDialer(proxy.Listener.Addr().String(), (&net.Dialer{Timeout: time.Second}).DialContext)
	if conn, err := dial(context.Background(), "tcp", "1.1.1.1:443"); err == nil {
		conn.Close()
		t.Fatal("proxy refusal accepted")
	}
}
