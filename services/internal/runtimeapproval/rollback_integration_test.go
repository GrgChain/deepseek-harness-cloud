package runtimeapproval

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

// Reuses the real upgrade fixture so both generations and the frozen old
// instance already exist. No source/product database is touched by this test.
func exerciseRollback(t *testing.T, ctx context.Context, pool *pgxpool.Pool, org, template, instance, op string, old, next Manifest, snapshot []byte) {
	t.Helper()
	must := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	newInstance, asset := webapi.ID(), webapi.ID()
	newSnapshot, _ := json.Marshal(map[string]any{"schema_version": 1, "template_version": next.TemplateVersion, "image_digest": next.Image, "sentinel": "immutable-new-instance"})
	must(`INSERT INTO instances(id,org_id,owner_id,name,external_create_key,workspace_name,desired_state,observed_state,model_config_id,model_config_version,config_snapshot) SELECT $1,org_id,owner_id,'rollback-new-instance',$1,'rollback-new-instance','STOPPED','STOPPED',model_config_id,model_config_version,$3 FROM instances WHERE id=$2`, newInstance, instance, newSnapshot)
	must(`INSERT INTO storage_assets(id,org_id,owner_id,instance_id,resource_name,capacity_bytes,init_version,status) SELECT $1::uuid,org_id,owner_id,id,'hc-'||$1::uuid::text,10737418240,1,'READY' FROM instances WHERE id=$2`, asset, newInstance)
	must(`UPDATE instances SET storage_asset_id=$2 WHERE id=$1`, newInstance, asset)
	check := func(want int, image string, audits int) {
		t.Helper()
		var version, count int
		var active string
		var frozen, newerFrozen, retained, revoked bool
		err := pool.QueryRow(ctx, `SELECT (SELECT version FROM organization_defaults WHERE org_id=$1),(SELECT image_digest FROM approved_templates WHERE enabled),(SELECT count(*) FROM audit_events WHERE action='RUNTIME_ROLLBACK'),(SELECT config_snapshot=$3::jsonb FROM instances WHERE id=$2),(SELECT count(*)=2 FROM approved_templates),(SELECT bool_and(revoked_at IS NOT NULL) FROM secrets),(SELECT config_snapshot=$5::jsonb AND storage_asset_id=$6 FROM instances WHERE id=$4)`, org, instance, snapshot, newInstance, newSnapshot, asset).Scan(&version, &active, &count, &frozen, &retained, &revoked, &newerFrozen)
		if err != nil || version != want || active != image || count != audits || !frozen || !newerFrozen || !retained || !revoked {
			t.Fatalf("rollback changed frozen data, revocation or partial defaults: %v", err)
		}
	}
	must(`UPDATE secrets SET revoked_at=clock_timestamp(); UPDATE model_configurations SET status='REVOKED'`)
	rollback := func(from string, target Manifest, reason string, apply bool) error {
		return Rollback(ctx, pool, org, template, "rollback-fixture", from, target, reason, apply)
	}
	if err := rollback(next.TemplateVersion, old, "COMPATIBILITY_REHEARSAL", false); err != nil {
		t.Fatal(err)
	}
	check(2, next.Image, 0)
	for _, state := range []string{"RUNNING", "ERROR", "DELETED"} {
		must(`UPDATE instances SET observed_state=$2,deleted_at=CASE WHEN $2='DELETED' THEN clock_timestamp() ELSE NULL END WHERE id=$1`, instance, state)
		if rollback(next.TemplateVersion, old, "COMPATIBILITY_REHEARSAL", true) == nil {
			t.Fatal("unsafe or mismatched state accepted", state)
		}
	}
	must(`UPDATE instances SET observed_state='STOPPED',deleted_at=NULL WHERE id=$1`, instance)
	must(`UPDATE operations SET status='UNCERTAIN' WHERE id=$1`, op)
	if rollback(next.TemplateVersion, old, "COMPATIBILITY_REHEARSAL", true) == nil {
		t.Fatal("uncertain operation accepted")
	}
	must(`UPDATE operations SET status='SUCCEEDED' WHERE id=$1`, op)
	for _, state := range []string{"PREPARING", "RESTORING", "RECLAIM_PENDING"} {
		must(`UPDATE storage_assets SET status=$2 WHERE id=$1`, asset, state)
		if rollback(next.TemplateVersion, old, "COMPATIBILITY_REHEARSAL", true) == nil {
			t.Fatal("active storage maintenance accepted", state)
		}
	}
	must(`UPDATE storage_assets SET status='READY' WHERE id=$1`, asset)
	for _, resource := range []string{"RUNNING", "CPU", "MEMORY", "CONCURRENT"} {
		id := webapi.ID()
		must(`INSERT INTO quota_reservations(id,org_id,owner_id,instance_id,operation_id,resource_type,amount,status) SELECT $1,org_id,owner_id,id,$3,$4,1,'HELD' FROM instances WHERE id=$2 ON CONFLICT(operation_id,resource_type) DO UPDATE SET status='HELD'`, id, instance, op, resource)
		if rollback(next.TemplateVersion, old, "COMPATIBILITY_REHEARSAL", true) == nil {
			t.Fatal("held compute accepted", resource)
		}
		must(`UPDATE quota_reservations SET status='RELEASED' WHERE operation_id=$1 AND resource_type=$2`, op, resource)
	}
	bad := old
	bad.StorageImage = next.Image
	if rollback(next.TemplateVersion, bad, "COMPATIBILITY_REHEARSAL", true) == nil {
		t.Fatal("incompatible/modified retained manifest accepted")
	}
	bad = old
	bad.TemplateVersion = webapi.ID()
	if rollback(next.TemplateVersion, bad, "COMPATIBILITY_REHEARSAL", true) == nil {
		t.Fatal("unapproved rollback target accepted")
	}
	if rollback(webapi.ID(), old, "COMPATIBILITY_REHEARSAL", true) == nil || rollback(next.TemplateVersion, old, "unsafe reason", true) == nil {
		t.Fatal("stale source or unsafe reason accepted")
	}
	must(`CREATE FUNCTION hc_test_reject_rollback() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='RUNTIME_ROLLBACK' THEN RAISE EXCEPTION 'fixture'; END IF; RETURN NEW; END $$; CREATE TRIGGER hc_test_reject_rollback BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION hc_test_reject_rollback()`)
	defer pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS hc_test_reject_rollback ON audit_events; DROP FUNCTION IF EXISTS hc_test_reject_rollback()`)
	if rollback(next.TemplateVersion, old, "COMPATIBILITY_REHEARSAL", true) == nil {
		t.Fatal("late audit failure accepted")
	}
	check(2, next.Image, 0)
	must(`DROP TRIGGER hc_test_reject_rollback ON audit_events`)
	if err := rollback(next.TemplateVersion, old, "COMPATIBILITY_REHEARSAL", true); err != nil {
		t.Fatal(err)
	}
	if err := rollback(next.TemplateVersion, old, "COMPATIBILITY_REHEARSAL", true); err != nil {
		t.Fatal("exact replay failed", err)
	}
	check(3, old.Image, 1)
	if rollback(next.TemplateVersion, old, "DIFFERENT_INTENT", true) == nil {
		t.Fatal("unrecorded intent treated as a successful replay")
	}
	if err := rollback(old.TemplateVersion, next, "REHEARSAL_RETURN", true); err != nil {
		t.Fatal("return to retained newer version failed", err)
	}
	check(4, next.Image, 2)
	var bytes int64
	if err := pool.QueryRow(ctx, `SELECT sum(amount) FROM quota_reservations WHERE resource_type='STORAGE' AND status='USED'`).Scan(&bytes); err != nil || bytes != 10<<30 {
		t.Fatal("storage ledger changed", err)
	}
}
