package access

import (
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
)

// IP access is an additional TLS entry point. Canonical runtime authorities
// and the OIDC issuer remain stable, including for existing workspaces/users.
type IPOptions struct {
	Address    string `json:"address"`
	PortalPort int    `json:"portal_port"`
	// Legacy port-range fields are accepted when reading existing configurations.
	// IP workspaces now share PortalPort and use an instance-specific mount.
	WorkspacePortStart int    `json:"workspace_port_start,omitempty"`
	WorkspacePortEnd   int    `json:"workspace_port_end,omitempty"`
	CertificateFile    string `json:"certificate_file"`
	KeyFile            string `json:"key_file"`
	PortalUpstream     string `json:"portal_upstream"`
	UpstreamCAFile     string `json:"upstream_ca_file,omitempty"`
}

func (o IPOptions) Validate() error {
	ip := net.ParseIP(o.Address)
	if ip == nil || ip.String() != o.Address || !(ip.IsGlobalUnicast() || ip.IsLoopback()) ||
		o.PortalPort < 1 || o.PortalPort > 65535 {
		return ErrConfiguration
	}
	for _, path := range []string{o.CertificateFile, o.KeyFile} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return ErrConfiguration
		}
	}
	if o.UpstreamCAFile != "" && (!filepath.IsAbs(o.UpstreamCAFile) || filepath.Clean(o.UpstreamCAFile) != o.UpstreamCAFile) {
		return ErrConfiguration
	}
	u, err := url.Parse(o.PortalUpstream)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
		return ErrConfiguration
	}
	upstream := net.ParseIP(u.Hostname())
	port, err := strconv.Atoi(u.Port())
	if upstream == nil || !upstream.IsLoopback() || err != nil || port < 1 || port > 65535 {
		return ErrConfiguration
	}
	return nil
}

func (o IPOptions) Origin(port int) string {
	if port == 443 {
		host := o.Address
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		return "https://" + host
	}
	return "https://" + net.JoinHostPort(o.Address, strconv.Itoa(port))
}
