package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"
)

// OAuth clients are deployment secrets. Only endpoint metadata is returned by List.
type OAuthClient struct {
	URL          string `json:"url"`
	Issuer       string `json:"issuer"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret,omitempty"`
	AuthMethod   string `json:"auth_method,omitempty"`
	Resource     string `json:"resource,omitempty"`
	Scopes       string `json:"scopes,omitempty"`
}
type oauthProvider struct{ ID, Name, URL, Issuer, Resource, Scopes string }

var oauthProviders = []oauthProvider{
	{"tencent-ardot", "腾讯设计 Ardot", "https://ardot.tencent.com/mcp", "https://ardot.tencent.com", "https://ardot.tencent.com/mcp", "mcp:use"},
	{"notion", "Notion", "https://mcp.notion.com/mcp", "https://mcp.notion.com", "https://mcp.notion.com/mcp", ""},
	{"openalex", "OpenAlex", "https://mcp.openalex.org/mcp", "https://mcp.openalex.org", "https://mcp.openalex.org/mcp", "openalex:query"},
	{"dida365", "滴答清单", "https://mcp.dida365.com", "https://dida365.com", "https://mcp.dida365.com/", "tasks:read tasks:write"},
	{"qcc", "企查查", "https://agent.qcc.com/mcp/company/stream", "https://agent.qcc.com", "https://agent.qcc.com/mcp/company/stream", "mcp:tools"},
	// Vercel publishes a trailing slash in its resource identifier.
	{"vercel", "Vercel", "https://mcp.vercel.com", "https://vercel.com", "https://mcp.vercel.com/", "openid"},
}

func (s *Service) loadPolicy() (Policy, error) {
	p, err := s.policy()
	if err != nil {
		return p, err
	}
	// Never replace an administrator's binding or reserved ID.
	p.Endpoints = slices.Clone(p.Endpoints)
	for _, v := range builtinCatalog().Entries {
		if v.DefaultURL == "" {
			continue
		}
		exists := false
		for _, e := range p.Endpoints {
			if e.CatalogID == v.ID || e.ID == "builtin-"+v.ID {
				exists = true
			}
		}
		if !exists {
			p.Endpoints = append(p.Endpoints, Endpoint{ID: "builtin-" + v.ID, Name: v.Name, URL: v.DefaultURL, CatalogID: v.ID})
		}
	}
	return p, nil
}

type oauthMetadata struct {
	Issuer           string   `json:"issuer"`
	Authorization    string   `json:"authorization_endpoint"`
	Token            string   `json:"token_endpoint"`
	Registration     string   `json:"registration_endpoint"`
	ChallengeMethods []string `json:"code_challenge_methods_supported"`
	AuthMethods      []string `json:"token_endpoint_auth_methods_supported"`
	Scopes           []string `json:"scopes_supported"`
}

// Uses the same DNS pinning, private-network policy and redirect prohibition as
// MCP, but allows bounded OAuth GET/POST requests. No ambient cookies or proxy.
func (s *Service) oauthHTTP(ctx context.Context, method, address string, body []byte, headers http.Header, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	u, err := url.Parse(address)
	if err != nil || u.User != nil || u.Fragment != "" {
		return invalid("授权服务地址无效")
	}
	base := *u
	base.RawQuery = ""
	base.ForceQuery = false
	e, err := s.personalEndpoint(base.String(), "")
	if err != nil {
		return err
	}
	tr, err := pinnedTransport(ctx, e)
	if err != nil {
		return invalid("授权服务地址不可访问")
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, method, address, bytes.NewReader(body))
	if err != nil {
		return invalid("授权请求无效")
	}
	req.Header = headers.Clone()
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "DSH-Cloud-Connector/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return invalid("授权服务暂不可用，请稍后重试")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10+1))
	if err != nil || len(data) > 64<<10 {
		return invalid("授权服务响应无效")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var problem struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &problem)
		if problem.Error == "invalid_grant" || problem.Error == "invalid_token" {
			return errAuth
		}
		return invalid("授权服务拒绝请求，请检查服务状态或平台 OAuth 配置")
	}
	if json.Unmarshal(data, out) != nil {
		return invalid("授权服务响应格式无效")
	}
	return nil
}
func (s *Service) authorizationMetadata(ctx context.Context, issuer string) (oauthMetadata, error) {
	var m oauthMetadata
	e, err := s.personalEndpoint(issuer, "")
	if err != nil {
		return m, err
	}
	u, _ := parseEndpoint(e)
	address := u.Scheme + "://" + u.Host + "/.well-known/oauth-authorization-server" + strings.TrimRight(u.Path, "/")
	if err = s.oauthHTTP(ctx, "GET", address, nil, nil, &m); err != nil {
		return m, err
	}
	if strings.TrimRight(m.Issuer, "/") != strings.TrimRight(issuer, "/") || m.Authorization == "" || m.Token == "" || !slices.Contains(m.ChallengeMethods, "S256") {
		return m, invalid("此服务未提供兼容的 OAuth 2.0 + PKCE 授权")
	}
	for _, address := range []string{m.Authorization, m.Token, m.Registration} {
		if address == "" {
			continue
		}
		if _, err = s.personalEndpoint(address, ""); err != nil {
			return m, invalid("授权服务元数据地址无效")
		}
	}
	return m, nil
}
func (s *Service) oauthConfiguration(ctx context.Context, e Endpoint) (OAuthClient, oauthMetadata, error) {
	config := OAuthClient{URL: e.URL, Resource: e.URL}
	for _, v := range oauthProviders {
		if e.URL == v.URL {
			config.Issuer, config.Resource, config.Scopes = v.Issuer, v.Resource, v.Scopes
		}
	}
	p, err := s.loadPolicy()
	if err != nil {
		return config, oauthMetadata{}, unavailable()
	}
	for _, v := range p.OAuthClients {
		if v.URL == e.URL {
			config = v
			break
		}
	}
	if config.Issuer == "" {
		u, _ := url.Parse(e.URL)
		// RFC 9728 path-specific and origin-wide protected-resource metadata.
		candidates := []string{u.Scheme + "://" + u.Host + "/.well-known/oauth-protected-resource" + strings.TrimRight(u.Path, "/"), u.Scheme + "://" + u.Host + "/.well-known/oauth-protected-resource"}
		for _, address := range candidates {
			var prm struct {
				Resource string   `json:"resource"`
				Servers  []string `json:"authorization_servers"`
				Scopes   []string `json:"scopes_supported"`
			}
			if s.oauthHTTP(ctx, "GET", address, nil, nil, &prm) != nil {
				continue
			}
			if prm.Resource != e.URL || len(prm.Servers) == 0 {
				continue
			}
			config.Issuer, config.Resource = prm.Servers[0], prm.Resource
			config.Scopes = strings.Join(prm.Scopes, " ")
			break
		}
	}
	if config.Issuer == "" {
		return config, oauthMetadata{}, invalid("此 MCP 服务未提供 OAuth 授权信息，请使用服务支持的认证方式")
	}
	m, err := s.authorizationMetadata(ctx, config.Issuer)
	if config.Scopes == "" {
		config.Scopes = strings.Join(m.Scopes, " ")
	}
	return config, m, err
}
func validOAuthValue(v string) bool {
	return len(v) <= 8192 && strings.IndexFunc(v, unicode.IsControl) < 0
}
