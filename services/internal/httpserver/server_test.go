package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
