package worker

import (
	"context"
	"errors"

	workspaceengine "github.com/jonluo94/deepseek-harness-cloud/services/internal/workspace"
)

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
