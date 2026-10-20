package instances

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"regexp"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/runtimeapproval"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	workspaceengine "github.com/jonluo94/deepseek-harness-cloud/services/internal/workspace"
)

var keyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)
var namePattern = regexp.MustCompile(`^[\p{L}\p{N}_-]{1,32}$`)

type CreateInput struct {
	OrganizationID  *string `json:"organization_id,omitempty"`
	Name            *string `json:"name,omitempty"`
	Backend         *string `json:"backend_type,omitempty"`
	TemplateID      *string `json:"template_id,omitempty"`
	TemplateVersion *string `json:"template_version,omitempty"`
	SpecID          *string `json:"spec_id,omitempty"`
	ModelID         *string `json:"model_configuration_id,omitempty"`
	ModelVersion    *int64  `json:"model_configuration_version,omitempty"`
	Autostart       *bool   `json:"autostart,omitempty"`
}
type Accepted struct {
	InstanceID  string `json:"instance_id"`
	OperationID string `json:"operation_id"`
	State       string `json:"state"`
}

// CreationGate performs bounded control-plane/capacity/access observations. A
// missing gate closes acceptance; approval in PostgreSQL alone is insufficient.
type CreationGate interface {
	Check(context.Context, identity.Principal) error
}
type CommandService struct {
	pool        *pgxpool.Pool
	gate        CreationGate
	maintenance bool
}

func NewCommandService(pool *pgxpool.Pool, gate CreationGate, maintenance bool) *CommandService {
	return &CommandService{pool: pool, gate: gate, maintenance: maintenance}
}
func requestHash(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var fields any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err = decoder.Decode(&fields); err != nil {
		return nil, err
	}
	encoded, err = json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(encoded)
	return hash[:], nil
}
func (input CreateInput) valid() bool {
	for _, id := range []*string{input.OrganizationID, input.TemplateID, input.TemplateVersion, input.SpecID, input.ModelID} {
		if id != nil && !idPattern.MatchString(*id) {
			return false
		}
	}
	return (input.ModelVersion == nil || (input.ModelID != nil && *input.ModelVersion > 0)) && (input.Name == nil || namePattern.MatchString(*input.Name)) && (input.Backend == nil || *input.Backend == "docker") && (input.Autostart == nil || *input.Autostart)
}
func (s *CommandService) Create(ctx context.Context, p identity.Principal, key string, input CreateInput, trace string) (Accepted, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Accepted{}, err
	}
	defer tx.Rollback(context.Background())
	if err = requireAction(ctx, tx, p, "CREATE"); err != nil {
		return Accepted{}, err
	}
	if !keyPattern.MatchString(key) || !input.valid() {
		return Accepted{}, invalidQuery()
	}
	hash, err := requestHash(input)
	if err != nil {
		return Accepted{}, err
	}
	var savedHash, response []byte
	err = tx.QueryRow(ctx, `SELECT request_sha256,response FROM idempotency_records WHERE org_id=$1 AND actor_id=$2 AND action='CREATE' AND target_id=$1 AND key=$3 AND (expires_at IS NULL OR expires_at>clock_timestamp())`, p.OrganizationID, p.UserID, key).Scan(&savedHash, &response)
	if err == nil {
		if !bytes.Equal(hash, savedHash) {
			return Accepted{}, webapi.Fail(409, "IDEMPOTENCY_CONFLICT", "此提交编号已用于不同请求")
		}
		var saved Accepted
		if webapi.StrictUnmarshal(response, &saved) != nil || !idPattern.MatchString(saved.InstanceID) || !idPattern.MatchString(saved.OperationID) {
			return Accepted{}, errors.New("persisted acceptance response is invalid")
		}
		return saved, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Accepted{}, err
	}
	if s.maintenance {
		return Accepted{}, webapi.Fail(503, "MAINTENANCE", "平台维护中，请稍后重试")
	}
	if input.OrganizationID != nil && *input.OrganizationID != p.OrganizationID {
		return Accepted{}, invalidQuery()
	}
	if s.gate == nil {
		return Accepted{}, webapi.Fail(503, "DEPENDENCY_UNAVAILABLE", "运行环境尚未通过创建检查")
	}
	// Network work stays outside the acceptance transaction and its row locks.
	// Reacquire and recheck everything afterward, including a possible same-key
	// acceptance by another request while observations were in flight.
	_ = tx.Rollback(ctx)
	if err = s.gate.Check(ctx, p); err != nil {
		return Accepted{}, err
	}
	return s.acceptCreate(ctx, p, key, input, hash, trace)
}
func (s *CommandService) acceptCreate(ctx context.Context, p identity.Principal, key string, input CreateInput, hash []byte, trace string) (Accepted, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Accepted{}, err
	}
	defer tx.Rollback(context.Background())
	if err = requireAction(ctx, tx, p, "CREATE"); err != nil {
		return Accepted{}, err
	}
	var savedHash, response []byte
	err = tx.QueryRow(ctx, `SELECT request_sha256,response FROM idempotency_records WHERE org_id=$1 AND actor_id=$2 AND action='CREATE' AND target_id=$1 AND key=$3 AND (expires_at IS NULL OR expires_at>clock_timestamp())`, p.OrganizationID, p.UserID, key).Scan(&savedHash, &response)
	if err == nil {
		if !bytes.Equal(hash, savedHash) {
			return Accepted{}, webapi.Fail(409, "IDEMPOTENCY_CONFLICT", "此提交编号已用于不同请求")
		}
		var saved Accepted
		if webapi.StrictUnmarshal(response, &saved) != nil {
			return Accepted{}, errors.New("persisted response invalid")
		}
		return saved, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Accepted{}, err
	}
	var head string
	err = tx.QueryRow(ctx, `SELECT id::text FROM model_configuration_heads WHERE org_id=$1 FOR UPDATE`, p.OrganizationID).Scan(&head)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Accepted{}, err
	}
	var snapshot Snapshot
	err = tx.QueryRow(ctx, `SELECT d.version,t.id::text,t.version::text,t.image_digest,s.id::text,s.cpu_millicores,s.memory_bytes,s.storage_bytes,COALESCE(c.id::text,''),COALESCE(c.version,0),COALESCE(c.provider,''),COALESCE(c.protocol,''),COALESCE(c.model,''),COALESCE(c.manifest->>'digest','') FROM organization_defaults d JOIN approved_templates t ON t.id=d.template_id AND t.version=d.template_version JOIN approved_specs s ON s.id=d.spec_id LEFT JOIN model_configurations c ON c.org_id=d.org_id AND c.id=d.model_config_id AND c.version=d.model_config_version WHERE d.org_id=$1 AND t.enabled AND s.enabled FOR UPDATE OF d`, p.OrganizationID).Scan(&snapshot.DefaultsVersion, &snapshot.TemplateID, &snapshot.TemplateVersion, &snapshot.ImageDigest, &snapshot.Spec.ID, &snapshot.Spec.CPU, &snapshot.Spec.Memory, &snapshot.Spec.Storage, &snapshot.ModelID, &snapshot.ModelVersion, &snapshot.Provider, &snapshot.Protocol, &snapshot.Model, &snapshot.ApprovalDigest)
	if errors.Is(err, pgx.ErrNoRows) {
		return Accepted{}, webapi.Fail(422, "CONFIGURATION_INVALID", "默认运行配置尚未就绪或已失效")
	}
	if err != nil {
		return Accepted{}, err
	}
	selectedID, selectedVersion := snapshot.ModelID, snapshot.ModelVersion
	if input.ModelID != nil {
		selectedID = *input.ModelID
		selectedVersion = 0
		if input.ModelVersion != nil {
			selectedVersion = *input.ModelVersion
		} else if selectedID == snapshot.ModelID {
			selectedVersion = snapshot.ModelVersion
		}
	} else {
		var personalID string
		var personalVersion int64
		e := tx.QueryRow(ctx, `SELECT connection_id::text,connection_version FROM user_model_defaults WHERE org_id=$1 AND owner_id=$2 AND connection_id IS NOT NULL`, p.OrganizationID, p.UserID).Scan(&personalID, &personalVersion)
		if e == nil {
			selectedID, selectedVersion = personalID, personalVersion
		} else if !errors.Is(e, pgx.ErrNoRows) {
			return Accepted{}, e
		}
	}
	if err = bindModel(ctx, tx, p, &snapshot, selectedID, selectedVersion); err != nil {
		return Accepted{}, err
	}
	for _, pair := range []struct {
		input    *string
		approved string
	}{{input.TemplateID, snapshot.TemplateID}, {input.TemplateVersion, snapshot.TemplateVersion}} {
		if pair.input != nil && *pair.input != pair.approved {
			return Accepted{}, webapi.Fail(422, "INVALID_SPEC", "只允许批准的默认配置")
		}
	}
	if input.SpecID != nil {
		err = tx.QueryRow(ctx, `SELECT id::text,cpu_millicores,memory_bytes,storage_bytes FROM approved_specs WHERE id=$1 AND enabled`, *input.SpecID).Scan(&snapshot.Spec.ID, &snapshot.Spec.CPU, &snapshot.Spec.Memory, &snapshot.Spec.Storage)
		if errors.Is(err, pgx.ErrNoRows) {
			return Accepted{}, webapi.Fail(422, "INVALID_SPEC", "所选运行规格不可用")
		}
		if err != nil {
			return Accepted{}, err
		}
	}
	if err = modelRuntime(ctx, tx, &snapshot, false); err != nil {
		return Accepted{}, err
	}
	if !runtimeapproval.SupportedResources(snapshot.Spec.CPU, snapshot.Spec.Memory, snapshot.Spec.Storage) {
		return Accepted{}, webapi.Fail(422, "INVALID_SPEC", "所选运行规格不受当前运行环境支持")
	}
	stored, err := json.Marshal(struct {
		SchemaVersion int `json:"schema_version"`
		Snapshot
	}{1, snapshot})
	if err != nil {
		return Accepted{}, err
	}
	if _, err := DecodeSnapshot(stored); err != nil {
		return Accepted{}, err
	}
	policy, usage, err := lockedQuota(ctx, tx, p.OrganizationID, p.UserID)
	if err != nil {
		return Accepted{}, err
	}
	reservations, err := policy.PlanReservation(true, usage, Resources{CPU: snapshot.Spec.CPU, Memory: snapshot.Spec.Memory, Storage: snapshot.Spec.Storage})
	if err != nil {
		return Accepted{}, webapi.Fail(429, "QUOTA_EXCEEDED", "实例、计算、存储或并发额度已用尽")
	}
	if len(reservations) > 0 {
		if err = checkUserQuota(ctx, tx, p.OrganizationID, p.UserID, reservations); err != nil {
			return Accepted{}, err
		}
	}
	// Quota/default locks may have waited beyond the original session deadline.
	if err = requireAction(ctx, tx, p, "CREATE"); err != nil {
		return Accepted{}, err
	}
	var modelCurrent bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM model_configurations c JOIN secrets sec ON sec.id=c.credential_ref AND sec.org_id=c.org_id JOIN model_validations v ON v.config_id=c.id AND v.config_version=c.version WHERE c.org_id=$1 AND c.id=$2 AND c.version=$3 AND public.hc_model_available(c.org_id,$4,c.id,c.version) AND c.status='VALID' AND c.revoked_at IS NULL AND sec.revoked_at IS NULL AND v.status='HEALTHY' AND v.checked_at<=clock_timestamp() AND v.expires_at>clock_timestamp())`, p.OrganizationID, snapshot.ModelID, snapshot.ModelVersion, p.UserID).Scan(&modelCurrent); err != nil {
		return Accepted{}, err
	}
	if !modelCurrent {
		return Accepted{}, webapi.Fail(422, "CONFIGURATION_INVALID", "模型校验已失效，请重新校验后创建")
	}
	accepted := Accepted{InstanceID: webapi.ID(), OperationID: webapi.ID(), State: "CREATING"}
	workspace, err := workspaceengine.WorkspaceName(accepted.InstanceID)
	if err != nil {
		return Accepted{}, err
	}
	name := "dsh-" + accepted.InstanceID[:8] + accepted.InstanceID[9:13]
	if input.Name != nil {
		name = *input.Name
	}
	asset := webapi.ID()
	_, err = tx.Exec(ctx, `INSERT INTO instances(id,org_id,owner_id,name,external_create_key,workspace_name,desired_state,observed_state,model_config_id,model_config_version,config_snapshot) VALUES($1,$2,$3,$4,$1,$5,'RUNNING','CREATING',$6,$7,$8)`, accepted.InstanceID, p.OrganizationID, p.UserID, name, workspace, snapshot.ModelID, snapshot.ModelVersion, stored)
	if err != nil {
		var postgres *pgconn.PgError
		if errors.As(err, &postgres) && postgres.Code == "23505" && postgres.ConstraintName == "instances_active_name" {
			return Accepted{}, webapi.Fail(409, "NAME_CONFLICT", "实例名称已被使用")
		}
		return Accepted{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO storage_assets(id,org_id,owner_id,instance_id,resource_name,capacity_bytes,status) VALUES($1,$2,$3,$4,$5,$6,'PREPARING')`, asset, p.OrganizationID, p.UserID, accepted.InstanceID, "hc-"+asset, snapshot.Spec.Storage)
	if err != nil {
		return Accepted{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE instances SET storage_asset_id=$2 WHERE id=$1`, accepted.InstanceID, asset); err != nil {
		return Accepted{}, err
	}
	if trace == "" {
		trace = webapi.ID()
	}
	intent, err := json.Marshal(struct {
		SchemaVersion   int             `json:"schema_version"`
		ExpectedVersion int64           `json:"expected_instance_version"`
		Desired         string          `json:"target_desired_state"`
		Reason          string          `json:"reason"`
		RequestID       string          `json:"request_id"`
		Generation      int64           `json:"accepted_auth_generation"`
		Snapshot        json.RawMessage `json:"target_snapshot"`
	}{1, 1, "RUNNING", "USER_REQUEST", trace, p.Generation, stored})
	if err != nil {
		return Accepted{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO operations(id,instance_id,actor_id,type,status,stage,intent,trace_id) VALUES($1,$2,$3,'CREATE','ACCEPTED','QUEUED',$4,$5)`, accepted.OperationID, accepted.InstanceID, p.UserID, intent, trace); err != nil {
		return Accepted{}, err
	}
	for resource, amount := range reservations {
		if _, err = tx.Exec(ctx, `INSERT INTO quota_reservations(id,org_id,owner_id,instance_id,operation_id,resource_type,amount,status) VALUES($1,$2,$3,$4,$5,$6,$7,'HELD')`, webapi.ID(), p.OrganizationID, p.UserID, accepted.InstanceID, accepted.OperationID, resource, amount); err != nil {
			return Accepted{}, err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(id,operation_id,status) VALUES($1,$2,'PENDING')`, webapi.ID(), accepted.OperationID); err != nil {
		return Accepted{}, err
	}
	response, err = json.Marshal(accepted)
	if err != nil {
		return Accepted{}, err
	}
	// Expired records are explicitly retired under the same membership lock.
	if _, err = tx.Exec(ctx, `DELETE FROM idempotency_records WHERE org_id=$1 AND actor_id=$2 AND action='CREATE' AND target_id=$1 AND key=$3 AND expires_at<=clock_timestamp()`, p.OrganizationID, p.UserID, key); err != nil {
		return Accepted{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO idempotency_records(org_id,actor_id,action,target_id,key,request_sha256,operation_id,response) VALUES($1,$2,'CREATE',$1,$3,$4,$5,$6)`, p.OrganizationID, p.UserID, key, hash, accepted.OperationID, response); err != nil {
		return Accepted{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(id,org_id,actor_id,owner_id,resource_id,action,outcome,trace_id) VALUES($1,$2,$3,$3,$4,'INSTANCE_CREATE','ACCEPTED',$5)`, webapi.ID(), p.OrganizationID, p.UserID, accepted.InstanceID, trace); err != nil {
		return Accepted{}, err
	}
	return accepted, tx.Commit(ctx)
}
