package settings

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/config"
)

type GatewayModel struct {
	ID              string   `json:"id"`
	ResponseModels  []string `json:"response_models"`
	ContextWindow   int64    `json:"context_window"`
	MaxOutputTokens int64    `json:"max_output_tokens"`
	Streaming       bool     `json:"streaming"`
	ToolUse         bool     `json:"tool_use"`
}
type Gateway struct {
	Endpoint               string         `json:"endpoint"`
	Revision               string         `json:"revision"`
	Addresses              []string       `json:"addresses"`
	CAFile                 string         `json:"ca_file,omitempty"`
	Models                 []GatewayModel `json:"models"`
	ProbeRetriesDisabled   bool           `json:"probe_retries_disabled"`
	ProbeFallbacksDisabled bool           `json:"probe_fallbacks_disabled"`
	ca                     []byte
}
type ModelManifest struct {
	GatewayModel
	Revision string `json:"revision"`
	Digest   string `json:"digest"`
}

var errGatewayPolicy = errors.New("invalid gateway approval: exact HTTPS origin, addresses, revision and model capabilities required")

func gatewayOrigin(value string) bool {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || len(value) > 2048 || strings.Contains(value, "\\") || strings.ToLower(u.Host) != u.Host {
		return false
	}
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return false
		}
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && !gatewayIP(ip) {
		return false
	}
	return true
}
func gatewayIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	// Never approve metadata, carrier, documentation or translation ranges.
	for _, prefix := range blockedNetworks {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}
func (p *Policy) validateGateways() error {
	endpoints := map[string]bool{}
	for i := range p.Gateways {
		gateway := &p.Gateways[i]
		if !gatewayOrigin(gateway.Endpoint) || endpoints[gateway.Endpoint] || !modelPattern.MatchString(gateway.Revision) || len(gateway.Addresses) == 0 || len(gateway.Addresses) > 16 || len(gateway.Models) > 100 || !gateway.ProbeRetriesDisabled || !gateway.ProbeFallbacksDisabled {
			return errGatewayPolicy
		}
		endpoints[gateway.Endpoint] = true
		totalAddresses := 0
		for _, address := range gateway.Addresses {
			prefix, err := netip.ParsePrefix(address)
			if err != nil || prefix != prefix.Masked() || !gatewayIP(prefix.Addr()) || prefix.Addr().Is4In6() || (prefix.Addr().Is4() && prefix.Bits() < 24) || !prefix.Addr().Is4() {
				return errGatewayPolicy
			}
			totalAddresses += 1 << uint(32-prefix.Bits())
			if totalAddresses > 256 {
				return errGatewayPolicy
			}
		}
		models := map[string]bool{}
		for _, m := range gateway.Models {
			if !modelPattern.MatchString(m.ID) || models[m.ID] || m.ContextWindow < 1024 || m.ContextWindow > 10000000 || m.MaxOutputTokens < 128 || m.MaxOutputTokens > 1000000 || m.MaxOutputTokens > m.ContextWindow || len(m.ResponseModels) == 0 || len(m.ResponseModels) > 16 {
				return errGatewayPolicy
			}
			models[m.ID] = true
			seen := map[string]bool{}
			for _, id := range m.ResponseModels {
				if !modelPattern.MatchString(id) || seen[id] {
					return errGatewayPolicy
				}
				seen[id] = true
			}
		}
		if gateway.CAFile != "" {
			pem, err := config.ReadPrivateFile(gateway.CAFile)
			if err != nil || len(pem) > 128<<10 {
				return errGatewayPolicy
			}
			roots := x509.NewCertPool()
			if !roots.AppendCertsFromPEM(pem) {
				return errGatewayPolicy
			}
			gateway.ca = pem
		}
	}
	return nil
}
func (p *Policy) Gateway(endpoint string) (Gateway, bool) {
	if p != nil {
		for _, gateway := range p.Gateways {
			if gateway.Endpoint == endpoint {
				return gateway, true
			}
		}
	}
	return Gateway{}, false
}
func (p *Policy) Manifest(endpoint, model string) (ModelManifest, bool) {
	gateway, ok := p.Gateway(endpoint)
	if !ok {
		return ModelManifest{}, false
	}
	for _, m := range gateway.Models {
		if m.ID == model {
			return gateway.Manifest(m), true
		}
	}
	return ModelManifest{}, false
}
func (p *Policy) AllowsConnection(provider, protocol, endpoint, model string) bool {
	if provider == "deepseek-official" && protocol == "deepseek-messages" {
		return p.Allows(endpoint, model)
	}
	if provider != "platform-models" || protocol != "anthropic-messages" {
		return false
	}
	_, ok := p.Manifest(endpoint, model)
	return ok
}
func (p *Policy) AllowsModel(m Model) bool {
	if !p.AllowsConnection(m.Provider, m.Protocol, m.Endpoint, m.Model) {
		return false
	}
	if m.Provider != "platform-models" {
		return true
	}
	manifest, ok := p.Manifest(m.Endpoint, m.Model)
	return ok && m.Manifest != nil && m.Manifest.Digest == manifest.Digest
}
func (g Gateway) AllowsAddress(ip netip.Addr) bool {
	if !gatewayIP(ip) {
		return false
	}
	for _, address := range g.Addresses {
		prefix, err := netip.ParsePrefix(address)
		if err == nil && prefix.Contains(ip.Unmap()) {
			return true
		}
	}
	return false
}
func (g Gateway) HTTPClient() *http.Client {
	origin, _ := url.Parse(g.Endpoint)
	port := origin.Port()
	if port == "" {
		port = "443"
	}
	roots, _ := x509.SystemCertPool()
	if roots == nil {
		roots = x509.NewCertPool()
	}
	if len(g.ca) > 0 {
		roots.AppendCertsFromPEM(g.ca)
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, ResponseHeaderTimeout: 10 * time.Second, ForceAttemptHTTP2: true, MaxConnsPerHost: 2, DisableKeepAlives: true}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, p, err := net.SplitHostPort(address)
		if err != nil || host != origin.Hostname() || p != port {
			return nil, errGatewayPolicy
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(ips) == 0 {
			return nil, errGatewayPolicy
		}
		for _, ip := range ips {
			if !g.AllowsAddress(ip) {
				return nil, errGatewayPolicy
			}
		}
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
	}
	return &http.Client{Transport: transport, Timeout: 14 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func (m ModelManifest) MatchesResponse(id string) bool { return slices.Contains(m.ResponseModels, id) }

func (g Gateway) CertificatePEM() string { return string(g.ca) }

// Manifest compiles trusted database catalog capabilities against deployment network approval.
func (gateway Gateway) Manifest(m GatewayModel) ModelManifest {
	ca := sha256.Sum256(gateway.ca)
	frozen, _ := json.Marshal(struct {
		Endpoint, Revision, CA string
		Addresses              []string
		Model                  GatewayModel
	}{gateway.Endpoint, gateway.Revision, hex.EncodeToString(ca[:]), gateway.Addresses, m})
	digest := sha256.Sum256(frozen)
	return ModelManifest{m, gateway.Revision, "sha256:" + hex.EncodeToString(digest[:])}
}
