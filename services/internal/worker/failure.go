package worker

import (
	"context"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/instances"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func validFailure(code string) bool {
	switch code {
	case "MODEL_UNAVAILABLE", "WORKSPACE_UNAVAILABLE", "STORAGE_MISSING", "CONFIGURATION_INVALID", "DEPENDENCY_UNAVAILABLE", "INTERNAL_ERROR", "AUTH_REVOKED", "CREDENTIAL_REVOKED":
		return true
	}
	return false
}

// Fail reconciles a determinate failure only after the adapter proves compute
// absent. Unknown external outcomes use LeaseStore.Defer and retain every slot.
// Security revocation may supersede a running intent; verified cleanup can close
// that old operation without ever restoring the revoked route/configuration.
func (s *ExecutionStore) Fail(ctx context.Context, lease Lease, code string, proof Completion) error {
	if !validFailure(code) || !proof.ComputeAbsent || proof.Running != nil {
		return ErrInvalidEvidence
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	r, err := s.lockExecution(ctx, tx, lease, true)
	if err != nil {
		return err
	}
	target := r.Snapshot
	if len(r.Intent.Target) > 0 {
		target, err = instances.DecodeSnapshot(r.Intent.Target)
		if err != nil {
			return err
		}
	}
	var revoked bool
	if err = tx.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM model_configurations c JOIN secrets sec ON sec.id=c.credential_ref AND sec.org_id=c.org_id WHERE c.org_id=$1 AND c.id=$2 AND c.version=$3 AND c.revoked_at IS NULL AND c.status<>'REVOKED' AND sec.revoked_at IS NULL)`, r.Org, target.ModelID, target.ModelVersion).Scan(&revoked); err != nil {
		return err
	}
	if r.Version != r.Intent.ExpectedVersion || r.Desired != r.Intent.Desired {
		if r.Desired == "RUNNING" || r.Authorized && !revoked {
			return ErrSuperseded
		}
	}
	state, desired := "ERROR", r.Desired
	if !r.Authorized || revoked || r.Desired == "STOPPED" {
		state = "STOPPED"
		if desired != "DELETED" {
			desired = "STOPPED"
		}
	}
	if proof.AssetReady {
		if r.AssetID == nil || !uuidPattern.MatchString(proof.FilesystemID) || r.AssetCapacity != r.Snapshot.Spec.Storage || r.AssetStatus == "DELETED" || r.AssetStatus == "RECLAIM_PENDING" || r.AssetStatus == "RESTORING" {
			return ErrInvalidEvidence
		}
		tag, err := tx.Exec(ctx, `UPDATE storage_assets SET status='READY',init_version=GREATEST(init_version,1),filesystem_id=$2 WHERE id=$1 AND (filesystem_id IS NULL OR filesystem_id=$2)`, *r.AssetID, proof.FilesystemID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrInvalidEvidence
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE access_routes SET enabled=false WHERE instance_id=$1`, lease.InstanceID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE access_sessions SET revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE instance_id=$1`, lease.InstanceID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM native_sessions WHERE instance_id=$1`, lease.InstanceID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE instance_health SET status='UNKNOWN',checked_at=NULL,expires_at=NULL,reason_code=$2 WHERE instance_id=$1`, lease.InstanceID, code); err != nil {
		return err
	}
	// An ERROR row still counts as an instance. Storage remains held until its
	// physical identity is known; only proved absence of compute releases compute.
	if _, err = tx.Exec(ctx, `UPDATE quota_reservations SET status=CASE WHEN resource_type IN ('CPU','MEMORY','RUNNING','CONCURRENT') THEN 'RELEASED' WHEN resource_type='INSTANCE' OR $2 THEN 'USED' ELSE status END,updated_at=clock_timestamp() WHERE instance_id=$1 AND status IN ('HELD','USED')`, lease.InstanceID, proof.AssetReady); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE instances SET observed_state=$2,desired_state=$3,version=version+1,access_generation=access_generation+1,updated_at=clock_timestamp() WHERE id=$1`, lease.InstanceID, state, desired); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE operations SET status='FAILED',stage='COMPLETE',error_code=$4,error_message=NULL,reconciled_at=clock_timestamp(),lease_owner=NULL,lease_expires_at=NULL,updated_at=clock_timestamp() WHERE id=$1 AND lease_owner=$2 AND lease_token=$3 AND lease_expires_at>clock_timestamp() AND status IN ('RUNNING','UNCERTAIN')`, lease.ID, lease.Owner, lease.Token, code)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrLostLease
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(id,org_id,actor_id,owner_id,resource_id,action,outcome,trace_id,details) VALUES($1,$2,$3,$4,$5,$6,'FAILED',$7,jsonb_build_object('schema_version',1,'operation_id',$8::text,'reason_code',$9::text))`, webapi.ID(), r.Org, r.ActorID, r.Owner, lease.InstanceID, "INSTANCE_"+r.Action, r.Trace, lease.ID, code); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE idempotency_records SET expires_at=clock_timestamp()+interval '7 days' WHERE operation_id=$1`, lease.ID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE outbox_events SET status='DONE' WHERE operation_id=$1 AND status='PENDING'`, lease.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
