package worker

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/instances"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/settings"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/skills"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/workspaceextensions"
)

// ExecutionPlan belongs exclusively to the trusted Worker. Its secret is not
// serializable, and fmt logging of the plan is redacted. Destroy it after use.
type ExecutionPlan struct {
	InstanceID, OrganizationID, OwnerID, WorkspaceOwner, WorkspaceName, Action, Desired string
	Observed                                                                            string
	WorkspaceID, BuildID, LastBuildID                                                   *string
	AssetID, AssetStatus, ResourceName, FilesystemID                                    string
	Capacity, Version                                                                   int64
	Snapshot                                                                            instances.Snapshot
	RuntimeGeneration                                                                   string
	Endpoint                                                                            string
	FailureCode                                                                         string
	External                                                                            *BuildAttempt
	SyncStoppedModel                                                                    bool
	modelManifest                                                                       *settings.ModelManifest
	modelKey                                                                            []byte
	skills                                                                              []skills.WorkspaceSkill
	extensions                                                                          workspaceextensions.Snapshot
}

func (p ExecutionPlan) String() string   { return "[private instance execution plan]" }
func (p ExecutionPlan) GoString() string { return p.String() }
func (p ExecutionPlan) MarshalJSON() ([]byte, error) {
	return nil, errors.New("execution plans cannot be serialized")
}
func (p *ExecutionPlan) ModelKey() []byte { return p.modelKey }
func (p *ExecutionPlan) Destroy() {
	clear(p.modelKey)
	p.modelKey = nil
	skills.DestroyWorkspaceSkills(p.skills)
	p.skills = nil
}

// Checkpoint rereads authority and intent before every external mutation. An
// accepted command survives Portal logout, but never disabled ownership or a
// revoked credential. Safety STOP/DELETE do not need to decrypt model secrets.
func (s *ExecutionStore) Checkpoint(ctx context.Context, lease Lease) (ExecutionPlan, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ExecutionPlan{}, err
	}
	defer tx.Rollback(context.Background())
	r, err := s.lockExecution(ctx, tx, lease, false)
	if err != nil {
		return ExecutionPlan{}, err
	}
	plan := ExecutionPlan{InstanceID: lease.InstanceID, OrganizationID: r.Org, OwnerID: r.Owner, Action: r.Action, Desired: r.Intent.Desired, Version: r.Version, WorkspaceID: r.WorkspaceID, Snapshot: r.Snapshot, RuntimeGeneration: lease.ID, Capacity: r.AssetCapacity, AssetStatus: r.AssetStatus, External: r.Intent.External}
	if r.AssetID != nil {
		plan.AssetID = *r.AssetID
		err = tx.QueryRow(ctx, `SELECT resource_name,COALESCE(filesystem_id,'') FROM storage_assets WHERE id=$1`, *r.AssetID).Scan(&plan.ResourceName, &plan.FilesystemID)
		if err != nil {
			return ExecutionPlan{}, err
		}
	}
	if len(r.Intent.Target) > 0 {
		plan.Snapshot, err = instances.DecodeSnapshot(r.Intent.Target)
		if err != nil {
			return ExecutionPlan{}, err
		}
	}
	if err = tx.QueryRow(ctx, `SELECT workspace_name FROM instances WHERE id=$1`, lease.InstanceID).Scan(&plan.WorkspaceName); err != nil {
		return ExecutionPlan{}, err
	}
	if err = tx.QueryRow(ctx, `SELECT workspace_owner_id::text FROM users WHERE id=$1`, r.Owner).Scan(&plan.WorkspaceOwner); err != nil {
		return ExecutionPlan{}, err
	}
	if err = tx.QueryRow(ctx, `SELECT build_id::text,COALESCE(error_code,'') FROM operations WHERE id=$1`, lease.ID).Scan(&plan.BuildID, &plan.FailureCode); err != nil {
		return ExecutionPlan{}, err
	}
	if err = tx.QueryRow(ctx, `SELECT (SELECT build_id::text FROM operations WHERE instance_id=$1 AND id<>$2 AND status IN ('SUCCEEDED','FAILED') AND build_id IS NOT NULL ORDER BY updated_at DESC,id DESC LIMIT 1)`, lease.InstanceID, lease.ID).Scan(&plan.LastBuildID); err != nil {
		return ExecutionPlan{}, err
	}
	if r.Action == "START" {
		if plan.SyncStoppedModel, err = needsStoppedModelSync(ctx, tx, lease.InstanceID, lease.ID); err != nil {
			return ExecutionPlan{}, err
		}
	}
	if r.Action == "CREATE" || r.Action == "START" || r.Action == "CONFIGURE" {
		if !r.Authorized || r.ActorKind != "USER" || r.ActorID == nil || *r.ActorID != r.Owner || s.cipher == nil {
			return ExecutionPlan{}, ErrExecutionRevoked
		}
		var credential string
		var envelope security.Envelope
		var manifest []byte
		err = tx.QueryRow(ctx, `SELECT c.endpoint,sec.id::text,sec.key_id,sec.nonce,sec.ciphertext,c.manifest FROM model_configurations c JOIN secrets sec ON sec.org_id=c.org_id AND sec.id=c.credential_ref JOIN model_validations v ON v.config_id=c.id AND v.config_version=c.version WHERE c.org_id=$1 AND c.id=$2 AND c.version=$3 AND c.status='VALID' AND c.revoked_at IS NULL AND sec.revoked_at IS NULL AND c.provider=$4 AND c.protocol=$5 AND c.model=$6 AND public.hc_model_available(c.org_id,$7,c.id,c.version) AND v.status='HEALTHY' AND v.checked_at<=clock_timestamp() AND v.expires_at>clock_timestamp()`, r.Org, plan.Snapshot.ModelID, plan.Snapshot.ModelVersion, plan.Snapshot.Provider, plan.Snapshot.Protocol, plan.Snapshot.Model, plan.OwnerID).Scan(&plan.Endpoint, &credential, &envelope.KeyID, &envelope.Nonce, &envelope.Ciphertext, &manifest)
		if errors.Is(err, pgx.ErrNoRows) {
			return ExecutionPlan{}, ErrExecutionRevoked
		}
		if err != nil {
			return ExecutionPlan{}, err
		}
		if plan.Snapshot.Provider == "platform-models" {
			plan.modelManifest = &settings.ModelManifest{}
			if webapi.StrictUnmarshal(manifest, plan.modelManifest) != nil {
				return ExecutionPlan{}, ErrExecutionRevoked
			}
		}
		plan.modelKey, err = s.cipher.Decrypt(envelope, security.Context{Environment: s.environment, OrganizationID: r.Org, RecordID: credential, Purpose: "model-key"})
		if err != nil {
			return ExecutionPlan{}, err
		}
	}
	if r.Action == "CREATE" {
		plan.extensions, err = workspaceextensions.Load(ctx, tx, r.Org, r.Owner, lease.InstanceID)
		if err != nil {
			plan.Destroy()
			return ExecutionPlan{}, err
		}
		if len(plan.extensions.Items) > 0 {
			var authorized bool
			err = tx.QueryRow(ctx, `SELECT e.auth_generation=m.auth_generation AND public.hc_has_permission(e.org_id,e.owner_id,'instances:configure') FROM workspace_extensions e JOIN memberships m ON m.org_id=e.org_id AND m.user_id=e.owner_id WHERE e.instance_id=$1`, lease.InstanceID).Scan(&authorized)
			if err != nil || !authorized {
				plan.Destroy()
				if err != nil {
					return ExecutionPlan{}, err
				}
				return ExecutionPlan{}, ErrExecutionRevoked
			}
		}
		plan.skills, err = skills.LoadWorkspaceSkills(ctx, tx, s.cipher, s.environment, r.Org, r.Owner, lease.InstanceID)
		if err != nil {
			plan.Destroy()
			return ExecutionPlan{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		plan.Destroy()
		return ExecutionPlan{}, err
	}
	return plan, nil
}

func needsStoppedModelSync(ctx context.Context, tx pgx.Tx, instanceID, operationID string) (bool, error) {
	var needed bool
	err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT type='CONFIGURE' AND intent->>'target_desired_state'='STOPPED' FROM operations WHERE instance_id=$1 AND id<>$2 AND status='SUCCEEDED' AND type IN ('CREATE','START','CONFIGURE') ORDER BY updated_at DESC,id DESC LIMIT 1),false)`, instanceID, operationID).Scan(&needed)
	return needed, err
}

// RememberExternal records only stable, verified external IDs. Callers first
// verify embedded workspace ownership, immutable parameters, template and build.
func (s *ExecutionStore) RememberExternal(ctx context.Context, lease Lease, workspace, build string) error {
	return s.rememberExternal(ctx, lease, workspace, build, false)
}
func (s *ExecutionStore) rememberExternal(ctx context.Context, lease Lease, workspace, build string, allowSuperseded bool) error {
	if !uuidPattern.MatchString(workspace) || !uuidPattern.MatchString(build) {
		return ErrInvalidEvidence
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	r, err := s.lockExecution(ctx, tx, lease, allowSuperseded)
	if err != nil {
		return err
	}
	if r.WorkspaceID != nil && *r.WorkspaceID != workspace {
		return ErrInvalidEvidence
	}
	if _, err = tx.Exec(ctx, `UPDATE instances SET workspace_id=$2 WHERE id=$1`, lease.InstanceID, workspace); err != nil {
		return err
	}
	if r.Intent.External != nil {
		r.Intent.External.BuildID = &build
		if err = writeBuildIntent(ctx, tx, lease, r.Intent); err != nil {
			return err
		}
	}
	tag, err := tx.Exec(ctx, `UPDATE operations SET build_id=$4,updated_at=clock_timestamp() WHERE id=$1 AND lease_owner=$2 AND lease_token=$3 AND lease_expires_at>clock_timestamp()`, lease.ID, lease.Owner, lease.Token, build)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrLostLease
	}
	return tx.Commit(ctx)
}

// Keep diagnostic formatting from accidentally exposing private plan fields.
var _ fmt.Stringer = ExecutionPlan{}
