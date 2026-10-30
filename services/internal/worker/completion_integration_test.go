package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/devlocal"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/instances"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func TestTerminalPublicationIsAtomicFencedAndRetainsOriginalStorage(t *testing.T) {
	path := os.Getenv("HC_TEST_DB_DSN_FILE")
	if path == "" {
		t.Skip("isolated PostgreSQL worker database required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("test connection unavailable")
	}
	connection, _ := url.Parse(strings.TrimSpace(string(raw)))
	connection.Path = "/hc_test_worker"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = db.Apply(ctx, connection.String()); err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.New(ctx, connection.String())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	must := func(sql string, args ...any) {
		t.Helper()
		if _, err := admin.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = devlocal.GrantWorker(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	workerRaw, err := os.ReadFile(filepath.Join(filepath.Dir(path), "worker-dsn"))
	if err != nil {
		t.Fatal("restricted worker connection unavailable")
	}
	workerURL, _ := url.Parse(strings.TrimSpace(string(workerRaw)))
	workerURL.Path = connection.Path
	pool, err := pgxpool.New(ctx, workerURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	must(`UPDATE outbox_events SET status='DONE' WHERE status='PENDING'`)
	must(`UPDATE operations SET status='FAILED',lease_owner=NULL,lease_expires_at=NULL WHERE status IN ('ACCEPTED','RUNNING','UNCERTAIN')`)
	org := "00000000-0000-4000-8000-000000000061"
	owner, model, secret := webapi.ID(), webapi.ID(), webapi.ID()
	must(`INSERT INTO organizations(id,name,status) VALUES($1,'Worker fixture','ACTIVE') ON CONFLICT(id) DO NOTHING`, org)
	must(`INSERT INTO users(id,identity_issuer,identity_subject,workspace_owner_id,status) VALUES($1,'https://completion.test',$2,$1,'ACTIVE')`, owner, owner)
	must(`INSERT INTO memberships(org_id,user_id,role,status) VALUES($1,$2,'MEMBER','ACTIVE')`, org, owner)
	must(`INSERT INTO quota_policies(org_id) VALUES($1) ON CONFLICT(org_id) DO NOTHING`, org)
	must(`INSERT INTO secrets(id,org_id,key_id,nonce,ciphertext) VALUES($1,$2,'fixture',$3,'x')`, secret, org, bytes.Repeat([]byte{1}, 12))
	must(`INSERT INTO model_configuration_heads(id,org_id) VALUES($1,$2) ON CONFLICT(org_id) DO NOTHING`, model, org)
	if err := admin.QueryRow(ctx, `SELECT id::text FROM model_configuration_heads WHERE org_id=$1`, org).Scan(&model); err != nil {
		t.Fatal(err)
	}
	must(`INSERT INTO model_configurations(org_id,id,version,provider,endpoint,protocol,model,credential_ref,status) VALUES($1,$2,1,'deepseek-official','https://api.deepseek.com/anthropic','deepseek-messages','fixture',$3,'VALID') ON CONFLICT(id,version) DO NOTHING`, org, model, secret)
	must(`UPDATE secrets SET revoked_at=NULL WHERE id=(SELECT credential_ref FROM model_configurations WHERE id=$1 AND version=1)`, model)
	must(`UPDATE model_configurations SET status='VALID',revoked_at=NULL WHERE id=$1 AND version=1`, model)
	must(`INSERT INTO model_validations(config_id,config_version,status,checked_at,expires_at) VALUES($1,1,'HEALTHY',clock_timestamp(),clock_timestamp()+interval '5 minutes') ON CONFLICT(config_id,config_version) DO UPDATE SET status='HEALTHY',checked_at=EXCLUDED.checked_at,expires_at=EXCLUDED.expires_at`, model)
	snapshot := instances.Snapshot{DefaultsVersion: 1, TemplateID: webapi.ID(), TemplateVersion: webapi.ID(), ImageDigest: "sha256:" + strings.Repeat("a", 64), Spec: instances.ResourceSpec{ID: webapi.ID(), CPU: 2000, Memory: 4 << 30, Storage: 10 << 30}, ModelID: model, ModelVersion: 1, Provider: "deepseek-official", Protocol: "deepseek-messages", Model: "fixture"}
	encoded, _ := json.Marshal(struct {
		SchemaVersion int `json:"schema_version"`
		instances.Snapshot
	}{1, snapshot})
	instance, asset := webapi.ID(), webapi.ID()
	must(`INSERT INTO instances(id,org_id,owner_id,name,external_create_key,workspace_name,desired_state,observed_state,model_config_id,model_config_version,config_snapshot) VALUES($1,$2,$3,$4,$1,$4,'STOPPED','STOPPING',$5,1,$6)`, instance, org, owner, "dsh-"+instance[:12], model, encoded)
	must(`INSERT INTO storage_assets(id,org_id,owner_id,instance_id,resource_name,capacity_bytes,init_version,status) VALUES($1,$2,$3,$4,$5,10737418240,1,'READY')`, asset, org, owner, instance, "hc-"+asset)
	must(`UPDATE instances SET storage_asset_id=$2 WHERE id=$1`, instance, asset)
	cipher, _ := security.NewSecretCipher("test", bytes.Repeat([]byte{7}, 32))
	store := NewExecutionStore(pool, cipher, "test")
	leases := NewLeaseStore(pool)
	var credential string
	if err := admin.QueryRow(ctx, `SELECT credential_ref::text FROM model_configurations WHERE id=$1 AND version=1`, model).Scan(&credential); err != nil {
		t.Fatal(err)
	}
	modelKey := []byte("synthetic-completion-model-key")
	sealed, err := cipher.Encrypt(modelKey, security.Context{Environment: "test", OrganizationID: org, RecordID: credential, Purpose: "model-key"})
	if err != nil {
		t.Fatal(err)
	}
	must(`UPDATE secrets SET key_id=$2,nonce=$3,ciphertext=$4 WHERE id=$1`, credential, sealed.KeyID, sealed.Nonce, sealed.Ciphertext)
	accept := func(action, desired string, version int64) Lease {
		t.Helper()
		op := webapi.ID()
		intent := map[string]any{"schema_version": 1, "expected_instance_version": version, "target_desired_state": desired, "reason": "USER_REQUEST", "request_id": "completion-fixture", "accepted_auth_generation": 1}
		if action == "CREATE" || action == "START" || action == "CONFIGURE" {
			intent["target_snapshot"] = json.RawMessage(encoded)
		}
		if action == "DELETE" {
			intent["confirmed_name"] = "dsh-" + instance[:12]
		}
		payload, _ := json.Marshal(intent)
		must(`INSERT INTO operations(id,instance_id,actor_id,type,status,stage,intent,trace_id) VALUES($1,$2,$3,$4,'ACCEPTED','QUEUED',$5,'completion-fixture')`, op, instance, owner, action, payload)
		must(`INSERT INTO outbox_events(id,operation_id,status) VALUES($1,$2,'PENDING')`, webapi.ID(), op)
		claimed, err := leases.Claim(ctx, "completion-worker", 1, false)
		if err != nil || len(claimed) != 1 || claimed[0].ID != op {
			t.Fatalf("claim: %v %v", claimed, err)
		}
		return claimed[0]
	}
	// A stopped update is applied on the next successful start. Failed starts
	// leave the update pending; later ordinary restarts preserve user selections.
	stoppedConfig, failedStart, successfulStart := webapi.ID(), webapi.ID(), webapi.ID()
	must(`INSERT INTO operations(id,instance_id,actor_id,type,status,stage,intent,trace_id,updated_at) VALUES($1,$2,$3,'CONFIGURE','SUCCEEDED','COMPLETE','{"target_desired_state":"STOPPED"}','fixture',clock_timestamp()-interval '2 hours'),($4,$2,$3,'START','FAILED','COMPLETE','{}','fixture',clock_timestamp()-interval '1 hour')`, stoppedConfig, instance, owner, failedStart)
	checkStoppedSync := func(want bool) {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(context.Background())
		got, err := needsStoppedModelSync(ctx, tx, instance, webapi.ID())
		if err != nil || got != want {
			t.Fatalf("stopped model sync = %v, error %v, want %v", got, err, want)
		}
	}
	checkStoppedSync(true)
	must(`INSERT INTO operations(id,instance_id,actor_id,type,status,stage,intent,trace_id,updated_at) VALUES($1,$2,$3,'START','SUCCEEDED','COMPLETE','{}','fixture',clock_timestamp()-interval '30 minutes')`, successfulStart, instance, owner)
	checkStoppedSync(false)
	stopped := accept("STOP", "STOPPED", 1)
	for resource, amount := range map[string]int64{"INSTANCE": 1, "RUNNING": 1, "CPU": 2000, "MEMORY": 4 << 30, "STORAGE": 10 << 30, "CONCURRENT": 1} {
		must(`INSERT INTO quota_reservations(id,org_id,owner_id,instance_id,operation_id,resource_type,amount,status) VALUES($1,$2,$3,$4,$5,$6,$7,'USED')`, webapi.ID(), org, owner, instance, stopped.ID, resource, amount)
	}
	must(`INSERT INTO access_routes(instance_id,host,upstream_ip,runtime_generation,enabled) VALUES($1,$2,'172.20.0.2',$3,true)`, instance, "i-"+strings.ReplaceAll(instance, "-", "")+".harness.test", webapi.ID())
	wrong := stopped
	wrong.Owner = "other-worker"
	result := Completion{ComputeAbsent: true, AssetReady: true, FilesystemID: asset}
	if err := store.Complete(ctx, wrong, result); !errors.Is(err, ErrLostLease) {
		t.Fatalf("forged lease published: %v", err)
	}
	must(`UPDATE storage_assets SET status='RESTORING',maintenance_intent='{"schema_version":1}' WHERE id=$1`, asset)
	if err := store.Complete(ctx, stopped, result); !errors.Is(err, ErrInvalidEvidence) {
		t.Fatal("worker overwrote operator-owned restore state", err)
	}
	must(`UPDATE storage_assets SET status='READY',maintenance_intent=NULL WHERE id=$1`, asset)
	must(`UPDATE instances SET version=2 WHERE id=$1`, instance)
	if err := store.Complete(ctx, stopped, result); !errors.Is(err, ErrSuperseded) {
		t.Fatalf("old instance version published: %v", err)
	}
	must(`UPDATE instances SET version=1 WHERE id=$1`, instance)
	// Failing the last write must roll back instance, quota and operation together.
	must(`CREATE OR REPLACE FUNCTION completion_reject_done() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.status='DONE' THEN RAISE EXCEPTION 'completion fault injection'; END IF; RETURN NEW; END; $$`)
	must(`DROP TRIGGER IF EXISTS completion_reject_done_trigger ON outbox_events`)
	must(`CREATE TRIGGER completion_reject_done_trigger BEFORE UPDATE ON outbox_events FOR EACH ROW EXECUTE FUNCTION completion_reject_done()`)
	if err := store.Complete(ctx, stopped, result); err == nil {
		t.Fatal("late failure did not roll back")
	}
	must(`DROP TRIGGER completion_reject_done_trigger ON outbox_events`)
	var state, status, outbox string
	var version, storage, running int64
	var route bool
	if err := admin.QueryRow(ctx, `SELECT i.observed_state,i.version,o.status,e.status,r.enabled FROM instances i JOIN operations o ON o.instance_id=i.id JOIN outbox_events e ON e.operation_id=o.id JOIN access_routes r ON r.instance_id=i.id WHERE o.id=$1`, stopped.ID).Scan(&state, &version, &status, &outbox, &route); err != nil || state != "STOPPING" || version != 1 || status != "RUNNING" || outbox != "PENDING" || !route {
		t.Fatalf("partial terminal write: %s %d %s %s %v %v", state, version, status, outbox, route, err)
	}
	if plan, err := store.Checkpoint(ctx, stopped); err != nil || plan.Action != "STOP" || len(plan.ModelKey()) != 0 {
		t.Fatalf("safety checkpoint needs a model credential: %v", err)
	}
	if err := store.Complete(ctx, stopped, result); err != nil {
		t.Fatal(err)
	}
	if err := admin.QueryRow(ctx, `SELECT i.observed_state,i.version,o.status,e.status,r.enabled FROM instances i JOIN operations o ON o.instance_id=i.id JOIN outbox_events e ON e.operation_id=o.id JOIN access_routes r ON r.instance_id=i.id WHERE o.id=$1`, stopped.ID).Scan(&state, &version, &status, &outbox, &route); err != nil || state != "STOPPED" || version != 2 || status != "SUCCEEDED" || outbox != "DONE" || route {
		t.Fatalf("invalid confirmed stop: %s %d %s %s %v %v", state, version, status, outbox, route, err)
	}
	if err := admin.QueryRow(ctx, `SELECT COALESCE(sum(amount) FILTER(WHERE resource_type='STORAGE'),0),COALESCE(sum(amount) FILTER(WHERE resource_type='RUNNING'),0) FROM quota_reservations WHERE instance_id=$1 AND status IN ('HELD','USED')`, instance).Scan(&storage, &running); err != nil || storage != 10<<30 || running != 0 {
		t.Fatalf("stop ledger: %d %d %v", storage, running, err)
	}
	if err := store.Complete(ctx, stopped, result); !errors.Is(err, ErrLostLease) {
		t.Fatalf("terminal lease published twice: %v", err)
	}
	// A runtime-only default must govern deletion retention without an
	// organization model binding, using the restricted worker role.
	must(`INSERT INTO approved_templates(id,version,name,image_digest,runtime_manifest,enabled) VALUES($1,$2,'Retention fixture',$3,'{}',false)`, snapshot.TemplateID, snapshot.TemplateVersion, snapshot.ImageDigest)
	must(`INSERT INTO approved_specs(id,cpu_millicores,memory_bytes,storage_bytes,enabled) VALUES($1,2000,4294967296,10737418240,false)`, snapshot.Spec.ID)
	must(`INSERT INTO organization_defaults(org_id,version,template_id,template_version,spec_id,retention_days) VALUES($1,1,$2,$3,$4,14) ON CONFLICT(org_id) DO UPDATE SET retention_days=14`, org, snapshot.TemplateID, snapshot.TemplateVersion, snapshot.Spec.ID)
	must(`UPDATE instances SET version=3,observed_state='DELETING',desired_state='DELETED' WHERE id=$1`, instance)
	deleted := accept("DELETE", "DELETED", 3)
	if err := store.Complete(ctx, deleted, Completion{ComputeAbsent: true, WorkspaceAbsent: true, AssetReady: true, FilesystemID: asset}); err != nil {
		t.Fatal(err)
	}
	var retained bool
	var activeInstance int64
	if err := admin.QueryRow(ctx, `SELECT a.status,i.observed_state,a.retention_until>=i.deleted_at+interval '14 days' FROM storage_assets a JOIN instances i ON i.storage_asset_id=a.id WHERE i.id=$1`, instance).Scan(&status, &state, &retained); err != nil || status != "RETAINED" || state != "DELETED" || !retained {
		t.Fatalf("delete lost storage retention: %s %s %v %v", status, state, retained, err)
	}
	if err := admin.QueryRow(ctx, `SELECT COALESCE(sum(amount) FILTER(WHERE resource_type='STORAGE'),0),COALESCE(sum(amount) FILTER(WHERE resource_type='INSTANCE'),0) FROM quota_reservations WHERE instance_id=$1 AND status IN ('HELD','USED')`, instance).Scan(&storage, &activeInstance); err != nil || storage != 10<<30 || activeInstance != 0 {
		t.Fatalf("delete ledger: %d %d %v", storage, activeInstance, err)
	}
	// A new runtime can publish only fresh, generation-bound evidence and must
	// recheck current owner and credentials at commit, even after model success.
	must(`UPDATE instances SET version=5,observed_state='STARTING',desired_state='RUNNING',deleted_at=NULL WHERE id=$1`, instance)
	must(`UPDATE storage_assets SET status='READY' WHERE id=$1`, asset)
	started := accept("START", "RUNNING", 5)
	must(`UPDATE quota_reservations SET status='USED' WHERE instance_id=$1 AND resource_type='INSTANCE'`, instance)
	for resource, amount := range map[string]int64{"RUNNING": 1, "CPU": 2000, "MEMORY": 4 << 30, "CONCURRENT": 1} {
		must(`INSERT INTO quota_reservations(id,org_id,owner_id,instance_id,operation_id,resource_type,amount,status) VALUES($1,$2,$3,$4,$5,$6,$7,'HELD')`, webapi.ID(), org, owner, instance, started.ID, resource, amount)
	}
	proof := RuntimeEvidence{WorkspaceID: webapi.ID(), BuildID: webapi.ID(), Host: "i-" + strings.ReplaceAll(instance, "-", "") + ".harness.test", IP: "172.20.0.2", Generation: started.ID, NativeCookie: []byte("synthetic-native-cookie"), NativeExpiresAt: time.Now().Add(time.Hour), CheckedAt: time.Now().UTC()}
	plan, err := store.Checkpoint(ctx, started)
	if err != nil || !bytes.Equal(plan.ModelKey(), modelKey) || plan.RuntimeGeneration != started.ID {
		t.Fatalf("runtime checkpoint: %v", err)
	}
	if _, err := json.Marshal(plan); err == nil {
		t.Fatal("private plan can be serialized")
	}
	if strings.Contains(fmt.Sprintf("%+v %#v", plan, plan), string(modelKey)) {
		t.Fatal("private plan formatting leaked the credential")
	}
	borrowed := plan.ModelKey()
	plan.Destroy()
	if !bytes.Equal(borrowed, make([]byte, len(modelKey))) {
		t.Fatal("plan credential was not cleared")
	}
	for _, bad := range []string{"stale", "future", "public-ip", "foreign-host", "generation", "filesystem"} {
		badProof := proof
		badResult := Completion{AssetReady: true, FilesystemID: asset, Running: &badProof}
		switch bad {
		case "stale":
			badProof.CheckedAt = time.Now().Add(-61 * time.Second)
		case "future":
			badProof.CheckedAt = time.Now().Add(time.Minute)
		case "public-ip":
			badProof.IP = "8.8.8.8"
		case "foreign-host":
			badProof.Host = "i-foreign.harness.test"
		case "generation":
			badProof.Generation = webapi.ID()
		case "filesystem":
			badResult.FilesystemID = webapi.ID()
		}
		if err := store.Complete(ctx, started, badResult); !errors.Is(err, ErrInvalidEvidence) {
			t.Fatalf("invalid %s completion was accepted: %v", bad, err)
		}
	}
	if err := store.RememberExternal(ctx, started, proof.WorkspaceID, proof.BuildID); err != nil {
		t.Fatal(err)
	}
	if err := store.RememberExternal(ctx, started, webapi.ID(), proof.BuildID); !errors.Is(err, ErrInvalidEvidence) {
		t.Fatalf("workspace binding was changed: %v", err)
	}
	if err := store.Complete(ctx, started, Completion{AssetReady: true, FilesystemID: asset, Running: &proof}); err != nil {
		t.Fatal(err)
	}
	var encrypted []byte
	if err := admin.QueryRow(ctx, `SELECT cookie_ciphertext FROM native_sessions WHERE instance_id=$1 AND runtime_generation=$2`, instance, proof.Generation).Scan(&encrypted); err != nil || bytes.Contains(encrypted, proof.NativeCookie) {
		t.Fatal("native credential was not encrypted and generation-bound")
	}
	must(`UPDATE instances SET version=7,observed_state='STOPPING',desired_state='RUNNING' WHERE id=$1`, instance)
	configured := accept("CONFIGURE", "RUNNING", 7)
	attempt, fresh, err := store.BeginBuild(ctx, configured, proof.BuildID, 1, "stop", "CONFIGURE_STOP")
	if err != nil || !fresh || attempt.BeforeID != proof.BuildID {
		t.Fatalf("build intent was not durably reserved: %+v %v", attempt, err)
	}
	must(`UPDATE operations SET intent=jsonb_set(intent,'{external_build,template_version}',to_jsonb($2::text)) WHERE id=$1`, configured.ID, webapi.ID())
	if _, err := store.Checkpoint(ctx, configured); !errors.Is(err, ErrInvalidEvidence) {
		t.Fatalf("build intent changed its frozen template: %v", err)
	}
	must(`UPDATE operations SET intent=jsonb_set(intent,'{external_build,template_version}',to_jsonb($2::text)) WHERE id=$1`, configured.ID, snapshot.TemplateVersion)
	repeated, fresh, err := store.BeginBuild(ctx, configured, webapi.ID(), 2, "stop", "CONFIGURE_STOP")
	if err != nil || fresh || repeated.BeforeID != attempt.BeforeID {
		t.Fatalf("unknown build intent was reissued: %+v %v", repeated, err)
	}
	if _, _, err := store.BeginBuild(ctx, configured, webapi.ID(), 2, "start", "CONFIGURE_START"); !errors.Is(err, ErrExternalPending) {
		t.Fatalf("unconfirmed stop advanced to start: %v", err)
	}
	stopBuild := webapi.ID()
	if err := store.RememberExternal(ctx, configured, proof.WorkspaceID, stopBuild); err != nil {
		t.Fatal(err)
	}
	if err := store.ConfirmBuild(ctx, configured, webapi.ID()); !errors.Is(err, ErrInvalidEvidence) {
		t.Fatalf("foreign build completed: %v", err)
	}
	if err := store.ConfirmBuild(ctx, configured, stopBuild); err != nil {
		t.Fatal(err)
	}
	if next, fresh, err := store.BeginBuild(ctx, configured, stopBuild, 2, "start", "CONFIGURE_START"); err != nil || !fresh || next.BeforeID != stopBuild {
		t.Fatalf("confirmed configure stop did not advance: %+v %v", next, err)
	}

	must(`UPDATE secrets SET revoked_at=clock_timestamp() WHERE id=(SELECT credential_ref FROM model_configurations WHERE id=$1 AND version=1)`, model)
	proof.CheckedAt = time.Now().UTC()
	proof.Generation = configured.ID
	if err := store.Complete(ctx, configured, Completion{AssetReady: true, FilesystemID: asset, Running: &proof}); !errors.Is(err, ErrExecutionRevoked) {
		t.Fatalf("revoked runtime was republished: %v", err)
	}
	must(`UPDATE secrets SET revoked_at=NULL WHERE id=(SELECT credential_ref FROM model_configurations WHERE id=$1 AND version=1)`, model)
	must(`UPDATE memberships SET status='DISABLED' WHERE org_id=$1 AND user_id=$2`, org, owner)
	if err := store.Complete(ctx, configured, Completion{AssetReady: true, FilesystemID: asset, Running: &proof}); !errors.Is(err, ErrExecutionRevoked) {
		t.Fatalf("disabled owner was republished: %v", err)
	}
	must(`UPDATE instances SET version=8,desired_state='STOPPED' WHERE id=$1`, instance)
	if err := store.Fail(ctx, configured, "AUTH_REVOKED", Completion{}); !errors.Is(err, ErrInvalidEvidence) {
		t.Fatalf("unknown compute released quota: %v", err)
	}
	if err := store.Fail(ctx, configured, "raw-upstream-secret", Completion{ComputeAbsent: true}); !errors.Is(err, ErrInvalidEvidence) {
		t.Fatalf("unsafe error was accepted: %v", err)
	}
	if err := store.Fail(ctx, configured, "AUTH_REVOKED", Completion{ComputeAbsent: true, AssetReady: true, FilesystemID: asset}); err != nil {
		t.Fatal(err)
	}
	if err := admin.QueryRow(ctx, `SELECT i.observed_state,o.status,e.status,r.enabled FROM instances i JOIN operations o ON o.instance_id=i.id JOIN outbox_events e ON e.operation_id=o.id JOIN access_routes r ON r.instance_id=i.id WHERE o.id=$1`, configured.ID).Scan(&state, &status, &outbox, &route); err != nil || state != "STOPPED" || status != "FAILED" || outbox != "DONE" || route {
		t.Fatalf("revoked operation did not close after verified cleanup: %s %s %s %v %v", state, status, outbox, route, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE memberships SET role='ADMIN' WHERE org_id=$1 AND user_id=$2`, org, owner); err == nil {
		t.Fatal("worker can change membership roles")
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET status='ACTIVE' WHERE id=$1`, owner); err == nil {
		t.Fatal("worker can modify user identity")
	}
}
