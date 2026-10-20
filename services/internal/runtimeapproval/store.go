package runtimeapproval

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

var ErrConflict = errors.New("runtime approval conflicts with existing configuration or retained assets")
var operatorPattern = regexp.MustCompile(`^[A-Za-z0-9_.:@/-]{1,128}$`)

// Approve is an operations-only transaction. It neither initializes model
// defaults nor changes an existing frozen contract. Exact replay is a no-op.
func Approve(ctx context.Context, pool *pgxpool.Pool, org, template, operator string, m Manifest, slots int) error {
	if !uuidPattern.MatchString(org) || !uuidPattern.MatchString(template) || !operatorPattern.MatchString(operator) || !m.Valid() || slots < 1 || slots > 5 {
		return ErrManifest
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(731804524)`); err != nil {
		return err
	}
	var active bool
	if err = tx.QueryRow(ctx, `SELECT status='ACTIVE' FROM organizations WHERE id=$1 FOR UPDATE`, org).Scan(&active); err != nil || !active {
		return ErrConflict
	}
	var same bool
	err = tx.QueryRow(ctx, `SELECT enabled AND image_digest=$3 AND runtime_manifest=$4::jsonb FROM approved_templates WHERE id=$1 AND version=$2 FOR UPDATE`, template, m.TemplateVersion, m.Image, raw).Scan(&same)
	if err == nil {
		if !same {
			return ErrConflict
		}
		var complete bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM approved_specs WHERE enabled AND cpu_millicores=2000 AND memory_bytes=4294967296 AND storage_bytes=10737418240) AND EXISTS(SELECT 1 FROM quota_policies WHERE org_id=$1)`, org).Scan(&complete); err != nil {
			return err
		}
		if !complete {
			return ErrConflict
		}
		return tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	var conflict bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM approved_templates WHERE enabled) OR EXISTS(SELECT 1 FROM approved_specs WHERE enabled) OR EXISTS(SELECT 1 FROM organization_defaults) OR EXISTS(SELECT 1 FROM instances WHERE deleted_at IS NULL) OR EXISTS(SELECT 1 FROM storage_assets WHERE status<>'DELETED')`).Scan(&conflict); err != nil {
		return err
	}
	if conflict {
		return ErrConflict
	}
	if _, err = tx.Exec(ctx, `INSERT INTO approved_templates(id,version,name,image_digest,runtime_manifest,enabled) VALUES($1,$2,'hc-dsh-initial',$3,$4,true)`, template, m.TemplateVersion, m.Image, raw); err != nil {
		return err
	}
	if err = approveResourceSpecs(ctx, tx, m); err != nil {
		return err
	}
	// Host capacity bounds build concurrency, while saved resource quotas remain
	// independent of the host's available memory at approval time.
	if _, err = tx.Exec(ctx, `INSERT INTO quota_policies(org_id,max_concurrent_creates) VALUES($1,LEAST(2,$2))
 ON CONFLICT(org_id) DO UPDATE SET version=quota_policies.version+1,max_concurrent_creates=LEAST(quota_policies.max_concurrent_creates,EXCLUDED.max_concurrent_creates)`, org, slots); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(id,org_id,resource_id,action,outcome,trace_id,details) VALUES($1,$2,$3,'RUNTIME_APPROVE','SUCCEEDED',$4,jsonb_build_object('schema_version',1,'operator_ref',$5::text,'reason_code','PINNED_RUNTIME_VERIFIED','compute_capacity_units',$6::int))`, webapi.ID(), org, template, webapi.ID(), operator, slots); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Only the controlled runtime approval transaction can expand the immutable
// catalog. An API identity cannot turn arbitrary numbers into approved specs.
func approveResourceSpecs(ctx context.Context, tx pgx.Tx, m Manifest) error {
	minimum, maximum := int64(10), int64(10)
	if m.StorageAdapter == StorageAdapter {
		minimum, maximum = 1, MaxStorageGiB
	}
	_, err := tx.Exec(ctx, `INSERT INTO approved_specs(id,cpu_millicores,memory_bytes,storage_bytes,enabled)
 SELECT gen_random_uuid(),cpu,memory,gib*1073741824,true
 FROM (VALUES(1000::bigint),(2000::bigint),(4000::bigint),(8000::bigint),(16000::bigint)) c(cpu)
 CROSS JOIN (VALUES(2147483648::bigint),(4294967296::bigint),(8589934592::bigint),(17179869184::bigint),(34359738368::bigint),(68719476736::bigint)) m(memory)
 CROSS JOIN generate_series($1::bigint,$2::bigint) sizes(gib)
 WHERE NOT EXISTS(SELECT 1 FROM approved_specs s WHERE s.enabled AND s.cpu_millicores=cpu AND s.memory_bytes=memory AND s.storage_bytes=gib*1073741824)`, minimum, maximum)
	return err
}
