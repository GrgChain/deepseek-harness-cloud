package worker

import (
	"context"
	"errors"
	"log/slog"
	"time"

	workspaceengine "github.com/jonluo94/deepseek-harness-cloud/services/internal/workspace"
)

var ErrBuildRejected = errors.New("owned workspace build failed")

type WorkspaceControl interface {
	User(context.Context, string) (workspaceengine.User, error)
	WorkspaceByName(context.Context, string, string) (workspaceengine.Workspace, error)
	WorkspaceByNameIncludingDeleted(context.Context, string, string) (workspaceengine.Workspace, error)
	CreateWorkspace(context.Context, string, workspaceengine.CreateWorkspaceRequest) (workspaceengine.Workspace, error)
	CreateBuild(context.Context, string, string, string, []workspaceengine.Parameter) (workspaceengine.Build, error)
	Build(context.Context, string) (workspaceengine.Build, error)
	BuildParameters(context.Context, string) ([]workspaceengine.Parameter, error)
}

type Executor struct {
	store                     *ExecutionStore
	leases                    *LeaseStore
	workspaces                WorkspaceControl
	host                      *LocalHost
	organization, controlUser string
}

func NewExecutor(store *ExecutionStore, leases *LeaseStore, client WorkspaceControl, host *LocalHost, organization, controlUser string) (*Executor, error) {
	if store == nil || leases == nil || client == nil || host == nil || !uuidPattern.MatchString(organization) || !uuidPattern.MatchString(controlUser) {
		return nil, ErrInvalidEvidence
	}
	return &Executor{store, leases, client, host, organization, controlUser}, nil
}

func (e *Executor) checkpoint(ctx context.Context, lease Lease, stage string, cleanup bool) (ExecutionPlan, error) {
	var plan ExecutionPlan
	var err error
	if cleanup {
		plan, err = e.store.CleanupPlan(ctx, lease)
	} else {
		plan, err = e.store.Checkpoint(ctx, lease)
	}
	if err != nil {
		return ExecutionPlan{}, err
	}
	if plan.FailureCode != "" && !cleanup {
		plan.Destroy()
		return ExecutionPlan{}, ErrBuildRejected
	}
	if err = e.leases.Stage(ctx, lease, stage); err != nil {
		plan.Destroy()
		return ExecutionPlan{}, err
	}
	return plan, nil
}

// Execute ends only after terminal publication or a durable UNCERTAIN retry.
// Lost leases stop subsequent effects; a later owner reconciles the stable IDs.
func (e *Executor) Execute(ctx context.Context, lease Lease) error {
	lockCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	release, err := e.host.Acquire(lockCtx, lease.InstanceID)
	cancel()
	if err != nil {
		return err
	}
	defer release()
	if err = e.host.EnsureHelpersAbsent(ctx, lease.InstanceID); err != nil {
		return err
	}
	p, err := e.checkpoint(ctx, lease, "RECONCILING", false)
	if err == nil {
		err = e.apply(ctx, lease, p)
		p.Destroy()
		if err == nil {
			return nil
		}
	}
	if errors.Is(err, ErrLostLease) || ctx.Err() != nil {
		return err
	}
	cleanupPlan, readErr := e.store.CleanupPlan(ctx, lease)
	if readErr != nil {
		slog.Warn("Worker reconciliation could not load cleanup evidence", "operation_id", lease.ID, "instance_id", lease.InstanceID, "reason_code", retryReason(readErr))
		return readErr
	}
	code := cleanupPlan.FailureCode
	cleanupPlan.Destroy()
	if code == "" {
		if errors.Is(err, ErrExternalPending) || errors.Is(err, workspaceengine.ErrUncertain) || errors.Is(err, workspaceengine.ErrUnavailable) || errors.Is(err, ErrHostUnavailable) {
			return e.leases.Defer(ctx, lease, true)
		}
		code = failureCode(err)
		if err = e.store.RequestCleanup(ctx, lease, code); err != nil {
			slog.Warn("Worker reconciliation could not request cleanup", "operation_id", lease.ID, "instance_id", lease.InstanceID, "reason_code", retryReason(err))
			return err
		}
	}
	if err = e.cleanup(ctx, lease); err == nil {
		return nil
	}
	slog.Warn("Worker cleanup evidence was incomplete", "operation_id", lease.ID, "instance_id", lease.InstanceID, "reason_code", retryReason(err))
	if errors.Is(err, ErrLostLease) || ctx.Err() != nil {
		return err
	}
	return e.leases.Defer(ctx, lease, true)
}
