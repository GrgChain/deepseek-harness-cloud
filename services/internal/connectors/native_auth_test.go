package connectors

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type requestTransport func(*http.Request) (*http.Response, error)

func (f requestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNativeCredentialsStayAtExactOfficialTarget(t *testing.T) {
	for _, tc := range []struct {
		address, credential, header, value, query string
	}{
		{"https://docs.qq.com/openapi/mcp", "private-token", "Authorization", "private-token", ""},
		{"https://mcp.meeting.tencent.com/mcp/wemeet-open/v1", "private-token", "X-Tencent-Meeting-Token", "private-token", ""},
		{"https://www.weiyun.com/api/v3/mcpserver", "private-token", "WyHeader", "mcp_token=private-token", ""},
		{"https://mcp.didichuxing.com/mcp-servers", "key +&?=汉字", "", "", "key=key+%2B%26%3F%3D%E6%B1%89%E5%AD%97"},
		{"https://mcp.lexiang-app.com/mcp", `{"token":"private-token","company_from":"company &+汉字"}`, "Authorization", "Bearer private-token", "company_from=company+%26%2B%E6%B1%89%E5%AD%97"},
		{"https://personal.example/mcp", "private-token", "Authorization", "Bearer private-token", ""},
		{"https://api.githubcopilot.com/mcp/", "private-token", "Authorization", "Bearer private-token", ""},
		{"https://api.gitee.com/mcp", "private-token", "Authorization", "Bearer private-token", ""},
		{"https://gitlab.com/api/v4/mcp", "private-token", "Authorization", "Bearer private-token", ""},
		{"https://openapi-rdc.aliyuncs.com/ai/mcp", "private-token", "Authorization", "Bearer private-token", ""},
		{"https://mcp.context7.com/mcp", "private-token", "Authorization", "Bearer private-token", ""},
		{"https://docs.qq.com.attacker.example/openapi/mcp", "private-token", "Authorization", "Bearer private-token", ""},
		{"https://docs.qq.com/openapi/other", "private-token", "Authorization", "Bearer private-token", ""},
	} {
		t.Run(tc.address, func(t *testing.T) {
			for _, method := range []string{"POST", "DELETE"} {
				received := 0
				transport := &boundedTransport{endpoint: tc.address, credential: tc.credential, base: requestTransport(func(r *http.Request) (*http.Response, error) {
					received++
					if r.URL.RawQuery != tc.query || r.GetBody != nil || r.Header.Get("Cookie") != "" {
						t.Fatal("request scope or query encoding changed")
					}
					for _, header := range []string{"Authorization", "WyHeader", "X-Tencent-Meeting-Token", "X-Skill-Version"} {
						want := ""
						if header == tc.header {
							want = tc.value
						}
						if header == "X-Skill-Version" && nativeProvider(tc.address) == "tencent-meeting" {
							want = "v1.0.1"
						}
						if r.Header.Get(header) != want {
							t.Fatalf("unexpected %s authentication", header)
						}
					}
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}"))}, nil
				})}
				req, _ := http.NewRequest(method, tc.address, strings.NewReader("{}"))
				for _, header := range []string{"Authorization", "WyHeader", "X-Tencent-Meeting-Token", "X-Skill-Version", "Cookie"} {
					req.Header.Set(header, "must-not-forward")
				}
				response, err := transport.RoundTrip(req)
				if err != nil {
					t.Fatal(err)
				}
				response.Body.Close()
				if req.URL.String() != tc.address || req.Header.Get("Authorization") != "must-not-forward" {
					t.Fatal("original request mutated; credential could leak through caller errors")
				}
				other, _ := http.NewRequest(method, "https://other.example/mcp", nil)
				if _, err := transport.RoundTrip(other); err == nil || received != 1 {
					t.Fatal("credentials forwarded to another address")
				}
			}
		})
	}
}

func TestLexiangRequiresBothEncryptedCredentialFields(t *testing.T) {
	for _, raw := range []string{"raw-token", `{}`, `{"token":"secret"}`, `{"token":"secret","company_from":" "}`, `{"token":"a\r\nb","company_from":"company"}`, `{"token":"secret","company_from":"a\u0000b"}`} {
		if _, err := parseLexiangCredential(raw); err == nil {
			t.Fatal("invalid native credential accepted")
		}
	}
}
