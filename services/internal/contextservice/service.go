package contextservice

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/config"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type Connection struct {
	ID                     string           `json:"id"`
	Kind                   string           `json:"kind"`
	Backend                string           `json:"backend"`
	EndpointID             string           `json:"endpoint_id"`
	ServiceURL             string           `json:"service_url,omitempty"`
	Embedding              *EmbeddingConfig `json:"embedding,omitempty"`
	EmbeddingKeyConfigured bool             `json:"embedding_key_configured,omitempty"`
	Name                   string           `json:"name"`
	Version                int64            `json:"version"`
	Enabled                bool             `json:"enabled"`
	Status                 string           `json:"status"`
	Capabilities           json.RawMessage  `json:"capabilities"`
	VerifiedAt             *time.Time       `json:"verified_at"`
	UpdatedAt              time.Time        `json:"updated_at"`
	Integration            string           `json:"integration"`
	envelope               security.Envelope
}
type Input struct {
	Name            string           `json:"name"`
	EndpointID      string           `json:"endpoint_id"`
	ServiceURL      string           `json:"service_url"`
	Embedding       *EmbeddingConfig `json:"embedding"`
	Credential      string           `json:"credential"`
	Enabled         *bool            `json:"enabled"`
	ExpectedVersion int64            `json:"expected_version"`
	ConfirmedName   string           `json:"confirmed_name"`
}

// Only these fields cross the external boundary. Scope and namespace are always
// resolved by the platform; clients cannot submit filters or external tenant IDs.
type Action struct {
	ExpectedVersion int64           `json:"expected_version"`
	IdempotencyKey  string          `json:"idempotency_key"`
	Scope           string          `json:"scope"`
	Query           string          `json:"query"`
	SearchMode      string          `json:"search_mode,omitempty"`
	MinSimilarity   *float64        `json:"min_similarity,omitempty"`
	Page            int             `json:"page,omitempty"`
	PageSize        int             `json:"page_size,omitempty"`
	Limit           int             `json:"limit"`
	RecordID        string          `json:"record_id"`
	RecordVersion   int64           `json:"record_version"`
	Content         string          `json:"content"`
	Title           string          `json:"title"`
	Type            string          `json:"type"`
	Source          string          `json:"source"`
	Token           string          `json:"token"`
	Mode            string          `json:"mode"`
	Package         json.RawMessage `json:"package,omitempty"`
	Confirmed       bool            `json:"confirmed"`
	AcceptLoss      bool            `json:"accept_loss"`
}
type Binding struct {
	InstanceID   string `json:"instance_id"`
	ConnectionID string `json:"connection_id"`
	Version      int64  `json:"version"`
	Enabled      bool   `json:"enabled"`
	Integration  string `json:"integration"`
}
type Service struct {
	pool        *pgxpool.Pool
	cipher      *security.SecretCipher
	environment string
	maintenance bool
	policy      policyLoader
}

func NewService(pool *pgxpool.Pool, cipher *security.SecretCipher, environment string, maintenance bool, path string) *Service {
	return &Service{pool: pool, cipher: cipher, environment: environment, maintenance: maintenance, policy: func() (Policy, error) { return LoadPolicy(path) }}
}
func invalid(message string) error { return webapi.Fail(422, "CONTEXT_INVALID", message) }
func missing() error               { return webapi.Fail(404, "NOT_FOUND", "连接、内容或授权范围不存在") }
func conflict() error {
	return webapi.Fail(409, "VERSION_CONFLICT", "配置已变更，请刷新后重试")
}
func unavailable() error {
	return webapi.Fail(503, "CONTEXT_UNAVAILABLE", "外部服务暂不可用，普通工作空间使用不受影响")
}
func (s *Service) aad(p identity.Principal, id, purpose string) security.Context {
	return security.Context{Environment: s.environment, OrganizationID: p.OrganizationID, RecordID: id, Purpose: purpose}
}
func (s *Service) begin(ctx context.Context, p identity.Principal, kind string, write bool) (pgx.Tx, error) {
	if kind != "memory" && kind != "knowledge" {
		return nil, missing()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	permission := kind + ":read"
	if write {
		permission = kind + ":manage"
	}
	if err = identity.RequirePermission(ctx, tx, p, permission); err == nil && write && s.maintenance {
		err = webapi.Fail(503, "MAINTENANCE", "平台维护中，请稍后重试")
	}
	if err == nil && write {
		_, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "context:"+p.OrganizationID+":"+p.UserID)
	}
	if err != nil {
		_ = tx.Rollback(context.Background())
		return nil, err
	}
	return tx, nil
}

const columns = `id::text,kind,backend,endpoint_id,name,version,enabled,status,capabilities,verified_at,updated_at,key_id,nonce,ciphertext`

func scan(row pgx.Row) (Connection, error) {
	var v Connection
	err := row.Scan(&v.ID, &v.Kind, &v.Backend, &v.EndpointID, &v.Name, &v.Version, &v.Enabled, &v.Status, &v.Capabilities, &v.VerifiedAt, &v.UpdatedAt, &v.envelope.KeyID, &v.envelope.Nonce, &v.envelope.Ciphertext)
	v.Integration = "NOT_CONNECTED"
	if errors.Is(err, pgx.ErrNoRows) {
		err = missing()
	}
	return v, err
}
func load(ctx context.Context, tx pgx.Tx, p identity.Principal, kind, id string) (Connection, error) {
	if !idPattern.MatchString(id) {
		return Connection{}, missing()
	}
	return scan(tx.QueryRow(ctx, `SELECT `+columns+` FROM context_connections WHERE org_id=$1 AND owner_id=$2 AND kind=$3 AND id=$4`, p.OrganizationID, p.UserID, kind, id))
}
func audit(ctx context.Context, tx pgx.Tx, p identity.Principal, id, action, trace string) error {
	if trace == "" {
		trace = webapi.ID()
	}
	_, err := tx.Exec(ctx, `INSERT INTO audit_events(id,org_id,actor_id,owner_id,resource_id,action,outcome,trace_id) VALUES($1,$2,$3,$3,$4,$5,'SUCCEEDED',$6)`, webapi.ID(), p.OrganizationID, p.UserID, id, action, trace)
	return err
}
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
func (s *Service) List(ctx context.Context, p identity.Principal, kind string) (any, error) {
	tx, err := s.begin(ctx, p, kind, false)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	rows, err := tx.Query(ctx, `SELECT `+columns+` FROM context_connections WHERE org_id=$1 AND owner_id=$2 AND kind=$3 ORDER BY updated_at DESC,id LIMIT 101`, p.OrganizationID, p.UserID, kind)
	if err != nil {
		return nil, err
	}
	items := []Connection{}
	for rows.Next() {
		v, e := scan(rows)
		if e == nil {
			v, e = s.presentConnection(p, v)
		}
		if e != nil {
			rows.Close()
			return nil, e
		}
		items = append(items, v)
	}
	rows.Close()
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	policy, err := s.policy()
	if err != nil {
		return nil, unavailable()
	}
	endpoints := []Endpoint{}
	for _, e := range policy.Endpoints {
		if kindOf(e.Backend) == kind {
			e.URL = ""
			e.ProxyKeyFile = ""
			e.CustomService = (e.Backend == "milvus" || e.Backend == "weaviate" || e.Backend == "qdrant") && e.ServiceKeyFile != ""
			e.ServiceKeyFile = ""
			endpoints = append(endpoints, e)
		}
	}
	bindings := []Binding{}
	rows, err = tx.Query(ctx, `SELECT b.instance_id::text,b.connection_id::text,b.version,b.enabled FROM context_bindings b JOIN context_connections c ON c.id=b.connection_id WHERE b.org_id=$1 AND b.owner_id=$2 AND c.kind=$3 ORDER BY b.created_at`, p.OrganizationID, p.UserID, kind)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var b Binding
		b.Integration = "NOT_CONNECTED"
		if err = rows.Scan(&b.InstanceID, &b.ConnectionID, &b.Version, &b.Enabled); err != nil {
			rows.Close()
			return nil, err
		}
		bindings = append(bindings, b)
	}
	rows.Close()
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return map[string]any{"items": items, "endpoints": endpoints, "bindings": bindings, "integration": "NOT_CONNECTED"}, nil
}
func pending(ctx context.Context, tx pgx.Tx, id string) error {
	var exists bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM context_operations WHERE connection_id=$1 AND status IN ('PENDING','UNKNOWN'))`, id).Scan(&exists)
	if err != nil {
		return err
	}
	if exists {
		return webapi.Fail(409, "CONTEXT_PENDING", "外部服务正在处理，请稍后点击“核实待确认操作”")
	}
	return nil
}
func (s *Service) Save(ctx context.Context, p identity.Principal, kind, id string, input Input, trace string) (Connection, error) {
	if strings.TrimSpace(input.Name) == "" || len([]rune(input.Name)) > 100 || !utf8.ValidString(input.Name) || strings.ContainsAny(input.Name, "\x00\r\n") {
		return Connection{}, invalid("请输入 1～100 字符的连接名称")
	}
	endpoint, err := s.endpoint(input.EndpointID, "")
	if err != nil {
		return Connection{}, err
	}
	if kindOf(endpoint.Backend) != kind {
		return Connection{}, invalid("服务类型不匹配")
	}
	if input.ServiceURL != "" {
		if (endpoint.Backend != "milvus" && endpoint.Backend != "weaviate" && endpoint.Backend != "qdrant") || endpoint.ServiceKeyFile == "" {
			return Connection{}, invalid("此服务尚不支持自定义地址")
		}
		input.ServiceURL, err = milvusURL(input.ServiceURL, endpoint.AllowPrivateHTTP)
		if err != nil {
			return Connection{}, err
		}
	}
	if len(input.Credential) > 4096 || strings.ContainsAny(input.Credential, "\r\n\x00") {
		return Connection{}, invalid("凭据格式不正确")
	}
	if kind == "knowledge" && id == "" && (input.Embedding == nil || input.Embedding.Provider != "openai_compatible") {
		return Connection{}, invalid("请配置自建或云端 Embedding API 的地址、模型名称和向量维度")
	}
	if input.Embedding != nil {
		if kind != "knowledge" {
			return Connection{}, invalid("记忆连接不支持此Embedding模型配置")
		}
		normalized, e := validateEmbedding(*input.Embedding, endpoint.AllowPrivateHTTP)
		if e != nil {
			return Connection{}, e
		}
		input.Embedding = &normalized
	}
	tx, err := s.begin(ctx, p, kind, true)
	if err != nil {
		return Connection{}, err
	}
	defer tx.Rollback(context.Background())
	v := Connection{ID: webapi.ID(), Kind: kind, Backend: endpoint.Backend, EndpointID: endpoint.ID, Name: strings.TrimSpace(input.Name), Enabled: true}
	if id != "" {
		v, err = load(ctx, tx, p, kind, id)
		if err != nil {
			return v, err
		}
		if input.ExpectedVersion != v.Version {
			return v, conflict()
		}
		if err = pending(ctx, tx, id); err != nil {
			return v, err
		}
		if input.EndpointID != v.EndpointID {
			return v, invalid("更换外部服务请新建连接，原数据和绑定不会迁移")
		}
	}
	if input.Enabled != nil {
		v.Enabled = *input.Enabled
	}
	settings := connectionSettings{}
	if id != "" {
		settings, err = s.connectionSettings(p, v)
		if err != nil {
			return v, err
		}
		if input.ServiceURL != "" && input.ServiceURL != settings.ServiceURL {
			return v, invalid("更换服务地址请新建连接，原数据和绑定不会迁移")
		}
	}
	if id == "" {
		settings.ServiceURL = input.ServiceURL
	}
	if input.Embedding != nil {
		if id != "" {
			previous := defaultEmbedding()
			if settings.Embedding != nil {
				previous = *settings.Embedding
			}
			if !sameEmbedding(previous, *input.Embedding) {
				return v, invalid("更换Embedding模型请新建知识库并重新导入文档，不能混用已有向量")
			}
			if input.Embedding.APIKey == "" {
				input.Embedding.APIKey = previous.APIKey
			}
		}
		settings.Embedding = input.Embedding
	}
	if input.Credential != "" {
		settings.Credential = input.Credential
	}
	if id == "" && settings.Credential == "" && settings.ServiceURL == "" {
		return v, invalid("请输入外部服务凭据")
	}
	if settings.ServiceURL != "" || settings.Embedding != nil {
		raw, _ := json.Marshal(settings)
		secret := append([]byte(settingsSecretPrefix), raw...)
		v.envelope, err = s.cipher.Encrypt(secret, s.aad(p, v.ID, "context-credential"))
		clear(secret)
		clear(raw)
		if err != nil {
			return v, err
		}
	} else if id == "" || input.Credential != "" {
		v.envelope, err = s.cipher.Encrypt([]byte(settings.Credential), s.aad(p, v.ID, "context-credential"))
		if err != nil {
			return v, err
		}
	}
	if id == "" {
		var count int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM context_connections WHERE org_id=$1 AND owner_id=$2 AND kind=$3`, p.OrganizationID, p.UserID, kind).Scan(&count); err != nil {
			return v, err
		}
		if count >= 100 {
			return v, invalid("最多创建 100 个连接")
		}
		_, err = tx.Exec(ctx, `INSERT INTO context_connections(id,org_id,owner_id,kind,backend,endpoint_id,name,enabled,key_id,nonce,ciphertext) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, v.ID, p.OrganizationID, p.UserID, kind, endpoint.Backend, endpoint.ID, v.Name, v.Enabled, v.envelope.KeyID, v.envelope.Nonce, v.envelope.Ciphertext)
	} else {
		_, err = tx.Exec(ctx, `UPDATE context_connections SET name=$2,enabled=$3,key_id=$4,nonce=$5,ciphertext=$6,version=version+1,status='UNVERIFIED',capabilities='{}',verified_at=NULL,updated_at=clock_timestamp() WHERE id=$1`, id, strings.TrimSpace(input.Name), v.Enabled, v.envelope.KeyID, v.envelope.Nonce, v.envelope.Ciphertext)
	}
	var duplicate *pgconn.PgError
	if errors.As(err, &duplicate) && duplicate.Code == "23505" {
		return v, invalid("连接名称已存在")
	}
	if err != nil {
		return v, err
	}
	if err = audit(ctx, tx, p, v.ID, "CONTEXT_CONNECTION_SAVE", trace); err != nil {
		return v, err
	}
	v, err = load(ctx, tx, p, kind, v.ID)
	if err == nil {
		v, err = s.presentConnection(p, v)
	}
	if err != nil {
		return v, err
	}
	return v, tx.Commit(ctx)
}
func (s *Service) Delete(ctx context.Context, p identity.Principal, kind, id string, input Input, trace string) error {
	tx, err := s.begin(ctx, p, kind, true)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	v, err := load(ctx, tx, p, kind, id)
	if err != nil {
		return err
	}
	if v.Version != input.ExpectedVersion {
		return conflict()
	}
	if input.ConfirmedName != v.Name {
		return invalid("请输入连接名称确认解绑；外部内容将保留")
	}
	if err = pending(ctx, tx, id); err != nil {
		return err
	}
	// Completed operation receipts contain no independently useful user content;
	// removing the connection also removes its encrypted receipts and bindings.
	if _, err = tx.Exec(ctx, `DELETE FROM context_operations WHERE connection_id=$1`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM context_connections WHERE id=$1`, id); err != nil {
		return err
	}
	if err = audit(ctx, tx, p, id, "CONTEXT_CONNECTION_DELETE", trace); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func scope(ctx context.Context, tx pgx.Tx, p identity.Principal, value string) (string, error) {
	if value == "" || value == "personal" {
		return "personal", nil
	}
	id := strings.TrimPrefix(value, "workspace:")
	if id == value || !idPattern.MatchString(id) {
		return "", invalid("记忆范围不正确")
	}
	if err := identity.RequirePermission(ctx, tx, p, "instances:read"); err != nil {
		return "", err
	}
	var exists bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM instances WHERE org_id=$1 AND owner_id=$2 AND id=$3)`, p.OrganizationID, p.UserID, id).Scan(&exists)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", missing()
	}
	return "workspace:" + id, nil
}
func (s *Service) Bind(ctx context.Context, p identity.Principal, kind, id, instance string, version, bindingVersion int64, enabled bool, trace string) (Binding, error) {
	b := Binding{InstanceID: instance, ConnectionID: id, Enabled: enabled, Integration: "NOT_CONNECTED"}
	tx, err := s.begin(ctx, p, kind, true)
	if err != nil {
		return b, err
	}
	defer tx.Rollback(context.Background())
	if err = identity.RequirePermission(ctx, tx, p, "instances:configure"); err != nil {
		return b, err
	}
	v, err := load(ctx, tx, p, kind, id)
	if err != nil {
		return b, err
	}
	if v.Version != version {
		return b, conflict()
	}
	if enabled && (!v.Enabled || v.Status != "READY") {
		return b, invalid("连接验证通过后才能绑定")
	}
	if !idPattern.MatchString(instance) {
		return b, missing()
	}
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM instances WHERE org_id=$1 AND owner_id=$2 AND id=$3 AND observed_state NOT IN ('DELETED','DELETING'))`, p.OrganizationID, p.UserID, instance).Scan(&exists); err != nil {
		return b, err
	}
	if !exists {
		return b, missing()
	}
	var currentVersion int64
	err = tx.QueryRow(ctx, `SELECT version FROM context_bindings WHERE instance_id=$1 AND connection_id=$2 FOR UPDATE`, instance, id).Scan(&currentVersion)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return b, err
	}
	if currentVersion != bindingVersion {
		return b, conflict()
	}
	err = tx.QueryRow(ctx, `INSERT INTO context_bindings(org_id,owner_id,instance_id,connection_id,enabled) VALUES($1,$2,$3,$4,$5) ON CONFLICT(instance_id,connection_id) DO UPDATE SET enabled=EXCLUDED.enabled,version=context_bindings.version+1,updated_at=clock_timestamp() RETURNING version`, p.OrganizationID, p.UserID, instance, id, enabled).Scan(&b.Version)
	if err != nil {
		return b, err
	}
	if err = audit(ctx, tx, p, id, "CONTEXT_BINDING_SAVE", trace); err != nil {
		return b, err
	}
	return b, tx.Commit(ctx)
}

func readAction(action string) bool {
	switch action {
	case "list", "search", "get", "export":
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
		if (e.Backend != "milvus" && e.Backend != "weaviate" && e.Backend != "qdrant") || e.ServiceKeyFile == "" {
			return nil, unavailable()
		}
		if _, err := milvusURL(service.URL, e.AllowPrivateHTTP); err != nil {
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
	body, _ := json.Marshal(struct {
		Contract  string           `json:"contract"`
		Namespace string           `json:"namespace"`
		Kind      string           `json:"kind"`
		Service   *milvusService   `json:"service,omitempty"`
		Embedding *EmbeddingConfig `json:"embedding,omitempty"`
		Action
	}{Contract, hex.EncodeToString(digest[:]), v.Kind, service, settings.Embedding, input})
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
	data, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if err != nil || len(data) > 4<<20 {
		return nil, unavailable()
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// Never reflect external error bodies (which may contain secrets or content).
		switch response.StatusCode {
		case 404:
			return nil, missing()
		case 409:
			return nil, conflict()
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

func validateAction(action string, in Action) error {
	if !readAction(action) && !remoteWrite(action) && action != "verify" && action != "preview" {
		return missing()
	}
	if in.ExpectedVersion < 1 || len(in.Query) > 4000 || len(in.Content) > 256<<10 || len(in.Title) > 200 || len(in.Source) > 1000 || len(in.Token) > 128 || len(in.RecordID) > 128 || len(in.Type) > 100 || len(in.Package) > 2<<20 || !utf8.ValidString(in.Content) || strings.ContainsRune(in.Content, 0) {
		return invalid("请求内容或版本不正确")
	}
	if in.Limit < 0 || in.Limit > 20 {
		return invalid("返回条数须为 1～20")
	}
	if in.Page < 0 || in.Page > 1000000 || in.PageSize < 0 || in.PageSize > 100 || (in.Page != 0 || in.PageSize != 0) && (action != "list" || in.Page < 1 || in.PageSize < 1) {
		return invalid("分页参数不正确")
	}
	if action == "search" && strings.TrimSpace(in.Query) == "" {
		return invalid("请输入检索问题")
	}
	if in.SearchMode != "" && (action != "search" || (in.SearchMode != "semantic" && in.SearchMode != "exact" && in.SearchMode != "keyword" && in.SearchMode != "hybrid")) {
		return invalid("检索方式不正确")
	}
	if in.MinSimilarity != nil && (action != "search" || in.SearchMode == "exact" || in.SearchMode == "keyword" || math.IsNaN(*in.MinSimilarity) || math.IsInf(*in.MinSimilarity, 0) || *in.MinSimilarity < -1 || *in.MinSimilarity > 1) {
		return invalid("最低相关度须在 -1 到 1 之间，仅适用于语义或混合检索")
	}
	if remoteWrite(action) && (!endpointPattern.MatchString(in.IdempotencyKey) || !in.Confirmed) {
		return invalid("请确认操作并提供唯一操作标识")
	}
	return nil
}

// Call records remote mutations before sending them. Unknown deletes suppress
// reads; pending indexing may serve the last usable version. Reconciliation
// replays the original request because a timeout does not prove rollback.
func (s *Service) Call(ctx context.Context, p identity.Principal, kind, id, action string, in Action, trace string) (json.RawMessage, error) {
	if err := validateAction(action, in); err != nil {
		return nil, err
	}
	if kind != "knowledge" && in.SearchMode != "" {
		return nil, invalid("此服务不支持该检索方式")
	}
	if kind != "knowledge" && in.MinSimilarity != nil {
		return nil, invalid("此服务不支持相似度过滤")
	}
	if kind != "knowledge" && (in.Page != 0 || in.PageSize != 0) {
		return nil, invalid("此服务不支持文档分页")
	}
	write := !readAction(action)
	tx, err := s.begin(ctx, p, kind, write)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	v, err := load(ctx, tx, p, kind, id)
	if err != nil {
		return nil, err
	}
	if in.ExpectedVersion != v.Version {
		return nil, conflict()
	}
	if !v.Enabled {
		return nil, webapi.Fail(409, "CONTEXT_DISABLED", "连接已停用")
	}
	if action != "verify" && v.Status != "READY" {
		return nil, invalid("请先验证外部连接")
	}
	e, err := s.endpoint(v.EndpointID, v.Backend)
	if err != nil {
		return nil, err
	}
	in.Scope, err = scope(ctx, tx, p, in.Scope)
	if err != nil {
		return nil, err
	}
	if in.Limit == 0 {
		in.Limit = 5
	}
	operationID := ""
	if remoteWrite(action) {
		reqBytes, _ := json.Marshal(struct {
			Action string
			Input  Action
		}{action, in})
		requestHash := sha256.Sum256(reqBytes)
		idemHash := sha256.Sum256([]byte(in.IdempotencyKey))
		var storedHash []byte
		var status string
		var envelope security.Envelope
		err = tx.QueryRow(ctx, `SELECT id::text,request_sha256,status,key_id,nonce,ciphertext FROM context_operations WHERE connection_id=$1 AND idempotency_sha256=$2`, id, idemHash[:]).Scan(&operationID, &storedHash, &status, &envelope.KeyID, &envelope.Nonce, &envelope.Ciphertext)
		if err == nil {
			if !bytes.Equal(storedHash, requestHash[:]) {
				return nil, conflict()
			}
			if status == "SUCCEEDED" {
				data, err := s.cipher.Decrypt(envelope, s.aad(p, operationID, "context-operation"))
				return data, err
			}
		} else if errors.Is(err, pgx.ErrNoRows) {
			if err = pending(ctx, tx, id); err != nil {
				return nil, err
			}
			operationID = webapi.ID()
			envelope, err = s.cipher.Encrypt(reqBytes, s.aad(p, operationID, "context-operation"))
			if err != nil {
				return nil, err
			}
			_, err = tx.Exec(ctx, `INSERT INTO context_operations(id,org_id,owner_id,connection_id,connection_version,action,idempotency_sha256,request_sha256,status,key_id,nonce,ciphertext) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'PENDING',$9,$10,$11)`, operationID, p.OrganizationID, p.UserID, id, v.Version, action, idemHash[:], requestHash[:], envelope.KeyID, envelope.Nonce, envelope.Ciphertext)
			if err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	} else if readAction(action) {
		var deleting bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM context_operations WHERE connection_id=$1 AND action='delete' AND status IN ('PENDING','UNKNOWN'))`, id).Scan(&deleting); err != nil {
			return nil, err
		}
		if deleting {
			return nil, webapi.Fail(409, "CONTEXT_PENDING", "外部删除尚未确认，请先核实待确认操作")
		}
	} else if err = pending(ctx, tx, id); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	result, remoteErr := s.remote(ctx, e, v, p, action, in)
	// Persist the remote outcome even if the browser disconnected. Then recheck
	// the current principal and configuration before returning any external text.
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 4*time.Second)
	defer cancel()
	finish, err := s.pool.Begin(finishCtx)
	if err != nil {
		return nil, unavailable()
	}
	defer finish.Rollback(context.Background())
	if operationID != "" {
		status := "SUCCEEDED"
		outcome := "SUCCEEDED"
		if remoteErr != nil {
			status = "UNKNOWN"
			outcome = "ACCEPTED"
			var problem *webapi.Problem
			if errors.As(remoteErr, &problem) && problem.Status >= 400 && problem.Status < 500 {
				status = "FAILED"
				outcome = "FAILED"
			}
			_, err = finish.Exec(finishCtx, `UPDATE context_operations SET status=$2,updated_at=clock_timestamp() WHERE id=$1 AND status<>'SUCCEEDED'`, operationID, status)
		} else {
			envelope, encryptErr := s.cipher.Encrypt(result, s.aad(p, operationID, "context-operation"))
			if encryptErr != nil {
				return nil, encryptErr
			}
			_, err = finish.Exec(finishCtx, `UPDATE context_operations SET status=$2,key_id=$3,nonce=$4,ciphertext=$5,updated_at=clock_timestamp() WHERE id=$1`, operationID, status, envelope.KeyID, envelope.Nonce, envelope.Ciphertext)
		}
		if err != nil {
			return nil, err
		}
		if trace == "" {
			trace = webapi.ID()
		}
		if _, err = finish.Exec(finishCtx, `INSERT INTO audit_events(id,org_id,actor_id,owner_id,resource_id,action,outcome,trace_id) VALUES($1,$2,$3,$3,$4,$5,$6,$7)`, webapi.ID(), p.OrganizationID, p.UserID, id, "CONTEXT_"+strings.ToUpper(action), outcome, trace); err != nil {
			return nil, err
		}

	}
	if err = finish.Commit(finishCtx); err != nil {
		return nil, err
	}
	check, err := s.begin(ctx, p, kind, write)
	if err != nil {
		return nil, err
	}
	defer check.Rollback(context.Background())
	current, err := load(ctx, check, p, kind, id)
	if err != nil {
		return nil, err
	}
	if current.Version != v.Version || !current.Enabled {
		return nil, conflict()
	}
	if _, err = s.endpoint(v.EndpointID, v.Backend); err != nil {
		return nil, err
	}
	if remoteErr != nil {
		if action == "verify" {
			_, _ = check.Exec(ctx, `UPDATE context_connections SET status='FAILED',verified_at=NULL WHERE id=$1`, id)
			_ = check.Commit(ctx)
		}
		var problem *webapi.Problem
		if operationID != "" && (!errors.As(remoteErr, &problem) || problem.Status >= 500) {
			return nil, webapi.Fail(503, "CONTEXT_PENDING", "外部服务正在处理，请稍后点击“核实待确认操作”")
		}
		return nil, remoteErr
	}
	if action == "verify" {
		var verified struct {
			Contract     string          `json:"contract"`
			Backend      string          `json:"backend"`
			Ready        bool            `json:"ready"`
			Capabilities json.RawMessage `json:"capabilities"`
		}
		if json.Unmarshal(result, &verified) != nil || verified.Contract != Contract || verified.Backend != v.Backend || !verified.Ready || len(verified.Capabilities) == 0 {
			_, _ = check.Exec(ctx, `UPDATE context_connections SET status='FAILED',verified_at=NULL WHERE id=$1`, id)
			_ = check.Commit(ctx)
			return nil, webapi.Fail(422, "CONTEXT_UNSUPPORTED", "服务或版本未通过适配验证")
		}
		if _, err = check.Exec(ctx, `UPDATE context_connections SET status='READY',capabilities=$2,verified_at=clock_timestamp() WHERE id=$1`, id, verified.Capabilities); err != nil {
			return nil, err
		}
		if err = audit(ctx, check, p, id, "CONTEXT_VERIFY", trace); err != nil {
			return nil, err
		}
	}
	if err = check.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

// Reconcile never accepts new content or an arbitrary external operation ID.
func (s *Service) Reconcile(ctx context.Context, p identity.Principal, kind, id, trace string) (json.RawMessage, error) {
	tx, err := s.begin(ctx, p, kind, true)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	if _, err = load(ctx, tx, p, kind, id); err != nil {
		return nil, err
	}
	var operationID string
	var envelope security.Envelope
	err = tx.QueryRow(ctx, `SELECT id::text,key_id,nonce,ciphertext FROM context_operations WHERE org_id=$1 AND owner_id=$2 AND connection_id=$3 AND status IN ('PENDING','UNKNOWN') ORDER BY created_at LIMIT 1`, p.OrganizationID, p.UserID, id).Scan(&operationID, &envelope.KeyID, &envelope.Nonce, &envelope.Ciphertext)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, missing()
	}
	if err != nil {
		return nil, err
	}
	data, err := s.cipher.Decrypt(envelope, s.aad(p, operationID, "context-operation"))
	if err != nil {
		return nil, err
	}
	var saved struct {
		Action string
		Input  Action
	}
	if err = json.Unmarshal(data, &saved); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.Call(ctx, p, kind, id, saved.Action, saved.Input, trace)
}
