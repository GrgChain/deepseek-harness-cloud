package operations

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/instances"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/runtimeapproval"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	workspaceengine "github.com/jonluo94/deepseek-harness-cloud/services/internal/workspace"
)

func exerciseRecoveryAdoption(t *testing.T, ctx context.Context, target *pgxpool.Pool, observer *inventoryFixtureObserver, database, org, owner string) {
	t.Helper()
	raw, err := os.ReadFile(os.Getenv("HC_TEST_DB_DSN_FILE"))
	if err != nil {
		t.Fatal("fixture connection missing")
	}
	admin, err := pgx.Connect(ctx, strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal("fixture administrator unavailable")
	}
	defer admin.Close(context.Background())
	sourceDB := database + "_metadata"
	if _, err = admin.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{sourceDB}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, err := admin.Exec(context.Background(), `DROP DATABASE `+pgx.Identifier{sourceDB}.Sanitize()+` WITH (FORCE)`)
		if err != nil {
			t.Error("owned metadata fixture cleanup failed")
		}
	}()
	u, err := url.Parse(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal("invalid fixture connection")
	}
	u.Path = "/" + sourceDB
	cfg, _ := pgxpool.ParseConfig(u.String())
	cfg.MaxConns = 1
	if err = db.Apply(ctx, u.String()); err != nil {
		t.Fatal(err)
	}
	source, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("metadata fixture unavailable")
	}
	defer source.Close()
	must := func(pool *pgxpool.Pool, sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"organizations", "users", "memberships", "secrets", "model_configuration_heads", "model_configurations"} {
		var rows []byte
		if err = target.QueryRow(ctx, `SELECT jsonb_agg(to_jsonb(r)) FROM `+table+` r`).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		must(source, `INSERT INTO `+table+` SELECT * FROM jsonb_populate_recordset(NULL::`+table+`,$1::jsonb)`, rows)
	}
	id, asset := observer.resources[3].InstanceID, observer.resources[3].AssetID
	workspace, template, templateVersion, spec, create := webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID()
	var model, secret string
	if err = source.QueryRow(ctx, `SELECT id::text,credential_ref::text FROM model_configurations LIMIT 1`).Scan(&model, &secret); err != nil {
		t.Fatal(err)
	}
	newSecret := webapi.ID()
	must(source, `INSERT INTO secrets(id,org_id,key_id,nonce,ciphertext,revoked_at) SELECT $1,org_id,key_id,nonce,'new-fixture-cipher',clock_timestamp() FROM secrets WHERE id=$2`, newSecret, secret)
	must(source, `INSERT INTO model_configurations SELECT org_id,id,2,provider,endpoint,protocol,model,$1,status,created_at,revoked_at FROM model_configurations WHERE id=$2 AND version=1`, newSecret, model)
	must(source, `UPDATE model_configuration_heads SET current_version=2`)
	digest := "sha256:" + strings.Repeat("a", 64)
	manifest := runtimeapproval.Manifest{SchemaVersion: 1, UpstreamCommit: runtimeapproval.ReviewCommit, Image: digest, StorageImage: digest, NetworkImage: digest, WorkspaceSourceVersion: runtimeapproval.WorkspaceSourceVersion, TerraformVersion: runtimeapproval.TerraformVersion, ProviderLock: digest, TemplateVersion: templateVersion, ProfileDigest: digest, ConfigFormat: runtimeapproval.ConfigFormat, Bootstrap: runtimeapproval.BootstrapAdapter, NativeAuth: runtimeapproval.NativeAdapter, Paths: []string{"/data/workspace", "/data/dsh"}, ToolDigest: digest, PackageVersion: runtimeapproval.PackageVersion, PackageIntegrity: runtimeapproval.PackageIntegrity, AdapterDigest: digest}
	encoded, _ := json.Marshal(manifest)
	must(source, `INSERT INTO approved_templates(id,version,name,image_digest,runtime_manifest,enabled) VALUES($1,$2,'hc-dsh-initial',$3,$4,true)`, template, templateVersion, digest, encoded)
	oldManifest := manifest
	oldManifest.TemplateVersion = webapi.ID()
	oldApproval, _ := json.Marshal(oldManifest)
	must(target, `INSERT INTO approved_templates(id,version,name,image_digest,runtime_manifest,enabled) VALUES($1,$2,'hc-dsh-initial',$3,$4,true)`, template, oldManifest.TemplateVersion, digest, oldApproval)
	must(source, `INSERT INTO approved_specs(id,cpu_millicores,memory_bytes,storage_bytes,enabled) VALUES($1,2000,4294967296,10737418240,true)`, spec)
	snapshot := map[string]any{"schema_version": 1, "defaults_version": 1, "template_id": template, "template_version": templateVersion, "image_digest": digest, "spec": instances.ResourceSpec{ID: spec, CPU: 2000, Memory: 4 << 30, Storage: 10 << 30}, "model_configuration_id": model, "model_configuration_version": 2, "provider": "deepseek-official", "protocol": "deepseek-messages", "model": "fixture"}
	encoded, _ = json.Marshal(snapshot)
	name, _ := workspaceengine.WorkspaceName(id)
	must(source, `INSERT INTO instances(id,org_id,owner_id,name,external_create_key,workspace_name,workspace_id,desired_state,observed_state,model_config_id,model_config_version,config_snapshot) VALUES($1,$2,$3,'dsh-recovered',$1,$4,$5,'STOPPED','STOPPED',$6,2,$7)`, id, org, owner, name, workspace, model, encoded)
	must(source, `INSERT INTO storage_assets(id,org_id,owner_id,instance_id,resource_name,filesystem_id,capacity_bytes,status,init_version) VALUES($1::uuid,$2,$3,$4,$5,$1::text,10737418240,'READY',1)`, asset, org, owner, id, "hc-"+asset)
	must(source, `UPDATE instances SET storage_asset_id=$2 WHERE id=$1`, id, asset)
	must(source, `INSERT INTO operations(id,instance_id,actor_id,type,status,stage,intent,trace_id) VALUES($1,$2,$3,'CREATE','SUCCEEDED','COMPLETE','{}','original-create')`, create, id, owner)
	for _, r := range []struct {
		kind   string
		amount int64
	}{{"INSTANCE", 1}, {"STORAGE", 10 << 30}} {
		must(source, `INSERT INTO quota_reservations(id,org_id,owner_id,instance_id,operation_id,resource_type,amount,status) VALUES($1,$2,$3,$4,$5,$6,$7,'USED')`, webapi.ID(), org, owner, id, create, r.kind, r.amount)
	}
	must(source, `INSERT INTO audit_events(id,org_id,action,outcome,trace_id,details) VALUES($1,$2,'RESTORE_ISOLATED','SUCCEEDED','checkpoint',jsonb_build_object('backup_sha256',$3::text))`, webapi.ID(), org, strings.Repeat("b", 64))
	must(source, `REVOKE CONNECT ON DATABASE `+pgx.Identifier{sourceDB}.Sanitize()+` FROM PUBLIC,hc_api,hc_worker,hc_gateway,hc_model_gateway`)
	observer.resources = append(observer.resources, ResourceObservation{Kind: "VOLUME", ExternalID: "hc-" + asset, InstanceID: id, AssetID: asset, OrganizationID: org, OwnerID: owner, Capacity: 10 << 30, IdentityValid: true}, ResourceObservation{Kind: "WORKSPACE", ExternalID: workspace, InstanceID: id, WorkspaceOwner: owner, TemplateVersion: templateVersion, Status: "stopped", IdentityValid: true})
	inventory := NewRecoveryInventory(target, observer, org, "adoption-fixture")
	reviewed, err := inventory.Run(ctx, database, true)
	if err != nil {
		t.Fatal(err)
	}
	verifyErr := error(nil)
	verified := 0
	verifier := func(context.Context, instances.Snapshot, runtimeapproval.Manifest) error {
		verified++
		return verifyErr
	}
	service := NewRecoveryAdoption(target, source, observer, verifier, org, "adoption-fixture")
	run := func(inventoryID, reason string, apply bool) (RecoveryAdoptionReport, error) {
		return service.Adopt(ctx, database, sourceDB, id, inventoryID, reason, apply)
	}
	check := func(instances, holds, audits int) {
		t.Helper()
		var gotInstances, gotHolds, gotAudits, chargedInstances int
		var storage int64
		var disabled, revoked bool
		err := target.QueryRow(ctx, `SELECT (SELECT count(*) FROM instances),(SELECT count(*) FROM recovery_quota_holds),(SELECT count(*) FROM audit_events WHERE action='RECOVERY_RESOURCE_ADOPT'),(SELECT sum(amount) FROM quota_commitments WHERE resource_type='INSTANCE'),(SELECT sum(amount) FROM quota_commitments WHERE resource_type='STORAGE'),(SELECT status='DISABLED' FROM organizations),NOT EXISTS(SELECT 1 FROM model_configurations WHERE status<>'REVOKED' OR revoked_at IS NULL)`).Scan(&gotInstances, &gotHolds, &gotAudits, &chargedInstances, &storage, &disabled, &revoked)
		if err != nil || gotInstances != instances || gotHolds != holds || gotAudits != audits || chargedInstances != 2 || storage != 20<<30 || !disabled || !revoked {
			t.Fatal("partial adoption or quota/auth change", err, gotInstances, gotHolds, gotAudits, chargedInstances, storage)
		}
	}
	preview, err := run(reviewed.RunID, "RECOVERY_ADOPT", false)
	if err != nil || preview.Applied || preview.PromotionAllowed || !preview.Quarantined || preview.Transferred.Storage != 10<<30 {
		t.Fatal("unsafe adoption preview", err)
	}
	check(1, 3, 0)
	for _, mutation := range []struct {
		name, change, restore string
		args                  []any
	}{
		{"wrong workspace", `UPDATE instances SET workspace_id=$2 WHERE id=$1`, `UPDATE instances SET workspace_id=$2 WHERE id=$1`, []any{id, webapi.ID(), workspace}},
		{"wrong filesystem", `UPDATE storage_assets SET filesystem_id=$2 WHERE id=$1`, `UPDATE storage_assets SET filesystem_id=$2 WHERE id=$1`, []any{asset, webapi.ID(), asset}},
		{"wrong original quota", `UPDATE quota_reservations SET amount=$2 WHERE instance_id=$1 AND resource_type='INSTANCE'`, `UPDATE quota_reservations SET amount=$2 WHERE instance_id=$1 AND resource_type='INSTANCE'`, []any{id, int64(2), int64(1)}},
		{"missing original create success", `UPDATE operations SET status=$2 WHERE id=$1`, `UPDATE operations SET status=$2 WHERE id=$1`, []any{create, "FAILED", "SUCCEEDED"}},
		{"active metadata checkpoint", `UPDATE organizations SET status=$2 WHERE id=$1`, `UPDATE organizations SET status=$2 WHERE id=$1`, []any{org, "ACTIVE", "DISABLED"}},
	} {
		must(source, mutation.change, mutation.args[0], mutation.args[1])
		if _, err := run(reviewed.RunID, "RECOVERY_ADOPT", true); err == nil {
			t.Fatal("unsafe metadata admitted:", mutation.name)
		}
		must(source, mutation.restore, mutation.args[0], mutation.args[2])
		check(1, 3, 0)
	}
	verifyErr = errors.New("image approval mismatch")
	if _, err = run(reviewed.RunID, "RECOVERY_ADOPT", true); err == nil {
		t.Fatal("unverified runtime admitted")
	}
	verifyErr = nil
	must(source, `UPDATE users SET identity_subject='different-principal' WHERE id=$1`, owner)
	if _, err = run(reviewed.RunID, "RECOVERY_ADOPT", true); err == nil {
		t.Fatal("changed identity admitted")
	}
	must(source, `UPDATE users SET identity_subject=id::text WHERE id=$1`, owner)
	must(target, `CREATE FUNCTION reject_adoption_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='RECOVERY_RESOURCE_ADOPT' THEN RAISE EXCEPTION 'fixture'; END IF; RETURN NEW; END $$;CREATE TRIGGER reject_adoption_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_adoption_audit()`)
	if r, err := run(reviewed.RunID, "RECOVERY_ADOPT", true); err == nil || r.Applied {
		t.Fatal("late audit failure kept adoption")
	}
	check(1, 3, 0)
	var versions int
	if err = target.QueryRow(ctx, `SELECT count(*) FROM model_configurations WHERE version=2`).Scan(&versions); err != nil || versions != 0 {
		t.Fatal("failed import retained model rows")
	}
	if err = target.QueryRow(ctx, `SELECT count(*) FROM secrets WHERE id=$1`, newSecret).Scan(&versions); err != nil || versions != 0 {
		t.Fatal("failed import retained encrypted credential")
	}
	must(target, `DROP TRIGGER reject_adoption_audit ON audit_events`)
	observer.calls = 0
	observer.hook = func(call int) {
		if call == 1 {
			must(source, `UPDATE instances SET name='changed-source' WHERE id=$1`, id)
		}
	}
	if _, err = run(reviewed.RunID, "RECOVERY_ADOPT", true); err == nil {
		t.Fatal("changing checkpoint admitted")
	}
	must(source, `UPDATE instances SET name='dsh-recovered' WHERE id=$1`, id)
	observer.hook = nil
	if _, err = run(webapi.ID(), "RECOVERY_ADOPT", true); err == nil {
		t.Fatal("unreviewed inventory admitted")
	}
	observer.calls = 0
	observer.hook = func(call int) {
		if call == 2 {
			observer.resources[len(observer.resources)-1].Status = "running"
		}
	}
	if _, err = run(reviewed.RunID, "RECOVERY_ADOPT", true); err == nil {
		t.Fatal("changing physical workspace admitted")
	}
	observer.resources[len(observer.resources)-1].Status = "stopped"
	observer.hook = nil
	// A previously revoked target credential may have been reencrypted since
	// the checkpoint. Preserve it byte for byte, including its revocation.
	var existingSecret []byte
	if err = source.QueryRow(ctx, `SELECT to_jsonb(s) FROM secrets s WHERE id=$1`, newSecret).Scan(&existingSecret); err != nil {
		t.Fatal(err)
	}
	must(target, `INSERT INTO secrets SELECT * FROM jsonb_populate_record(NULL::secrets,$1::jsonb)`, existingSecret)
	must(target, `UPDATE secrets SET ciphertext='rotated-revoked-fixture' WHERE id=$1`, newSecret)
	result, err := run(reviewed.RunID, "RECOVERY_ADOPT", true)
	if err != nil || !result.Applied || result.PromotionAllowed {
		t.Fatal("verified resources not adopted", err)
	}
	check(2, 1, 1)
	replay, err := run(reviewed.RunID, "RECOVERY_ADOPT", true)
	if err != nil || replay.RunID != result.RunID {
		t.Fatal("adoption replay duplicated metadata", err)
	}
	check(2, 1, 1)
	if _, err = run(reviewed.RunID, "OTHER_REASON", true); err == nil {
		t.Fatal("changed intent replayed")
	}
	var importedCreate, headVersion, enabled int
	if err = target.QueryRow(ctx, `SELECT (SELECT count(*) FROM operations WHERE id=$1 AND instance_id=$2 AND status='SUCCEEDED'),(SELECT current_version FROM model_configuration_heads),(SELECT count(*) FROM approved_templates WHERE enabled)`, create, id).Scan(&importedCreate, &headVersion, &enabled); err != nil || importedCreate != 1 || headVersion != 2 || enabled != 1 {
		t.Fatal("source provenance/default/versions changed", err)
	}
	var preserved bool
	if err = target.QueryRow(ctx, `SELECT ciphertext='rotated-revoked-fixture'::bytea AND revoked_at IS NOT NULL FROM secrets WHERE id=$1`, newSecret).Scan(&preserved); err != nil || !preserved {
		t.Fatal("adoption replaced a revoked credential", err)
	}
	post, err := inventory.Run(ctx, database, false)
	if err != nil || post.Ledger.Storage != 20<<30 || post.Conservative.Storage != 20<<30 {
		t.Fatal("adoption lost actual capacity", err)
	}
	for _, f := range post.Findings {
		if f.InstanceID == id && f.Finding != "MATCHED" {
			t.Fatal("adopted resource still mismatched", f.Kind, f.Finding)
		}
	}
	if verified < 1 {
		t.Fatal("runtime verifier not used")
	}
}
