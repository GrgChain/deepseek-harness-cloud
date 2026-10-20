package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

type ProvisionTask struct {
	Workspace     Workspace
	Build         Build
	Parameters    []Parameter
	Source        map[string][]byte
	PreviousState []byte
}
type Provisioner interface {
	Apply(context.Context, ProvisionTask) ([]byte, error)
}

// RunBuilds runs in the platform Worker. A durable pending/running record is
// recoverable after a process restart. PostgreSQL and host locks serialize each
// workspace; Terraform also keeps its native state lock.
func (c *Client) RunBuilds(ctx context.Context, runner Provisioner) error {
	if c.pool == nil || runner == nil {
		return ErrUnavailable
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := c.RunPendingBuilds(ctx, runner); err != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
func (c *Client) RunPendingBuilds(ctx context.Context, runner Provisioner) error {
	if c.pool == nil || runner == nil {
		return ErrUnavailable
	}
	rows, err := c.pool.Query(ctx, `SELECT id::text FROM workspace_builds WHERE job_status IN ('pending','running') ORDER BY created_at LIMIT 16`)
	if err != nil {
		return ErrUnavailable
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err != nil || rows.Err() != nil {
		return ErrUnavailable
	}
	for _, id := range ids {
		if err = c.runBuild(ctx, id, runner); err != nil {
			return err
		}
	}
	return nil
}
func (c *Client) runBuild(ctx context.Context, id string, runner Provisioner) error {
	b, err := c.Build(ctx, id)
	if err != nil {
		return err
	}
	conn, err := c.pool.Acquire(ctx)
	if err != nil {
		return ErrUnavailable
	}
	defer conn.Release()
	var locked bool
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(731804530,hashtext($1))`, b.WorkspaceID).Scan(&locked); err != nil {
		return ErrUnavailable
	}
	if !locked {
		return nil
	}
	defer func() {
		release, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, err := conn.Exec(release, `SELECT pg_advisory_unlock(731804530,hashtext($1))`, b.WorkspaceID); err != nil {
			_ = conn.Conn().Close(release)
		}
	}()
	b, err = readBuild(ctx, conn, `WHERE b.id=$1`, id)
	if err != nil {
		return err
	}
	if b.Job.Status != "pending" && b.Job.Status != "running" {
		return nil
	}
	w, err := readWorkspace(ctx, conn, `WHERE id=$1`, b.WorkspaceID)
	if err != nil || w.LatestBuild.ID != b.ID {
		return ErrConflict
	}
	sourceVersion, err := c.TemplateVersion(ctx, b.TemplateVersionID)
	if err != nil {
		return err
	}
	source, err := c.TemplateSource(ctx, sourceVersion.Job.FileID)
	if err != nil {
		return err
	}
	var previous, raw []byte
	var parameters []Parameter
	if err = conn.QueryRow(ctx, `SELECT parameters,coalesce(provisioner_state,(SELECT provisioner_state FROM workspace_builds WHERE workspace_id=$2 AND build_number<$3 AND provisioner_state IS NOT NULL ORDER BY build_number DESC LIMIT 1),''::bytea) FROM workspace_builds WHERE id=$1`, b.ID, b.WorkspaceID, b.BuildNumber).Scan(&raw, &previous); err != nil || json.Unmarshal(raw, &parameters) != nil {
		return ErrUnavailable
	}
	if !validParameters(parameters) {
		return ErrConflict
	}
	if _, err = conn.Exec(ctx, `UPDATE workspace_builds SET job_status='running',started_at=coalesce(started_at,clock_timestamp()) WHERE id=$1 AND job_status IN ('pending','running')`, b.ID); err != nil {
		return ErrUnavailable
	}
	state, applyErr := runner.Apply(ctx, ProvisionTask{Workspace: w, Build: b, Parameters: parameters, Source: source, PreviousState: previous})
	// Cancellation never publishes a false failure. A later Worker reconciles the
	// same build and partially applied state, without allocating another build ID.
	if ctx.Err() != nil || errors.Is(applyErr, context.Canceled) {
		if len(state) > 0 && len(state) <= 16<<20 && json.Valid(state) {
			save, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, _ = conn.Exec(save, `UPDATE workspace_builds SET provisioner_state=$2 WHERE id=$1 AND job_status='running'`, b.ID, state)
		}
		return ctx.Err()
	}
	status, code := "succeeded", ""
	if applyErr != nil {
		status, code = "failed", "TERRAFORM_APPLY_FAILED"
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return ErrUnavailable
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `UPDATE workspace_builds SET job_status=$2,error_code=$3,provisioner_state=$4,completed_at=clock_timestamp() WHERE id=$1 AND job_status='running'`, b.ID, status, code, state); err != nil {
		return ErrUnavailable
	}
	if b.Transition == "delete" && applyErr == nil {
		if _, err = tx.Exec(ctx, `UPDATE workspace_workspaces SET deleted=true WHERE id=$1`, b.WorkspaceID); err != nil {
			return ErrUnavailable
		}
	}
	return storeError(tx.Commit(ctx))
}
