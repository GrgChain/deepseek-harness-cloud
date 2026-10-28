package skills

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

var reserved = func() []netip.Prefix {
	values := []string{"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/3", "::/96", "::ffff:0:0/96", "64:ff9b::/96", "64:ff9b:1::/48", "100::/64", "2001::/23", "2001:db8::/32", "2002::/16", "fc00::/7", "fe80::/10", "ff00::/8"}
	out := make([]netip.Prefix, 0, len(values))
	for _, v := range values {
		out = append(out, netip.MustParsePrefix(v))
	}
	return out
}()

func publicAddress(address netip.Addr) bool {
	if !address.IsValid() || address.Is4In6() || !address.IsGlobalUnicast() || address.IsPrivate() {
		return false
	}
	for _, block := range reserved {
		if block.Contains(address) {
			return false
		}
	}
	return true
}

func sourceURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || u.Port() != "" && u.Port() != "443" {
		return nil, invalid("请输入不含凭据和查询参数的 HTTPS 文件或 GitHub 链接")
	}
	u.Host = strings.ToLower(u.Host)
	if u.Hostname() == "skills.sh" || u.Hostname() == "clawhub.ai" || u.Hostname() == "skillhub.cn" || u.Hostname() == "api.skillhub.cn" {
		return hubSourceURL(u.String())
	}
	// The familiar GitHub file URL has a public raw-file equivalent.
	if u.Hostname() == "github.com" {
		parts := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
		if len(parts) >= 5 && parts[2] == "blob" {
			u, err = url.Parse("https://raw.githubusercontent.com/" + strings.Join(append(parts[:2], parts[3:]...), "/"))
			if err != nil {
				return nil, invalid("GitHub 文件链接无效")
			}
		} else {
			_, err := githubLocation(u)
			return u, err
		}
	}
	if !strings.HasSuffix(strings.ToLower(u.Path), ".md") && !strings.HasSuffix(strings.ToLower(u.Path), ".zip") {
		return nil, invalid("请使用 GitHub 仓库、技能目录或文件链接，或 .md / .zip 直链")
	}
	if address, err := netip.ParseAddr(u.Hostname()); err == nil && !publicAddress(address) {
		return nil, invalid("来源不能是本机、内网或保留地址")
	}
	return u, nil
}

type sourceFetcher func(context.Context, string) ([]byte, string, error)

func fetchSource(ctx context.Context, raw string) ([]byte, string, error) {
	dial, err := configuredSourceDialer()
	if err != nil {
		return nil, "", invalid("技能来源网络配置不可用，请联系管理员")
	}
	return fetchSourceWithTransport(ctx, raw, sourceTransport(net.DefaultResolver.LookupNetIP, dial))
}

// The resolver and dialer are explicit so tests can exercise the complete TLS
// download path without permitting local addresses in production policy.
func sourceTransport(resolve func(context.Context, string, string) ([]netip.Addr, error), dial func(context.Context, string, string) (net.Conn, error)) *http.Transport {
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, DisableCompression: true, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 6 * time.Second}
	transport.DialContext = func(ctx context.Context, network, authority string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(authority)
		if err != nil || port != "443" {
			return nil, invalid("来源端口不可用")
		}
		addresses, err := resolve(ctx, "ip", host)
		if err != nil || len(addresses) == 0 || len(addresses) > 32 {
			return nil, invalid("来源域名无法解析")
		}
		for _, address := range addresses {
			if !publicAddress(address) {
				return nil, invalid("来源解析到了内网或保留地址")
			}
		}
		var last error
		for _, address := range addresses {
			connection, e := dial(ctx, "tcp", net.JoinHostPort(address.String(), port))
			if e == nil {
				return connection, nil
			}
			last = e
		}
		return nil, last
	}
	return transport
}

func fetchSourceWithTransport(ctx context.Context, raw string, transport *http.Transport) ([]byte, string, error) {
	defer transport.CloseIdleConnections()
	u, err := sourceURL(raw)
	if err != nil {
		return nil, "", err
	}
	budget := 12 * time.Second
	if u.Hostname() == "github.com" || u.Hostname() == "skills.sh" {
		budget = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if u.Hostname() == "skills.sh" || u.Hostname() == "clawhub.ai" || u.Hostname() == "skillhub.cn" || u.Hostname() == "api.skillhub.cn" {
		return fetchHubSource(ctx, client, u)
	}
	if u.Hostname() == "github.com" {
		location, err := githubLocation(u)
		if err != nil {
			return nil, "", err
		}
		content, err := fetchGitHubSource(ctx, client, location, "")
		if err != nil {
			return nil, "", err
		}
		return content, u.String(), nil
	}
	return downloadSource(ctx, client, u, 0, sourceURL)
}

func downloadSource(ctx context.Context, client *http.Client, u *url.URL, limit int, validate func(string) (*url.URL, error)) ([]byte, string, error) {
	var content bytes.Buffer
	final, err := downloadSourceTo(ctx, client, u, limit, validate, &content)
	if err != nil {
		return nil, "", err
	}
	return content.Bytes(), final, nil
}

// A zero limit streams the complete response to the caller's writer.
func downloadSourceTo(ctx context.Context, client *http.Client, u *url.URL, limit int, validate func(string) (*url.URL, error), output io.Writer) (string, error) {
	for hop := 0; hop <= 3; hop++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return "", invalid("来源链接无效")
		}
		request.Header.Set("User-Agent", "DSH-Cloud-Skills/1.0")
		request.Header.Set("Accept", "text/markdown,application/zip,application/octet-stream")
		response, err := client.Do(request)
		if err != nil {
			return "", invalid("来源下载失败，请检查链接是否公开可访问")
		}
		if response.StatusCode >= 300 && response.StatusCode < 400 {
			location, e := response.Location()
			response.Body.Close()
			if e != nil || hop == 3 {
				return "", invalid("来源重定向无效或次数过多")
			}
			u, e = validate(location.String())
			if e != nil {
				return "", e
			}
			continue
		}
		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusTooManyRequests {
				return "", invalid("来源访问受限或请求过多，请稍后重试")
			}
			return "", invalid("来源未返回可下载文件，请检查链接")
		}
		if limit > 0 && response.ContentLength > int64(limit) {
			response.Body.Close()
			return "", invalid("远端文件超过 5 MiB")
		}
		var reader io.Reader = response.Body
		if limit > 0 {
			reader = io.LimitReader(reader, int64(limit)+1)
		}
		size, e := io.Copy(output, reader)
		response.Body.Close()
		if limit > 0 && size > int64(limit) {
			return "", invalid("远端文件超过 5 MiB")
		}
		if e != nil {
			return "", invalid("来源读取失败，请稍后重试")
		}
		return u.String(), nil
	}
	return "", invalid("来源重定向次数过多")
}
