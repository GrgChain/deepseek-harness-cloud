package access

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"errors"
	"html"
	"io"
	"log"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

//go:embed connection.css
var connectionStyle string

//go:embed connection.html
var connectionHTML string

var connectionCSP = func() string {
	sum := sha256.Sum256([]byte(connectionStyle))
	return "default-src 'none'; style-src 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"
}()

type BrowserAccess interface {
	ReplaceChallenge(context.Context, string, string) (Challenge, string, error)
	Redeem(context.Context, string, string, string, string, string) (string, Authorization, error)
	CheckSession(context.Context, string, string, bool) (Authorization, error)
}
type Limit func(context.Context, string, int) error
type runtimeAddressKey struct{}

// Pool connections by runtime identity, while dialing only the authorized IP.
// A replacement container can reuse the same IP after a model change.
func runtimeTransport() *http.Transport {
	t := fixedTransport()
	dial := t.DialContext
	t.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		address, ok := ctx.Value(runtimeAddressKey{}).(string)
		if !ok || address == "" {
			return nil, errors.New("runtime address missing")
		}
		return dial(ctx, network, address)
	}
	return t
}

type Gateway struct {
	rules             Rules
	organization      string
	service           BrowserAccess
	limit             Limit
	transport         *http.Transport
	documentTransport *http.Transport
	ctx               context.Context
	stop              context.CancelFunc
	mu                sync.Mutex
	sockets           map[string]int
	checkInterval     time.Duration
}

func NewGateway(rules Rules, organization string, service BrowserAccess, limit Limit) *Gateway {
	ctx, stop := context.WithCancel(context.Background())
	transport := runtimeTransport()
	// Browser pages and plugin bundles may need cold initialization. Keep the
	// verifier's short probe budget separate from real browser requests.
	transport.ResponseHeaderTimeout = 15 * time.Second
	documentTransport := transport.Clone()
	// Office rendering permits a 60-second conversion after cold engine/font
	// initialization. Keep its budget separate from ordinary browser requests.
	documentTransport.ResponseHeaderTimeout = 90 * time.Second
	return &Gateway{rules: rules, organization: organization, service: service, limit: limit, transport: transport, documentTransport: documentTransport, ctx: ctx, stop: stop, sockets: map[string]int{}, checkInterval: 10 * time.Second}
}
func (g *Gateway) Close() {
	g.stop()
	g.transport.CloseIdleConnections()
	g.documentTransport.CloseIdleConnections()
}
func singleCookie(r *http.Request, name string) (string, bool) {
	value := ""
	n := 0
	for _, c := range r.Cookies() {
		if c.Name == name {
			n++
			value = c.Value
		}
	}
	return value, n == 1 && opaque.MatchString(value)
}
func singleOrigin(r *http.Request, expected string) bool {
	return len(r.Header.Values("Origin")) == 1 && r.Header.Get("Origin") == expected
}
func setCookie(w http.ResponseWriter, name, value string, expires time.Time, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, Expires: expires.UTC(), MaxAge: maxAge})
}
func clearCookie(w http.ResponseWriter, name string) { setCookie(w, name, "", time.Unix(1, 0), -1) }
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Request-ID", webapi.ID())
	if g.ctx.Err() != nil {
		webapi.WriteError(w, r, unavailable())
		return
	}
	id, ok := g.rules.Resolve(r.Host)
	if !ok {
		webapi.WriteError(w, r, notFound())
		return
	}
	host := g.rules.Host(id)
	if r.URL.Path == "/__dsh/connect" {
		g.connectionPage(w, r, id)
		return
	}
	if r.URL.Path == "/__dsh/challenge" {
		g.challenge(w, r, host)
		return
	}
	if r.URL.Path == "/__dsh/handoff" {
		g.handoff(w, r, host)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/__dsh/") {
		webapi.WriteError(w, r, notFound())
		return
	}
	g.proxy(w, r, host)
}

// This public page lets a browser establish trust for the instance HTTPS
// authority. It creates no challenge, ticket, session or native authorization.
func (g *Gateway) connectionPage(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != "GET" || r.URL.RawQuery != "" {
		webapi.WriteError(w, r, notFound())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", connectionCSP)
	w.Header().Set("X-Frame-Options", "DENY")
	// Return to the fixed Portal route. Only a recent intent from the original
	// user click in this tab may resume; this public page grants no access.
	resume := html.EscapeString(g.rules.PortalOrigin + "/app/instances/" + id + "?resume_workspace=1")
	_, _ = io.WriteString(w, strings.NewReplacer("TRANSITION_CSS", connectionStyle, "RESUME_URL", resume).Replace(connectionHTML))

}
func (g *Gateway) challenge(w http.ResponseWriter, r *http.Request, host string) {
	if r.Method != "GET" || r.URL.RawQuery != "" || !singleOrigin(r, g.rules.PortalOrigin) {
		webapi.WriteError(w, r, denied())
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", g.rules.PortalOrigin)
	w.Header().Set("Access-Control-Allow-Credentials", "true")
	w.Header().Set("Access-Control-Allow-Methods", "GET")
	w.Header().Set("Vary", "Origin")
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || net.ParseIP(ip) == nil || g.limit == nil || g.service == nil {
		webapi.WriteError(w, r, unavailable())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err = g.limit(ctx, "challenge-ip:"+base64Host(net.ParseIP(ip).String()), 30); err == nil {
		err = g.limit(ctx, "challenge-org:"+g.organization, 300)
	}
	if err != nil {
		webapi.WriteError(w, r, err)
		return
	}
	previous, _ := singleCookie(r, ChallengeCookie)
	c, secret, err := g.service.ReplaceChallenge(ctx, host, previous)
	if err != nil {
		webapi.WriteError(w, r, err)
		return
	}
	setCookie(w, ChallengeCookie, secret, c.ExpiresAt, 60)
	webapi.JSON(w, 200, c)
}
func (g *Gateway) handoff(w http.ResponseWriter, r *http.Request, host string) {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	browser, ok := singleCookie(r, ChallengeCookie)
	if r.Method != "POST" || r.URL.RawQuery != "" || !singleOrigin(r, g.rules.PortalOrigin) || err != nil || media != "application/x-www-form-urlencoded" || !ok || g.service == nil {
		webapi.WriteError(w, r, denied())
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<10))
	if err != nil {
		webapi.WriteError(w, r, webapi.Fail(413, "PAYLOAD_TOO_LARGE", "进入请求超过 8KiB"))
		return
	}
	form, err := url.ParseQuery(string(body))
	if err != nil || len(form) != 2 || len(form["ticket"]) != 1 || len(form["challenge_id"]) != 1 || !opaque.MatchString(form.Get("ticket")) || !uuid.MatchString(form.Get("challenge_id")) {
		webapi.WriteError(w, r, denied())
		return
	}
	secret, a, err := g.service.Redeem(r.Context(), host, form.Get("ticket"), form.Get("challenge_id"), browser, w.Header().Get("X-Request-ID"))
	defer a.Destroy()
	if err != nil {
		clearCookie(w, ChallengeCookie)
		var problem *webapi.Problem
		if errors.As(err, &problem) && problem.Code == "STATE_CONFLICT" && r.Header.Get("Sec-Fetch-Mode") == "navigate" {
			id, _ := g.rules.Resolve(g.rules.authority(host))
			w.Header().Set("Location", g.rules.PortalOrigin+"/app/instances/"+id+"?resume_workspace=1")
			w.WriteHeader(http.StatusSeeOther)
			return
		}
		webapi.WriteError(w, r, err)
		return
	}
	maxAge := int(time.Until(a.ExpiresAt) / time.Second)
	if maxAge <= 0 || maxAge > 1800 || !opaque.MatchString(secret) {
		webapi.WriteError(w, r, unavailable())
		return
	}
	setCookie(w, SessionCookie, secret, a.ExpiresAt, maxAge)
	clearCookie(w, ChallengeCookie)
	w.Header().Set("Location", "/")
	w.WriteHeader(http.StatusSeeOther)
}
func isWebsocket(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}
func validWebsocket(r *http.Request) bool {
	key, err := base64.StdEncoding.DecodeString(r.Header.Get("Sec-WebSocket-Key"))
	connection := false
	for _, v := range strings.Split(r.Header.Get("Connection"), ",") {
		connection = connection || strings.EqualFold(strings.TrimSpace(v), "upgrade")
	}
	return r.Method == "GET" && r.URL.Path == "/api/remote.mux" && connection && r.Header.Get("Sec-WebSocket-Version") == "13" && len(r.Header.Values("Sec-WebSocket-Key")) == 1 && err == nil && len(key) == 16
}
func proxyRequestAllowed(r *http.Request, origin string) bool {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(r.URL.RawQuery) > 16<<10 {
		return false
	}
	for key := range query {
		switch strings.ToLower(key) {
		case "token", "access_token", "coder_session_token":
			return false
		}
	}
	if r.Header.Get("Upgrade") != "" {
		return isWebsocket(r) && validWebsocket(r) && singleOrigin(r, origin)
	}
	switch r.Method {
	case "GET", "HEAD":
		return len(r.Header.Values("Origin")) == 0 || singleOrigin(r, origin)
	case "POST", "PUT", "PATCH", "DELETE":
		return singleOrigin(r, origin)
	default:
		return false
	}
}
func (g *Gateway) socket(id string) (func(), bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.ctx.Err() != nil || g.sockets[id] >= 20 {
		return nil, false
	}
	g.sockets[id]++
	return func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.sockets[id]--
		if g.sockets[id] == 0 {
			delete(g.sockets, id)
		}
	}, true
}
func (g *Gateway) proxy(w http.ResponseWriter, r *http.Request, host string) {
	if !proxyRequestAllowed(r, g.rules.URL(host)) {
		webapi.WriteError(w, r, denied())
		return
	}
	secret, ok := singleCookie(r, SessionCookie)
	if !ok || g.service == nil {
		g.sessionError(w, r, host, denied())
		return
	}
	a, err := g.service.CheckSession(r.Context(), host, secret, true)
	if err != nil {
		g.sessionError(w, r, host, err)
		return
	}
	defer a.Destroy()
	ip := net.ParseIP(a.Target.IP)
	if a.Target.Host != host || ip == nil || ip.To4() == nil || !ip.IsPrivate() || !validNativeHeader(g.rules.authority(host), a.Cookie) {
		webapi.WriteError(w, r, unavailable())
		return
	}
	// Bound unknown/chunked bodies before sending any partial business request.
	if r.ContentLength > 10<<20 {
		webapi.WriteError(w, r, webapi.Fail(413, "PAYLOAD_TOO_LARGE", "请求内容超过 10MiB"))
		return
	}
	if r.Body != nil && r.Body != http.NoBody {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 10<<20))
		if err != nil {
			webapi.WriteError(w, r, webapi.Fail(413, "PAYLOAD_TOO_LARGE", "请求内容超过 10MiB"))
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
	}
	ctx, cancel := context.WithCancel(context.WithValue(r.Context(), runtimeAddressKey{}, net.JoinHostPort(a.Target.IP, "3080")))
	defer cancel()
	transport := g.transport
	if r.Method == http.MethodPost && r.URL.Path == "/api/officeToPdf/render" {
		transport = g.documentTransport
		var cancelDocument context.CancelFunc
		ctx, cancelDocument = context.WithTimeout(ctx, 90*time.Second)
		defer cancelDocument()
	}
	stop := context.AfterFunc(g.ctx, cancel)
	defer stop()
	var activity atomic.Bool
	websocket := isWebsocket(r)
	if websocket {
		release, ok := g.socket(a.Target.InstanceID)
		if !ok {
			webapi.WriteError(w, r, webapi.Fail(429, "RATE_LIMITED", "该工作空间的连接数已达到上限"))
			return
		}
		defer release()
		_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
		_ = http.NewResponseController(w).SetReadDeadline(time.Time{})
		w = &socketWriter{ResponseWriter: w, activity: &activity}
		go g.watch(ctx, cancel, host, secret, a.Target, &activity)
	}
	proxy := &httputil.ReverseProxy{Transport: transport, FlushInterval: -1, ErrorLog: log.New(io.Discard, "", 0), Rewrite: func(p *httputil.ProxyRequest) {
		p.Out.URL.Scheme = "http"
		// URL authority is the transport pool key; the actual dial address comes
		// from the validated target above. Host remains the public authority.
		p.Out.URL.Host = a.Target.Generation + "." + a.Target.InstanceID + "." + a.Target.IP + ".runtime.invalid:3080"
		p.Out.Host = g.rules.authority(host)
		p.Out.Header = proxyHeaders(p.In.Header, websocket)
		p.Out.Trailer = nil
		p.Out.Header.Set("Cookie", string(a.Cookie))
	}, ModifyResponse: func(response *http.Response) error {
		if err := managedModelsResponse(response, g.rules.PortalOrigin, a.Target.InstanceID); err != nil {
			return err
		}
		response.Header.Del("Set-Cookie")
		for name := range response.Header {
			if strings.HasPrefix(strings.ToLower(name), "access-control-") {
				response.Header.Del(name)
			}
		}
		response.Header.Set("Cache-Control", "no-store")
		response.Header.Set("Referrer-Policy", "no-referrer")
		if location := response.Header.Get("Location"); location != "" && !safeLocation(location, g.rules.URL(host)) {
			return errors.New("upstream redirect rejected")
		}
		if response.StatusCode == 101 {
			if !websocket || response.Header.Get("Sec-WebSocket-Extensions") != "" {
				return errors.New("unexpected upgrade")
			}
			if body, ok := response.Body.(io.ReadWriteCloser); ok {
				response.Body = &socketBody{ReadWriteCloser: body, reader: newFrameReader(body, false, &activity)}
			}
		}
		compressStaticResponse(response, r.Header.Get("Accept-Encoding"))
		return nil
	}, ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
		kind := "upstream_failure"
		var networkError net.Error
		if errors.Is(err, context.Canceled) {
			kind = "cancelled"
		} else if errors.As(err, &networkError) && networkError.Timeout() {
			kind = "timeout"
		}
		// Never log URLs, queries, cookies or the raw transport error.
		slog.Warn("workspace proxy failed", "request_id", w.Header().Get("X-Request-ID"), "instance_id", a.Target.InstanceID, "kind", kind)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		webapi.WriteError(w, r, webapi.Fail(502, "UPSTREAM_UNAVAILABLE", "工作空间连接暂时中断，请重新加载"))
	}}
	w.Header().Del("Content-Type")
	proxy.ServeHTTP(w, r.WithContext(ctx))
}
func proxyHeaders(in http.Header, websocket bool) http.Header {
	out := http.Header{}
	for _, name := range []string{"Accept", "Accept-Language", "Content-Type", "Range", "If-Range", "If-None-Match", "If-Modified-Since", "If-Match", "If-Unmodified-Since", "Origin"} {
		if values := in.Values(name); len(values) > 0 {
			out[name] = append([]string(nil), values...)
		}
	}
	if websocket {
		for _, name := range []string{"Sec-WebSocket-Key", "Sec-WebSocket-Version", "Sec-WebSocket-Protocol"} {
			if values := in.Values(name); len(values) > 0 {
				out[name] = append([]string(nil), values...)
			}
		}
		out.Set("Connection", "Upgrade")
		out.Set("Upgrade", "websocket")
	}
	return out
}
func safeLocation(location, origin string) bool {
	u, err := url.Parse(location)
	if err != nil || u.User != nil || strings.ContainsAny(location, "\\\r\n") || strings.HasPrefix(location, "//") {
		return false
	}
	if u.IsAbs() && u.Scheme+"://"+u.Host != origin {
		return false
	}
	for key := range u.Query() {
		if strings.EqualFold(key, "token") {
			return false
		}
	}
	return true
}
func (g *Gateway) sessionError(w http.ResponseWriter, r *http.Request, host string, err error) {
	var problem *webapi.Problem
	if errors.As(err, &problem) && (problem.Code == "FORBIDDEN" || problem.Code == "NOT_FOUND") {
		// A late request from the previous runtime must not delete a newer
		// handoff cookie shared by other tabs. Rejection revokes no new session;
		// the next successful handoff replaces the cookie normally.
		if r.Method == "GET" && !isWebsocket(r) && r.Header.Get("Sec-Fetch-Mode") == "navigate" && strings.Contains(r.Header.Get("Accept"), "text/html") {
			id, _ := g.rules.Resolve(g.rules.authority(host))
			w.Header().Set("Location", g.rules.PortalOrigin+"/app/instances/"+id)
			w.WriteHeader(http.StatusSeeOther)
			return
		}
		err = webapi.Fail(401, "UNAUTHENTICATED", "访问会话已失效，请从控制台重新进入")
	}
	webapi.WriteError(w, r, err)
}
func (g *Gateway) watch(ctx context.Context, cancel context.CancelFunc, host, secret string, target Target, activity *atomic.Bool) {
	ticker := time.NewTicker(g.checkInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a, err := g.service.CheckSession(ctx, host, secret, activity.Swap(false))
			same := a.Target.same(target)
			a.Destroy()
			if err != nil || !same {
				cancel()
				return
			}
		}
	}
}
