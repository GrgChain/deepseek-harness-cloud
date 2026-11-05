package access

import (
	"bufio"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func TestIPWorkspaceWebSocketHandshakeAndFrames(t *testing.T) {
	rules, _ := NewRules("https://portal.harness.test:42013", "harness.test")
	id := webapi.ID()
	host := rules.Host(id)
	service := &browserService{target: Target{InstanceID: id, Host: host, IP: "172.18.0.5"}, cookie: []byte("dsh-auth-" + base64Host(rules.authority(host)) + "=v1.synthetic.signature")}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != rules.URL(host) {
			t.Error("native WebSocket origin changed")
		}
		connection, buffer, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer connection.Close()
		_, _ = buffer.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Accept: synthetic\r\n\r\n")
		_ = buffer.Flush()
		_, _ = connection.Write([]byte{0x81, 2, 'o', 'k'})
		_, _ = io.Copy(io.Discard, connection)
	}))
	defer upstream.Close()
	g := NewGateway(rules, webapi.ID(), service, nil)
	defer g.Close()
	g.transport.DialContext = testDial(upstream)
	site := httptest.NewUnstartedServer(nil)
	port := site.Listener.Addr().(*net.TCPAddr).Port
	e := &IPEntry{options: IPOptions{Address: "127.0.0.1", PortalPort: port}, rules: rules, gateway: g}
	site.Config.Handler = e.workspaceHandler(id)
	site.StartTLS()
	defer site.Close()
	connection, err := tls.Dial("tcp", site.Listener.Addr().String(), &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
	request := httptest.NewRequest("GET", e.options.Origin(port)+ipWorkspacePath(id)+"/api/remote.mux", nil)
	request.Header.Set("Origin", e.options.Origin(port))
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Sec-WebSocket-Version", "13")
	request.Header.Set("Sec-WebSocket-Key", "MDEyMzQ1Njc4OWFiY2RlZg==")
	request.Header.Set("Cookie", ipCookieName(SessionCookie, id)+"="+strings.Repeat("s", 43))
	if err = request.Write(connection); err != nil {
		t.Fatal(err)
	}
	buffer := bufio.NewReader(connection)
	response, err := http.ReadResponse(buffer, request)
	if err != nil {
		t.Fatal("IP WebSocket handshake failed", err)
	}
	if response.StatusCode != 101 {
		t.Fatal("IP WebSocket rejected", response.StatusCode)
	}
	frame := make([]byte, 4)
	if _, err = io.ReadFull(buffer, frame); err != nil || string(frame[2:]) != "ok" {
		t.Fatal("IP WebSocket frame did not reach browser", err)
	}
}

func TestIPWorkspaceOriginAndCookieIsolation(t *testing.T) {
	rules, _ := NewRules("https://portal.harness.test:42013", "harness.test")
	id := webapi.ID()
	opts := IPOptions{Address: "192.168.1.20", PortalPort: 42013, WorkspacePortStart: 43000, WorkspacePortEnd: 43999}
	service := &browserService{target: Target{InstanceID: id, Host: rules.Host(id), IP: "172.18.0.5"}}
	g := NewGateway(rules, webapi.ID(), service, func(context.Context, string, int) error { return nil })
	defer g.Close()
	e := &IPEntry{options: opts, rules: rules, gateway: g}
	h := e.workspaceHandler(id)
	challengeName, sessionName := ipCookieName(ChallengeCookie, id), ipCookieName(SessionCookie, id)
	request := func(path, origin, cookies string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", opts.Origin(42013)+ipWorkspacePath(id)+path, nil)
		r.RemoteAddr = "192.168.1.30:51000"
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		r.Header.Set("Cookie", cookies)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	w := request("/__dsh/challenge", opts.Origin(42013), challengeName+"="+strings.Repeat("p", 43)+"; __Host-hc_session=portal-secret; "+ChallengeCookie+"="+strings.Repeat("x", 43))
	if w.Code != 200 || w.Header().Get("Access-Control-Allow-Origin") != opts.Origin(42013) || service.previous != strings.Repeat("p", 43) {
		t.Fatal("IP challenge lost exact origin or browser binding")
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != challengeName || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].Path != ipWorkspacePath(id)+"/" || cookies[0].Domain != "" || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("IP challenge cookie can collide with another workspace")
	}
	if request("/__dsh/challenge", "", "").Code != 403 {
		t.Fatal("challenge accepted missing Origin and browser fetch metadata")
	}
	fetch := httptest.NewRequest("GET", opts.Origin(42013)+ipWorkspacePath(id)+"/__dsh/challenge", nil)
	fetch.RemoteAddr = "192.168.1.30:51000"
	fetch.Header.Set("Sec-Fetch-Site", "same-origin")
	fetch.Header.Set("Sec-Fetch-Mode", "cors")
	fetched := httptest.NewRecorder()
	h.ServeHTTP(fetched, fetch)
	if fetched.Code != 200 {
		t.Fatal("same-origin browser challenge fetch was rejected")
	}
	for _, origin := range []string{rules.PortalOrigin, opts.Origin(43000), opts.Origin(43001), "https://foreign.test", opts.Origin(42013) + ".evil"} {
		if request("/__dsh/challenge", origin, "").Code != 403 {
			t.Fatal("IP challenge accepted another origin")
		}
	}
	if request("/api/action", opts.Origin(43000), "").Code != 403 {
		t.Fatal("legacy port origin may perform workspace requests")
	}
	w = request("/__dsh/connect", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), opts.Origin(42013)+"/app/instances/"+id) || strings.Contains(w.Body.String(), rules.PortalOrigin) {
		t.Fatal("IP reconnect page leaves the IP site")
	}
	seen := false
	e.gateway = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = true
		if r.Host != rules.authority(rules.Host(id)) || r.Header.Get("Origin") != rules.URL(rules.Host(id)) || r.Header.Get("Forwarded") != "" || r.Header.Get("X-Forwarded-Host") != "" || r.URL.Path != "/api/remote.mux" {
			t.Fatal("unvalidated routing headers reached Gateway")
		}
		if len(r.Cookies()) != 1 || r.Cookies()[0].Name != SessionCookie || r.Cookies()[0].Value != strings.Repeat("s", 43) {
			t.Fatal("Portal/other-workspace cookies reached Gateway")
		}
		w.Header().Set("Location", rules.PortalOrigin+"/app/instances/"+id)
		w.WriteHeader(303)
	})
	r := httptest.NewRequest("GET", opts.Origin(42013)+ipWorkspacePath(id)+"/api/remote.mux", nil)
	r.Header.Set("Origin", opts.Origin(42013))
	r.Header.Set("Forwarded", "host=attacker.test")
	r.Header.Set("X-Forwarded-Host", "attacker.test")
	r.Header.Set("Cookie", "__Host-hc_session=portal-secret; "+sessionName+"="+strings.Repeat("s", 43)+"; "+SessionCookie+"_foreign=other-secret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if !seen || w.Header().Get("Location") != opts.Origin(42013)+"/app/instances/"+id {
		t.Fatal("IP workspace redirect escaped to canonical site")
	}
	r = httptest.NewRequest("GET", "https://other.test:43000/", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatal("unrecognized IP listener host accepted")
	}
}

func TestIPSinglePortMountRoutingAndRedirects(t *testing.T) {
	rules, _ := NewRules("https://portal.harness.test:42013", "harness.test")
	id := webapi.ID()
	o := IPOptions{Address: "192.168.1.20", PortalPort: 42013}
	called := 0
	e := &IPEntry{options: o, rules: rules, gateway: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		if r.URL.Path != "/" || r.Host != rules.authority(rules.Host(id)) {
			t.Fatal("mount changed the authorized target or native root")
		}
		w.Header().Set("Location", "/")
		w.WriteHeader(http.StatusSeeOther)
	})}
	for _, path := range []string{"/dsh/invalid/", "/dsh/" + id + "%2f/", "/dsh/" + id + "/%2e%2e/v1/me"} {
		w := httptest.NewRecorder()
		e.handler().ServeHTTP(w, httptest.NewRequest("GET", o.Origin(42013)+path, nil))
		if w.Code != 404 || called != 0 {
			t.Fatal("invalid mount reached a runtime")
		}
	}
	w := httptest.NewRecorder()
	e.handler().ServeHTTP(w, httptest.NewRequest("GET", o.Origin(42013)+ipWorkspacePath(id), nil))
	if w.Code != 308 || w.Header().Get("Location") != ipWorkspacePath(id)+"/" || called != 0 {
		t.Fatal("missing trailing slash did not resolve to workspace root")
	}
	w = httptest.NewRecorder()
	e.handler().ServeHTTP(w, httptest.NewRequest("GET", o.Origin(42013)+ipWorkspacePath(id)+"/", nil))
	if w.Code != 303 || w.Header().Get("Location") != ipWorkspacePath(id)+"/" || called != 1 {
		t.Fatal("native redirect escaped the workspace mount")
	}
	headers := http.Header{"Location": {rules.URL(rules.Host(id)) + "/index.html?view=files"}}
	rewriteIPHeaders(headers, rules.PortalOrigin, o.Origin(42013), rules.URL(rules.Host(id)), o.Origin(42013)+ipWorkspacePath(id), id)
	if headers.Get("Location") != o.Origin(42013)+ipWorkspacePath(id)+"/index.html?view=files" {
		t.Fatal("absolute native redirect lost its mount or query")
	}
}

func TestIPOptionsRequireSingleValidPortalPortAndFixedLoopbackUpstream(t *testing.T) {
	good := IPOptions{Address: "192.168.1.20", PortalPort: 42013, WorkspacePortStart: 43000, WorkspacePortEnd: 43999, CertificateFile: "/private/cert", KeyFile: "/private/key", PortalUpstream: "https://127.0.0.1:42013"}
	if good.Validate() != nil {
		t.Fatal("valid IP config rejected")
	}
	for _, change := range []func(*IPOptions){
		func(o *IPOptions) { o.Address = "0.0.0.0" }, func(o *IPOptions) { o.Address = "example.com" }, func(o *IPOptions) { o.PortalPort = 65536 }, func(o *IPOptions) { o.PortalUpstream = "https://192.168.1.2:42013" }, func(o *IPOptions) { o.PortalUpstream = "https://user:pass@127.0.0.1:42013" }, func(o *IPOptions) { o.CertificateFile = "/private/../cert" },
	} {
		o := good
		change(&o)
		if o.Validate() == nil {
			t.Fatal("unsafe IP entry config accepted")
		}
	}
	ipv6 := good
	ipv6.Address = "::1"
	if ipv6.Validate() != nil || ipv6.Origin(443) != "https://[::1]" || ipv6.Origin(42013) != "https://[::1]:42013" {
		t.Fatal("IPv6 IP authority malformed")
	}
}
