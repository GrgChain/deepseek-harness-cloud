package access

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type browserService struct {
	target                      Target
	cookie                      []byte
	challenges, redeems, checks int
	fail                        error
	previous                    string
}

func (s *browserService) ReplaceChallenge(ctx context.Context, host, previous string) (Challenge, string, error) {
	s.challenges++
	s.previous = previous
	return Challenge{webapi.ID(), time.Now().Add(time.Minute)}, strings.Repeat("b", 43), s.fail
}
func (s *browserService) Redeem(ctx context.Context, host, ticket, challenge, browser, trace string) (string, Authorization, error) {
	s.redeems++
	return strings.Repeat("s", 43), Authorization{Target: s.target, ExpiresAt: time.Now().Add(30 * time.Minute)}, s.fail
}
func (s *browserService) CheckSession(ctx context.Context, host, secret string, activity bool) (Authorization, error) {
	s.checks++
	return Authorization{Target: s.target, ExpiresAt: time.Now().Add(time.Minute), Cookie: append([]byte(nil), s.cookie...)}, s.fail
}

func TestGatewayBrowserBoundaryBeforeUpstream(t *testing.T) {
	rules, _ := NewRules("https://portal.harness.test:42013", "harness.test")
	id := webapi.ID()
	host := rules.Host(id)
	svc := &browserService{target: Target{InstanceID: id, Host: host, IP: "172.18.0.5"}}
	var limits []int
	g := NewGateway(rules, webapi.ID(), svc, func(ctx context.Context, key string, n int) error { limits = append(limits, n); return nil })
	defer g.Close()
	request := func(method, path, origin, body, kind, cookie string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://"+rules.authority(host)+path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:44000"
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		r.Header.Set("Content-Type", kind)
		if cookie != "" {
			r.Header.Set("Cookie", cookie)
		}
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		return w
	}
	for _, origin := range []string{"", "https://foreign.test", rules.PortalOrigin + ".evil"} {
		if w := request("GET", "/__dsh/challenge", origin, "", "", ""); w.Code != 403 || w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("challenge allowed foreign origin")
		}
	}
	w := request("GET", "/__dsh/challenge", rules.PortalOrigin, "", "", ChallengeCookie+"="+strings.Repeat("p", 43))
	if w.Code != 200 || svc.challenges != 1 || svc.previous != strings.Repeat("p", 43) || w.Header().Get("Access-Control-Allow-Origin") != rules.PortalOrigin || w.Header().Get("Access-Control-Allow-Credentials") != "true" || len(limits) != 2 || limits[0] != 30 || limits[1] != 300 {
		t.Fatal("challenge binding, exact CORS or limits missing")
	}
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name != ChallengeCookie || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Domain != "" || cookie.Path != "/" || cookie.MaxAge > 60 {
			t.Fatal("challenge cookie is not restricted to this host")
		}
	}
	form := url.Values{"ticket": {strings.Repeat("t", 43)}, "challenge_id": {webapi.ID()}}.Encode()
	browser := ChallengeCookie + "=" + strings.Repeat("b", 43)
	for _, bad := range []struct{ origin, kind, form, cookie string }{{"https://other.harness.test:42013", "application/x-www-form-urlencoded", form, browser}, {rules.PortalOrigin, "application/json", form, browser}, {rules.PortalOrigin, "application/x-www-form-urlencoded", form, ""}, {rules.PortalOrigin, "application/x-www-form-urlencoded", form + "&ticket=duplicate", browser}, {rules.PortalOrigin, "application/x-www-form-urlencoded", form + "&extra=value", browser}, {rules.PortalOrigin, "application/x-www-form-urlencoded", form, browser + "; " + browser}, {rules.PortalOrigin, "application/x-www-form-urlencoded", strings.Repeat("x", 8193), browser}} {
		if w := request("POST", "/__dsh/handoff", bad.origin, bad.form, bad.kind, bad.cookie); w.Code < 400 {
			t.Fatal("invalid handoff accepted")
		}
	}
	if svc.redeems != 0 {
		t.Fatal("invalid handoff reached credential redemption")
	}
	w = request("POST", "/__dsh/handoff", rules.PortalOrigin, form, "application/x-www-form-urlencoded", browser)
	if w.Code != 303 || w.Header().Get("Location") != "/" || svc.redeems != 1 {
		t.Fatal("valid handoff did not redirect to fixed root")
	}
	found := false
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == SessionCookie {
			found = true
			if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Domain != "" || cookie.Path != "/" || cookie.MaxAge > 1800 {
				t.Fatal("access session cookie weakened")
			}
		}
	}
	if !found {
		t.Fatal("access cookie missing")
	}
	for _, tc := range []struct{ method, path, origin string }{{"POST", "/api/action", ""}, {"POST", "/api/action", rules.PortalOrigin}, {"DELETE", "/api/action", "https://other.harness.test:42013"}, {"CONNECT", "/", ""}, {"TRACE", "/", ""}, {"GET", "/?token=secret", ""}, {"GET", "/?%74oken=secret", ""}} {
		if w := request(tc.method, tc.path, tc.origin, "", "", SessionCookie+"="+strings.Repeat("s", 43)); w.Code < 400 {
			t.Fatal("proxy allowed forbidden method, credential query or Origin")
		}
	}
	if svc.checks != 0 {
		t.Fatal("invalid proxy request reached session authorization")
	}
}

func TestGatewayConnectionConfirmationHasNoAuthorizationSideEffects(t *testing.T) {
	rules, _ := NewRules("https://portal.harness.localhost:42013", "harness.localhost")
	id := webapi.ID()
	svc := &browserService{}
	limits := 0
	g := NewGateway(rules, webapi.ID(), svc, func(context.Context, string, int) error { limits++; return nil })
	defer g.Close()
	r := httptest.NewRequest("GET", rules.URL(rules.Host(id))+"/__dsh/connect", nil)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != 200 || w.Header().Get("Content-Type") != "text/html; charset=utf-8" || !strings.Contains(w.Body.String(), `http-equiv="refresh"`) || !strings.Contains(w.Body.String(), "?resume_workspace=1") || strings.Contains(w.Body.String(), "再次点击") || strings.Contains(w.Body.String(), "开发证书") || !strings.Contains(w.Body.String(), rules.PortalOrigin+"/app/instances/"+id) {
		t.Fatal("connection confirmation page missing")
	}
	if svc.challenges != 0 || svc.redeems != 0 || svc.checks != 0 || limits != 0 || len(w.Result().Cookies()) != 0 || w.Header().Get("Content-Security-Policy") != connectionCSP {
		t.Fatal("public confirmation weakened authentication or browser isolation")
	}
	for _, tc := range []struct{ method, path string }{{"POST", "/__dsh/connect"}, {"GET", "/__dsh/connect?ticket=private"}} {
		w := httptest.NewRecorder()
		g.ServeHTTP(w, httptest.NewRequest(tc.method, rules.URL(rules.Host(id))+tc.path, nil))
		if w.Code < 400 || strings.Contains(w.Body.String(), "private") {
			t.Fatal("connection confirmation accepts credentials or unsafe methods")
		}
	}
}

func TestProxyNeverForwardsBrowserIdentityOrUpstreamCookie(t *testing.T) {
	rules, _ := NewRules("https://portal.harness.test:42013", "harness.test")
	id := webapi.ID()
	host := rules.Host(id)
	native := []byte("dsh-auth-" + base64Host(rules.authority(host)) + "=v1.synthetic.signature")
	svc := &browserService{target: Target{InstanceID: id, Host: host, IP: "172.18.0.5"}, cookie: native}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != rules.authority(host) || r.Header.Get("Cookie") != string(native) || r.Header.Get("Authorization") != "" || r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("Forwarded") != "" || r.Header.Get("Coder-Session-Token") != "" {
			t.Error("browser headers or incorrect authority reached native runtime")
		}
		w.Header().Set("Set-Cookie", "native=leak")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("native response"))
	}))
	defer upstream.Close()
	g := NewGateway(rules, webapi.ID(), svc, nil)
	defer g.Close()
	g.transport.DialContext = testDial(upstream)
	r := httptest.NewRequest("GET", "https://"+rules.authority(host)+"/assets/script.js", nil)
	r.Header.Set("Cookie", SessionCookie+"="+strings.Repeat("s", 43)+"; malicious=identity")
	r.Header.Set("Authorization", "Bearer browser")
	r.Header.Set("Coder-Session-Token", "browser-control")
	r.Header.Set("X-Forwarded-For", "untrusted")
	r.Header.Set("Forwarded", "host=foreign.test")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != 200 || w.Body.String() != "native response" || w.Header().Get("Set-Cookie") != "" || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("native response identity leaked or proxy failed")
	}
	if w.Header().Get("Content-Type") != "text/plain" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("proxy corrupted response metadata")
	}
	// Streaming requests have a fixed body bound even when Content-Length is absent.
	r = httptest.NewRequest("POST", "https://"+rules.authority(host)+"/api/action", io.NopCloser(strings.NewReader(strings.Repeat("x", 10<<20+1))))
	r.ContentLength = -1
	r.Header.Set("Origin", rules.URL(host))
	r.Header.Set("Cookie", SessionCookie+"="+strings.Repeat("s", 43))
	w = httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code < 400 {
		t.Fatal("unbounded request body reached upstream")
	}
}

func TestBrowserProxyAllowsColdPageInitialization(t *testing.T) {
	rules, _ := NewRules("https://portal.harness.test:42013", "harness.test")
	id := webapi.ID()
	host := rules.Host(id)
	svc := &browserService{target: Target{InstanceID: id, Host: host, IP: "172.18.0.5"}, cookie: []byte("dsh-auth-" + base64Host(rules.authority(host)) + "=v1.synthetic.signature")}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Cold page construction exceeds the separate two-second probe budget.
		time.Sleep(2200 * time.Millisecond)
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<div id=\"root\">ready</div>"))
	}))
	defer upstream.Close()
	g := NewGateway(rules, webapi.ID(), svc, nil)
	defer g.Close()
	g.transport.DialContext = testDial(upstream)
	r := httptest.NewRequest("GET", rules.URL(host)+"/", nil)
	r.Header.Set("Cookie", SessionCookie+"="+strings.Repeat("s", 43))
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "ready") {
		t.Fatalf("cold page failed: status=%d", w.Code)
	}
	probe := fixedTransport()
	defer probe.CloseIdleConnections()
	if probe.ResponseHeaderTimeout != 2*time.Second {
		t.Fatal("browser budget changed the native verification budget")
	}
}

func TestProxyTransportFailureIsNotAuthenticationFailure(t *testing.T) {
	rules, _ := NewRules("https://portal.harness.test:42013", "harness.test")
	id := webapi.ID()
	host := rules.Host(id)
	svc := &browserService{target: Target{InstanceID: id, Host: host, IP: "172.18.0.5"}, cookie: []byte("dsh-auth-" + base64Host(rules.authority(host)) + "=v1.synthetic.signature")}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = connection.Close()
		}
	}))
	defer upstream.Close()
	g := NewGateway(rules, webapi.ID(), svc, nil)
	defer g.Close()
	g.transport.DialContext = testDial(upstream)
	r := httptest.NewRequest("GET", rules.URL(host)+"/", nil)
	r.Header.Set("Cookie", SessionCookie+"="+strings.Repeat("s", 43))
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), `"code":"UPSTREAM_UNAVAILABLE"`) || strings.Contains(w.Body.String(), "NATIVE_AUTH_FAILED") {
		t.Fatalf("transport failure misclassified: status=%d", w.Code)
	}
}

func TestOfficePreviewHasSeparateBoundedProxyBudget(t *testing.T) {
	rules, _ := NewRules("https://portal.harness.test:42013", "harness.test")
	id := webapi.ID()
	host := rules.Host(id)
	svc := &browserService{target: Target{InstanceID: id, Host: host, IP: "172.18.0.5"}, cookie: []byte("dsh-auth-" + base64Host(rules.authority(host)) + "=v1.synthetic.signature")}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-1.7"))
	}))
	defer upstream.Close()
	g := NewGateway(rules, webapi.ID(), svc, nil)
	defer g.Close()
	if g.transport.ResponseHeaderTimeout != 15*time.Second || g.documentTransport.ResponseHeaderTimeout != 90*time.Second {
		t.Fatal("preview and ordinary request budgets must remain distinct and bounded")
	}
	// Scale down the same relative budgets to exercise the actual HTTP transport.
	g.transport.DialContext = testDial(upstream)
	g.documentTransport.DialContext = testDial(upstream)
	g.transport.ResponseHeaderTimeout = 20 * time.Millisecond
	g.documentTransport.ResponseHeaderTimeout = time.Second
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"POST", "/api/officeToPdf/render", http.StatusOK},
		{"POST", "/api/session/modelCatalog", http.StatusBadGateway},
		{"POST", "/api/officeToPdf/render/other", http.StatusBadGateway},
		{"GET", "/api/officeToPdf/render", http.StatusBadGateway},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, rules.URL(host)+tc.path, strings.NewReader("{}"))
			r.Header.Set("Cookie", SessionCookie+"="+strings.Repeat("s", 43))
			r.Header.Set("Origin", rules.URL(host))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			g.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status=%d, expected=%d", w.Code, tc.status)
			}
		})
	}
}
