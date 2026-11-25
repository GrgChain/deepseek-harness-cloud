package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHealthAndClosedBusinessRoutes(t *testing.T) {
	for _, role := range []string{"api", "worker", "gateway"} {
		t.Run(role, func(t *testing.T) {
			h := New(role, func(context.Context) error { return errors.New("secret-dsn-must-not-leak") })
			for _, tc := range []struct {
				method, path string
				status       int
			}{
				{"GET", "/livez", 200}, {"GET", "/readyz", 503}, {"GET", "/v1/me", 401},
				{"POST", "/v1/instances", 401}, {"GET", "/auth/login", 503}, {"GET", "/unknown", 404},
			} {
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
				if w.Code != tc.status {
					t.Fatalf("%s: got %d want %d", tc.path, w.Code, tc.status)
				}
				if strings.Contains(w.Body.String(), "secret-dsn") {
					t.Fatal("readiness leaked dependency details")
				}
				if w.Header().Get("Cache-Control") != "no-store" {
					t.Fatal("missing no-store")
				}
			}
		})
	}
}

func TestReadinessUsesRequestContext(t *testing.T) {
	h := New("api", func(ctx context.Context) error { return ctx.Err() })
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if w.Code != 200 {
		t.Fatal("healthy dependencies rejected")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil).WithContext(ctx))
	if w.Code != 503 {
		t.Fatal("cancelled probe accepted")
	}
}

func TestRouteBudgetBoundsAllEndpoints(t *testing.T) {
	cases := []struct {
		method    string
		path      string
		wantWrite time.Duration
		wantRead  time.Duration
	}{
		{http.MethodGet, "/v1/instances", 9 * time.Second, 0},
		{http.MethodGet, "/v1/skills", 20 * time.Second, 0},
		{http.MethodPost, "/v1/skills:source", 125 * time.Second, 0},
		{http.MethodPost, "/v1/skills/abc:check-update", 125 * time.Second, 0},
		{http.MethodPost, "/v1/skills:preview", 300 * time.Second, 60 * time.Second},
		{http.MethodPost, "/v1/skills:apply", 300 * time.Second, 60 * time.Second},
		{http.MethodPost, "/v1/skills:export", 300 * time.Second, 60 * time.Second},
		{http.MethodPost, "/v1/skills/abc:previous", 300 * time.Second, 60 * time.Second},
		{http.MethodPost, "/v1/other/abc:previous", 9 * time.Second, 0},
		{http.MethodPost, "/v1/context/knowledge/parse", 300 * time.Second, 60 * time.Second},
		{http.MethodPost, "/v1/context/memory/123/export", 95 * time.Second, 60 * time.Second},
		{http.MethodPost, "/v1/context/memory/123/preview", 95 * time.Second, 60 * time.Second},
		{http.MethodGet, "/v1/context/memory", 20 * time.Second, 0},
		{http.MethodGet, "/v1/connectors", 125 * time.Second, 0},
		{http.MethodGet, "/v1/storage-assets/123/files", 65 * time.Second, 0},
		{http.MethodGet, "/v1/storage-assets/123/file", 65 * time.Second, 0},
		{http.MethodPost, "/v1/model-configuration:validate", 35 * time.Second, 0},
		{http.MethodPost, "/v1/my-models/123:validate", 35 * time.Second, 0},
		{http.MethodPost, "/v1/model-providers/openai:sync", 35 * time.Second, 0},
		{http.MethodPost, "/v1/my-provider-accounts/123:sync", 35 * time.Second, 0},
		{http.MethodPost, "/v1/plugins", 25 * time.Second, 20 * time.Second},
		{http.MethodPost, "/v1/plugins:preview", 25 * time.Second, 20 * time.Second},
	}
	for _, tc := range cases {
		got := routeBudget(tc.method, tc.path)
		if got.write != tc.wantWrite || got.read != tc.wantRead {
			t.Errorf("%s %s: got (%v, %v), want (%v, %v)", tc.method, tc.path, got.write, got.read, tc.wantWrite, tc.wantRead)
		}
		if got.write <= 0 {
			t.Errorf("%s %s: write timeout must be strictly bounded", tc.method, tc.path)
		}
	}
}
