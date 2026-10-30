// Package contextservice manages authorization and connections to independently
// deployed memory and RAG services. It never installs code in workspace runtimes.
package contextservice

import (
	"context"
	"crypto/tls"
	"errors"
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

const Contract = "hc-context-v1"

var idPattern = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
var endpointPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

type Endpoint struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Backend          string `json:"backend"`
	URL              string `json:"url"`
	AllowPrivateHTTP bool   `json:"allow_private_http"`
	DataUse          string `json:"data_use"`
	ProxyKeyFile     string `json:"proxy_key_file,omitempty"`
	ServiceKeyFile   string `json:"service_key_file,omitempty"`
	CustomService    bool   `json:"custom_service,omitempty"`
}
type Policy struct {
	Endpoints []Endpoint `json:"endpoints"`
}
type policyLoader func() (Policy, error)

func kindOf(backend string) string {
	switch backend {
	case "mem0", "openviking", "graphiti":
		return "memory"
	case "qdrant", "weaviate", "milvus":
		return "knowledge"
	}
	return ""
}
func LoadPolicy(path string) (Policy, error) {
	if path == "" {
		return Policy{Endpoints: []Endpoint{}}, nil
	}
	data, err := config.ReadPrivateFile(path)
	if err != nil {
		return Policy{}, errors.New("context endpoint policy unavailable")
	}
	var p Policy
	if len(data) > 64<<10 || webapi.StrictUnmarshal(data, &p) != nil || len(p.Endpoints) > 32 {
		return Policy{}, errors.New("invalid context endpoint policy")
	}
	seen := map[string]bool{}
	for _, e := range p.Endpoints {
		if !endpointPattern.MatchString(e.ID) || seen[e.ID] || kindOf(e.Backend) == "" || len(e.Name) < 1 || len(e.Name) > 100 || len(e.DataUse) < 1 || len(e.DataUse) > 1000 {
			return Policy{}, errors.New("invalid context endpoint policy")
		}
		if _, err := endpointURL(e); err != nil {
			return Policy{}, err
		}
		seen[e.ID] = true
	}
	if p.Endpoints == nil {
		p.Endpoints = []Endpoint{}
	}
	return p, nil
}
func endpointURL(e Endpoint) (*url.URL, error) {
	u, err := url.Parse(e.URL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || u.RawPath != "" || strings.Contains(u.Path, "..") || len(e.URL) > 1000 || u.Scheme != "https" && (u.Scheme != "http" || !e.AllowPrivateHTTP) {
		return nil, errors.New("invalid approved context endpoint")
	}
	return u, nil
}

// Pin DNS answers for the request and disallow redirects and environment proxies.
// Plain HTTP is an explicit administrator opt-in, restricted to private hosts.
func endpointClient(ctx context.Context, e Endpoint) (*http.Client, error) {
	u, err := endpointURL(e)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", u.Hostname())
	if err != nil || len(ips) == 0 {
		return nil, errors.New("context endpoint DNS unavailable")
	}
	for _, ip := range ips {
		ip = ip.Unmap()
		if !ip.IsValid() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || u.Scheme == "http" && !ip.IsLoopback() && !ip.IsPrivate() {
			return nil, errors.New("context endpoint address denied")
		}
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	addresses := append([]netip.Addr{}, ips...)
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, DisableKeepAlives: true, ResponseHeaderTimeout: 15 * time.Second, MaxResponseHeaderBytes: 16 << 10}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		var last error
		for _, ip := range addresses {
			c, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return c, nil
			}
			last = err
		}
		return nil, last
	}
	return &http.Client{Transport: transport, Timeout: 18 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}
