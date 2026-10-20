package modelgateway

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/modelendpoint"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Exact provider roots are checked again at execution. DNS is resolved once,
// every answer must be public, and the chosen address is dialed directly.
var providerRoots = map[string][]string{
	"openai":    {"https://api.openai.com/v1"},
	"anthropic": {"https://api.anthropic.com"},
	"gemini":    {"https://generativelanguage.googleapis.com"},
	"dashscope": {"https://dashscope.aliyuncs.com/compatible-mode/v1", "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"},
	"deepseek":  {"https://api.deepseek.com"},
	"moonshot":  {"https://api.moonshot.ai/v1", "https://api.moonshot.cn/v1"},
	"zai":       {"https://api.z.ai/api/paas/v4"},
}

func approvedParams(params map[string]any, customBases ...string) bool {
	model, ok := params["model"].(string)
	if !ok {
		return false
	}
	provider, id, ok := strings.Cut(model, "/")
	if !ok || len(id) == 0 || len(id) > 128 || strings.ContainsAny(id, "?&#%\\") {
		return false
	}
	base, _ := params["api_base"].(string)
	found := false
	for _, root := range providerRoots[provider] {
		found = found || root == base
	}
	if provider == "custom_openai" || provider == "custom_openai_responses" || provider == "custom_anthropic" {
		found = modelendpoint.ValidBase(base) && slices.Contains(customBases, base)
	}
	if !found {
		return false
	}
	if params["max_retries"] != float64(0) {
		return false
	}
	for k, v := range params {
		switch k {
		case "model", "api_base", "max_retries":
		case "reasoning_effort":
			if (provider != "gemini" && provider != "openai") || v != "none" {
				return false
			}
		case "extra_body":
			extra, ok := v.(map[string]any)
			if !ok {
				return false
			}
			for field, value := range extra {
				switch field {
				case "thinking":
					mode, ok := value.(map[string]any)
					if (provider != "moonshot" && provider != "zai" && provider != "deepseek") || !ok || len(mode) != 1 || mode["type"] != "disabled" {
						return false
					}
				case "enable_thinking":
					if provider != "dashscope" || value != false {
						return false
					}
				default:
					return false
				}
			}
		default:
			return false
		}
	}
	return true
}
func publicAddress(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, network := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "64:ff9b::/96", "64:ff9b:1::/48", "2002::/16"} {
		if netip.MustParsePrefix(network).Contains(ip) {
			return false
		}
	}
	return true
}
func providerClient(customBases ...string) *http.Client {
	tr := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, ResponseHeaderTimeout: 15 * time.Second, MaxConnsPerHost: 8, MaxIdleConns: 16, IdleConnTimeout: 30 * time.Second, ForceAttemptHTTP2: true}
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, errProtocol
		}
		custom := false
		local := false
		for _, base := range customBases {
			if !modelendpoint.ValidBase(base) {
				continue
			}
			u, _ := url.Parse(base)
			p := u.Port()
			if p == "" {
				p = "443"
			}
			if host == u.Hostname() && port == p {
				custom = true
				ip, e := netip.ParseAddr(host)
				local = host == "localhost" || (e == nil && (ip.IsPrivate() || ip.IsLoopback()))
			}
		}
		known := custom
		for _, roots := range providerRoots {
			for _, root := range roots {
				u, _ := url.Parse(root)
				known = known || (u.Hostname() == host && port == "443")
			}
		}
		if !known {
			return nil, errProtocol
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(ips) == 0 {
			return nil, errProtocol
		}
		for _, ip := range ips {
			if (local && !(ip.IsPrivate() || ip.IsLoopback())) || (!local && !publicAddress(ip)) {
				return nil, errors.New("model destination rejected")
			}
		}
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
	}
	return &http.Client{Transport: tr, Timeout: 120 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func frozenParameters(raw []byte) (map[string]any, error) {
	var frozen struct {
		Prefix     string
		Parameters map[string]any
		Catalog    struct {
			Model   string `json:"model"`
			APIBase string `json:"api_base"`
		}
	}
	if json.Unmarshal(raw, &frozen) != nil {
		return nil, errProtocol
	}
	if frozen.Parameters == nil {
		frozen.Parameters = map[string]any{}
	}
	frozen.Parameters["model"] = frozen.Prefix + "/" + frozen.Catalog.Model
	frozen.Parameters["api_base"] = frozen.Catalog.APIBase
	frozen.Parameters["max_retries"] = float64(0)
	return frozen.Parameters, nil
}
