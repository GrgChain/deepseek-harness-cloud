package worker

import (
	"context"
	"errors"
	"log/slog"

	workspaceengine "github.com/jonluo94/deepseek-harness-cloud/services/internal/workspace"
)

func failureCode(err error) string {
	switch {
	case errors.Is(err, ErrStorageUnavailable):
		return "STORAGE_MISSING"
	case errors.Is(err, ErrExecutionRevoked) || errors.Is(err, ErrSuperseded):
		return "CONFIGURATION_INVALID"
	case errors.Is(err, ErrHostCapacity):
		return "DEPENDENCY_UNAVAILABLE"
	case errors.Is(err, ErrNativeModelUnavailable):
		return "MODEL_UNAVAILABLE"
	case errors.Is(err, ErrBuildRejected):
		return "WORKSPACE_UNAVAILABLE"
	default:
		return "INTERNAL_ERROR"
	}
}

func (e *Executor) cleanup(ctx context.Context, lease Lease) error {
	p, err := e.checkpoint(ctx, lease, "REVOKING_ACCESS", true)
	if err != nil {
		slog.Warn("Worker cleanup checkpoint failed", "operation_id", lease.ID, "instance_id", lease.InstanceID, "reason_code", retryReason(err))
		return err
	}
	defer p.Destroy()
	w, deleted, err := e.lookup(ctx, p, true)
	if errors.Is(err, workspaceengine.ErrNotFound) || deleted {
		completion, err := e.host.ConfirmAbsent(ctx, p, deleted)
		if err != nil {
			slog.Warn("Worker cleanup could not confirm absent compute", "operation_id", lease.ID, "instance_id", lease.InstanceID, "reason_code", retryReason(err))
			return err
		}
		err = e.store.Fail(ctx, lease, p.FailureCode, completion)
		if err != nil {
			slog.Warn("Worker cleanup terminal publication failed", "operation_id", lease.ID, "instance_id", lease.InstanceID, "reason_code", retryReason(err))
		}
		return err
	}
	if err != nil {
		slog.Warn("Worker cleanup could not verify workspace identity", "operation_id", lease.ID, "instance_id", lease.InstanceID, "reason_code", retryReason(err))
		return err
	}
	p.WorkspaceID = &w.ID
	if err = e.host.Quiesce(ctx, p); err != nil {
		slog.Warn("Worker cleanup could not quiesce workspace", "operation_id", lease.ID, "instance_id", lease.InstanceID, "reason_code", retryReason(err))
		return err
	}
	if p.External != nil && !p.External.Completed {
		if buildDisposition(*p.External, w.LatestBuild, false) != "ADOPT" {
			return ErrExternalPending
		}
		if err = e.remember(ctx, lease, w.ID, w.LatestBuild.ID, true); err != nil {
			return err
		}
		_, waitErr := e.waitBuild(ctx, p, w, w.LatestBuild.ID)
		if waitErr != nil && !errors.Is(waitErr, ErrBuildRejected) {
			return waitErr
		}
		if err = e.store.ConfirmBuild(ctx, lease, w.LatestBuild.ID); err != nil {
			return err
		}
	}
	if _, err = e.ensureBuild(ctx, lease, p, w, "stop", "CLEANUP_STOP", true); err != nil {
		return err
	}
	completion, err := e.host.ConfirmAbsent(ctx, p, false)
	if err != nil {
		slog.Warn("Worker cleanup could not confirm stopped compute", "operation_id", lease.ID, "instance_id", lease.InstanceID, "reason_code", retryReason(err))
		return err
	}
	err = e.store.Fail(ctx, lease, p.FailureCode, completion)
	if err != nil {
		slog.Warn("Worker cleanup terminal publication failed", "operation_id", lease.ID, "instance_id", lease.InstanceID, "reason_code", retryReason(err))
	}
	return err
}
