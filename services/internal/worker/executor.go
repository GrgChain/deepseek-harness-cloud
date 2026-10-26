package worker

import (
	"context"
	"errors"
	"log/slog"
	"time"

	workspaceengine "github.com/jonluo94/deepseek-harness-cloud/services/internal/workspace"
)

var ErrBuildRejected = errors.New("owned workspace build failed")

func buildDisposition(attempt BuildAttempt, current workspaceengine.Build, fresh bool) string {
	if current.ID == attempt.BeforeID && current.BuildNumber == attempt.BeforeNumber {
		if fresh {
			return "ISSUE"
		}
		return "UNCERTAIN"
	}
	if current.BuildNumber == attempt.BeforeNumber+1 && current.Transition == attempt.Transition && current.TemplateVersionID == attempt.TemplateVersion && (attempt.BuildID == nil || *attempt.BuildID == current.ID) {
		return "ADOPT"
	}
	return "CONFLICT"
}

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
func (e *Executor) verifyBuild(p ExecutionPlan, w workspaceengine.Workspace, build workspaceengine.Build) error {
	if !uuidPattern.MatchString(build.ID) || build.WorkspaceID != w.ID || build.OwnerID != p.WorkspaceOwner || build.InitiatorID != e.controlUser || build.TemplateVersionID != p.Snapshot.TemplateVersion || build.BuildNumber < 1 {
		return ErrHostIdentity
	}
	switch build.Transition {
	case "start", "stop", "delete":
	default:
		return ErrHostIdentity
	}
	return nil
}
func (e *Executor) verifyWorkspace(ctx context.Context, p ExecutionPlan, w workspaceengine.Workspace, cleanup bool) error {
	if !uuidPattern.MatchString(w.ID) || w.Name != p.WorkspaceName || w.OwnerID != p.WorkspaceOwner || w.OrganizationID != e.organization || w.TemplateID != p.Snapshot.TemplateID || w.AutomaticUpdates != "never" || (w.TTLMillis != nil && *w.TTLMillis != 0) || (p.WorkspaceID != nil && *p.WorkspaceID != w.ID) {
		return ErrHostIdentity
	}
	priorPlan := p
	if w.LatestBuild.TemplateVersionID != p.Snapshot.TemplateVersion {
		if err := e.verifyMigratedPriorBuild(ctx, p, w.LatestBuild); err != nil {
			return err
		}
		priorPlan.Snapshot.TemplateVersion = w.LatestBuild.TemplateVersionID
	}
	if err := e.verifyBuild(priorPlan, w, w.LatestBuild); err != nil {
		return err
	}
	parameters, err := e.workspaces.BuildParameters(ctx, w.LatestBuild.ID)
	if err != nil {
		return err
	}
	if len(parameters) != 2 {
		return ErrHostIdentity
	}
	seen := map[string]string{}
	for _, parameter := range parameters {
		if _, ok := seen[parameter.Name]; ok {
			return ErrHostIdentity
		}
		seen[parameter.Name] = parameter.Value
	}
	if seen["instance_id"] != p.InstanceID || seen["asset_id"] != p.AssetID {
		return ErrHostIdentity
	}
	if !cleanup && p.BuildID == nil && p.External == nil && p.LastBuildID != nil && w.LatestBuild.ID != *p.LastBuildID {
		return ErrHostIdentity
	}
	return nil
}
func (e *Executor) lookup(ctx context.Context, p ExecutionPlan, cleanup bool) (workspaceengine.Workspace, bool, error) {
	w, err := e.workspaces.WorkspaceByName(ctx, p.WorkspaceOwner, p.WorkspaceName)
	deleted := false
	if errors.Is(err, workspaceengine.ErrNotFound) {
		w, err = e.workspaces.WorkspaceByNameIncludingDeleted(ctx, p.WorkspaceOwner, p.WorkspaceName)
		deleted = err == nil
	}
	if err != nil {
		return workspaceengine.Workspace{}, false, err
	}
	if err = e.verifyWorkspace(ctx, p, w, cleanup); err != nil {
		return workspaceengine.Workspace{}, false, err
	}
	if deleted && (w.LatestBuild.Transition != "delete" || w.LatestBuild.Job.Status != "succeeded") {
		return workspaceengine.Workspace{}, false, ErrHostIdentity
	}
	return w, deleted, nil
}
func (e *Executor) remember(ctx context.Context, lease Lease, workspace, build string, cleanup bool) error {
	if cleanup {
		return e.store.rememberExternal(ctx, lease, workspace, build, true)
	}
	return e.store.RememberExternal(ctx, lease, workspace, build)
}
func (e *Executor) waitBuild(ctx context.Context, p ExecutionPlan, w workspaceengine.Workspace, id string) (workspaceengine.Build, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	timer := time.NewTicker(250 * time.Millisecond)
	defer timer.Stop()
	for {
		build, err := e.workspaces.Build(ctx, id)
		if err != nil {
			return workspaceengine.Build{}, err
		}
		if err = e.verifyBuild(p, w, build); err != nil {
			return workspaceengine.Build{}, err
		}
		switch build.Job.Status {
		case "succeeded":
			return build, nil
		case "failed", "canceled":
			return build, ErrBuildRejected
		case "pending", "running", "canceling":
		default:
			return workspaceengine.Build{}, ErrHostIdentity
		}
		select {
		case <-ctx.Done():
			return workspaceengine.Build{}, ErrExternalPending
		case <-timer.C:
		}
	}
}
func (e *Executor) ensureBuild(ctx context.Context, lease Lease, p ExecutionPlan, w workspaceengine.Workspace, transition, phase string, cleanup bool) (workspaceengine.Build, error) {
	if w.LatestBuild.Transition == transition && w.LatestBuild.TemplateVersionID == p.Snapshot.TemplateVersion && p.External == nil {
		// Safety cleanup can acknowledge an already achieved external target. New
		// START needs its own durable build attempt and cannot adopt an old start.
		if transition == "stop" || transition == "delete" {
			build, err := e.waitBuild(ctx, p, w, w.LatestBuild.ID)
			if err != nil {
				return build, err
			}
			if err = e.remember(ctx, lease, w.ID, build.ID, cleanup); err != nil {
				return workspaceengine.Build{}, err
			}
			return build, nil
		}
	}
	if w.LatestBuild.Job.Status != "succeeded" && w.LatestBuild.Job.Status != "failed" && w.LatestBuild.Job.Status != "canceled" {
		build, err := e.waitBuild(ctx, p, w, w.LatestBuild.ID)
		if err != nil {
			return build, err
		}
		w.LatestBuild = build
	}
	attempt, fresh, err := e.store.BeginBuild(ctx, lease, w.LatestBuild.ID, w.LatestBuild.BuildNumber, transition, phase)
	if err != nil {
		return workspaceengine.Build{}, err
	}
	build := w.LatestBuild
	switch buildDisposition(attempt, build, fresh) {
	case "ISSUE":
		checked, err := e.checkpoint(ctx, lease, "BUILDING", cleanup)
		if err != nil {
			return workspaceengine.Build{}, err
		}
		checked.Destroy()
		build, err = e.workspaces.CreateBuild(ctx, w.ID, transition, p.Snapshot.TemplateVersion, nil)
		if errors.Is(err, workspaceengine.ErrConflict) {
			latest, _, readErr := e.lookup(ctx, p, true)
			if readErr != nil {
				return workspaceengine.Build{}, readErr
			}
			build = latest.LatestBuild
			if buildDisposition(attempt, build, false) != "ADOPT" {
				return workspaceengine.Build{}, ErrExternalPending
			}
		} else if err != nil {
			return workspaceengine.Build{}, err
		}
	case "UNCERTAIN":
		return workspaceengine.Build{}, ErrExternalPending
	case "CONFLICT":
		return workspaceengine.Build{}, ErrHostIdentity
	}
	if err = e.verifyBuild(p, w, build); err != nil {
		return workspaceengine.Build{}, err
	}
	if buildDisposition(attempt, build, false) != "ADOPT" {
		return workspaceengine.Build{}, ErrHostIdentity
	}
	if err = e.remember(ctx, lease, w.ID, build.ID, cleanup); err != nil {
		return workspaceengine.Build{}, err
	}
	settled, waitErr := e.waitBuild(ctx, p, w, build.ID)
	if waitErr != nil && !errors.Is(waitErr, ErrBuildRejected) {
		return workspaceengine.Build{}, waitErr
	}
	if err = e.store.ConfirmBuild(ctx, lease, build.ID); err != nil {
		return workspaceengine.Build{}, err
	}
	return settled, waitErr
}
func (e *Executor) apply(ctx context.Context, lease Lease, p ExecutionPlan) error {
	if p.Action == "CREATE" || p.Action == "START" || p.Action == "CONFIGURE" {
		if err := e.verifyOwner(ctx, p); err != nil {
			return err
		}
	}
	if p.Desired == "RUNNING" {
		checked, err := e.checkpoint(ctx, lease, "PREPARING_STORAGE", false)
		if err != nil {
			return err
		}
		defer checked.Destroy()
		if _, err = e.host.Prepare(ctx, checked); err != nil {
			return err
		}
	}
	w, deleted, err := e.lookup(ctx, p, false)
	if errors.Is(err, workspaceengine.ErrNotFound) && p.Action == "CREATE" && p.WorkspaceID == nil {
		checked, err := e.checkpoint(ctx, lease, "CONFIGURING", false)
		if err != nil {
			return err
		}
		err = e.host.Configure(ctx, checked)
		checked.Destroy()
		if err != nil {
			return err
		}
		checked, err = e.checkpoint(ctx, lease, "CREATING_WORKSPACE", false)
		if err != nil {
			return err
		}
		checked.Destroy()
		w, err = e.workspaces.CreateWorkspace(ctx, p.WorkspaceOwner, workspaceengine.CreateWorkspaceRequest{Name: p.WorkspaceName, TemplateVersionID: p.Snapshot.TemplateVersion, RichParameterValues: []workspaceengine.Parameter{{Name: "instance_id", Value: p.InstanceID}, {Name: "asset_id", Value: p.AssetID}}})
		if errors.Is(err, workspaceengine.ErrConflict) {
			w, deleted, err = e.lookup(ctx, p, false)
		}
		if err != nil {
			return err
		}
		if deleted {
			return ErrHostIdentity
		}
		if err = e.verifyWorkspace(ctx, p, w, false); err != nil {
			return err
		}
	} else if errors.Is(err, workspaceengine.ErrNotFound) && (p.Action == "STOP" || p.Action == "DELETE" || p.Action == "CONFIGURE" && p.Desired == "STOPPED") {
		completion, err := e.host.ConfirmAbsent(ctx, p, p.Action == "DELETE")
		if err != nil {
			return err
		}
		if p.Action == "CONFIGURE" {
			checked, err := e.checkpoint(ctx, lease, "CONFIGURING", false)
			if err != nil {
				return err
			}
			err = e.host.Configure(ctx, checked)
			checked.Destroy()
			if err != nil {
				return err
			}
		}
		return e.store.Complete(ctx, lease, completion)
	} else if err != nil {
		return err
	}
	if deleted {
		if p.Action != "STOP" && p.Action != "DELETE" {
			return ErrHostIdentity
		}
		completion, err := e.host.ConfirmAbsent(ctx, p, true)
		if err != nil {
			return err
		}
		return e.store.Complete(ctx, lease, completion)
	}
	if p.WorkspaceID == nil {
		if err = e.store.RememberExternal(ctx, lease, w.ID, w.LatestBuild.ID); err != nil {
			return err
		}
		p.WorkspaceID = &w.ID
	}
	if p.Action == "STOP" || p.Action == "DELETE" {
		transition, stage, phase := "stop", "STOPPING", "STOP"
		if p.Action == "DELETE" {
			transition, stage, phase = "delete", "DELETING", "DELETE"
		}
		checked, err := e.checkpoint(ctx, lease, stage, false)
		if err != nil {
			return err
		}
		checked.Destroy()
		if _, err = e.ensureBuild(ctx, lease, p, w, transition, phase, false); err != nil {
			return err
		}
		completion, err := e.host.ConfirmAbsent(ctx, p, p.Action == "DELETE")
		if err != nil {
			return err
		}
		return e.store.Complete(ctx, lease, completion)
	}
	if p.Action == "CONFIGURE" && p.Desired == "STOPPED" {
		completion, err := e.host.ConfirmAbsent(ctx, p, false)
		if err != nil {
			return err
		}
		checked, err := e.checkpoint(ctx, lease, "CONFIGURING", false)
		if err != nil {
			return err
		}
		err = e.host.Configure(ctx, checked)
		checked.Destroy()
		if err != nil {
			return err
		}
		return e.store.Complete(ctx, lease, completion)
	}
	if p.Action == "CONFIGURE" && (p.External == nil || p.External.Phase == "CONFIGURE_STOP") {
		stop, err := e.ensureBuild(ctx, lease, p, w, "stop", "CONFIGURE_STOP", false)
		if err != nil {
			return err
		}
		if _, err = e.host.ConfirmAbsent(ctx, p, false); err != nil {
			return err
		}
		w.LatestBuild = stop
	}
	checked, err := e.checkpoint(ctx, lease, "CONFIGURING", false)
	if err != nil {
		return err
	}
	err = e.host.Configure(ctx, checked)
	checked.Destroy()
	if err != nil {
		return err
	}
	var build workspaceengine.Build
	if p.Action == "CREATE" && w.LatestBuild.BuildNumber == 1 && w.LatestBuild.Transition == "start" && p.External == nil {
		if err = e.store.RememberExternal(ctx, lease, w.ID, w.LatestBuild.ID); err != nil {
			return err
		}
		build, err = e.waitBuild(ctx, p, w, w.LatestBuild.ID)
	} else {
		phase := "START"
		if p.Action == "CONFIGURE" {
			phase = "CONFIGURE_START"
		} else if p.Action == "CREATE" {
			phase = "CREATE_START"
		}
		build, err = e.ensureBuild(ctx, lease, p, w, "start", phase, false)
	}
	if err != nil {
		return err
	}
	checked, err = e.checkpoint(ctx, lease, "CHECKING", false)
	if err != nil {
		return err
	}
	defer checked.Destroy()
	completion, err := e.host.Start(ctx, checked, build.ID)
	if err != nil {
		return err
	}
	defer clear(completion.Running.NativeCookie)
	return e.store.Complete(ctx, lease, completion)
}
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

func (e *Executor) verifyOwner(ctx context.Context, p ExecutionPlan) error {
	user, err := e.workspaces.User(ctx, p.WorkspaceOwner)
	if err != nil {
		return err
	}
	if user.ID != p.WorkspaceOwner || !workspaceengine.OwnerStatusUsable(user.Status) || len(user.OrganizationIDs) != 1 || user.OrganizationIDs[0] != e.organization {
		return ErrExecutionRevoked
	}
	for _, role := range user.Roles {
		if role.Name != "member" {
			return ErrExecutionRevoked
		}
	}
	return nil
}
