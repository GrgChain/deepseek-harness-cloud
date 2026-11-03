package contextservice

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

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
			e.CustomService = supportsCustomService(e.Backend) && e.ServiceKeyFile != ""
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
		if !supportsCustomService(endpoint.Backend) || endpoint.ServiceKeyFile == "" {
			return Connection{}, invalid("此服务尚不支持自定义地址")
		}
		if endpoint.Backend == "graphiti" {
			input.ServiceURL, err = graphitiURL(input.ServiceURL, endpoint.AllowPrivateHTTP)
		} else {
			input.ServiceURL, err = milvusURL(input.ServiceURL, endpoint.AllowPrivateHTTP)
		}
		if err != nil {
			return Connection{}, err
		}
	}
	if len(input.Credential) > 4096 || strings.ContainsAny(input.Credential, "\r\n\x00") {
		return Connection{}, invalid("凭据格式不正确")
	}
	if kind == "memory" && id == "" && input.ServiceURL != "" && strings.TrimSpace(input.Credential) == "" {
		return Connection{}, invalid("请输入记忆服务 API Key")
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
	if endpoint.Backend == "graphiti" && settings.ServiceURL != "" {
		user, password, ok := strings.Cut(settings.Credential, ":")
		if !ok || strings.TrimSpace(user) == "" || password == "" {
			return Connection{}, invalid("请输入 Neo4j 用户名:密码")
		}
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
