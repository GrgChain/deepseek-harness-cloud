package connectors

import (
	"net/netip"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func (s *Service) endpoint(id string) (Endpoint, error) {
	policy, err := s.loadPolicy()
	if err != nil {
		return Endpoint{}, unavailable()
	}
	for _, e := range policy.Endpoints {
		if e.ID == id {
			return e, nil
		}
	}
	return Endpoint{}, webapi.Fail(422, "ENDPOINT_NOT_APPROVED", "此MCP服务尚未批准或批准已撤销")
}

// Personal endpoints permit public HTTPS. Only an exact platform endpoint may
// opt into private addresses or HTTP; DNS checks and IP pinning still apply.
func (s *Service) personalEndpoint(address, catalog string) (Endpoint, error) {
	e := Endpoint{ID: "personal", URL: address, CatalogID: catalog}
	policy, err := s.loadPolicy()
	if err != nil {
		return e, unavailable()
	}
	for _, approved := range policy.Endpoints {
		if approved.URL == address {
			e.AllowPrivate, e.AllowHTTP = approved.AllowPrivate, approved.AllowHTTP
			break
		}
	}
	u, err := parseEndpoint(e)
	if err != nil {
		return e, invalid("请输入 HTTPS MCP 服务地址，凭据请单独填写；内网或 HTTP 地址须由管理员配置")
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && (!allowedIP(ip, e.AllowPrivate) || u.Scheme == "http" && !ip.IsPrivate() && !ip.IsLoopback()) {
		return e, invalid("此 MCP 服务地址不可访问，内网地址须由管理员配置")
	}
	return e, nil
}

func (s *Service) connectionEndpoint(c Connection) (Endpoint, error) {
	if c.EndpointID == "personal" {
		return s.personalEndpoint(c.URL, c.CatalogID)
	}
	return s.endpoint(c.EndpointID)
}
