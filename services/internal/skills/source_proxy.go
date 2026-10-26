package skills

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/config"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type sourceDialer func(context.Context, string, string) (net.Conn, error)

// An operator may explicitly configure a trusted local CONNECT proxy. The
// downloader still resolves and approves all destination IPs itself; the proxy
// receives only a pinned public IP:443, never a hostname to resolve. Normal
// HTTP_PROXY / HTTPS_PROXY settings remain ignored.
func configuredSourceDialer() (sourceDialer, error) {
	direct := (&net.Dialer{Timeout: 4 * time.Second}).DialContext
	path := os.Getenv("HC_SKILL_SOURCE_PROXY_FILE")
	if path == "" {
		return direct, nil
	}
	data, err := config.ReadPrivateFile(path)
	var policy struct {
		URL string `json:"url"`
	}
	if err != nil || len(data) > 4096 || webapi.StrictUnmarshal(data, &policy) != nil {
		return nil, errors.New("invalid skill source proxy configuration")
	}
	u, err := url.Parse(policy.URL)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || u.Path != "" && u.Path != "/" {
		return nil, errors.New("invalid skill source proxy configuration")
	}
	ip, err := netip.ParseAddr(u.Hostname())
	port, e := strconv.Atoi(u.Port())
	if err != nil || !ip.IsLoopback() || e != nil || port < 1 || port > 65535 {
		return nil, errors.New("skill source proxy must be an explicit loopback endpoint")
	}
	return connectSourceDialer(u.Host, direct), nil
}

func connectSourceDialer(proxy string, dial sourceDialer) sourceDialer {
	return func(ctx context.Context, network, authority string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(authority)
		ip, e := netip.ParseAddr(host)
		if err != nil || e != nil || !publicAddress(ip) || port != "443" || network != "tcp" {
			return nil, errors.New("skill source destination must be a pinned public IP:443")
		}
		conn, err := dial(ctx, "tcp", proxy)
		if err != nil {
			return nil, err
		}
		accepted := false
		defer func() {
			if !accepted {
				_ = conn.Close()
			}
		}()
		deadline := time.Now().Add(4 * time.Second)
		if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
			deadline = end
		}
		if err = conn.SetDeadline(deadline); err != nil {
			return nil, err
		}
		if _, err = io.WriteString(conn, "CONNECT "+authority+" HTTP/1.1\r\nHost: "+authority+"\r\n\r\n"); err != nil {
			return nil, err
		}
		response, err := http.ReadResponse(bufio.NewReader(io.LimitReader(conn, 16<<10)), &http.Request{Method: http.MethodConnect})
		if err != nil || response.StatusCode != http.StatusOK || response.ContentLength > 0 || len(response.TransferEncoding) > 0 {
			return nil, errors.New("skill source proxy tunnel unavailable")
		}
		if err = conn.SetDeadline(time.Time{}); err != nil {
			return nil, err
		}
		accepted = true
		return conn, nil
	}
}
