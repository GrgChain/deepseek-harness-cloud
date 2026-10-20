// Package modelendpoint validates explicitly configured custom model roots.
package modelendpoint

import (
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// HTTP is limited to a fixed local IP or localhost on an unprivileged port.
// HTTPS permits public host names or fixed local addresses; TLS is never skipped.
func ValidBase(base string) bool {
	if len(base) == 0 || len(base) > 2048 || strings.TrimSpace(base) != base || strings.HasSuffix(base, "/") || strings.ContainsAny(base, "\\?#") {
		return false
	}
	u, err := url.Parse(base)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawPath != "" || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	for _, part := range strings.Split(u.Path, "/") {
		if part == "." || part == ".." {
			return false
		}
	}
	if strings.Contains(u.Path, "//") {
		return false
	}
	host := u.Hostname()
	ip, e := netip.ParseAddr(host)
	local := host == "localhost" || (e == nil && (ip.IsPrivate() || ip.IsLoopback()))
	if e == nil && (ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsMulticast()) {
		return false
	}
	if u.Port() != "" {
		port, e := strconv.Atoi(u.Port())
		if e != nil || port < 1 || port > 65535 || (local && port < 1024) {
			return false
		}
	} else if local || u.Scheme == "http" {
		return false
	}
	return u.Scheme == "https" || local
}

func ValidBases(bases []string) bool {
	if len(bases) > 32 {
		return false
	}
	seen := map[string]bool{}
	for _, base := range bases {
		if !ValidBase(base) || seen[base] {
			return false
		}
		seen[base] = true
	}
	return true
}
