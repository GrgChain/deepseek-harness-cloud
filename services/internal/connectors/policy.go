// Package connectors implements owner-scoped remote MCP clients in the Portal.
package connectors

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/config"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

const maxBytes = 1 << 20

var uuidPattern = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
var endpointID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

type Endpoint struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	URL          string `json:"url"`
	CatalogID    string `json:"catalog_id,omitempty"`
	AllowPrivate bool   `json:"allow_private"`
	AllowHTTP    bool   `json:"allow_http"`
}
type Policy struct {
	Endpoints    []Endpoint    `json:"endpoints"`
	OAuthClients []OAuthClient `json:"oauth_clients,omitempty"`
}

func LoadPolicy(path string) (Policy, error) {
	p := Policy{Endpoints: []Endpoint{}}
	if path == "" {
		return p, nil
	}
	data, err := config.ReadPrivateFile(path)
	if err != nil || len(data) > 64<<10 || webapi.StrictUnmarshal(data, &p) != nil || len(p.Endpoints) > 100 || len(p.OAuthClients) > 100 {
		return p, errors.New("connector policy unavailable")
	}
	seen := map[string]bool{}
	for _, e := range p.Endpoints {
		if !endpointID.MatchString(e.ID) || e.ID == "personal" || seen[e.ID] || len(e.Name) == 0 || len(e.Name) > 100 {
			return p, errors.New("invalid connector policy")
		}
		if e.CatalogID != "" && !knownCatalogID(e.CatalogID) {
			return p, errors.New("unknown connector catalog binding")
		}
		if _, err := parseEndpoint(e); err != nil {
			return p, err
		}
		seen[e.ID] = true
	}
	clients := map[string]bool{}
	for _, c := range p.OAuthClients {
		if clients[c.URL] || !validOAuthValue(c.ClientID) || !validOAuthValue(c.ClientSecret) || !validOAuthValue(c.Scopes) {
			return p, errors.New("invalid OAuth client policy")
		}
		for _, address := range []string{c.URL, c.Issuer} {
			if _, err := parseEndpoint(Endpoint{URL: address}); err != nil {
				return p, errors.New("invalid OAuth client URL")
			}
		}
		clients[c.URL] = true
	}
	return p, nil
}
func parseEndpoint(e Endpoint) (*url.URL, error) {
	u, err := url.Parse(e.URL)
	if err != nil || len(e.URL) > 1000 || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || strings.Contains(u.Path, "..") || u.Scheme != "https" && (u.Scheme != "http" || !e.AllowHTTP || !e.AllowPrivate) {
		return nil, errors.New("invalid approved MCP URL")
	}
	return u, nil
}
func allowedIP(ip netip.Addr, private bool) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.IsGlobalUnicast() && !ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return false
	}
	if ip.IsPrivate() || ip.IsLoopback() {
		return private
	}
	for _, cidr := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32"} {
		if netip.MustParsePrefix(cidr).Contains(ip) {
			return false
		}
	}
	return true
}

// Credentials go only to the exact configured URL. DNS is pinned per client,
// redirects and environment proxies are disabled, all response bytes are bounded.
func pinnedTransport(ctx context.Context, e Endpoint) (*http.Transport, error) {
	u, err := parseEndpoint(e)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", u.Hostname())
	if err != nil || len(ips) == 0 {
		return nil, errors.New("MCP DNS unavailable")
	}
	for _, ip := range ips {
		if !allowedIP(ip, e.AllowPrivate) || u.Scheme == "http" && !ip.IsPrivate() && !ip.IsLoopback() {
			return nil, errors.New("MCP address denied")
		}
	}
	port := u.Port()
	if port == "" {
		port = "443"
		if u.Scheme == "http" {
			port = "80"
		}
	}
	tr := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, DisableKeepAlives: true, MaxResponseHeaderBytes: 16 << 10, ResponseHeaderTimeout: 120 * time.Second}
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		var last error
		for _, ip := range ips {
			conn, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			last = err
		}
		return nil, last
	}
	return tr, nil
}
func endpointClient(ctx context.Context, e Endpoint, credential string) (*http.Client, error) {
	tr, err := pinnedTransport(ctx, e)
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: &boundedTransport{tr, e.URL, credential}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}

type boundedTransport struct {
	base                 http.RoundTripper
	endpoint, credential string
}

func (t *boundedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.String() != t.endpoint || r.Method != "POST" && r.Method != "DELETE" {
		return nil, errors.New("MCP request target denied")
	}
	c := r.Clone(r.Context())
	c.Header = r.Header.Clone()
	c.GetBody = nil
	c.Header.Del("Cookie")
	if err := applyEndpointCredential(c, t.endpoint, t.credential); err != nil {
		return nil, err
	}
	if c.ContentLength > maxBytes {
		return nil, errors.New("MCP request exceeds limit")
	}
	response, err := t.base.RoundTrip(c)
	if err != nil {
		return nil, err
	}
	if response.StatusCode == 401 || response.StatusCode == 403 {
		response.Body.Close()
		return nil, errAuth
	}
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		response.Body.Close()
		return nil, errors.New("MCP redirect denied")
	}
	if response.ContentLength > maxBytes {
		response.Body.Close()
		return nil, errors.New("MCP response exceeds limit")
	}
	response.Body = &boundedBody{ReadCloser: response.Body, left: maxBytes}
	return response, nil
}

type boundedBody struct {
	io.ReadCloser
	left int
}

func (b *boundedBody) Read(p []byte) (int, error) {
	if len(p) > b.left+1 {
		p = p[:b.left+1]
	}
	n, err := b.ReadCloser.Read(p)
	b.left -= n
	if b.left < 0 {
		return 0, errors.New("MCP response exceeds limit")
	}
	return n, err
}

var errAuth = errors.New("MCP authentication required")
