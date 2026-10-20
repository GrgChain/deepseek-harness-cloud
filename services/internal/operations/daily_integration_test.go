package operations

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/config"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func TestDailySnapshotRejectsActiveChangedOrHeldEnvironmentOnPostgres(t *testing.T) {
	path := os.Getenv("HC_TEST_DB_DSN_FILE")
	if path == "" {
		t.Skip("isolated PostgreSQL required")
	}
	dsn, err := config.ReadSecret(path)
	if err != nil {
		t.Fatal("private fixture connection required")
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal("invalid connection")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal("fixture administrator unavailable")
	}
	defer admin.Close(context.Background())
	database := "hc_test_daily_" + strings.ReplaceAll(webapi.ID(), "-", "")[:16]
	if _, err = admin.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{database}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		bounded, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, e := admin.Exec(bounded, `DROP DATABASE `+pgx.Identifier{database}.Sanitize()+` WITH (FORCE)`); e != nil {
			t.Error("owned daily fixture cleanup failed")
		}
	}()
	u.Path = "/" + database
	if db.Apply(ctx, u.String()) != nil {
		t.Fatal("fixture migration failed")
	}
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal("fixture pool unavailable")
	}
	defer pool.Close()
	must := func(sql string, args ...any) {
		t.Helper()
		if _, e := pool.Exec(ctx, sql, args...); e != nil {
			t.Fatal(e)
		}
	}
	org, owner, model, secret, instance, asset, op := webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID()
	must(`INSERT INTO organizations(id,name,status) VALUES($1,'Daily fixture','ACTIVE')`, org)
	must(`INSERT INTO users(id,identity_issuer,identity_subject,workspace_owner_id,status) VALUES($1::uuid,'https://daily.test',$1::text,$1::uuid,'ACTIVE')`, owner)
	must(`INSERT INTO memberships(org_id,user_id,role,status) VALUES($1,$2,'MEMBER','ACTIVE')`, org, owner)
	must(`INSERT INTO secrets(id,org_id,key_id,nonce,ciphertext) VALUES($1,$2,'synthetic',$3,'synthetic-never-decrypted')`, secret, org, make([]byte, 12))
	must(`INSERT INTO model_configuration_heads(id,org_id) VALUES($1,$2)`, model, org)
	must(`INSERT INTO model_configurations(org_id,id,version,provider,endpoint,protocol,model,credential_ref,status) VALUES($1,$2,1,'deepseek-official','https://api.deepseek.com/anthropic','deepseek-messages','fixture',$3,'VALID')`, org, model, secret)
	must(`INSERT INTO instances(id,org_id,owner_id,name,external_create_key,workspace_name,desired_state,observed_state,model_config_id,model_config_version,config_snapshot) VALUES($1,$2,$3,'daily-fixture',$1,'daily-fixture','STOPPED','STOPPED',$4,1,'{}')`, instance, org, owner, model)
	must(`INSERT INTO storage_assets(id,org_id,owner_id,instance_id,resource_name,filesystem_id,capacity_bytes,status,init_version) VALUES($1::uuid,$2,$3,$4,$5,$1::text,10737418240,'READY',1)`, asset, org, owner, instance, "hc-"+asset)
	must(`UPDATE instances SET storage_asset_id=$2 WHERE id=$1`, instance, asset)
	must(`INSERT INTO operations(id,instance_id,actor_id,type,status,stage,intent,trace_id) VALUES($1,$2,$3,'CREATE','SUCCEEDED','COMPLETE','{}','daily-fixture')`, op, instance, owner)
	must(`INSERT INTO quota_reservations(id,org_id,owner_id,instance_id,operation_id,resource_type,amount,status) VALUES($1,$2,$3,$4,$5,'STORAGE',10737418240,'USED')`, webapi.ID(), org, owner, instance, op)
	source := NewPostgresDailySource(pool, u.String(), nil, org, "daily-fixture-operator")
	before, err := source.Snapshot(ctx)
	if err != nil || len(before.Assets) != 1 || before.Assets[0] != asset {
		t.Fatal("stopped source snapshot failed", err)
	}
	account := webapi.ID()
	must(`INSERT INTO personal_provider_accounts(id,org_id,owner_id,name,provider_id,api_base) VALUES($1,$2,$3,'snapshot account','deepseek','https://api.deepseek.com')`, account, org, owner)
	withAccount, err := source.Snapshot(ctx)
	if err != nil || withAccount.StateSHA256 == before.StateSHA256 {
		t.Fatal("provider account creation omitted from backup consistency fingerprint", err)
	}
	must(`UPDATE personal_provider_accounts SET name='renamed snapshot account',version=version+1 WHERE id=$1`, account)
	renamedAccount, err := source.Snapshot(ctx)
	if err != nil || renamedAccount.StateSHA256 == withAccount.StateSHA256 {
		t.Fatal("provider account edits omitted from backup consistency fingerprint", err)
	}
	must(`INSERT INTO quota_policies(org_id) VALUES($1)`, org)
	must(`INSERT INTO user_quota_policies(org_id,owner_id,max_instances,cpu_millicores,memory_bytes,storage_bytes) VALUES($1,$2,2,4000,8589934592,21474836480)`, org, owner)
	personalized, err := source.Snapshot(ctx)
	if err != nil || before.StateSHA256 == personalized.StateSHA256 {
		t.Fatal("user quota changes omitted from backup consistency fingerprint", err)
	}
	must(`UPDATE user_quota_policies SET version=version+1,cpu_millicores=6000 WHERE org_id=$1 AND owner_id=$2`, org, owner)
	changedQuota, err := source.Snapshot(ctx)
	if err != nil || personalized.StateSHA256 == changedQuota.StateSHA256 {
		t.Fatal("quota adjustment during backup not detected", err)
	}
	must(`UPDATE instances SET observed_state='RUNNING' WHERE id=$1`, instance)
	if _, err := source.Snapshot(ctx); err == nil {
		t.Fatal("running source accepted")
	}
	must(`UPDATE instances SET observed_state='STOPPED' WHERE id=$1`, instance)
	must(`UPDATE operations SET status='UNCERTAIN' WHERE id=$1`, op)
	if _, err := source.Snapshot(ctx); err == nil {
		t.Fatal("active operation accepted")
	}
	must(`UPDATE operations SET status='SUCCEEDED' WHERE id=$1`, op)
	must(`UPDATE storage_assets SET legal_hold=true WHERE id=$1`, asset)
	held, err := source.Snapshot(ctx)
	if err != nil || !held.Held {
		t.Fatal("asset legal hold absent", err)
	}
	called := false
	if err := source.RetentionGuard(ctx, func() error { called = true; return nil }); err == nil || called {
		t.Fatal("held source authorized physical prune")
	}
	archive, err := NewAuditArchiver(pool, org, "daily-fixture-operator").Batch(ctx, "", nil, "", 1, false)
	if err != nil {
		t.Fatal(err)
	}
	if !archive.Held {
		t.Fatal("asset hold did not protect audit")
	}
	must(`UPDATE storage_assets SET legal_hold=false WHERE id=$1`, asset)
	if err := source.RetentionGuard(ctx, func() error {
		return source.Audit(ctx, webapi.ID(), "BACKUP_SET_PRUNE_ACCEPTED", map[string]any{"schema_version": 1})
	}); err != nil {
		t.Fatal("retention guard blocked its own independent audit", err)
	}
	// Daily publication and retention must update safe history atomically with audit.
	setID, backupID := webapi.ID(), webapi.ID()
	must(`INSERT INTO storage_backups(id,org_id,owner_id,instance_id,asset_id,capacity_bytes,template_version,image_digest,status,completed_at,verified_at,backup_sha256,set_id,archive_path) VALUES($1,$2,$3,$4,$5,10737418240,$6,$7,'SUCCEEDED',clock_timestamp(),clock_timestamp(),$8,$9,$10)`, backupID, org, owner, instance, asset, webapi.ID(), "sha256:"+strings.Repeat("a", 64), strings.Repeat("b", 64), setID, "/private/pending-"+setID+"/asset.hcbak")
	if err := source.Audit(ctx, setID, "BACKUP_SET_CREATE", map[string]any{"schema_version": 1, "day": "2026-10-16"}); err != nil {
		t.Fatal("daily history publication failed", err)
	}
	var archivePath, status string
	if err := pool.QueryRow(ctx, `SELECT archive_path FROM storage_backups WHERE id=$1`, backupID).Scan(&archivePath); err != nil || archivePath != "/private/day-2026-10-16-"+setID+"/asset.hcbak" {
		t.Fatal("daily rename left reconciliation pointing at old archive", err)
	}
	must(`UPDATE storage_assets SET legal_hold=true WHERE id=$1`, asset)
	if err := source.Audit(ctx, setID, "BACKUP_SET_PRUNE_ACCEPTED", map[string]any{"schema_version": 1}); err == nil {
		t.Fatal("legal hold allowed backup history pruning")
	}
	must(`UPDATE storage_assets SET legal_hold=false WHERE id=$1`, asset)
	for _, step := range []struct{ action, want string }{{"BACKUP_SET_PRUNE_ACCEPTED", "PRUNE_PENDING"}, {"BACKUP_SET_PRUNE", "PRUNED"}} {
		if err := source.RetentionGuard(ctx, func() error { return source.Audit(ctx, setID, step.action, map[string]any{"schema_version": 1}) }); err != nil {
			t.Fatal("daily prune history failed", err)
		}
		if err := pool.QueryRow(ctx, `SELECT status FROM storage_backups WHERE id=$1`, backupID).Scan(&status); err != nil || status != step.want {
			t.Fatal("daily backup history disagrees with physical prune phase", status, err)
		}
	}
	must(`UPDATE model_configurations SET model='changed-fixture' WHERE id=$1`, model)
	after, err := source.Snapshot(ctx)
	if err != nil || before.StateSHA256 == after.StateSHA256 {
		t.Fatal("model change not included in snapshot")
	}
	must(`INSERT INTO recovery_quota_holds(org_id,owner_id,instance_id,resource_type,amount,inventory_run_id,inventory_sha256,reason_code,operator_ref) VALUES($1,$2,$3,'STORAGE',10737418240,$4,$5,'TEST_RETAIN','fixture')`, org, owner, webapi.ID(), webapi.ID(), strings.Repeat("a", 64))
	if _, err := source.Snapshot(ctx); err == nil {
		t.Fatal("daily backup declared complete while a retained orphan asset had no backup source")
	}
}
