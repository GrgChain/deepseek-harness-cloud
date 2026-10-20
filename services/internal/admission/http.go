package admission

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/access"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/config"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

var errObservation = errors.New("runtime observation is unavailable or does not match controlled configuration")

type workerFacts struct {
	SchemaVersion         int      `json:"schema_version"`
	Domain                string   `json:"domain"`
	PublicPort            int      `json:"public_port"`
	RuntimeImage          string   `json:"runtime_image"`
	PreviousRuntimeImages []string `json:"previous_runtime_images,omitempty"`
	StorageImage          string   `json:"storage_image"`
	NetworkImage          string   `json:"network_image"`
	Workspace             bool     `json:"workspace"`
	Docker                bool     `json:"docker"`
	Storage               bool     `json:"storage"`
	Capacity              bool     `json:"capacity"`
	HostCPUCount          int      `json:"host_cpu_count"`
	HostAvailableMemory   int64    `json:"host_available_memory_bytes"`
}
type httpChecks struct {
	client                *http.Client
	transport             *http.Transport
	rules                 access.Rules
	workerURL, gatewayURL string
}

func newHTTPChecks(rules access.Rules, environment, workerURL, gatewayURL, caPath string) (*httpChecks, error) {
	for _, endpoint := range []string{workerURL, gatewayURL} {
		u, err := url.Parse(endpoint)
		if err != nil || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return nil, errObservation
		}
		ip := net.ParseIP(u.Hostname())
		port, err := strconv.Atoi(u.Port())
		if ip == nil || !ip.IsLoopback() || err != nil || port <= 10000 || port > 65535 {
			return nil, errObservation
		}
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		return nil, errObservation
	}
	if caPath != "" {
		raw, err := config.ReadPrivateFile(caPath)
		if err != nil || !roots.AppendCertsFromPEM(raw) {
			return nil, errObservation
		}
	}
	ingress, _ := url.Parse(rules.URL(rules.Host("00000000-0000-4000-8000-000000000001")))
	ingressPort := ingress.Port()
	if ingressPort == "" {
		ingressPort = "443"
	}
	ingressAddress := net.JoinHostPort(ingress.Hostname(), ingressPort)
	dialer := &net.Dialer{Timeout: time.Second}
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, MaxConnsPerHost: 2, MaxIdleConns: 4, IdleConnTimeout: 30 * time.Second, ResponseHeaderTimeout: 2 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if (environment == "local" || environment == "test") && rules.Domain == "harness.localhost" && address == ingressAddress {
			address = net.JoinHostPort("127.0.0.1", ingressPort)
		}
		return dialer.DialContext(ctx, network, address)
	}
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &httpChecks{client, transport, rules, workerURL, gatewayURL}, nil
}
func (p *httpChecks) Close() { p.transport.CloseIdleConnections() }
func (p *httpChecks) get(ctx context.Context, address string) (int, http.Header, []byte, error) {
	r, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return 0, nil, nil, errObservation
	}
	r.Header.Set("Accept", "application/json")
	response, err := p.client.Do(r)
	if err != nil {
		return 0, nil, nil, errObservation
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 8193))
	if err != nil || len(data) > 8192 {
		return 0, nil, nil, errObservation
	}
	return response.StatusCode, response.Header, data, nil
}
func (p *httpChecks) worker(ctx context.Context) (workerFacts, error) {
	code, _, body, err := p.get(ctx, p.workerURL+"/internal/preflight")
	var facts workerFacts
	portal, _ := url.Parse(p.rules.PortalOrigin)
	port := 443
	if portal.Port() != "" {
		port, _ = strconv.Atoi(portal.Port())
	}
	if err != nil || code != 200 || webapi.StrictUnmarshal(body, &facts) != nil || facts.SchemaVersion != 1 || facts.Domain != p.rules.Domain || facts.PublicPort != port || !digest.MatchString(facts.RuntimeImage) {
		return workerFacts{}, errObservation
	}
	return facts, nil
}
func (p *httpChecks) gateway(ctx context.Context) error {
	code, _, body, err := p.get(ctx, p.gatewayURL+"/readyz")
	var result struct {
		Status  string `json:"status"`
		Process string `json:"process"`
	}
	if err != nil || code != 200 || webapi.StrictUnmarshal(body, &result) != nil || result.Status != "ready" || result.Process != "gateway" {
		return errObservation
	}
	return nil
}
func (p *httpChecks) ingress(ctx context.Context) error {
	address := p.rules.URL(p.rules.Host("00000000-0000-4000-8000-000000000001")) + "/"
	code, headers, body, err := p.get(ctx, address)
	var result struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	}
	if err != nil || code != 401 || headers.Get("Referrer-Policy") != "no-referrer" || headers.Get("Cache-Control") != "no-store" || webapi.StrictUnmarshal(body, &result) != nil || result.Code != "UNAUTHENTICATED" || result.RequestID != headers.Get("X-Request-ID") || len(result.RequestID) != 36 {
		return errObservation
	}
	return nil
}
