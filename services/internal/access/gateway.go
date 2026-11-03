package access

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"html"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/apps/connection-assets"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

var connectionStyle = connectionassets.CSS
var connectionHTML = connectionassets.HTML

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
