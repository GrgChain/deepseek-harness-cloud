package admission

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/access"
)

func TestRuntimeHTTPObservationsRejectRedirectsAndMismatchedWorkerAuthority(t *testing.T) {
	rules, err := access.NewRules("https://portal.harness.localhost:42013", "harness.localhost")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		body   string
		status int
		valid  bool
	}{
		{`{"schema_version":1,"domain":"harness.localhost","public_port":42013,"runtime_image":"sha256:` + strings.Repeat("a", 64) + `","workspace":true,"docker":true,"storage":true}`, 200, true},
		{`{"schema_version":1,"domain":"foreign.test","public_port":42013,"runtime_image":"sha256:` + strings.Repeat("a", 64) + `","workspace":true,"docker":true,"storage":true}`, 200, false},
		{`{"schema_version":1,"domain":"harness.localhost","public_port":443,"runtime_image":"sha256:` + strings.Repeat("a", 64) + `","workspace":true,"docker":true,"storage":true}`, 200, false},
		{`{"schema_version":1,"domain":"harness.localhost","public_port":42013,"runtime_image":"latest","workspace":true,"docker":true,"storage":true}`, 200, false},
		{`{"schema_version":1,"schema_version":1}`, 200, false},
		{strings.Repeat("x", 8193), 200, false},
		{`{}`, 302, false},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
				t.Fatal("credential leaked to observation")
			}
			w.Header().Set("Location", "http://127.0.0.1:1/private")
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		probe, err := newHTTPChecks(rules, "local", server.URL, server.URL, "")
		if err != nil {
			server.Close()
			t.Fatal(err)
		}
		_, err = probe.worker(context.Background())
		probe.Close()
		server.Close()
		if (err == nil) != tc.valid {
			t.Fatalf("worker observation status %d validity %v: %v", tc.status, tc.valid, err)
		}
	}
	for _, endpoint := range []string{"http://example.com:18081", "http://127.0.0.1:18081/private", "http://127.0.0.1:18081?token=secret", "https://127.0.0.1:18081", "http://user:pass@127.0.0.1:18081"} {
		if probe, err := newHTTPChecks(rules, "local", endpoint, "http://127.0.0.1:18082", ""); err == nil {
			probe.Close()
			t.Fatal("uncontrolled probe endpoint accepted")
		}
	}
}
