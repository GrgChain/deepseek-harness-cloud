package contextservice

import (
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

type EmbeddingConfig struct {
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	APIBase    string `json:"api_base,omitempty"`
	Dimensions int    `json:"dimensions"`
	APIKey     string `json:"api_key,omitempty"`
}

func defaultEmbedding() EmbeddingConfig {
	return EmbeddingConfig{Provider: "local", Model: "BAAI/bge-small-zh-v1.5", Dimensions: 512}
}

func validateEmbedding(input EmbeddingConfig, allowHTTP bool) (EmbeddingConfig, error) {
	if input.Provider == "local" {
		if input.Model != "" && input.Model != defaultEmbedding().Model || input.Dimensions != 0 && input.Dimensions != 512 || input.APIBase != "" || input.APIKey != "" {
			return input, invalid("本地Embedding模型配置不正确")
		}
		return defaultEmbedding(), nil
	}
	if input.Provider != "openai_compatible" || !utf8.ValidString(input.Model) || strings.TrimSpace(input.Model) == "" || len(input.Model) > 256 || strings.ContainsFunc(input.Model, unicode.IsControl) || input.Dimensions < 1 || input.Dimensions > 32768 || len(input.APIKey) > 4096 || strings.ContainsAny(input.APIKey, "\r\n\x00") {
		return input, invalid("请填写Embedding模型名称及 1–32768 的向量维度，并检查 API Key")
	}
	u, err := url.Parse(strings.TrimSpace(input.APIBase))
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || u.RawPath != "" || strings.Contains(u.Path, "..") || len(input.APIBase) > 1000 || u.Scheme != "https" && (u.Scheme != "http" || !allowHTTP) {
		return input, invalid("请输入Embedding服务的 HTTP(S) API Base URL，不要包含查询参数或凭据")
	}
	input.Model = strings.TrimSpace(input.Model)
	input.APIBase = strings.TrimRight(u.String(), "/")
	return input, nil
}

func sameEmbedding(a, b EmbeddingConfig) bool {
	a.APIKey = ""
	b.APIKey = ""
	return a == b
}
