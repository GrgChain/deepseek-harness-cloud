package instances

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type retrySource struct {
	Instance, Action, Actor, Status, Code string
	Reconciled                            *time.Time
	Intent                                []byte
}
type retryIntent struct {
	Schema     int             `json:"schema_version"`
	Expected   int64           `json:"expected_instance_version"`
	Desired    string          `json:"target_desired_state"`
	Reason     string          `json:"reason"`
	Request    string          `json:"request_id"`
	Generation int64           `json:"accepted_auth_generation"`
	Target     json.RawMessage `json:"target_snapshot,omitempty"`
	Name       *string         `json:"confirmed_name,omitempty"`
	External   json.RawMessage `json:"external_build,omitempty"`
}

func readRetrySource(ctx context.Context, tx pgx.Tx, p identity.Principal, id string) (retrySource, error) {
	var source retrySource
	err := tx.QueryRow(ctx, `SELECT o.instance_id::text,o.type,o.actor_kind,o.status,COALESCE(o.error_code,''),o.reconciled_at,o.intent FROM operations o JOIN instances i ON i.id=o.instance_id WHERE o.id=$1 AND i.org_id=$2 AND i.owner_id=$3`, id, p.OrganizationID, p.UserID).Scan(&source.Instance, &source.Action, &source.Actor, &source.Status, &source.Code, &source.Reconciled, &source.Intent)
	if errors.Is(err, pgx.ErrNoRows) {
		return source, queryNotFound()
	}
	return source, err
}
func (s *CommandService) Retry(ctx context.Context, p identity.Principal, id, key, trace string, limit func(context.Context, string, int) error) (Accepted, error) {
	if !idPattern.MatchString(id) || !keyPattern.MatchString(key) {
		return Accepted{}, invalidQuery()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Accepted{}, err
	}
	defer tx.Rollback(context.Background())
	if err = identity.RequirePermission(ctx, tx, p, "instances:retry"); err != nil {
		return Accepted{}, err
	}
	source, err := readRetrySource(ctx, tx, p, id)
	if err != nil {
		return Accepted{}, err
	}
	if err = requireAction(ctx, tx, p, source.Action); err != nil {
		return Accepted{}, err
	}
	hash, _ := requestHash(struct{}{})
	saved, err := savedAcceptance(ctx, tx, p, "RETRY", id, key, hash)
	if err != nil {
		return Accepted{}, err
	}
	if saved != nil {
		return *saved, tx.Commit(ctx)
	}
	if source.Status != "FAILED" || source.Actor != "USER" || source.Reconciled == nil {
		return Accepted{}, lifecycleError(ErrStateConflict)
	}
	if s.maintenance && source.Action != "STOP" && source.Action != "DELETE" {
		return Accepted{}, webapi.Fail(503, "MAINTENANCE", "平台维护中")
	}
	var original retryIntent
	if webapi.StrictUnmarshal(source.Intent, &original) != nil {
		return Accepted{}, lifecycleError(ErrStateConflict)
	}
	_ = tx.Rollback(ctx)
	if original.Desired == "RUNNING" {
		if limit == nil || s.gate == nil {
			return Accepted{}, webapi.Fail(503, "DEPENDENCY_UNAVAILABLE", "运行检查暂不可用")
		}
		if err = limit(ctx, "instance-run:"+p.OrganizationID+":"+p.UserID, 10); err != nil {
			return Accepted{}, err
		}
		if err = s.gate.Check(ctx, p); err != nil {
			return Accepted{}, err
		}
	}
	return s.acceptRetry(ctx, p, id, key, trace, hash)
}
func (s *CommandService) acceptRetry(ctx context.Context, p identity.Principal, id, key, trace string, hash []byte) (Accepted, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Accepted{}, err
	}
	defer tx.Rollback(context.Background())
	if err = identity.RequirePermission(ctx, tx, p, "instances:retry"); err != nil {
		return Accepted{}, err
	}
	saved, err := savedAcceptance(ctx, tx, p, "RETRY", id, key, hash)
	if err != nil {
		return Accepted{}, err
	}
	if saved != nil {
		return *saved, tx.Commit(ctx)
	}
	source, err := readRetrySource(ctx, tx, p, id)
	if err != nil {
		return Accepted{}, err
	}
	if err = requireAction(ctx, tx, p, source.Action); err != nil {
		return Accepted{}, err
	}
	var ignored string
	if err = tx.QueryRow(ctx, `SELECT id::text FROM model_configuration_heads WHERE org_id=$1 FOR UPDATE`, p.OrganizationID).Scan(&ignored); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Accepted{}, err
	}
	policy, usage, err := lockedQuota(ctx, tx, p.OrganizationID, p.UserID)
	if err != nil {
		return Accepted{}, err
	}
	var state Lifecycle
	var raw []byte
	var assetReady, assetMaintenance bool
	var name string
	err = tx.QueryRow(ctx, `SELECT i.observed_state,i.desired_state,i.version,i.access_generation,i.config_snapshot,i.name,EXISTS(SELECT 1 FROM operations o WHERE o.instance_id=i.id AND o.status IN ('ACCEPTED','RUNNING','UNCERTAIN')),EXISTS(SELECT 1 FROM storage_assets a WHERE a.id=i.storage_asset_id AND a.status='READY' AND a.init_version>=1 AND a.filesystem_id IS NOT NULL AND a.capacity_bytes=(i.config_snapshot->'spec'->>'storage_bytes')::bigint),EXISTS(SELECT 1 FROM storage_assets a WHERE a.id=i.storage_asset_id AND a.status IN ('RESTORING','RECLAIM_PENDING')) FROM instances i WHERE i.id=$1 AND i.org_id=$2 AND i.owner_id=$3 FOR UPDATE OF i`, source.Instance, p.OrganizationID, p.UserID).Scan(&state.Observed, &state.Desired, &state.Version, &state.AccessGeneration, &raw, &name, &state.ActiveOperation, &assetReady, &assetMaintenance)
	if err != nil {
		return Accepted{}, err
	}
	if assetMaintenance && source.Action != "STOP" {
		return Accepted{}, lifecycleError(ErrStateConflict)
	}
	// Re-read after the instance lock: no late acceptance can pass this boundary.
	source, err = readRetrySource(ctx, tx, p, id)
	if err != nil {
		return Accepted{}, err
	}
	var latest string
	if err = tx.QueryRow(ctx, `SELECT id::text FROM operations WHERE instance_id=$1 ORDER BY created_at DESC,id DESC LIMIT 1`, source.Instance).Scan(&latest); err != nil {
		return Accepted{}, err
	}
	var original retryIntent
	if webapi.StrictUnmarshal(source.Intent, &original) != nil || original.Schema != 1 || original.Reason != "USER_REQUEST" || original.Expected < 1 || original.Generation < 1 {
		return Accepted{}, lifecycleError(ErrStateConflict)
	}
	target, err := DecodeSnapshot(raw)
	if err != nil {
		return Accepted{}, err
	}
	if len(original.Target) > 0 {
		target, err = DecodeSnapshot(original.Target)
		if err != nil {
			return Accepted{}, err
		}
	}
	facts := RetryFacts{Latest: latest == id, Failed: source.Status == "FAILED", User: source.Actor == "USER", Reconciled: source.Reconciled != nil, Diagnostic: retryDiagnostic(source.Code), OriginalAssetReady: assetReady}
	if source.Action == "CREATE" || source.Action == "START" || source.Action == "CONFIGURE" {
		if len(original.Target) == 0 || original.Name != nil {
			return Accepted{}, lifecycleError(ErrStateConflict)
		}
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM model_configurations c JOIN secrets sec ON sec.id=c.credential_ref AND sec.org_id=c.org_id JOIN model_validations v ON v.config_id=c.id AND v.config_version=c.version WHERE c.org_id=$1 AND c.id=$2 AND c.version=$3 AND c.status='VALID' AND c.revoked_at IS NULL AND sec.revoked_at IS NULL AND v.status='HEALTHY' AND v.checked_at<=clock_timestamp() AND v.expires_at>clock_timestamp())`, p.OrganizationID, target.ModelID, target.ModelVersion).Scan(&facts.ModelUsable); err != nil {
			return Accepted{}, err
		}
	} else if len(original.Target) > 0 || (source.Action == "DELETE" && (original.Name == nil || *original.Name != name)) || (source.Action == "STOP" && original.Name != nil) {
		return Accepted{}, lifecycleError(ErrStateConflict)
	}
	next, err := state.PlanRetry(source.Action, original.Desired, facts)
	if err != nil {
		return Accepted{}, lifecycleError(err)
	}
	var own QuotaUsage
	if err = tx.QueryRow(ctx, `SELECT COALESCE(sum(amount) FILTER(WHERE resource_type='INSTANCE'),0),COALESCE(sum(amount) FILTER(WHERE resource_type='STORAGE'),0),COALESCE(sum(amount) FILTER(WHERE resource_type='RUNNING'),0),COALESCE(sum(amount) FILTER(WHERE resource_type='CPU'),0),COALESCE(sum(amount) FILTER(WHERE resource_type='MEMORY'),0),COALESCE(sum(amount) FILTER(WHERE resource_type='CONCURRENT'),0) FROM quota_reservations WHERE instance_id=$1 AND status IN ('HELD','USED')`, source.Instance).Scan(&own.Instances, &own.Storage, &own.Running, &own.CPU, &own.Memory, &own.Concurrent); err != nil {
		return Accepted{}, err
	}
	reservations := map[string]int64{}
	if original.Desired == "RUNNING" {
		reservations, err = retryReservations(policy, usage, own, Resources{target.Spec.CPU, target.Spec.Memory, target.Spec.Storage}, true)
		if err != nil {
			return Accepted{}, webapi.Fail(429, "QUOTA_EXCEEDED", "重试所需计算或并发额度不足")
		}
	}
	if len(reservations) > 0 {
		if err = checkUserQuota(ctx, tx, p.OrganizationID, p.UserID, reservations); err != nil {
			return Accepted{}, err
		}
	}
	if err = identity.RequirePermission(ctx, tx, p, "instances:retry"); err != nil {
		return Accepted{}, err
	}
	if trace == "" {
		trace = webapi.ID()
	}
	if err = requireAction(ctx, tx, p, source.Action); err != nil {
		return Accepted{}, err
	}
	original.Expected, original.Request, original.Generation = next.Version, trace, p.Generation
	// Failed build history stays on the linked source operation. A new attempt
	// reconciles the same Workspace's latest build before issuing another build.
	original.External = nil
	intent, err := json.Marshal(original)
	if err != nil {
		return Accepted{}, err
	}
	accepted := Accepted{InstanceID: source.Instance, OperationID: webapi.ID(), State: next.Observed}
	if _, err = tx.Exec(ctx, `UPDATE instances SET observed_state=$2,desired_state=$3,version=$4,access_generation=$5,updated_at=clock_timestamp() WHERE id=$1`, source.Instance, next.Observed, next.Desired, next.Version, next.AccessGeneration); err != nil {
		return Accepted{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO operations(id,instance_id,actor_id,type,status,stage,retry_of,intent,trace_id) VALUES($1,$2,$3,$4,'ACCEPTED','QUEUED',$5,$6,$7)`, accepted.OperationID, source.Instance, p.UserID, source.Action, id, intent, trace); err != nil {
		return Accepted{}, err
	}
	for resource, amount := range reservations {
		if amount == 0 {
			continue
		}
		if _, err = tx.Exec(ctx, `INSERT INTO quota_reservations(id,org_id,owner_id,instance_id,operation_id,resource_type,amount,status) VALUES($1,$2,$3,$4,$5,$6,$7,'HELD')`, webapi.ID(), p.OrganizationID, p.UserID, source.Instance, accepted.OperationID, resource, amount); err != nil {
			return Accepted{}, err
		}
	}
	for _, sql := range []string{`UPDATE access_routes SET enabled=false WHERE instance_id=$1`, `UPDATE access_sessions SET revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE instance_id=$1`} {
		if _, err = tx.Exec(ctx, sql, source.Instance); err != nil {
			return Accepted{}, err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(id,operation_id,status) VALUES($1,$2,'PENDING')`, webapi.ID(), accepted.OperationID); err != nil {
		return Accepted{}, err
	}
	response, _ := json.Marshal(accepted)
	if _, err = tx.Exec(ctx, `INSERT INTO idempotency_records(org_id,actor_id,action,target_id,key,request_sha256,operation_id,response) VALUES($1,$2,'RETRY',$3,$4,$5,$6,$7)`, p.OrganizationID, p.UserID, id, key, hash, accepted.OperationID, response); err != nil {
		return Accepted{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(id,org_id,actor_id,owner_id,resource_id,action,outcome,trace_id,details) VALUES($1,$2,$3,$3,$4,'OPERATION_RETRY','ACCEPTED',$5,jsonb_build_object('retry_of',$6::text))`, webapi.ID(), p.OrganizationID, p.UserID, source.Instance, trace, id); err != nil {
		return Accepted{}, err
	}
	return accepted, tx.Commit(ctx)
}
