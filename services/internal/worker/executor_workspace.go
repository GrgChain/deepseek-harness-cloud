package worker

import (
	"context"
	"errors"
	"time"

	workspaceengine "github.com/jonluo94/deepseek-harness-cloud/services/internal/workspace"
)

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
