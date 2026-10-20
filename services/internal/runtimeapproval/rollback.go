package runtimeapproval

import (
	"context"
	"encoding/json"
	"reflect"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

var rollbackReason = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// Rollback selects an already approved compatible version for future creates.
// It never downgrades a schema, edits frozen instances or resurrects secrets.
// The caller verifies retained images/Workspace templates and coordinates process maintenance.
func Rollback(ctx context.Context, pool *pgxpool.Pool, org, template, operator, previous string, target Manifest, reason string, apply bool) error {
	if !uuidPattern.MatchString(org) || !uuidPattern.MatchString(template) || !uuidPattern.MatchString(previous) || !operatorPattern.MatchString(operator) || !target.Valid() || previous == target.TemplateVersion || !rollbackReason.MatchString(reason) {
		return ErrManifest
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SET LOCAL lock_timeout='1s'; SET LOCAL statement_timeout='5s'; SET LOCAL idle_in_transaction_session_timeout='5s'; SELECT pg_advisory_xact_lock(731804524); LOCK TABLE approved_templates, organization_defaults, instances, operations, quota_reservations, storage_assets IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return err
	}
	var active bool
	if err = tx.QueryRow(ctx, `SELECT status='ACTIVE' FROM organizations WHERE id=$1 FOR UPDATE`, org).Scan(&active); err != nil || !active {
		return ErrConflict
	}
	var sourceRaw, targetRaw []byte
	var sourceImage, targetImage string
	if err = tx.QueryRow(ctx, `SELECT runtime_manifest,image_digest FROM approved_templates WHERE id=$1 AND version=$2`, template, previous).Scan(&sourceRaw, &sourceImage); err != nil {
		return ErrConflict
	}
	if err = tx.QueryRow(ctx, `SELECT runtime_manifest,image_digest FROM approved_templates WHERE id=$1 AND version=$2`, template, target.TemplateVersion).Scan(&targetRaw, &targetImage); err != nil {
		return ErrConflict
	}
	source, err := Parse(sourceRaw)
	if err != nil || !source.Matches(sourceImage, previous) {
		return ErrManifest
	}
	retained, err := Parse(targetRaw)
	if err != nil || !retained.Matches(targetImage, target.TemplateVersion) || !reflect.DeepEqual(retained, target) {
		return ErrManifest
	}
	if !compatibleRuntime(source, target) {
		return ErrManifest
	}
	var enabledID, enabledVersion string
	if err = tx.QueryRow(ctx, `SELECT id::text,version::text FROM approved_templates WHERE enabled`).Scan(&enabledID, &enabledVersion); err != nil || enabledID != template {
		return ErrConflict
	}
	if enabledVersion == target.TemplateVersion {
		var replay bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organization_defaults WHERE org_id=$1 AND template_id=$2 AND template_version=$3) AND EXISTS(SELECT 1 FROM audit_events WHERE org_id=$1 AND resource_id=$2 AND action='RUNTIME_ROLLBACK' AND outcome='SUCCEEDED' AND details @> $4::jsonb)`, org, template, target.TemplateVersion, rollbackDetails(operator, previous, target, reason)).Scan(&replay); err != nil || !replay {
			return ErrConflict
		}
		return nil
	}
	if enabledVersion != previous {
		return ErrConflict
	}
	var safe bool
	if err = tx.QueryRow(ctx, `SELECT
 NOT EXISTS(SELECT 1 FROM instances WHERE desired_state<>observed_state OR observed_state NOT IN ('STOPPED','DELETED'))
 AND NOT EXISTS(SELECT 1 FROM operations WHERE status IN ('ACCEPTED','RUNNING','UNCERTAIN'))
 AND NOT EXISTS(SELECT 1 FROM quota_reservations WHERE status IN ('HELD','USED') AND resource_type IN ('RUNNING','CPU','MEMORY','CONCURRENT'))
 AND NOT EXISTS(SELECT 1 FROM storage_assets WHERE status IN ('PREPARING','RESTORING','RECLAIM_PENDING'))
 AND EXISTS(SELECT 1 FROM organization_defaults WHERE org_id=$1 AND template_id=$2 AND template_version=$3)`, org, template, previous).Scan(&safe); err != nil || !safe {
		return ErrConflict
	}
	if !apply {
		return nil
	}
	if _, err = tx.Exec(ctx, `UPDATE approved_templates SET enabled=false WHERE id=$1 AND version=$2`, template, previous); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE approved_templates SET enabled=true WHERE id=$1 AND version=$2`, template, target.TemplateVersion); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE organization_defaults SET version=version+1,template_version=$3 WHERE org_id=$1 AND template_id=$2`, org, template, target.TemplateVersion); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(id,org_id,resource_id,action,outcome,trace_id,details) VALUES($1::uuid,$2,$3,'RUNTIME_ROLLBACK','SUCCEEDED',$1::text,$4)`, webapi.ID(), org, template, rollbackDetails(operator, previous, target, reason)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func rollbackDetails(operator, previous string, target Manifest, reason string) []byte {
	raw, _ := json.Marshal(map[string]any{"schema_version": 1, "operator_ref": operator, "previous_template_version": previous, "template_version": target.TemplateVersion, "image_digest": target.Image, "reason_code": reason})
	return raw
}
