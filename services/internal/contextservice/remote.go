package contextservice

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/config"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func (s *Service) endpoint(id, backend string) (Endpoint, error) {
	policy, err := s.policy()
	if err != nil {
		return Endpoint{}, unavailable()
	}
	for _, e := range policy.Endpoints {
		if e.ID == id && (backend == "" || e.Backend == backend) {
			return e, nil
		}
	}
	return Endpoint{}, webapi.Fail(422, "ENDPOINT_NOT_APPROVED", "此服务尚未获管理员批准，或批准已撤销")
}

func readAction(action string) bool {
	switch action {
	case "list", "search", "get", "export", "session-list", "session-get":
		return true
	}
	return false
}

func remoteWrite(action string) bool {
	switch action {
	case "apply", "delete", "rebuild":
		return true
	}
	return false
}

func (s *Service) remote(ctx context.Context, e Endpoint, v Connection, p identity.Principal, action string, input Action) (json.RawMessage, error) {
	settings, err := s.connectionSettings(p, v)
	if err != nil {
		return nil, unavailable()
	}
	authorization := settings.Credential
	var service *milvusService
	if settings.ServiceURL != "" {
		service = &milvusService{URL: settings.ServiceURL, Token: settings.Credential}
		if !supportsCustomService(e.Backend) || e.ServiceKeyFile == "" {
			return nil, unavailable()
		}
		var err error
		if e.Backend == "graphiti" {
			_, err = graphitiURL(service.URL, e.AllowPrivateHTTP)
		} else {
			_, err = milvusURL(service.URL, e.AllowPrivateHTTP)
		}
		if err != nil {
			return nil, err
		}
		key, err := config.ReadPrivateFile(e.ServiceKeyFile)
		if err != nil || len(bytes.TrimSpace(key)) < 32 {
			return nil, unavailable()
		}
		authorization = string(bytes.TrimSpace(key))
		clear(key)
	}
	if settings.Embedding != nil {
		if v.Kind != "knowledge" {
			return nil, unavailable()
		}
		if _, err := validateEmbedding(*settings.Embedding, e.AllowPrivateHTTP); err != nil {
			return nil, err
		}
	}
	client, err := endpointClient(ctx, e)
	if err != nil {
		return nil, unavailable()
	}
	// Namespaces do not expose browser-selected organization or user identities.
	digest := sha256.Sum256([]byte(p.OrganizationID + ":" + p.UserID + ":" + v.ID))
	var sessionNamespace string
	if v.Kind == "memory" && v.Backend == "openviking" && (action == "search" || action == "export" || action == "preview" || action == "apply") {
		// Search and complete archives span manual and official-plugin memories.
		// The second tenant is resolved here, never by the caller.
		nativeDigest := sha256.Sum256([]byte(p.OrganizationID + ":" + p.UserID + ":" + v.ID + ":dsh-session:" + input.Scope))
		sessionNamespace = hex.EncodeToString(nativeDigest[:])
	}
	if v.Kind == "memory" && (action == "export" || action == "preview") {
		client.Timeout = 90 * time.Second
		client.Transport.(*http.Transport).ResponseHeaderTimeout = 85 * time.Second
	}
	if action == "session" || action == "session-list" || action == "session-get" {
		// Native archives and extracted memories use a dedicated tenant per
		// connection. Session memory is shared across this user's workspaces.
		digest = sha256.Sum256([]byte(p.OrganizationID + ":" + p.UserID + ":" + v.ID + ":dsh-session:" + input.Scope))
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(struct {
		Contract         string           `json:"contract"`
		Namespace        string           `json:"namespace"`
		SessionNamespace string           `json:"session_namespace,omitempty"`
		Kind             string           `json:"kind"`
		Service          *milvusService   `json:"service,omitempty"`
		Embedding        *EmbeddingConfig `json:"embedding,omitempty"`
		Action
	}{Contract, hex.EncodeToString(digest[:]), sessionNamespace, v.Kind, service, settings.Embedding, input}); err != nil {
		return nil, invalid("请求内容格式不正确")
	}
	body := buffer.Bytes()
	defer clear(body)
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(e.URL, "/")+"/v1/"+action, bytes.NewReader(body))
	if err != nil {
		return nil, unavailable()
	}
	proxyKey, err := config.ReadPrivateFile(e.ProxyKeyFile)
	if err != nil || len(bytes.TrimSpace(proxyKey)) < 32 {
		return nil, unavailable()
	}
	stamp := strconv.FormatInt(time.Now().Unix(), 10)
	signature := hmac.New(sha256.New, bytes.TrimSpace(proxyKey))
	signature.Write([]byte(stamp + "\n" + req.URL.EscapedPath() + "\n"))
	signature.Write(body)
	req.Header.Set("X-Context-Timestamp", stamp)
	req.Header.Set("X-Context-Signature", hex.EncodeToString(signature.Sum(nil)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+authorization)
	response, err := client.Do(req)
	if err != nil {
		return nil, unavailable()
	}
	defer response.Body.Close()
	responseLimit := 4 << 20
	if v.Kind == "memory" && (action == "export" || action == "preview") {
		responseLimit = 20 << 20
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, int64(responseLimit)+1))
	if err != nil || len(data) > responseLimit {
		return nil, unavailable()
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// Never reflect external error bodies (which may contain secrets or content).
		switch response.StatusCode {
		case 404:
			return nil, missing()
		case 409:
			if v.Kind == "memory" && action == "export" {
				return nil, webapi.Fail(409, "VERSION_CONFLICT", "部分记忆尚未就绪，暂时无法完整导出，请稍后重试")
			}
			if v.Kind == "memory" && (action == "preview" || action == "apply") {
				return nil, webapi.Fail(409, "VERSION_CONFLICT", "记忆已变化，请重新预览后导入")
			}
			return nil, conflict()
		case 413:
			return nil, webapi.Fail(413, "PAYLOAD_TOO_LARGE", "记忆包超过导入导出限制（16 MiB 或 10000 条）")
		case 422:
			return nil, invalid("外部服务拒绝此内容或操作，请检查支持格式和预览有效期")
		case 501:
			return nil, webapi.Fail(422, "CONTEXT_UNSUPPORTED", "此版本的适配器尚不支持该操作")
		}
		return nil, unavailable()
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil || object == nil {
		return nil, unavailable()
	}
	return data, nil
}
