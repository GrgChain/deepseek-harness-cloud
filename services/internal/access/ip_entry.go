package access

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/config"
)

type browserPortalKey struct{}

func browserPortal(r *http.Request, fallback string) string {
	if origin, ok := r.Context().Value(browserPortalKey{}).(string); ok {
		return origin
	}
	return fallback
}

// IPEntry serves the Portal and instance-specific DSH mounts on one TLS port.
// Upstream selection and authorization stay in Gateway.
type IPEntry struct {
	options   IPOptions
	rules     Rules
	gateway   http.Handler
	tls       *tls.Config
	portal    *httputil.ReverseProxy
	transport *http.Transport
	mu        sync.Mutex
	server    *http.Server
}

func NewIPEntry(runtime *Runtime, gateway http.Handler) (*IPEntry, error) {
	if runtime.Options.IPAccess == nil {
		return nil, nil
	}
	o := *runtime.Options.IPAccess
	if o.Validate() != nil {
		return nil, ErrConfiguration
	}
	certPEM, err := config.ReadPrivateFile(o.CertificateFile)
	if err != nil {
		return nil, ErrConfiguration
	}
	keyPEM, err := config.ReadPrivateFile(o.KeyFile)
	if err != nil {
		return nil, ErrConfiguration
	}
	defer clear(keyPEM)
	certificate, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, ErrConfiguration
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil || leaf.VerifyHostname(o.Address) != nil || time.Now().Before(leaf.NotBefore) || !time.Now().Before(leaf.NotAfter) {
		return nil, ErrConfiguration
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	// Parsing, OAuth discovery and skill imports have their own API budgets,
	// including streaming operations. The extra entry point must preserve them.
	transport.ResponseHeaderTimeout = 0
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	if o.UpstreamCAFile != "" {
		ca, err := config.ReadPrivateFile(o.UpstreamCAFile)
		if err != nil {
			return nil, ErrConfiguration
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(ca) {
			return nil, ErrConfiguration
		}
		transport.TLSClientConfig.RootCAs = roots
	}
	canonical, _ := url.Parse(runtime.Rules.PortalOrigin)
	transport.TLSClientConfig.ServerName = canonical.Hostname()
	upstream, _ := url.Parse(o.PortalUpstream)
	entry := &IPEntry{options: o, rules: runtime.Rules, gateway: gateway, transport: transport,
		tls: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}}
	entry.portal = &httputil.ReverseProxy{Transport: transport, FlushInterval: -1, ErrorLog: log.New(io.Discard, "", 0), Rewrite: func(p *httputil.ProxyRequest) {
		p.SetURL(upstream)
		p.Out.Host = canonical.Host
		stripForwarding(p.Out.Header)
		p.Out.Header.Set("X-HC-Public-Origin", o.Origin(o.PortalPort))
		if singleOrigin(p.In, o.Origin(o.PortalPort)) {
			p.Out.Header.Set("Origin", runtime.Rules.PortalOrigin)
		}
	}, ModifyResponse: func(response *http.Response) error {
		rewriteIPHeaders(response.Header, runtime.Rules.PortalOrigin, o.Origin(o.PortalPort), "", "", "")
		return nil
	}, ErrorHandler: func(w http.ResponseWriter, r *http.Request, _ error) {
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "控制台暂不可用，请稍后重试", http.StatusServiceUnavailable)
	}}
	return entry, nil
}

func stripForwarding(headers http.Header) {
	for name := range headers {
		lower := strings.ToLower(name)
		if lower == "forwarded" || strings.HasPrefix(lower, "x-forwarded-") {
			headers.Del(name)
		}
	}
}

func rewriteOrigin(value, from, to string) string {
	u, err := url.Parse(value)
	f, _ := url.Parse(from)
	t, _ := url.Parse(to)
	if err == nil && f != nil && t != nil && u.User == nil && u.Scheme == f.Scheme && u.Host == f.Host {
		u.Scheme, u.Host = t.Scheme, t.Host
		if t.Path != "" && t.Path != "/" {
			u.Path = strings.TrimSuffix(t.Path, "/") + u.Path
			u.RawPath = ""
		}
		return u.String()
	}
	return value
}

func rewriteIPHeaders(headers http.Header, canonicalPortal, ipPortal, canonicalWorkspace, ipWorkspace, id string) {
	if location := headers.Get("Location"); location != "" {
		location = rewriteOrigin(location, canonicalPortal, ipPortal)
		if canonicalWorkspace != "" {
			if strings.HasPrefix(location, "/") && !strings.HasPrefix(location, "//") {
				location = ipWorkspacePath(id) + location
			} else {
				location = rewriteOrigin(location, canonicalWorkspace, ipWorkspace)
			}
		}
		headers.Set("Location", location)
	}
	if headers.Get("Access-Control-Allow-Origin") == canonicalPortal {
		headers.Set("Access-Control-Allow-Origin", ipPortal)
	}
	if id != "" {
		cookies := headers.Values("Set-Cookie")
		headers.Del("Set-Cookie")
		for _, cookie := range cookies {
			for _, name := range []string{ChallengeCookie, SessionCookie} {
				if strings.HasPrefix(cookie, name+"=") {
					headers.Add("Set-Cookie", ipCookieName(name, id)+strings.Replace(strings.TrimPrefix(cookie, name), "Path=/;", "Path="+ipWorkspacePath(id)+"/;", 1))
					break
				}
			}
		}
	}
}

type ipResponseWriter struct {
	http.ResponseWriter
	entry   *IPEntry
	id      string
	origin  string
	written bool
}

func (w *ipResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *ipResponseWriter) WriteHeader(status int) {
	if !w.written {
		w.written = true
		e := w.entry
		rewriteIPHeaders(w.Header(), e.rules.PortalOrigin, e.options.Origin(e.options.PortalPort), e.rules.URL(e.rules.Host(w.id)), w.origin, w.id)
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *ipResponseWriter) Write(body []byte) (int, error) {
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}
func ipWorkspacePath(id string) string { return "/dsh/" + id }

func ipCookieName(name, id string) string {
	return strings.Replace(name, "__Host-", "__Secure-", 1) + "_" + strings.ReplaceAll(id, "-", "")
}

func (e *IPEntry) workspaceHandler(id string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := e.options.Origin(e.options.PortalPort)
		u, _ := url.Parse(origin)
		prefix := ipWorkspacePath(id)
		if r.Host != u.Host || !strings.HasPrefix(r.URL.Path, prefix+"/") || r.URL.RawPath != "" {
			http.NotFound(w, r)
			return
		}
		if len(r.Header.Values("Origin")) > 0 {
			if !singleOrigin(r, origin) {
				http.Error(w, "访问来源无效", http.StatusForbidden)
				return
			}
			if r.URL.Path == prefix+"/__dsh/challenge" || r.URL.Path == prefix+"/__dsh/handoff" {
				r.Header.Set("Origin", e.rules.PortalOrigin)
			} else {
				r.Header.Set("Origin", e.rules.URL(e.rules.Host(id)))
			}
		}
		// Same-origin fetch does not send Origin for GET requests. Only accept
		// the browser's same-origin fetch metadata for this public challenge.
		if r.URL.Path == prefix+"/__dsh/challenge" && len(r.Header.Values("Origin")) == 0 && r.Header.Get("Sec-Fetch-Site") == "same-origin" && r.Header.Get("Sec-Fetch-Mode") == "cors" {
			r.Header.Set("Origin", e.rules.PortalOrigin)
		}
		cookies := r.Cookies()
		r.Header.Del("Cookie")
		for _, cookie := range cookies {
			for _, name := range []string{ChallengeCookie, SessionCookie} {
				if cookie.Name == ipCookieName(name, id) {
					r.AddCookie(&http.Cookie{Name: name, Value: cookie.Value})
				}
			}
		}
		stripForwarding(r.Header)
		r.Host = e.rules.authority(e.rules.Host(id))
		r.URL.Path = strings.TrimPrefix(r.URL.Path, prefix)
		r = r.WithContext(context.WithValue(r.Context(), browserPortalKey{}, origin))
		e.gateway.ServeHTTP(&ipResponseWriter{ResponseWriter: w, entry: e, id: id, origin: origin + prefix}, r)
	})
}

func (e *IPEntry) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := e.options.Origin(e.options.PortalPort)
		u, _ := url.Parse(origin)
		if r.Host != u.Host {
			http.NotFound(w, r)
			return
		}
		if len(r.Header.Values("Origin")) > 0 && !singleOrigin(r, origin) {
			http.Error(w, "访问来源无效", http.StatusForbidden)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/dsh/") {
			id, _, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/dsh/"), "/")
			if !uuid.MatchString(id) || r.URL.RawPath != "" {
				http.NotFound(w, r)
				return
			}
			if !ok {
				w.Header().Set("Location", ipWorkspacePath(id)+"/")
				w.WriteHeader(http.StatusPermanentRedirect)
				return
			}
			e.workspaceHandler(id).ServeHTTP(w, r)
			return
		}
		e.portal.ServeHTTP(w, r)
	})
}

func (e *IPEntry) Start(ctx context.Context) error {
	if e == nil {
		return nil
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(e.options.Address, strconv.Itoa(e.options.PortalPort)))
	if err != nil {
		return errors.New("IP access TLS port is unavailable")
	}
	e.server = &http.Server{Handler: e.handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10, ErrorLog: log.New(io.Discard, "", 0)}
	go func() { _ = e.server.Serve(tls.NewListener(listener, e.tls)) }()
	return nil
}

func (e *IPEntry) Close() {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.server != nil {
		_ = e.server.Close()
	}
	e.transport.CloseIdleConnections()
}
