package worker

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/instances"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type storageReleaseHost interface {
	Acquire(context.Context, string) (func(), error)
	ReclaimAsset(context.Context, ExecutionPlan) error
}
type StorageReleaser struct {
	pool        *pgxpool.Pool
	host        storageReleaseHost
	maintenance bool
}

func NewStorageReleaser(pool *pgxpool.Pool, host storageReleaseHost) *StorageReleaser {
	return &StorageReleaser{pool: pool, host: host}
}

// The accepted owner intent survives cancellation and lost final commits. Every
// retry checks the same original disk; quota stays allocated until physical proof.
func (s *StorageReleaser) release(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	var p ExecutionPlan
	var owner, org string
	err := s.pool.QueryRow(ctx, `SELECT instance_id::text,org_id::text,owner_id::text FROM storage_release_requests WHERE id=$1`, id).Scan(&p.InstanceID, &org, &owner)
	if err != nil {
		return err
	}
	unlock, err := s.host.Acquire(ctx, p.InstanceID)
	if err != nil {
		return err
	}
	defer unlock()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SET LOCAL lock_timeout='2s'; SET LOCAL statement_timeout='5s'; SET LOCAL idle_in_transaction_session_timeout='55s'`); err != nil {
		return err
	}
	var authorized bool
	if err = tx.QueryRow(ctx, `SELECT public.hc_worker_lock_authority($1,$2) AND public.hc_has_permission($1,$2,'instances:release-storage')`, org, owner).Scan(&authorized); err != nil {
		return err
	}
	if !authorized {
		return ErrExecutionRevoked
	}
	var quotaVersion int64
	if err = tx.QueryRow(ctx, `SELECT version FROM quota_policies WHERE org_id=$1 FOR UPDATE`, org).Scan(&quotaVersion); err != nil {
		return err
	}
	var raw, intent []byte
	var name, desired, observed, status, requestStatus, confirmed string
	var held, busy, reserved bool
	err = tx.QueryRow(ctx, `SELECT i.config_snapshot,i.workspace_id::text,i.workspace_name,u.workspace_owner_id::text,a.id::text,a.resource_name,a.capacity_bytes,a.filesystem_id,a.maintenance_intent,i.name,i.desired_state,i.observed_state,a.status,a.legal_hold,
 EXISTS(SELECT 1 FROM operations o WHERE o.instance_id=i.id AND o.status IN ('ACCEPTED','RUNNING','UNCERTAIN')) OR EXISTS(SELECT 1 FROM storage_backups b WHERE b.asset_id=a.id AND b.status IN ('RUNNING','UNCERTAIN')) OR EXISTS(SELECT 1 FROM recovery_quota_holds h WHERE h.org_id=i.org_id AND h.instance_id=i.id),
 (SELECT coalesce(sum(q.amount),0)=a.capacity_bytes FROM quota_reservations q WHERE q.org_id=a.org_id AND q.owner_id=a.owner_id AND q.instance_id=i.id AND q.resource_type='STORAGE' AND q.status IN ('HELD','USED'))
 FROM instances i JOIN storage_assets a ON a.id=i.storage_asset_id AND a.instance_id=i.id AND a.org_id=i.org_id AND a.owner_id=i.owner_id JOIN users u ON u.id=i.owner_id
 WHERE i.id=$1 AND i.org_id=$2 AND i.owner_id=$3 FOR UPDATE OF i,a`, p.InstanceID, org, owner).Scan(&raw, &p.WorkspaceID, &p.WorkspaceName, &p.WorkspaceOwner, &p.AssetID, &p.ResourceName, &p.Capacity, &p.FilesystemID, &intent, &name, &desired, &observed, &status, &held, &busy, &reserved)
	if err != nil {
		return err
	}
	err = tx.QueryRow(ctx, `SELECT status,confirmed_name FROM storage_release_requests WHERE id=$1 AND org_id=$2 AND owner_id=$3 AND instance_id=$4 AND asset_id=$5 FOR UPDATE`, id, org, owner, p.InstanceID, p.AssetID).Scan(&requestStatus, &confirmed)
	if err != nil {
		return err
	}
	if requestStatus == "SUCCEEDED" {
		return nil
	}
	var saved struct {
		SchemaVersion int    `json:"schema_version"`
		Action        string `json:"action"`
		RequestID     string `json:"request_id"`
	}
	if webapi.StrictUnmarshal(intent, &saved) != nil || saved.SchemaVersion != 1 || saved.Action != "OWNER_RELEASE" || saved.RequestID != id || name != confirmed || desired != "DELETED" || observed != "DELETED" || status != "RECLAIM_PENDING" || held || busy || !reserved || p.FilesystemID != p.AssetID {
		return ErrInvalidEvidence
	}
	p.OrganizationID = org
	p.OwnerID = owner
	p.AssetStatus = status
	p.Snapshot, err = instances.DecodeSnapshot(raw)
	if err != nil || p.Snapshot.Spec.Storage != p.Capacity {
		return ErrInvalidEvidence
	}
	// Keep authorization, legal-hold, lifecycle and quota locks through physical
	// confirmation and the final atomic audit, so concurrent changes cannot race IO.
	if err = s.host.ReclaimAsset(ctx, p); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE storage_assets SET status='DELETED',maintenance_intent=NULL WHERE id=$1`, p.AssetID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE quota_reservations SET status='RELEASED',updated_at=clock_timestamp() WHERE instance_id=$1 AND resource_type='STORAGE' AND status IN ('HELD','USED')`, p.InstanceID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE quota_policies SET version=version+1 WHERE org_id=$1`, org); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE storage_release_requests SET status='SUCCEEDED',completed_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1`, id); err != nil {
		return err
	}
	details, _ := json.Marshal(map[string]any{"schema_version": 1, "request_id": id, "source_asset_id": p.AssetID, "reason_code": "OWNER_CONFIRMED_RELEASE"})
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(id,org_id,owner_id,resource_id,action,outcome,trace_id,details) VALUES($1,$2,$3,$4,'STORAGE_OWNER_RELEASE','SUCCEEDED',$5,$6)`, webapi.ID(), org, owner, p.AssetID, id, details); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *StorageReleaser) tick(ctx context.Context) {
	if s.maintenance {
		return
	}
	query, stop := context.WithTimeout(ctx, 3*time.Second)
	var id string
	err := s.pool.QueryRow(query, `SELECT id::text FROM storage_release_requests WHERE status<>'SUCCEEDED' AND retry_at<=clock_timestamp() ORDER BY retry_at,id LIMIT 1`).Scan(&id)
	stop()
	if errors.Is(err, pgx.ErrNoRows) || err != nil {
		return
	}
	if err = s.release(ctx, id); err != nil {
		finish, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		// A failed/lost result retains the accepted intent and all storage quota.
		_, _ = s.pool.Exec(finish, `UPDATE storage_release_requests SET status='UNCERTAIN',retry_at=clock_timestamp()+interval '60 seconds',updated_at=clock_timestamp() WHERE id=$1 AND status<>'SUCCEEDED'`, id)
	}
}
func (s *StorageReleaser) Run(ctx context.Context) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		s.tick(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
