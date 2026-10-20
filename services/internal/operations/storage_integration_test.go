package operations

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/devlocal"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/instances"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/worker"
)

func TestRealOfflineStorageBackupRestoreAndProtectedReclamation(t *testing.T) {
	path, workerPath := os.Getenv("HC_TEST_DB_DSN_FILE"), os.Getenv("HC_WORKER_CONFIG_FILE")
	if path == "" || workerPath == "" {
		t.Skip("explicit isolated storage DB and trusted local Docker host required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("private database connection missing")
	}
	u, err := url.Parse(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal("invalid connection")
	}
	u.Path = "/hc_test_storage_ops"
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	if err = db.Apply(ctx, u.String()); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal("database unavailable")
	}
	defer pool.Close()
	must := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	must(`TRUNCATE organizations CASCADE`)
	o, err := worker.LoadOptions(workerPath)
	if err != nil {
		t.Fatal(err)
	}
	host, err := worker.NewLocalHost(o.Host)
	if err != nil {
		t.Fatal(err)
	}
	org, owner, secret, model := webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID()
	must(`INSERT INTO organizations(id,name,status) VALUES($1,'Real offline storage fixture','ACTIVE')`, org)
	must(`INSERT INTO users(id,identity_issuer,identity_subject,workspace_owner_id,status) VALUES($1::uuid,'https://storage-ops.test',$1::text,$1::uuid,'ACTIVE')`, owner)
	must(`INSERT INTO memberships(org_id,user_id,role,status) VALUES($1,$2,'MEMBER','ACTIVE')`, org, owner)
	must(`INSERT INTO secrets(id,org_id,key_id,nonce,ciphertext) VALUES($1,$2,'fixture',$3,'x')`, secret, org, bytes.Repeat([]byte{0}, 12))
	must(`INSERT INTO model_configuration_heads(id,org_id) VALUES($1,$2)`, model, org)
	must(`INSERT INTO model_configurations(org_id,id,version,provider,endpoint,protocol,model,credential_ref,status) VALUES($1,$2,1,'deepseek-official','https://api.deepseek.com/anthropic','deepseek-messages','fixture',$3,'VALID')`, org, model, secret)
	must(`INSERT INTO quota_policies(org_id) VALUES($1)`, org)
	snapshot := []byte(`{"schema_version":1,"defaults_version":1,"template_id":"` + webapi.ID() + `","template_version":"` + webapi.ID() + `","image_digest":"` + o.Host.RuntimeImage + `","spec":{"id":"` + webapi.ID() + `","cpu_millicores":2000,"memory_bytes":4294967296,"storage_bytes":10737418240},"model_configuration_id":"` + model + `","model_configuration_version":1,"provider":"deepseek-official","protocol":"deepseek-messages","model":"fixture"}`)
	decoded, err := instances.DecodeSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var plans []worker.ExecutionPlan
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 90*time.Second)
		defer stop()
		for _, p := range plans {
			unlock, err := host.Acquire(cleanup, p.InstanceID)
			if err != nil {
				t.Error("storage fixture guard cleanup failed")
				continue
			}
			err = host.ReclaimAsset(cleanup, p)
			unlock()
			if err != nil {
				t.Error("owned storage fixture cleanup failed", p.AssetID)
			}
		}
	}()
	for range 2 {
		id, asset, op := webapi.ID(), webapi.ID(), webapi.ID()
		must(`INSERT INTO instances(id,org_id,owner_id,name,external_create_key,workspace_name,desired_state,observed_state,model_config_id,model_config_version,config_snapshot) VALUES($1,$2,$3,$4,$1,$4,'STOPPED','STOPPED',$5,1,$6)`, id, org, owner, "dsh-"+id[:12], model, snapshot)
		must(`INSERT INTO operations(id,instance_id,actor_id,type,status,stage,intent,trace_id) VALUES($1,$2,$3,'CREATE','SUCCEEDED','COMPLETE','{}','storage-fixture')`, op, id, owner)
		must(`INSERT INTO storage_assets(id,org_id,owner_id,instance_id,resource_name,capacity_bytes,status) VALUES($1,$2,$3,$4,$5,10737418240,'PREPARING')`, asset, org, owner, id, "hc-"+asset)
		must(`UPDATE instances SET storage_asset_id=$2 WHERE id=$1`, id, asset)
		must(`INSERT INTO quota_reservations(id,org_id,owner_id,instance_id,operation_id,resource_type,amount,status) VALUES($1,$2,$3,$4,$5,'STORAGE',10737418240,'USED')`, webapi.ID(), org, owner, id, op)
		p := worker.ExecutionPlan{InstanceID: id, AssetID: asset, OrganizationID: org, OwnerID: owner, Capacity: 10 << 30, ResourceName: "hc-" + asset, Action: "CREATE", AssetStatus: "PREPARING", Snapshot: decoded, WorkspaceOwner: owner}
		plans = append(plans, p)
		unlock, err := host.Acquire(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		proof, err := host.Prepare(ctx, p)
		unlock()
		if err != nil {
			t.Fatal("real asset initialization failed", err)
		}
		plans[len(plans)-1].FilesystemID = proof.FilesystemID
		must(`UPDATE storage_assets SET status='READY',init_version=1,filesystem_id=$1::text WHERE id=$1::uuid`, asset)
	}
	source, target := plans[0], plans[1]
	docker := func(p worker.ExecutionPlan, script string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "docker", "--host", "unix:///var/run/docker.sock", "run", "--rm", "--network", "none", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--user", "1000:1000", "--mount", "type=volume,src="+p.ResourceName+",dst=/data,volume-nocopy", "--entrypoint", "python3", o.Host.StorageImage, "-c", script)
		cmd.Stderr = nil
		out, err := cmd.Output()
		if err != nil {
			t.Fatal("owned proof-file operation failed")
		}
		return strings.TrimSpace(string(out))
	}
	docker(source, `from pathlib import Path; p=Path('/data/workspace');p.mkdir(exist_ok=True);(p/'restore-proof').write_text('source data retained')`)
	docker(target, `from pathlib import Path; p=Path('/data/workspace');p.mkdir(exist_ok=True);(p/'restore-proof').write_text('target original preserved')`)
	dir := t.TempDir()
	if err = os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{13}, 32)
	backup, preimage := filepath.Join(dir, "source.hcbk"), filepath.Join(dir, "target-preimage.hcbk")
	service := NewStorageService(pool, host, org, "integration-operator")
	proof, err := service.Backup(ctx, source.AssetID, backup, key)
	if err != nil {
		t.Fatal("real encrypted storage backup failed", err)
	}

	var backupID string
	if err = pool.QueryRow(ctx, `SELECT id::text FROM storage_backups WHERE asset_id=$1 AND status='SUCCEEDED' AND verified_at IS NOT NULL`, source.AssetID).Scan(&backupID); err != nil {
		t.Fatal("verified backup metadata missing", err)
	}
	must(`UPDATE storage_backups SET status='UNCERTAIN' WHERE id=$1`, backupID)
	if state, e := service.ReconcileBackup(ctx, backupID, key); e != nil || state != "SUCCEEDED" {
		t.Fatal("interrupted backup could not be reconciled", state, e)
	}
	if e := service.SetStorageHold(ctx, target.AssetID, "", "TEST_HOLD", true, false); e != nil {
		t.Fatal("hold preview", e)
	}
	var held bool
	_ = pool.QueryRow(ctx, `SELECT legal_hold FROM storage_assets WHERE id=$1`, target.AssetID).Scan(&held)
	if held {
		t.Fatal("hold preview mutated asset")
	}
	if e := service.SetStorageHold(ctx, target.AssetID, "wrong", "TEST_HOLD", true, true); e == nil {
		t.Fatal("imprecise hold confirmation accepted")
	}
	must(`CREATE OR REPLACE FUNCTION reject_backup_publish() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='STORAGE_BACKUP' THEN RAISE EXCEPTION 'injected backup audit failure'; END IF; RETURN NEW; END $$`)
	must(`CREATE TRIGGER backup_publish_failure BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_backup_publish()`)
	failedPath := filepath.Join(dir, "pending-backup.hcbk")
	if _, e := service.Backup(ctx, source.AssetID, failedPath, key); e == nil {
		t.Fatal("backup final audit failure reported success")
	}
	var pendingBackup string
	if e := pool.QueryRow(ctx, `SELECT id::text FROM storage_backups WHERE asset_id=$1 AND status='UNCERTAIN'`, source.AssetID).Scan(&pendingBackup); e != nil {
		t.Fatal("backup pending intent lost", e)
	}
	must(`DROP TRIGGER backup_publish_failure ON audit_events`)
	if state, e := service.ReconcileBackup(ctx, pendingBackup, key); e != nil || state != "SUCCEEDED" {
		t.Fatal("pending physical backup not recovered", state, e)
	}
	if _, err := service.Restore(ctx, backup, source.AssetID, preimage, source.AssetID, key, true); err == nil {
		t.Fatal("source original asset overwritten")
	}
	if report, err := service.Restore(ctx, backup, target.AssetID, preimage, "", key, false); err != nil || report.Applied || report.Proof != proof {
		t.Fatal("restore preview failed", err)
	}
	if _, err := os.Stat(preimage); !os.IsNotExist(err) {
		t.Fatal("preview created preimage")
	}
	if e := service.SetStorageHold(ctx, target.AssetID, target.AssetID, "TEST_HOLD", true, true); e != nil {
		t.Fatal(e)
	}
	if _, err := service.Restore(ctx, backup, target.AssetID, preimage, target.AssetID, key, true); err == nil {
		t.Fatal("held target overwritten")
	}
	if e := service.SetStorageHold(ctx, target.AssetID, target.AssetID, "TEST_RELEASE", false, true); e != nil {
		t.Fatal(e)
	}
	must(`CREATE OR REPLACE FUNCTION reject_storage_publish() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action IN ('STORAGE_RESTORE','STORAGE_RECLAIM') THEN RAISE EXCEPTION 'injected final audit failure'; END IF; RETURN NEW; END $$`)
	must(`CREATE TRIGGER storage_publish_failure BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_storage_publish()`)
	defer func() {
		bounded, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, _ = pool.Exec(bounded, `DROP TRIGGER IF EXISTS storage_publish_failure ON audit_events`)
	}()
	if _, err := service.Restore(ctx, backup, target.AssetID, preimage, target.AssetID, key, true); err == nil {
		t.Fatal("final restore audit failure committed")
	}
	var pending string
	if err := pool.QueryRow(ctx, `SELECT status FROM storage_assets WHERE id=$1`, target.AssetID).Scan(&pending); err != nil || pending != "RESTORING" {
		t.Fatal("interrupted restore lost durable startup barrier")
	}
	must(`DROP TRIGGER storage_publish_failure ON audit_events`)
	// Simulate the private staging file left by a process crash. The same bound
	// intent must remove that partial copy and resume without replacing preimage.
	partial := exec.CommandContext(ctx, "docker", "run", "--rm", "--network", "none", "--mount", "type=bind,src="+o.Host.AssetRoot+",dst=/assets", "--entrypoint", "python3", o.Host.StorageImage, "-c", `import os;fd=os.open('/assets/`+target.AssetID+`/restore-`+proof.SHA256+`.img',os.O_CREAT|os.O_EXCL|os.O_RDWR,0o600);os.ftruncate(fd,1048576);os.close(fd)`)
	if partial.Run() != nil {
		t.Fatal("interrupted fixture staging setup failed")
	}
	report, err := service.Restore(ctx, backup, target.AssetID, preimage, target.AssetID, key, true)
	if err != nil || !report.Applied {
		t.Fatal("real offline restore failed", err)
	}
	if report.Preimage.Bytes == 0 || len(report.Preimage.SHA256) != 64 {
		t.Fatal("target preimage not retained")
	}
	if docker(source, `print(open('/data/workspace/restore-proof').read())`) != "source data retained" || docker(target, `print(open('/data/workspace/restore-proof').read())`) != "source data retained" {
		t.Fatal("restore changed source or failed target checksum")
	}
	if err := host.InspectAsset(ctx, target); err != nil {
		t.Fatal("target identity marker not rebound")
	}
	var state string
	var used int64
	if err = pool.QueryRow(ctx, `SELECT status FROM storage_assets WHERE id=$1`, target.AssetID).Scan(&state); err != nil || state != "READY" {
		t.Fatal("target not safely published")
	}
	if err = pool.QueryRow(ctx, `SELECT sum(amount) FROM quota_reservations WHERE resource_type='STORAGE' AND status IN ('HELD','USED')`).Scan(&used); err != nil || used != 20<<30 {
		t.Fatal("restore bypassed storage quota")
	}
	must(`UPDATE instances SET desired_state='DELETED',observed_state='DELETED',deleted_at=clock_timestamp()-interval '8 days' WHERE id=$1`, source.InstanceID)
	must(`UPDATE storage_assets SET status='RETAINED',retention_until=clock_timestamp()-interval '1 day',legal_hold=true WHERE id=$1`, source.AssetID)
	if _, err := service.Reclaim(ctx, source.AssetID, source.AssetID, "RETENTION_EXPIRED", true); err == nil {
		t.Fatal("legal hold bypassed")
	}
	must(`UPDATE storage_assets SET legal_hold=false WHERE id=$1`, source.AssetID)
	if r, err := service.Reclaim(ctx, source.AssetID, "", "RETENTION_EXPIRED", false); err != nil || r.Applied {
		t.Fatal("reclaim dry-run failed", err)
	}
	if err = host.InspectAsset(ctx, source); err != nil {
		t.Fatal("dry-run deleted original data")
	}
	if _, err := service.Reclaim(ctx, source.AssetID, "wrong", "RETENTION_EXPIRED", true); err == nil {
		t.Fatal("wrong confirmation accepted")
	}
	must(`CREATE TRIGGER storage_publish_failure BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_storage_publish()`)
	if _, err := service.Reclaim(ctx, source.AssetID, source.AssetID, "RETENTION_EXPIRED", true); err == nil {
		t.Fatal("final reclaim audit failure committed")
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM storage_assets WHERE id=$1`, source.AssetID).Scan(&pending); err != nil || pending != "RECLAIM_PENDING" {
		t.Fatal("reclaim lost durable retry intent")
	}
	if err := pool.QueryRow(ctx, `SELECT sum(amount) FROM quota_reservations WHERE resource_type='STORAGE' AND status IN ('HELD','USED')`).Scan(&used); err != nil || used != 20<<30 {
		t.Fatal("uncommitted reclaim released quota")
	}
	must(`DROP TRIGGER storage_publish_failure ON audit_events`)
	reclaimed, err := service.Reclaim(ctx, source.AssetID, source.AssetID, "RETENTION_EXPIRED", true)
	if err != nil || !reclaimed.Applied {
		t.Fatal("owned expired reclamation failed", err)
	}
	if _, err = os.Stat(filepath.Join(o.Host.AssetRoot, source.AssetID)); !os.IsNotExist(err) {
		t.Fatal("original asset remains after confirmed reclaim")
	}
	if err = pool.QueryRow(ctx, `SELECT sum(amount) FROM quota_reservations WHERE resource_type='STORAGE' AND status IN ('HELD','USED')`).Scan(&used); err != nil || used != 10<<30 {
		t.Fatal("physical reclaim did not release exactly source storage")
	}
	if docker(target, `print(open('/data/workspace/restore-proof').read())`) != "source data retained" {
		t.Fatal("reclaim touched restored target")
	}
	// Exercise the web-authorized early release through the restricted API role
	// and real Worker host, without changing retention timestamps to fake eligibility.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = devlocal.GrantAPI(ctx, tx); err == nil {
		err = devlocal.GrantWorker(ctx, tx)
	}
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	rolePool := func(filename string) *pgxpool.Pool {
		t.Helper()
		raw, e := os.ReadFile(filepath.Join(filepath.Dir(path), filename))
		if e != nil {
			t.Fatal("restricted fixture connection unavailable")
		}
		parsed, e := url.Parse(strings.TrimSpace(string(raw)))
		if e != nil {
			t.Fatal(e)
		}
		parsed.Path = u.Path
		result, e := pgxpool.New(ctx, parsed.String())
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(result.Close)
		return result
	}
	apiRole, workerRole := rolePool("api-dsn"), rolePool("worker-dsn")
	session := webapi.ID()
	hash := sha256.Sum256([]byte(session))
	must(`INSERT INTO portal_sessions(id,secret_sha256,org_id,user_id,auth_generation,csrf_sha256,csrf_cipher,csrf_key_id,csrf_nonce,expires_at) VALUES($1,$2,$3,$4,1,$2,'x','test',$5,clock_timestamp()+interval '1 hour')`, session, hash[:], org, owner, bytes.Repeat([]byte{0}, 12))
	must(`UPDATE instances SET desired_state='DELETED',observed_state='DELETED',deleted_at=clock_timestamp() WHERE id=$1`, target.InstanceID)
	must(`UPDATE storage_assets SET status='RETAINED',retention_until=clock_timestamp()+interval '7 days' WHERE id=$1`, target.AssetID)
	var targetName string
	if err = pool.QueryRow(ctx, `SELECT name FROM instances WHERE id=$1`, target.InstanceID).Scan(&targetName); err != nil {
		t.Fatal(err)
	}
	accepted, err := instances.NewCommandService(apiRole, nil, false).ReleaseStorage(ctx, identity.Principal{UserID: owner, OrganizationID: org, Role: "MEMBER", SessionID: session, Generation: 1}, target.AssetID, instances.StorageReleaseInput{ConfirmedName: targetName, ConfirmedAssetID: target.AssetID}, "real-owner-release")
	if err != nil {
		t.Fatal("real owner early release acceptance failed", err)
	}
	releaseCtx, stopRelease := context.WithTimeout(ctx, 20*time.Second)
	done := make(chan error, 1)
	go func() { done <- worker.NewStorageReleaser(workerRole, host).Run(releaseCtx) }()
	for {
		var state string
		if err = pool.QueryRow(ctx, `SELECT status FROM storage_release_requests WHERE id=$1`, accepted.RequestID).Scan(&state); err != nil {
			stopRelease()
			<-done
			t.Fatal(err)
		}
		if state == "SUCCEEDED" {
			break
		}
		select {
		case <-releaseCtx.Done():
			stopRelease()
			<-done
			t.Fatal("real owner release did not complete", state)
		case <-time.After(100 * time.Millisecond):
		}
	}
	stopRelease()
	<-done
	if _, err = os.Stat(filepath.Join(o.Host.AssetRoot, target.AssetID)); !os.IsNotExist(err) {
		t.Fatal("real owner release left original disk present")
	}
	if err = pool.QueryRow(ctx, `SELECT coalesce(sum(amount),0) FROM quota_reservations WHERE resource_type='STORAGE' AND status IN ('HELD','USED')`).Scan(&used); err != nil || used != 0 {
		t.Fatal("real owner release did not return exactly target capacity", err)
	}
	evidence, _ := json.Marshal(map[string]any{"schema_version": 1, "same_owner_restore": true, "source_preserved": true, "target_preimage_encrypted": true, "target_identity_rebound": true, "two_assets_accounted": true, "legal_hold_rejected": true, "dry_run_preserved": true, "restore_interrupted_stage_resumed": true, "restore_late_audit_failure_barrier": true, "reclaim_late_failure_no_early_quota_release": true, "reclaim_storage_released_after_physical_absence": true, "model_requests": 0})
	if output := os.Getenv("HC_STORAGE_EVIDENCE_FILE"); output != "" {
		if err = os.WriteFile(output, evidence, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
