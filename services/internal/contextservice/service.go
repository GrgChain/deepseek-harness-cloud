package contextservice

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

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
