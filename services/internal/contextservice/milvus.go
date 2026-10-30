package contextservice

import (
	"bytes"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
)

// A newline cannot occur in a legacy bearer credential, making this envelope
// format unambiguous without changing existing encrypted records or key rotation.
const milvusSecretPrefix = "hc-milvus-v1\n"
const settingsSecretPrefix = "hc-context-v2\n"

type connectionSettings struct {
	Credential string           `json:"credential"`
	ServiceURL string           `json:"service_url,omitempty"`
	Embedding  *EmbeddingConfig `json:"embedding,omitempty"`
}

type milvusService struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

func milvusURL(value string, allowHTTP bool) (string, error) {
	u, err := url.Parse(strings.TrimSpace(value))
	if err != nil || u.Hostname() == "" || len(value) > 1000 || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || u.RawPath != "" || u.Path != "" && u.Path != "/" || u.Scheme != "https" && (u.Scheme != "http" || !allowHTTP) {
		return "", invalid("请输入 HTTP(S) 服务地址，不要包含路径、查询参数或凭据")
	}
	u.Path = ""
	return u.String(), nil
}

func (s *Service) connectionSettings(p identity.Principal, v Connection) (connectionSettings, error) {
	raw, err := s.cipher.Decrypt(v.envelope, s.aad(p, v.ID, "context-credential"))
	if err != nil {
		return connectionSettings{}, unavailable()
	}
	defer clear(raw)
	if bytes.HasPrefix(raw, []byte(settingsSecretPrefix)) {
		var settings connectionSettings
		if json.Unmarshal(raw[len(settingsSecretPrefix):], &settings) != nil {
			return settings, unavailable()
		}
		return settings, nil
	}
	if !bytes.HasPrefix(raw, []byte(milvusSecretPrefix)) {
		return connectionSettings{Credential: string(raw)}, nil
	}
	var service milvusService
	if v.Backend != "milvus" || json.Unmarshal(raw[len(milvusSecretPrefix):], &service) != nil || service.URL == "" {
		return connectionSettings{}, unavailable()
	}
	return connectionSettings{Credential: service.Token, ServiceURL: service.URL}, nil
}

func (s *Service) presentConnection(p identity.Principal, v Connection) (Connection, error) {
	if v.Kind == "knowledge" {
		settings, err := s.connectionSettings(p, v)
		if err != nil {
			return v, err
		}
		v.ServiceURL = settings.ServiceURL
		embedding := defaultEmbedding()
		if settings.Embedding != nil {
			embedding = *settings.Embedding
		}
		v.EmbeddingKeyConfigured = embedding.APIKey != ""
		embedding.APIKey = ""
		v.Embedding = &embedding
	}
	return v, nil
}
