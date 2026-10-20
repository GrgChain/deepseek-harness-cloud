package instances

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type CommandInput struct {
	ExpectedVersion *int64  `json:"expected_instance_version,omitempty"`
	ModelID         *string `json:"model_configuration_id,omitempty"`
	ModelVersion    *int64  `json:"model_configuration_version,omitempty"`
	ConfirmedName   *string `json:"confirmed_name,omitempty"`
}

func (input CommandInput) valid(action Action) bool {
	switch action {
	case Start, Stop:
		return input.ExpectedVersion == nil && input.ModelID == nil && input.ModelVersion == nil && input.ConfirmedName == nil
	case Delete:
		return input.ExpectedVersion == nil && input.ModelID == nil && input.ModelVersion == nil && input.ConfirmedName != nil && namePattern.MatchString(*input.ConfirmedName)
	case Configure:
		return input.ExpectedVersion != nil && *input.ExpectedVersion > 0 && input.ModelID != nil && idPattern.MatchString(*input.ModelID) && input.ModelVersion != nil && *input.ModelVersion > 0 && input.ConfirmedName == nil
	}
	return false
}
func savedAcceptance(ctx context.Context, tx pgx.Tx, p identity.Principal, action, target, key string, hash []byte) (*Accepted, error) {
	var stored, response []byte
	err := tx.QueryRow(ctx, `SELECT request_sha256,response FROM idempotency_records WHERE org_id=$1 AND actor_id=$2 AND action=$3 AND target_id=$4 AND key=$5 AND (expires_at IS NULL OR expires_at>clock_timestamp())`, p.OrganizationID, p.UserID, action, target, key).Scan(&stored, &response)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(hash, stored) {
		return nil, webapi.Fail(409, "IDEMPOTENCY_CONFLICT", "此提交编号已用于不同请求")
	}
	var accepted Accepted
	if webapi.StrictUnmarshal(response, &accepted) != nil || !idPattern.MatchString(accepted.InstanceID) || !idPattern.MatchString(accepted.OperationID) || !validState(accepted.State) || accepted.State == "" {
		return nil, errors.New("persisted acceptance response is invalid")
	}
	return &accepted, nil
}
func lifecycleError(err error) error {
	switch {
	case errors.Is(err, ErrVersionConflict):
		return webapi.Fail(409, "VERSION_CONFLICT", "实例版本已变化，请重新加载")
	case errors.Is(err, ErrStorageMissing):
		return webapi.Fail(422, "STORAGE_MISSING", "原数据资产不可用，请联系管理员核实")
	case errors.Is(err, ErrConfigurationInvalid):
		return webapi.Fail(422, "CONFIGURATION_INVALID", "模型配置不可用或需要重新校验")
	default:
		return webapi.Fail(409, "STATE_CONFLICT", "实例正在处理其他操作或当前状态不允许此命令")
	}
}
func (s *CommandService) Command(ctx context.Context, p identity.Principal, id string, action Action, key string, input CommandInput, trace string) (Accepted, error) {
	// Inspect visibility and any accepted replay before doing external readiness
	// observations. The final short transaction rechecks both under authority.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Accepted{}, err
	}
	defer tx.Rollback(context.Background())
	if err = requireAction(ctx, tx, p, string(action)); err != nil {
		return Accepted{}, err
	}
	if !idPattern.MatchString(id) {
		return Accepted{}, invalidQuery()
	}
	var visible bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM instances WHERE id=$1 AND org_id=$2 AND owner_id=$3)`, id, p.OrganizationID, p.UserID).Scan(&visible); err != nil {
		return Accepted{}, err
	}
	if !visible {
		return Accepted{}, queryNotFound()
	}
	if !keyPattern.MatchString(key) || !input.valid(action) {
		return Accepted{}, invalidQuery()
	}
	hash, err := requestHash(input)
	if err != nil {
		return Accepted{}, err
	}
	saved, err := savedAcceptance(ctx, tx, p, string(action), id, key, hash)
	if err != nil {
		return Accepted{}, err
	}
	if saved != nil {
		return *saved, tx.Commit(ctx)
	}
	if s.maintenance && action != Stop && action != Delete {
		return Accepted{}, webapi.Fail(503, "MAINTENANCE", "平台维护中，请稍后重试")
	}
	_ = tx.Rollback(ctx)
	if action == Start {
		if s.gate == nil {
			return Accepted{}, webapi.Fail(503, "DEPENDENCY_UNAVAILABLE", "运行环境尚未通过启动检查")
		}
		if err = s.gate.Check(ctx, p); err != nil {
			return Accepted{}, err
		}
	}
	return s.acceptCommand(ctx, p, id, action, key, input, hash, trace)
}
func (s *CommandService) acceptCommand(ctx context.Context, p identity.Principal, id string, action Action, key string, input CommandInput, hash []byte, trace string) (Accepted, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Accepted{}, err
	}
	defer tx.Rollback(context.Background())
	if err = requireAction(ctx, tx, p, string(action)); err != nil {
		return Accepted{}, err
	}
	saved, err := savedAcceptance(ctx, tx, p, string(action), id, key, hash)
	if err != nil {
		return Accepted{}, err
	}
	if saved != nil {
		return *saved, tx.Commit(ctx)
	}
	// One organization has one model head. Lock it before quota and instance so
	// credential revocation cannot interleave a newly accepted start/configure.
	var head string
	if err = tx.QueryRow(ctx, `SELECT id::text FROM model_configuration_heads WHERE org_id=$1 FOR UPDATE`, p.OrganizationID).Scan(&head); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Accepted{}, err
	}
	if action == Start || action == Configure {
		var binding Snapshot
		var modelID string
		var modelVersion int64
		if action == Configure {
			modelID, modelVersion = *input.ModelID, *input.ModelVersion
		} else {
			if err = tx.QueryRow(ctx, `SELECT model_config_id::text,model_config_version FROM instances WHERE id=$1 AND org_id=$2 AND owner_id=$3`, id, p.OrganizationID, p.UserID).Scan(&modelID, &modelVersion); err != nil {
				return Accepted{}, err
			}
		}
		if err = bindModel(ctx, tx, p, &binding, modelID, modelVersion); err != nil {
			return Accepted{}, err
		}
	}
	policy, usage, err := lockedQuota(ctx, tx, p.OrganizationID, p.UserID)
	if err != nil {
		return Accepted{}, err
	}
	var state Lifecycle
	var name string
	var encoded []byte
	var assetReady, assetMaintenance bool
	err = tx.QueryRow(ctx, `SELECT i.observed_state,i.desired_state,i.version,i.access_generation,i.name,i.config_snapshot,EXISTS(SELECT 1 FROM operations op WHERE op.instance_id=i.id AND op.status IN ('ACCEPTED','RUNNING','UNCERTAIN')),EXISTS(SELECT 1 FROM storage_assets a WHERE a.id=i.storage_asset_id AND a.status='READY' AND a.init_version>=1 AND a.filesystem_id IS NOT NULL AND a.capacity_bytes=(i.config_snapshot->'spec'->>'storage_bytes')::bigint),EXISTS(SELECT 1 FROM storage_assets a WHERE a.id=i.storage_asset_id AND a.status IN ('RESTORING','RECLAIM_PENDING')) FROM instances i WHERE i.id=$1 AND i.org_id=$2 AND i.owner_id=$3 FOR UPDATE OF i`, id, p.OrganizationID, p.UserID).Scan(&state.Observed, &state.Desired, &state.Version, &state.AccessGeneration, &name, &encoded, &state.ActiveOperation, &assetReady, &assetMaintenance)
	if errors.Is(err, pgx.ErrNoRows) {
		return Accepted{}, queryNotFound()
	}
	if err != nil {
		return Accepted{}, err
	}
	if assetMaintenance && action != Stop {
		return Accepted{}, lifecycleError(ErrStateConflict)
	}
	snapshot, err := DecodeSnapshot(encoded)
	if err != nil {
		return Accepted{}, err
	}
	if action == Delete && *input.ConfirmedName != name {
		return Accepted{}, invalidQuery()
	}
	facts := ResourceFacts{OriginalAssetReady: assetReady}
	if action == Start || action == Configure {
		if action == Configure {
			snapshot.ModelID = *input.ModelID
			snapshot.ModelVersion = *input.ModelVersion
		}
		err = tx.QueryRow(ctx, `SELECT c.provider,c.protocol,c.model,COALESCE(c.manifest->>'digest','') FROM model_configurations c JOIN secrets sec ON sec.id=c.credential_ref AND sec.org_id=c.org_id JOIN model_validations v ON v.config_id=c.id AND v.config_version=c.version WHERE c.org_id=$1 AND c.id=$2 AND c.version=$3 AND public.hc_model_available(c.org_id,$4,c.id,c.version) AND c.status='VALID' AND c.revoked_at IS NULL AND sec.revoked_at IS NULL AND v.status='HEALTHY' AND v.checked_at<=clock_timestamp() AND v.expires_at>clock_timestamp()`, p.OrganizationID, snapshot.ModelID, snapshot.ModelVersion, p.UserID).Scan(&snapshot.Provider, &snapshot.Protocol, &snapshot.Model, &snapshot.ApprovalDigest)
		if errors.Is(err, pgx.ErrNoRows) {
			facts.ModelUsable = false
		} else if err != nil {
			return Accepted{}, err
		} else {
			facts.ModelUsable = true
		}
	}
	if facts.ModelUsable && (action == Start || action == Configure) {
		if err = modelRuntime(ctx, tx, &snapshot, action == Configure); err != nil {
			return Accepted{}, err
		}
	}
	expected := int64(0)
	if input.ExpectedVersion != nil {
		expected = *input.ExpectedVersion
	}
	next, err := state.Plan(action, expected, facts)
	if err != nil {
		return Accepted{}, lifecycleError(err)
	}
	if err = requireAction(ctx, tx, p, string(action)); err != nil {
		return Accepted{}, err
	}
	if action == Configure && next.Desired == "RUNNING" {
		if err = identity.RequirePermission(ctx, tx, p, "instances:start"); err != nil {
			return Accepted{}, err
		}
	}
	reservations := map[string]int64{}
	if action == Start {
		reservations, err = policy.PlanReservation(false, usage, Resources{CPU: snapshot.Spec.CPU, Memory: snapshot.Spec.Memory, Storage: snapshot.Spec.Storage})
		if err != nil {
			return Accepted{}, webapi.Fail(429, "QUOTA_EXCEEDED", "计算或并发额度已用尽")
		}
	}
	if len(reservations) > 0 {
		if err = checkUserQuota(ctx, tx, p.OrganizationID, p.UserID, reservations); err != nil {
			return Accepted{}, err
		}
	}
	accepted := Accepted{InstanceID: id, OperationID: webapi.ID(), State: next.Observed}
	target, err := json.Marshal(struct {
		SchemaVersion int `json:"schema_version"`
		Snapshot
	}{1, snapshot})
	if err != nil {
		return Accepted{}, err
	}
	if trace == "" {
		trace = webapi.ID()
	}
	intent := map[string]any{"schema_version": 1, "expected_instance_version": next.Version, "target_desired_state": next.Desired, "reason": "USER_REQUEST", "request_id": trace, "accepted_auth_generation": p.Generation}
	if action == Start || action == Configure {
		intent["target_snapshot"] = json.RawMessage(target)
	}
	if action == Delete {
		intent["confirmed_name"] = name
	}
	serialized, err := json.Marshal(intent)
	if err != nil {
		return Accepted{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE instances SET observed_state=$2,desired_state=$3,version=$4,access_generation=$5,updated_at=clock_timestamp() WHERE id=$1`, id, next.Observed, next.Desired, next.Version, next.AccessGeneration); err != nil {
		return Accepted{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO operations(id,instance_id,actor_id,type,status,stage,intent,trace_id) VALUES($1,$2,$3,$4,'ACCEPTED','QUEUED',$5,$6)`, accepted.OperationID, id, p.UserID, string(action), serialized, trace); err != nil {
		return Accepted{}, err
	}
	for resource, amount := range reservations {
		if _, err = tx.Exec(ctx, `INSERT INTO quota_reservations(id,org_id,owner_id,instance_id,operation_id,resource_type,amount,status) VALUES($1,$2,$3,$4,$5,$6,$7,'HELD')`, webapi.ID(), p.OrganizationID, p.UserID, id, accepted.OperationID, resource, amount); err != nil {
			return Accepted{}, err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(id,operation_id,status) VALUES($1,$2,'PENDING')`, webapi.ID(), accepted.OperationID); err != nil {
		return Accepted{}, err
	}
	if next.AccessGeneration != state.AccessGeneration {
		if _, err = tx.Exec(ctx, `UPDATE access_routes SET enabled=false WHERE instance_id=$1`, id); err != nil {
			return Accepted{}, err
		}
		if _, err = tx.Exec(ctx, `UPDATE access_sessions SET revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE instance_id=$1`, id); err != nil {
			return Accepted{}, err
		}
	}
	response, err := json.Marshal(accepted)
	if err != nil {
		return Accepted{}, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM idempotency_records WHERE org_id=$1 AND actor_id=$2 AND action=$3 AND target_id=$4 AND key=$5 AND expires_at<=clock_timestamp()`, p.OrganizationID, p.UserID, string(action), id, key); err != nil {
		return Accepted{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO idempotency_records(org_id,actor_id,action,target_id,key,request_sha256,operation_id,response) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, p.OrganizationID, p.UserID, string(action), id, key, hash, accepted.OperationID, response); err != nil {
		return Accepted{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(id,org_id,actor_id,owner_id,resource_id,action,outcome,trace_id) VALUES($1,$2,$3,$3,$4,$5,'ACCEPTED',$6)`, webapi.ID(), p.OrganizationID, p.UserID, id, "INSTANCE_"+string(action), trace); err != nil {
		return Accepted{}, err
	}
	return accepted, tx.Commit(ctx)
}
