package worker

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var ErrNativeUnavailable = errors.New("native runtime authentication is unavailable")
var ErrNativeModelUnavailable = errors.New("native default model is unavailable")
var nativeValue = regexp.MustCompile(`^v1\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$`)

// Publishing RUNNING requires HTTP/WS readiness and an advertised default model.
func exchangeReadyNative(ctx context.Context, endpoint, host, token, provider, model string) ([]byte, time.Time, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cookie, expires, err := exchangeNative(ctx, endpoint, host, token)
	if err != nil {
		return nil, time.Time{}, err
	}
	application, access := checkNativeHealth(ctx, endpoint, host, cookie)
	if !application || !access {
		clear(cookie)
		return nil, time.Time{}, ErrNativeUnavailable
	}
	if !checkNativeModel(ctx, endpoint, host, cookie, provider, model) {
		clear(cookie)
		return nil, time.Time{}, ErrNativeModelUnavailable
	}
	return cookie, expires, nil
}

// Read the same catalog as the browser, without inference or changing defaults.
// A saved selection alone is insufficient: the adapter must advertise its route.
func checkNativeModel(ctx context.Context, endpoint, host string, cookie []byte, provider, model string) bool {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || net.ParseIP(u.Hostname()) == nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || provider == "" || model == "" || !validNativeHeader(host, cookie) {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = (&net.Dialer{Timeout: 2 * time.Second}).DialContext
	transport.ResponseHeaderTimeout = 12 * time.Second
	transport.MaxResponseHeaderBytes = 32 << 10
	defer transport.CloseIdleConnections()
	const rpcID = "harness-model-readiness"
	body := `{"type":"client-request","rpcId":"` + rpcID + `","method":"session/modelCatalog","payload":{"args":{}}}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(endpoint, "/")+"/api/session/modelCatalog", strings.NewReader(body))
	if err != nil {
		return false
	}
	req.Host = host
	req.Header.Set("Cookie", string(cookie))
	req.Header.Set("Origin", "https://"+host)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || len(response.Header.Values("Set-Cookie")) != 0 || !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		return false
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (128<<10)+1))
	if err != nil || len(raw) > 128<<10 {
		return false
	}
	var envelope struct {
		Type   string `json:"type"`
		RPCID  string `json:"rpcId"`
		Result struct {
			OK    bool `json:"ok"`
			Value struct {
				Default struct {
					Provider string `json:"provider"`
					Model    string `json:"model"`
				} `json:"default"`
				Routable []string `json:"routableProviders"`
				Groups   []struct {
					ID     string `json:"id"`
					Models []struct {
						ID string `json:"id"`
					} `json:"models"`
				} `json:"groups"`
			} `json:"value"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Type != "server-response" || envelope.RPCID != rpcID || !envelope.Result.OK {
		return false
	}
	value := envelope.Result.Value
	if value.Default.Provider != provider || value.Default.Model != model {
		return false
	}
	routable := false
	for _, id := range value.Routable {
		if id == provider {
			routable = true
		}
	}
	if !routable {
		return false
	}
	for _, group := range value.Groups {
		if group.ID == provider {
			for _, item := range group.Models {
				if item.ID == model {
					return true
				}
			}
		}
	}
	return false
}

// exchangeNative is used only against an already inspected container address.
// It has no proxy, cookies, redirects or logging hooks. The private launch query
// never reaches the browser, Terraform providers, ordinary logs or an error string.
func exchangeNative(ctx context.Context, endpoint, host, token string) ([]byte, time.Time, error) {
	base, err := url.Parse(endpoint)
	if err != nil || base.Scheme != "http" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Path != "" && base.Path != "/") || net.ParseIP(base.Hostname()) == nil || len(token) == 0 || len(token) > 1024 || strings.ContainsAny(token, "\r\n\x00") {
		return nil, time.Time{}, ErrNativeUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = (&net.Dialer{Timeout: 2 * time.Second}).DialContext
	transport.ResponseHeaderTimeout = 2 * time.Second
	transport.MaxResponseHeaderBytes = 32 << 10
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request := func(path, cookie string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(endpoint, "/")+path, nil)
		if err != nil {
			return nil, ErrNativeUnavailable
		}
		req.Host = host
		if cookie != "" {
			req.Header.Set("Cookie", cookie)
		}
		response, err := client.Do(req)
		if err != nil {
			return nil, ErrNativeUnavailable
		}
		return response, nil
	}
	anonymous, err := request("/", "")
	if err != nil {
		return nil, time.Time{}, ErrNativeUnavailable
	}
	anonymous.Body.Close()
	if anonymous.StatusCode != 401 {
		return nil, time.Time{}, ErrNativeUnavailable
	}
	response, err := request("/?token="+url.QueryEscape(token), "")
	if err != nil {
		return nil, time.Time{}, ErrNativeUnavailable
	}
	response.Body.Close()
	if response.StatusCode != 303 || response.Header.Get("Location") != "./" || len(response.Header.Values("Set-Cookie")) != 1 {
		return nil, time.Time{}, ErrNativeUnavailable
	}
	cookies := response.Cookies()
	if len(cookies) != 1 {
		return nil, time.Time{}, ErrNativeUnavailable
	}
	cookie := cookies[0]
	expected := nativeCookieName(host)
	now := time.Now().UTC()
	if cookie.Name != expected || !nativeValue.MatchString(cookie.Value) || len(cookie.Value) > 8192 || cookie.Domain != "" || cookie.Path != "/" || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.MaxAge <= 0 || cookie.MaxAge > 2592000 || !cookie.Expires.After(now) || len(cookie.Unparsed) > 0 {
		return nil, time.Time{}, ErrNativeUnavailable
	}
	header := cookie.Name + "=" + cookie.Value
	response, err = request("/", header)
	if err != nil {
		return nil, time.Time{}, ErrNativeUnavailable
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		return nil, time.Time{}, ErrNativeUnavailable
	}
	expires := now.Add(8 * time.Hour)
	if cookie.Expires.Before(expires) {
		expires = cookie.Expires
	}
	maxAge := now.Add(time.Duration(cookie.MaxAge) * time.Second)
	if maxAge.Before(expires) {
		expires = maxAge
	}
	return []byte(header), expires, nil
}

func nativeCookieName(host string) string {
	sum := sha256.Sum256([]byte(host))
	return "dsh-auth-" + base64.RawURLEncoding.EncodeToString(sum[:])
}
func validNativeHeader(host string, header []byte) bool {
	prefix := nativeCookieName(host) + "="
	return len(header) <= 8192 && strings.HasPrefix(string(header), prefix) && nativeValue.Match(header[len(prefix):])
}
