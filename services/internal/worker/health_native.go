package worker

import (
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Probe only the already inspected private IP. Native cookies remain server-side
// and no launch token, proxy, redirect, query or response body is used here.
func checkNativeHealth(ctx context.Context, endpoint, host string, cookie []byte) (application, access bool) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || net.ParseIP(u.Hostname()) == nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || !validNativeHeader(host, cookie) {
		return false, false
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = (&net.Dialer{Timeout: time.Second}).DialContext
	transport.ResponseHeaderTimeout = 2 * time.Second
	transport.MaxResponseHeaderBytes = 32 << 10
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request := func(path string, auth, websocket bool, origin string) (int, bool, bool) {
		req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(endpoint, "/")+path, nil)
		if err != nil {
			return 0, false, false
		}
		req.Host = host
		if auth {
			req.Header.Set("Cookie", string(cookie))
		}
		expected := ""
		if websocket {
			nonce := make([]byte, 16)
			if _, err = rand.Read(nonce); err != nil {
				return 0, false, false
			}
			key := base64.StdEncoding.EncodeToString(nonce)
			sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
			expected = base64.StdEncoding.EncodeToString(sum[:])
			req.Header.Set("Connection", "Upgrade")
			req.Header.Set("Upgrade", "websocket")
			req.Header.Set("Sec-WebSocket-Version", "13")
			req.Header.Set("Sec-WebSocket-Key", key)
			req.Header.Set("Origin", origin)
		}
		response, err := client.Do(req)
		if err != nil {
			return 0, false, false
		}
		defer response.Body.Close()
		connectionUpgrade := false
		for _, token := range strings.Split(response.Header.Get("Connection"), ",") {
			connectionUpgrade = connectionUpgrade || strings.EqualFold(strings.TrimSpace(token), "upgrade")
		}
		upgraded := websocket && response.StatusCode == 101 && strings.EqualFold(response.Header.Get("Upgrade"), "websocket") && connectionUpgrade && len(response.Header.Values("Sec-WebSocket-Accept")) == 1 && response.Header.Get("Sec-WebSocket-Accept") == expected
		return response.StatusCode, len(response.Header.Values("Set-Cookie")) == 0, upgraded
	}
	anonymous, cleanAnonymous, _ := request("/", false, false, "")
	authenticated, cleanAuthenticated, _ := request("/", true, false, "")
	application = authenticated == 200
	if anonymous != 401 || !application || !cleanAnonymous || !cleanAuthenticated {
		return application, false
	}
	denied, clean, _ := request("/api/remote.mux", false, true, "https://"+host)
	if denied != 401 || !clean {
		return application, false
	}
	wrongOrigin, clean, _ := request("/api/remote.mux", true, true, "https://invalid.invalid")
	if wrongOrigin != 403 || !clean {
		return application, false
	}
	accepted, clean, upgraded := request("/api/remote.mux", true, true, "https://"+host)
	return application, accepted == 101 && clean && upgraded
}
