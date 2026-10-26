package identity

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReturnPathRejectsRedirectBypasses(t *testing.T) {
	for _, path := range []string{"https://evil.example", "//evil.example", "/app//evil", "/app/../auth", "/app/%2f%2fevil", "/app/instances?next=x", "/app/instances#x", "/app/\\evil"} {
		if _, err := ReturnPath(path); err == nil {
			t.Fatalf("accepted %q", path)
		}
	}
	for _, path := range []string{"", "/app/instances", "/app/settings", "/app/instances/00000000-0000-4000-8000-000000000001"} {
		if _, err := ReturnPath(path); err != nil {
			t.Fatalf("rejected %q", path)
		}
	}
}

func TestMappingValidatesIdentityWithoutEmailLinking(t *testing.T) {
	valid := `{"schema_version":1,"organization_id":"00000000-0000-4000-8000-000000000001","members":[{"issuer":"https://idp.example","subject":"subject","role":"ADMIN","workspace_owner_id":"00000000-0000-4000-8000-000000000002"}]}`
	if _, err := ParseMapping([]byte(valid), "https://idp.example", "00000000-0000-4000-8000-000000000001"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		strings.Replace(valid, `"ADMIN"`, `"OWNER"`, 1),
		strings.Replace(valid, `"subject":"subject"`, `"subject":""`, 1),
		strings.Replace(valid, `"schema_version":1`, `"schema_version":2`, 1),
		strings.Replace(valid, `"role":"ADMIN"`, `"email":"a@example.com","role":"ADMIN"`, 1),
		valid + ` {}`,
		strings.Replace(valid, `"schema_version":1`, `"schema_version":1,"schema_version":1`, 1),
	} {
		if _, err := ParseMapping([]byte(bad), "https://idp.example", "00000000-0000-4000-8000-000000000001"); err == nil {
			t.Fatal("invalid mapping accepted")
		}
	}
}

type closedService struct{}

func (closedService) Login(context.Context, string, string) (LoginResult, error) {
	return LoginResult{}, ErrUnavailable
}
func (closedService) Callback(context.Context, string, string, string, string) (CallbackResult, error) {
	return CallbackResult{}, ErrUnauthenticated
}
func (closedService) Authenticate(context.Context, string) (Principal, error) {
	return Principal{}, ErrUnauthenticated
}
func (closedService) Logout(context.Context, Principal, string, string) error {
	return errors.New("must not be called")
}

func TestHTTPIdentityNeverTrustsUserHeaders(t *testing.T) {
	h := NewHandler(closedService{})
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/v1/me", 401}, {"POST", "/auth/logout", 401}, {"GET", "/auth/login", 503},
		{"GET", "/auth/callback?state=one&state=two&code=secret", 401},
		{"GET", "/auth/callback?state=one&code=secret&error=denied", 401},
	} {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		r.Header.Set("X-User-ID", "00000000-0000-4000-8000-000000000001")
		r.Header.Set("Authorization", "Bearer forged")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s: got %d", tc.path, w.Code)
		}
		if strings.Contains(w.Body.String(), "secret") {
			t.Fatal("callback leaked code")
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("identity response can be cached")
		}
	}
}

func TestCookiesUseSecureHostBoundary(t *testing.T) {
	w := httptest.NewRecorder()
	setCookie(w, SessionCookie, "opaque", 3600)
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("missing cookie")
	}
	c := cookies[0]
	if !c.Secure || !c.HttpOnly || c.Domain != "" || c.Path != "/" || c.SameSite != http.SameSiteLaxMode {
		t.Fatal("insecure session cookie")
	}
}

func TestExpiredCallbackRestartsBrowserLoginWithoutCreatingSession(t *testing.T) {
	h := NewHandler(closedService{})
	for _, path := range []string{
		"/auth/callback?state=expired&code=private-code",
		"/auth/callback?state=one&state=two&code=private-code",
		"/auth/callback?state=expired&error=access_denied",
	} {
		for _, accept := range []string{"text/html,application/xhtml+xml", "application/json"} {
			r := httptest.NewRequest(http.MethodGet, path, nil)
			r.Header.Set("Accept", accept)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if accept == "application/json" {
				if w.Code != 401 || !strings.Contains(w.Body.String(), "UNAUTHENTICATED") {
					t.Fatal("API authentication failures must remain JSON 401")
				}
			} else {
				if w.Code != 303 || w.Header().Get("Location") != "/auth/login" {
					t.Fatal("expired browser callback did not restart login")
				}
				cleared := map[string]bool{}
				for _, c := range w.Result().Cookies() {
					if c.Name == SessionCookie || c.MaxAge >= 0 || c.Value != "" {
						t.Fatal("failed callback must only clear handshake cookies")
					}
					cleared[c.Name] = true
				}
				if !cleared[LoginCookie] || !cleared[ProviderCookie] {
					t.Fatal("stale login cookies retained")
				}
			}
			if strings.Contains(w.Body.String(), "private-code") || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("callback leaked credentials or could be cached")
			}
		}
	}
}
