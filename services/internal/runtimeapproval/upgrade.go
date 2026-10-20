package runtimeapproval

import (
	"context"
	"encoding/json"
	"reflect"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

// Upgrade only changes the approved version for future instances. Existing
// instance snapshots and historical manifests are never rewritten. Operators
// coordinate API/Worker maintenance before invoking this short transaction.
// It supports a runtime/profile change with identical storage/native formats;
// migrations and incompatible runtime upgrades require a separate procedure.
func Upgrade(ctx context.Context, pool *pgxpool.Pool, org, template, operator, previous string, next Manifest, apply bool) error {
	if !uuidPattern.MatchString(org) || !uuidPattern.MatchString(template) || !uuidPattern.MatchString(previous) || !operatorPattern.MatchString(operator) || !next.Valid() || previous == next.TemplateVersion {
		return ErrManifest
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SET LOCAL lock_timeout='1s'; SET LOCAL statement_timeout='5s'; SELECT pg_advisory_xact_lock(731804524); LOCK TABLE approved_templates, organization_defaults, instances, operations, quota_reservations, storage_assets IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return err
	}
	var active bool
	if err = tx.QueryRow(ctx, `SELECT status='ACTIVE' FROM organizations WHERE id=$1 FOR UPDATE`, org).Scan(&active); err != nil || !active {
		return ErrConflict
	}
	var raw []byte
	if err = tx.QueryRow(ctx, `SELECT runtime_manifest FROM approved_templates WHERE id=$1 AND version=$2`, template, previous).Scan(&raw); err != nil {
		return ErrConflict
	}
	old, err := Parse(raw)
	if err != nil {
		return ErrManifest
	}
	if !compatibleRuntime(old, next) {
		return ErrManifest
	}
	encoded, err := json.Marshal(next)
	if err != nil {
		return err
	}
	var current, version string
	if err = tx.QueryRow(ctx, `SELECT id::text,version::text FROM approved_templates WHERE enabled`).Scan(&current, &version); err != nil || current != template {
		return ErrConflict
	}
	if version == next.TemplateVersion {
		var same bool
		if err = tx.QueryRow(ctx, `SELECT runtime_manifest=$3::jsonb AND image_digest=$4 AND EXISTS(SELECT 1 FROM organization_defaults WHERE org_id=$1 AND template_id=$2 AND template_version=$5) FROM approved_templates WHERE id=$2 AND version=$5`, org, template, encoded, next.Image, next.TemplateVersion).Scan(&same); err != nil || !same {
			return ErrConflict
		}
		return nil
	}
	if version != previous {
		return ErrConflict
	}
	var safe bool
	if err = tx.QueryRow(ctx, `SELECT
 NOT EXISTS(SELECT 1 FROM instances WHERE desired_state NOT IN ('STOPPED','DELETED') OR observed_state NOT IN ('STOPPED','DELETED'))
 AND NOT EXISTS(SELECT 1 FROM operations WHERE status IN ('ACCEPTED','RUNNING','UNCERTAIN'))
 AND NOT EXISTS(SELECT 1 FROM quota_reservations WHERE status IN ('HELD','USED') AND resource_type IN ('RUNNING','CPU','MEMORY','CONCURRENT'))
 AND NOT EXISTS(SELECT 1 FROM storage_assets WHERE status IN ('PREPARING','RESTORING','RECLAIM_PENDING'))
 AND EXISTS(SELECT 1 FROM organization_defaults WHERE org_id=$1 AND template_id=$2 AND template_version=$3)
 AND NOT EXISTS(SELECT 1 FROM approved_templates WHERE id=$2 AND version=$4)`, org, template, previous, next.TemplateVersion).Scan(&safe); err != nil || !safe {
		return ErrConflict
	}
	if !apply {
		return nil
	}
	if _, err = tx.Exec(ctx, `UPDATE approved_templates SET enabled=false WHERE id=$1 AND version=$2`, template, previous); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO approved_templates(id,version,name,image_digest,runtime_manifest,enabled) VALUES($1,$2,'hc-dsh-initial',$3,$4,true)`, template, next.TemplateVersion, next.Image, encoded); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE organization_defaults SET version=version+1,template_version=$3 WHERE org_id=$1 AND template_id=$2`, org, template, next.TemplateVersion); err != nil {
		return err
	}
	if err = approveResourceSpecs(ctx, tx, next); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(id,org_id,resource_id,action,outcome,trace_id,details) VALUES($1::uuid,$2,$3,'RUNTIME_UPGRADE','SUCCEEDED',$1::text,jsonb_build_object('schema_version',1,'operator_ref',$4::text,'previous_template_version',$5::text,'template_version',$6::text,'image_digest',$7::text,'reason_code','COMPATIBLE_RUNTIME_UPGRADE'))`, webapi.ID(), org, template, operator, previous, next.TemplateVersion, next.Image); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// The additive Messages adapter preserves native auth and the disk/config
// format. Its reviewed network helper must pass both the legacy public-TLS
// fence and the gateway-only fence before the operator approves its manifest.
func compatibleRuntime(source, target Manifest) bool {
	compatible := source
	compatible.Image, compatible.TemplateVersion = target.Image, target.TemplateVersion
	compatible.ProfileDigest, compatible.ToolDigest, compatible.AdapterDigest = target.ProfileDigest, target.ToolDigest, target.AdapterDigest
	// The reviewed bounded adapter preserves existing ext4 assets and adds
	// integer-GiB allocation. Reverse migration cannot support those new assets.
	if source.StorageAdapter == "" && target.StorageAdapter == StorageAdapter {
		compatible.StorageAdapter, compatible.StorageImage = target.StorageAdapter, target.StorageImage
	}
	if (source.ExternalModels == "" && target.ExternalModels == ExternalModelAdapter) || (source.ExternalModels == ExternalModelAdapter && target.ExternalModels == "") {
		compatible.ExternalModels, compatible.NetworkImage = target.ExternalModels, target.NetworkImage
	}
	return reflect.DeepEqual(compatible, target)
}

// CompatibleUpgrade exposes the same reviewed format boundary to the Worker
// when an Owner explicitly applies a newly approved runtime to retained data.
func CompatibleUpgrade(source, target Manifest) bool {
	return source.Valid() && target.Valid() && compatibleRuntime(source, target)
}
