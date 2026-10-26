package connectors

import (
	"encoding/json"
	"net/http"
	"strings"
	"unicode"
)

// Native authentication applies only to these exact official URLs. Personal
// gateways keep standard Bearer authentication even with the same catalog ID.
func nativeProvider(address string) string {
	switch address {
	case "https://docs.qq.com/openapi/mcp":
		return "tencent-docs"
	case "https://mcp.meeting.tencent.com/mcp/wemeet-open/v1":
		return "tencent-meeting"
	case "https://www.weiyun.com/api/v3/mcpserver":
		return "tencent-weiyun"
	case "https://mcp.lexiang-app.com/mcp":
		return "tencent-lexiang"
	case "https://mcp.didichuxing.com/mcp-servers":
		return "didi"
	}
	return ""
}

type lexiangCredential struct {
	Token   string `json:"token"`
	Company string `json:"company_from"`
}

func parseLexiangCredential(raw string) (lexiangCredential, error) {
	var credential lexiangCredential
	if json.Unmarshal([]byte(raw), &credential) != nil || strings.TrimSpace(credential.Token) == "" || strings.TrimSpace(credential.Company) == "" || len(credential.Company) > 256 || strings.IndexFunc(credential.Token+credential.Company, unicode.IsControl) >= 0 {
		return credential, invalid("请填写乐享企业标识和访问令牌")
	}
	return credential, nil
}

func applyEndpointCredential(r *http.Request, endpoint, credential string) error {
	r.Header.Del("Authorization")
	r.Header.Del("WyHeader")
	r.Header.Del("X-Tencent-Meeting-Token")
	r.Header.Del("X-Skill-Version")
	if credential == "" {
		return nil
	}
	switch nativeProvider(endpoint) {
	case "tencent-docs":
		r.Header.Set("Authorization", credential)
	case "tencent-meeting":
		r.Header.Set("X-Tencent-Meeting-Token", credential)
		r.Header.Set("X-Skill-Version", "v1.0.1")
	case "tencent-weiyun":
		r.Header.Set("WyHeader", "mcp_token="+credential)
	case "didi":
		query := r.URL.Query()
		query.Set("key", credential)
		r.URL.RawQuery = query.Encode()
	case "tencent-lexiang":
		value, err := parseLexiangCredential(credential)
		if err != nil {
			return err
		}
		query := r.URL.Query()
		query.Set("company_from", value.Company)
		r.URL.RawQuery = query.Encode()
		r.Header.Set("Authorization", "Bearer "+value.Token)
	default:
		r.Header.Set("Authorization", "Bearer "+credential)
	}
	return nil
}
