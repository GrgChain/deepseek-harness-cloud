package worker

import (
	"context"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/instances"
)

// CleanupPlan has no credentials and permits only safety mutations. It is also
// available when revocation has advanced the instance past the accepted intent.
func (s *ExecutionStore) CleanupPlan(ctx context.Context, lease Lease) (ExecutionPlan, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ExecutionPlan{}, err
	}
	defer tx.Rollback(context.Background())
	r, err := s.lockExecution(ctx, tx, lease, true)
	if err != nil {
		return ExecutionPlan{}, err
	}
	plan := ExecutionPlan{InstanceID: lease.InstanceID, OrganizationID: r.Org, OwnerID: r.Owner, Action: r.Action, Desired: r.Desired, Version: r.Version, WorkspaceID: r.WorkspaceID, Snapshot: r.Snapshot, RuntimeGeneration: lease.ID, Capacity: r.AssetCapacity, AssetStatus: r.AssetStatus, External: r.Intent.External}
	if r.AssetID != nil {
		plan.AssetID = *r.AssetID
		if err = tx.QueryRow(ctx, `SELECT resource_name,COALESCE(filesystem_id,'') FROM storage_assets WHERE id=$1`, *r.AssetID).Scan(&plan.ResourceName, &plan.FilesystemID); err != nil {
			return ExecutionPlan{}, err
		}
	}
	if len(r.Intent.Target) > 0 {
		plan.Snapshot, err = instances.DecodeSnapshot(r.Intent.Target)
		if err != nil {
			return ExecutionPlan{}, err
		}
	}
	if err = tx.QueryRow(ctx, `SELECT i.workspace_name,u.workspace_owner_id::text FROM instances i JOIN users u ON u.id=i.owner_id WHERE i.id=$1`, lease.InstanceID).Scan(&plan.WorkspaceName, &plan.WorkspaceOwner); err != nil {
		return ExecutionPlan{}, err
	}
	if err = tx.QueryRow(ctx, `SELECT build_id::text,COALESCE(error_code,'') FROM operations WHERE id=$1`, lease.ID).Scan(&plan.BuildID, &plan.FailureCode); err != nil {
		return ExecutionPlan{}, err
	}
	if err = tx.QueryRow(ctx, `SELECT (SELECT build_id::text FROM operations WHERE instance_id=$1 AND id<>$2 AND status IN ('SUCCEEDED','FAILED') AND build_id IS NOT NULL ORDER BY updated_at DESC,id DESC LIMIT 1)`, lease.InstanceID, lease.ID).Scan(&plan.LastBuildID); err != nil {
		return ExecutionPlan{}, err
	}
	return plan, tx.Commit(ctx)
}
func (s *ExecutionStore) RequestCleanup(ctx context.Context, lease Lease, code string) error {
	if !validFailure(code) {
		return ErrInvalidEvidence
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err = s.lockExecution(ctx, tx, lease, true); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE operations SET error_code=$4,stage='REVOKING_ACCESS',updated_at=clock_timestamp() WHERE id=$1 AND lease_owner=$2 AND lease_token=$3 AND lease_expires_at>clock_timestamp() AND status IN ('RUNNING','UNCERTAIN')`, lease.ID, lease.Owner, lease.Token, code)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrLostLease
	}
	if _, err = tx.Exec(ctx, `UPDATE access_routes SET enabled=false WHERE instance_id=$1`, lease.InstanceID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (h *LocalHost) Quiesce(ctx context.Context, p ExecutionPlan) error {
	container, err := h.container(ctx, p.InstanceID)
	if err != nil {
		return err
	}
	if container == nil {
		return nil
	}
	// Safety stop checks immutable ownership, even when isolation/readiness has
	// failed. It never starts or mutates a foreign or unverified container.
	labels := container.Config.Labels
	if labels["hc.instance_id"] != p.InstanceID || labels["hc.asset_id"] != p.AssetID || labels["hc.workspace_owner_id"] != p.WorkspaceOwner || p.WorkspaceID == nil || labels["hc.workspace_id"] != *p.WorkspaceID {
		return ErrHostIdentity
	}
	if container.State.Running {
		_, err = dockerCommand(ctx, nil, "stop", "--time", "30", "hc-"+p.InstanceID)
	}
	return err
}
