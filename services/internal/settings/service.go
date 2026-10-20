package settings

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type Health struct {
	Status    string     `json:"status"`
	CheckedAt *time.Time `json:"checked_at"`
	ExpiresAt *time.Time `json:"expires_at"`
	Reason    string     `json:"reason_code"`
}
type Model struct {
	ID                   string         `json:"id"`
	Version              int64          `json:"version"`
	Provider             string         `json:"provider"`
	Endpoint             string         `json:"endpoint"`
	Protocol             string         `json:"protocol"`
	Model                string         `json:"model"`
	Status               string         `json:"status"`
	CredentialConfigured bool           `json:"credential_configured"`
	Validation           Health         `json:"validation"`
	Manifest             *ModelManifest `json:"manifest,omitempty"`
}
type ModelInput struct {
	Provider        string  `json:"provider"`
	Endpoint        string  `json:"endpoint"`
	Protocol        string  `json:"protocol"`
	Model           string  `json:"model"`
	APIKey          *string `json:"api_key,omitempty"`
	ExpectedVersion *int64  `json:"expected_version"`
}
type VersionInput struct {
	Version int64 `json:"version"`
}
type Probe interface {
	Check(context.Context, Model, string) (reason string, retryable bool)
}
type Service struct {
	pool                  *pgxpool.Pool
	cipher                *security.SecretCipher
	policy                *Policy
	environment           string
	maintenance           bool
	probe                 Probe
	runtime               RuntimeChecks
	gatewayClientOverride func(Gateway) *http.Client
}

type RuntimeChecks interface {
	Checks(context.Context, identity.Principal) ([]Check, error)
	PlatformChecks(context.Context, identity.Principal) ([]Check, error)
}

type RuntimeResourceChecks interface {
	ChecksForResources(context.Context, identity.Principal, Spec) ([]Check, error)
}

func (s *Service) WithRuntimeChecks(checks RuntimeChecks) *Service { s.runtime = checks; return s }

func NewService(pool *pgxpool.Pool, cipher *security.SecretCipher, policy *Policy, environment string, maintenance bool) *Service {
	return &Service{pool: pool, cipher: cipher, policy: policy, environment: environment, maintenance: maintenance, probe: &MessagesProbe{client: modelHTTPClient(), policy: policy}}
}
func (s *Service) aad(org, id string) security.Context {
	return security.Context{Environment: s.environment, OrganizationID: org, RecordID: id, Purpose: "model-key"}
}
func invalid() error {
	return webapi.Fail(422, "CONFIGURATION_INVALID", "模型配置不合法或未获部署批准")
}
func notFound() error {
	return webapi.Fail(404, "NOT_FOUND", "尚未保存模型配置或指定版本不存在")
}
func versionConflict() error {
	return webapi.Fail(409, "VERSION_CONFLICT", "配置版本已变化，请重新加载后保存")
}
func (s *Service) authorize(ctx context.Context, tx pgx.Tx, p identity.Principal, permission string) error {
	return identity.RequirePermission(ctx, tx, p, permission)
}
func (s *Service) begin(ctx context.Context, p identity.Principal, permission string, mutate bool) (pgx.Tx, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if err = s.authorize(ctx, tx, p, permission); err == nil && mutate && s.maintenance {
		err = webapi.Fail(503, "MAINTENANCE", "平台维护中，请稍后重试")
	}
	if err != nil {
		_ = tx.Rollback(context.Background())
		return nil, err
	}
	return tx, nil
}
func (s *Service) audit(ctx context.Context, tx pgx.Tx, p identity.Principal, id, action, trace string) error {
	if trace == "" {
		trace = webapi.ID()
	}
	_, err := tx.Exec(ctx, `INSERT INTO audit_events(id,org_id,actor_id,owner_id,resource_id,action,outcome,trace_id) VALUES($1,$2,$3,$3,$4,$5,'SUCCEEDED',$6)`, webapi.ID(), p.OrganizationID, p.UserID, id, action, trace)
	return err
}
func readModel(ctx context.Context, q dbQuery, org string, version int64) (Model, error) {
	var m Model
	var manifest []byte
	err := q.QueryRow(ctx, `SELECT c.id::text,c.version,c.provider,c.endpoint,c.protocol,c.model,c.status,s.revoked_at IS NULL,c.manifest,
 CASE WHEN c.status='REVOKED' OR c.revoked_at IS NOT NULL OR s.revoked_at IS NOT NULL THEN 'UNHEALTHY' WHEN v.status='CHECKING' AND v.lease_expires_at>clock_timestamp() THEN 'CHECKING'
 WHEN v.status='CHECKING' THEN 'UNKNOWN' WHEN v.checked_at<=clock_timestamp() AND v.expires_at>clock_timestamp() AND v.expires_at>v.checked_at THEN v.status ELSE 'UNKNOWN' END,
 CASE WHEN v.status='CHECKING' THEN v.lease_expires_at-interval '45 seconds' ELSE v.checked_at END,
 CASE WHEN v.status='CHECKING' THEN v.lease_expires_at ELSE v.expires_at END,
 CASE WHEN c.status='REVOKED' OR c.revoked_at IS NOT NULL OR s.revoked_at IS NOT NULL THEN 'CREDENTIAL_REVOKED' WHEN v.status='CHECKING' AND v.lease_expires_at>clock_timestamp() THEN ''
 WHEN v.status='CHECKING' THEN 'VALIDATION_REQUIRED' WHEN v.checked_at<=clock_timestamp() AND v.expires_at>clock_timestamp() AND v.expires_at>v.checked_at THEN v.reason_code ELSE 'VALIDATION_REQUIRED' END
 FROM model_configuration_heads h JOIN model_configurations c ON c.id=h.id JOIN secrets s ON s.id=c.credential_ref
 LEFT JOIN model_validations v ON v.config_id=c.id AND v.config_version=c.version
 WHERE h.org_id=$1 AND c.version=CASE WHEN $2::bigint=0 THEN h.current_version ELSE $2 END`, org, version).Scan(&m.ID, &m.Version, &m.Provider, &m.Endpoint, &m.Protocol, &m.Model, &m.Status, &m.CredentialConfigured, &manifest, &m.Validation.Status, &m.Validation.CheckedAt, &m.Validation.ExpiresAt, &m.Validation.Reason)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, notFound()
	}
	if err == nil && m.Provider == "platform-models" {
		m.Manifest = &ModelManifest{}
		err = webapi.StrictUnmarshal(manifest, m.Manifest)
	}
	return m, err
}

type dbQuery interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (s *Service) GetModel(ctx context.Context, p identity.Principal, version int64) (Model, error) {
	tx, err := s.begin(ctx, p, "configuration:read", false)
	if err != nil {
		return Model{}, err
	}
	defer tx.Rollback(context.Background())
	m, err := readModel(ctx, tx, p.OrganizationID, version)
	if err != nil {
		return m, err
	}
	return m, tx.Commit(ctx)
}
func (s *Service) SaveModel(ctx context.Context, p identity.Principal, input ModelInput, trace string) (Model, error) {
	tx, err := s.begin(ctx, p, "model:write", true)
	if err != nil {
		return Model{}, err
	}
	defer tx.Rollback(context.Background())
	if input.ExpectedVersion == nil || *input.ExpectedVersion < 0 || !s.policy.AllowsConnection(input.Provider, input.Protocol, input.Endpoint, input.Model) {
		return Model{}, invalid()
	}
	if input.APIKey != nil {
		if len(*input.APIKey) < 1 || len(*input.APIKey) > 4096 {
			return Model{}, invalid()
		}
		for _, b := range []byte(*input.APIKey) {
			if b < 0x21 || b > 0x7e {
				return Model{}, invalid()
			}
		}
	}
	if input.Provider == "platform-models" && input.APIKey != nil {
		if err := tx.Commit(ctx); err != nil {
			return Model{}, err
		}
		check, cancel := context.WithTimeout(ctx, 5*time.Second)
		gateway, _ := s.policy.Gateway(input.Endpoint)
		reason := checkRestrictedKey(check, gateway.HTTPClient(), input.Endpoint, *input.APIKey)
		cancel()
		if reason != "" {
			return Model{}, webapi.Fail(422, reason, "需要无网关管理权限的受限虚拟密钥，且网关须支持权限确认")
		}
		tx, err = s.begin(ctx, p, "model:write", true)
		if err != nil {
			return Model{}, err
		}
		defer tx.Rollback(context.Background())
	}
	_, err = tx.Exec(ctx, `INSERT INTO model_configuration_heads(id,org_id) VALUES($1,$2) ON CONFLICT(org_id) DO NOTHING`, webapi.ID(), p.OrganizationID)
	if err != nil {
		return Model{}, err
	}
	var id string
	var current int64
	if err := tx.QueryRow(ctx, `SELECT id::text,current_version FROM model_configuration_heads WHERE org_id=$1 FOR UPDATE`, p.OrganizationID).Scan(&id, &current); err != nil {
		return Model{}, err
	}
	if current != *input.ExpectedVersion {
		return Model{}, versionConflict()
	}
	var credential string
	if input.APIKey != nil {
		credential = webapi.ID()
		envelope, err := s.cipher.Encrypt([]byte(*input.APIKey), s.aad(p.OrganizationID, credential))
		if err != nil {
			return Model{}, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO secrets(id,org_id,key_id,nonce,ciphertext) VALUES($1,$2,$3,$4,$5)`, credential, p.OrganizationID, envelope.KeyID, envelope.Nonce, envelope.Ciphertext); err != nil {
			return Model{}, err
		}
	} else {
		if current == 0 {
			return Model{}, invalid()
		}
		err := tx.QueryRow(ctx, `SELECT c.credential_ref::text FROM model_configurations c JOIN secrets s ON s.id=c.credential_ref WHERE c.id=$1 AND c.version=$2 AND c.revoked_at IS NULL AND s.revoked_at IS NULL AND c.provider=$3 AND c.endpoint=$4 AND c.protocol=$5`, id, current, input.Provider, input.Endpoint, input.Protocol).Scan(&credential)
		if errors.Is(err, pgx.ErrNoRows) {
			return Model{}, invalid()
		}
		if err != nil {
			return Model{}, err
		}
	}
	manifest := []byte("{}")
	if input.Provider == "platform-models" {
		approved, _ := s.policy.Manifest(input.Endpoint, input.Model)
		manifest, _ = json.Marshal(approved)
	}
	version := current + 1
	if _, err := tx.Exec(ctx, `INSERT INTO model_configurations(org_id,id,version,provider,endpoint,protocol,model,credential_ref,status,manifest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'UNVALIDATED',$9)`, p.OrganizationID, id, version, input.Provider, input.Endpoint, input.Protocol, input.Model, credential, manifest); err != nil {
		return Model{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO model_validations(config_id,config_version,status,reason_code) VALUES($1,$2,'UNKNOWN','VALIDATION_REQUIRED')`, id, version); err != nil {
		return Model{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE model_configuration_heads SET current_version=$2 WHERE id=$1`, id, version); err != nil {
		return Model{}, err
	}
	if err := s.audit(ctx, tx, p, id, "MODEL_SAVE", trace); err != nil {
		return Model{}, err
	}
	m, err := readModel(ctx, tx, p.OrganizationID, version)
	if err != nil {
		return Model{}, err
	}
	return m, tx.Commit(ctx)
}

func (s *Service) gatewayClient(g Gateway) *http.Client {
	if s.gatewayClientOverride != nil {
		return s.gatewayClientOverride(g)
	}
	return g.HTTPClient()
}
