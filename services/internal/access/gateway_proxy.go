package access

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/apps/dsh-adapters"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

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
		if err := dshadapters.ManagedModelsResponse(response, g.rules.PortalOrigin, a.Target.InstanceID); err != nil {
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
