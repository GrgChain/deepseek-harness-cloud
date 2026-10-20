package settings

import (
	"context"
	"crypto/tls"
	"errors"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/modelendpoint"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

type Policy struct {
	SchemaVersion  int       `json:"schema_version"`
	Endpoints      []string  `json:"endpoints"`
	Models         []string  `json:"models"`
	Gateways       []Gateway `json:"gateways,omitempty"`
	CustomAPIBases []string  `json:"custom_api_bases,omitempty"`
}

var modelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`)
var blockedNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("64:ff9b::/96"), netip.MustParsePrefix("64:ff9b:1::/48"), netip.MustParsePrefix("2002::/16"),
}

func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, p := range blockedNetworks {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}
func validEndpoint(value string) bool {
	u, err := url.Parse(value)
	if err != nil || len(value) > 2048 || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || strings.Contains(value, "\\") || strings.Contains(u.Path, "//") {
		return false
	}
	for _, part := range strings.Split(u.Path, "/") {
		if part == ".." || part == "." {
			return false
		}
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return false
	}
	if ip, err := netip.ParseAddr(host); err == nil && !publicIP(ip) {
		return false
	}
	return true
}
func ParsePolicy(data []byte) (*Policy, error) {
	var p Policy
	if err := webapi.StrictUnmarshal(data, &p); err != nil {
		return nil, errors.New("invalid model allowlist")
	}
	if !modelendpoint.ValidBases(p.CustomAPIBases) || p.SchemaVersion != 1 || len(p.Endpoints) > 16 || len(p.Models) > 100 || len(p.Gateways) > 8 || (len(p.Endpoints) == 0 && len(p.Gateways) == 0) || (len(p.Endpoints) > 0 && len(p.Models) == 0) {
		return nil, errors.New("invalid model allowlist bounds")
	}
	seen := map[string]bool{}
	for _, endpoint := range p.Endpoints {
		if !validEndpoint(endpoint) || seen[endpoint] {
			return nil, errors.New("invalid model allowlist endpoint")
		}
		seen[endpoint] = true
	}
	seen = map[string]bool{}
	for _, model := range p.Models {
		if !modelPattern.MatchString(model) || seen[model] {
			return nil, errors.New("invalid model allowlist model")
		}
		seen[model] = true
	}
	if err := p.validateGateways(); err != nil {
		return nil, err
	}
	return &p, nil
}
func (p *Policy) Allows(endpoint, model string) bool {
	return p != nil && validEndpoint(endpoint) && slices.Contains(p.Endpoints, endpoint) && slices.Contains(p.Models, model)
}

// Resolve every answer and dial an approved public address directly. No proxy,
// redirect or second DNS lookup may move a credential to another destination.
func modelHTTPClient() *http.Client {
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, MaxConnsPerHost: 2, MaxIdleConns: 4, IdleConnTimeout: 30 * time.Second, ResponseHeaderTimeout: 12 * time.Second, ForceAttemptHTTP2: true}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, errors.New("invalid model destination")
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(ips) == 0 {
			return nil, errors.New("model DNS unavailable")
		}
		for _, ip := range ips {
			if !publicIP(ip) {
				return nil, errors.New("model destination rejected")
			}
		}
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
	}
	return &http.Client{Transport: transport, Timeout: 14 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
}
