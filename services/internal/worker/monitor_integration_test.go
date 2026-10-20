package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
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
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/settings"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type monitorFixture struct {
	ctx                context.Context
	admin, pool        *pgxpool.Pool
	cipher             *security.SecretCipher
	org, model, secret string
	version            int64
}

func newMonitorFixture(t *testing.T) *monitorFixture {
	t.Helper()
	path := os.Getenv("HC_TEST_DB_DSN_FILE")
	if path == "" {
		t.Skip("isolated monitor PostgreSQL database required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("test connection unavailable")
	}
	parsed, err := url.Parse(string(bytes.TrimSpace(raw)))
	if err != nil {
		t.Fatal("invalid test connection")
	}
	parsed.Path = "/hc_test_monitor"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	if err = db.Apply(ctx, parsed.String()); err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.New(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
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
	raw, err = os.ReadFile(filepath.Join(filepath.Dir(path), "worker-dsn"))
	if err != nil {
		t.Fatal("restricted connection unavailable")
	}
	role, err := url.Parse(string(bytes.TrimSpace(raw)))
	if err != nil {
		t.Fatal("invalid restricted connection")
	}
	role.Path = parsed.Path
	pool, err := pgxpool.New(ctx, role.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	cipher, _ := security.NewSecretCipher("test", bytes.Repeat([]byte{4}, 32))
	f := &monitorFixture{ctx: ctx, admin: admin, pool: pool, cipher: cipher, org: "00000000-0000-4000-8000-000000000062", model: webapi.ID(), secret: webapi.ID()}
	// This database contains synthetic monitor fixtures only, no external compute.
	f.exec(t, `UPDATE outbox_events SET status='DONE' WHERE status='PENDING'`)
	f.exec(t, `UPDATE operations SET status='FAILED',lease_owner=NULL,lease_expires_at=NULL WHERE status IN ('ACCEPTED','RUNNING','UNCERTAIN')`)
	f.exec(t, `UPDATE instances SET observed_state='DELETED',desired_state='DELETED',deleted_at=clock_timestamp() WHERE deleted_at IS NULL`)
	f.exec(t, `INSERT INTO organizations(id,name,status) VALUES($1,'Monitor fixture','ACTIVE') ON CONFLICT(id) DO UPDATE SET status='ACTIVE'`, f.org)
	f.exec(t, `INSERT INTO model_configuration_heads(id,org_id) VALUES($1,$2) ON CONFLICT(org_id) DO NOTHING`, f.model, f.org)
	if err = admin.QueryRow(ctx, `SELECT id::text FROM model_configuration_heads WHERE org_id=$1`, f.org).Scan(&f.model); err != nil {
		t.Fatal(err)
	}
	if err = admin.QueryRow(ctx, `SELECT COALESCE(max(version),0)+1 FROM model_configurations WHERE id=$1`, f.model).Scan(&f.version); err != nil {
		t.Fatal(err)
	}
	sealed, err := cipher.Encrypt([]byte("synthetic-monitor-key"), security.Context{Environment: "test", OrganizationID: f.org, RecordID: f.secret, Purpose: "model-key"})
	if err != nil {
		t.Fatal(err)
	}
	f.exec(t, `INSERT INTO secrets(id,org_id,key_id,nonce,ciphertext) VALUES($1,$2,$3,$4,$5)`, f.secret, f.org, sealed.KeyID, sealed.Nonce, sealed.Ciphertext)
	f.exec(t, `INSERT INTO model_configurations(org_id,id,version,provider,endpoint,protocol,model,credential_ref,status) VALUES($1,$2,$3,'deepseek-official','https://api.deepseek.com/anthropic','deepseek-messages','deepseek-flash',$4,'VALID')`, f.org, f.model, f.version, f.secret)
	f.exec(t, `INSERT INTO model_validations(config_id,config_version,status,checked_at,expires_at) VALUES($1,$2,'HEALTHY',clock_timestamp(),clock_timestamp()+interval '5 minutes')`, f.model, f.version)
	f.exec(t, `UPDATE model_configuration_heads SET current_version=$2 WHERE id=$1`, f.model, f.version)
	f.exec(t, `INSERT INTO quota_policies(org_id) VALUES($1) ON CONFLICT(org_id) DO NOTHING`, f.org)
	return f
}
func (f *monitorFixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := f.admin.Exec(f.ctx, query, args...); err != nil {
		t.Fatal(err)
	}
}
func (f *monitorFixture) instance(t *testing.T) (string, string) {
	t.Helper()
	id, owner, asset, session := webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID()
	f.exec(t, `INSERT INTO users(id,identity_issuer,identity_subject,workspace_owner_id,status) VALUES($1,'https://monitor.test',$2,$1,'ACTIVE')`, owner, owner)
	f.exec(t, `INSERT INTO memberships(org_id,user_id,role,status) VALUES($1,$2,'MEMBER','ACTIVE')`, f.org, owner)
	snapshot := instances.Snapshot{DefaultsVersion: 1, TemplateID: webapi.ID(), TemplateVersion: webapi.ID(), ImageDigest: "sha256:" + string(bytes.Repeat([]byte{'a'}, 64)), Spec: instances.ResourceSpec{ID: webapi.ID(), CPU: 2000, Memory: 4 << 30, Storage: 10 << 30}, ModelID: f.model, ModelVersion: f.version, Provider: "deepseek-official", Protocol: "deepseek-messages", Model: "deepseek-flash"}
	raw, _ := json.Marshal(struct {
		SchemaVersion int `json:"schema_version"`
		instances.Snapshot
	}{1, snapshot})
	f.exec(t, `INSERT INTO instances(id,org_id,owner_id,name,external_create_key,workspace_name,workspace_id,desired_state,observed_state,model_config_id,model_config_version,config_snapshot) VALUES($1,$2,$3,$4,$1,$4,$5,'RUNNING','RUNNING',$6,$7,$8)`, id, f.org, owner, "dsh-"+id[:12], webapi.ID(), f.model, f.version, raw)
	f.exec(t, `INSERT INTO storage_assets(id,org_id,owner_id,instance_id,resource_name,capacity_bytes,init_version,filesystem_id,status) VALUES($1,$2,$3,$4,$5,10737418240,1,$6,'READY')`, asset, f.org, owner, id, "hc-"+asset, asset)
	f.exec(t, `UPDATE instances SET storage_asset_id=$2 WHERE id=$1`, id, asset)
	generation := webapi.ID()
	f.exec(t, `INSERT INTO access_routes(instance_id,host,upstream_ip,runtime_generation,enabled) VALUES($1,$2,'172.20.0.2',$3,true)`, id, "i-"+strings.ReplaceAll(id, "-", "")+".harness.test", generation)
	sealed, err := f.cipher.Encrypt([]byte("synthetic-native-cookie"), security.Context{Environment: "test", OrganizationID: f.org, RecordID: id + ":" + generation, Purpose: "native-cookie"})
	if err != nil {
		t.Fatal(err)
	}
	f.exec(t, `INSERT INTO native_sessions(instance_id,runtime_generation,key_id,nonce,cookie_ciphertext,expires_at) VALUES($1,$2,$3,$4,$5,clock_timestamp()+interval '1 hour')`, id, generation, sealed.KeyID, sealed.Nonce, sealed.Ciphertext)
	hash := sha256.Sum256([]byte(session))
	f.exec(t, `INSERT INTO portal_sessions(id,secret_sha256,org_id,user_id,auth_generation,csrf_sha256,csrf_cipher,csrf_key_id,csrf_nonce,expires_at) VALUES($1,$2,$3,$4,1,$2,'x','fixture',$5,clock_timestamp()+interval '1 hour')`, session, hash[:], f.org, owner, bytes.Repeat([]byte{0}, 12))
	f.exec(t, `INSERT INTO access_sessions(id,secret_sha256,portal_session_id,instance_id,host,auth_generation,access_generation,expires_at) VALUES($1,$2,$3,$4,$5,1,1,clock_timestamp()+interval '30 minutes')`, webapi.ID(), hash[:], session, id, "i-"+strings.ReplaceAll(id, "-", "")+".harness.test")
	original := generation
	intent, _ := json.Marshal(Intent{SchemaVersion: 1, ExpectedVersion: 1, Desired: "RUNNING", Reason: "USER_REQUEST", RequestID: "fixture-created", AcceptedGeneration: ptrInt64(1), Target: raw})
	f.exec(t, `INSERT INTO operations(id,instance_id,actor_id,type,status,stage,intent,trace_id,build_id,reconciled_at) VALUES($1,$2,$3,'CREATE','SUCCEEDED','COMPLETE',$4,'fixture-created',$5,clock_timestamp())`, original, id, owner, intent, webapi.ID())
	for _, r := range []struct {
		name   string
		amount int64
	}{{"INSTANCE", 1}, {"STORAGE", 10 << 30}, {"CPU", 2000}, {"MEMORY", 4 << 30}, {"RUNNING", 1}} {
		f.exec(t, `INSERT INTO quota_reservations(id,org_id,owner_id,instance_id,resource_type,amount,operation_id,status) VALUES($1,$2,$3,$4,$5,$6,$7,'USED')`, webapi.ID(), f.org, owner, id, r.name, r.amount, original)
	}
	return id, owner
}
func TestSafetyScannerCompensatesRevocationWithoutDuplicateOperations(t *testing.T) {
	f := newMonitorFixture(t)
	store := NewSafetyStore(f.pool)
	healthy, healthyOwner := f.instance(t)
	disabled, disabledOwner := f.instance(t)
	active, activeOwner := f.instance(t)
	deleting, deletingOwner := f.instance(t)
	f.exec(t, `UPDATE memberships SET role='ADMIN',auth_generation=auth_generation+1 WHERE org_id=$1 AND user_id=$2`, f.org, healthyOwner)
	for _, owner := range []string{disabledOwner, activeOwner, deletingOwner} {
		f.exec(t, `UPDATE memberships SET status='DISABLED' WHERE org_id=$1 AND user_id=$2`, f.org, owner)
	}
	f.exec(t, `UPDATE instances SET desired_state='DELETED',observed_state='DELETING' WHERE id=$1`, deleting)
	op := webapi.ID()
	intent, _ := json.Marshal(Intent{SchemaVersion: 1, ExpectedVersion: 1, Desired: "RUNNING", Reason: "USER_REQUEST", RequestID: "monitor-active", AcceptedGeneration: ptrInt64(1), Target: mustSnapshot(t, f, active)})
	f.exec(t, `INSERT INTO operations(id,instance_id,actor_id,type,status,stage,intent,trace_id) VALUES($1,$2,$3,'START','ACCEPTED','QUEUED',$4,'monitor-active')`, op, active, activeOwner, intent)
	f.exec(t, `INSERT INTO outbox_events(id,operation_id,status) VALUES($1,$2,'PENDING')`, webapi.ID(), op)
	var cursor string
	for i := 0; i < 4; i++ {
		next, err := store.Scan(f.ctx, cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		cursor = next
		if cursor == "" {
			break
		}
	}
	var system int
	if err := f.admin.QueryRow(f.ctx, `SELECT count(*) FROM operations WHERE actor_kind='SYSTEM' AND status='ACCEPTED'`).Scan(&system); err != nil || system != 2 {
		t.Fatalf("safety STOP count=%d error=%v", system, err)
	}
	for _, id := range []string{disabled, active, deleting} {
		var version int64
		var enabled, revoked, nativeAbsent bool
		if err := f.admin.QueryRow(f.ctx, `SELECT i.version,r.enabled,NOT EXISTS(SELECT 1 FROM access_sessions WHERE instance_id=i.id AND revoked_at IS NULL),NOT EXISTS(SELECT 1 FROM native_sessions WHERE instance_id=i.id) FROM instances i JOIN access_routes r ON r.instance_id=i.id WHERE i.id=$1`, id).Scan(&version, &enabled, &revoked, &nativeAbsent); err != nil || version != 2 || enabled || !revoked || !nativeAbsent {
			t.Fatalf("revocation was not fully blocked: %v", err)
		}
	}
	var healthyDesired, activeCode string
	if err := f.admin.QueryRow(f.ctx, `SELECT desired_state FROM instances WHERE id=$1`, healthy).Scan(&healthyDesired); err != nil || healthyDesired != "RUNNING" {
		t.Fatal("role change stopped an authorized Owner")
	}
	if err := f.admin.QueryRow(f.ctx, `SELECT error_code FROM operations WHERE id=$1`, op).Scan(&activeCode); err != nil || activeCode != "AUTH_REVOKED" {
		t.Fatal("active unsafe operation was not directed to cleanup")
	}
	_, err := store.Scan(f.ctx, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	var duplicate int
	if err = f.admin.QueryRow(f.ctx, `SELECT count(*) FROM operations WHERE instance_id=$1 AND status IN ('ACCEPTED','RUNNING','UNCERTAIN')`, active).Scan(&duplicate); err != nil || duplicate != 1 {
		t.Fatal("scanner created a second active operation")
	}
	var stableVersion int64
	if err = f.admin.QueryRow(f.ctx, `SELECT version FROM instances WHERE id=$1`, disabled).Scan(&stableVersion); err != nil || stableVersion != 2 {
		t.Fatal("repeat scan churned the security generation")
	}
	// A security stop can finish physical cleanup while preserving DELETE intent.
	leases := NewLeaseStore(f.pool)
	claimed, err := leases.Claim(f.ctx, "monitor-safety", 2, true)
	if err != nil || len(claimed) != 2 {
		t.Fatal("safety lane could not claim compensation")
	}
	execution := NewExecutionStore(f.pool, f.cipher, "test")
	for _, lease := range claimed {
		if err = execution.Complete(f.ctx, lease, Completion{ComputeAbsent: true}); err != nil {
			t.Fatal(err)
		}
	}
	var desired, state string
	if err = f.admin.QueryRow(f.ctx, `SELECT desired_state,observed_state FROM instances WHERE id=$1`, deleting).Scan(&desired, &state); err != nil || desired != "DELETED" || state != "STOPPED" {
		t.Fatal("safety cleanup discarded the user's delete intent")
	}
	var compute, storage int64
	if err = f.admin.QueryRow(f.ctx, `SELECT COALESCE(sum(amount) FILTER(WHERE resource_type='CPU'),0),COALESCE(sum(amount) FILTER(WHERE resource_type='STORAGE'),0) FROM quota_reservations WHERE instance_id=$1 AND status IN ('HELD','USED')`, disabled).Scan(&compute, &storage); err != nil || compute != 0 || storage != 10<<30 {
		t.Fatal("safety stop failed to release only confirmed compute")
	}
	// Credentials can be revoked outside the API transaction; scans compensate.
	f.exec(t, `UPDATE secrets SET revoked_at=clock_timestamp() WHERE id=$1`, f.secret)
	_, err = store.Scan(f.ctx, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	var reason string
	if err = f.admin.QueryRow(f.ctx, `SELECT intent->>'reason' FROM operations WHERE instance_id=$1 AND actor_kind='SYSTEM'`, healthy).Scan(&reason); err != nil || reason != "CREDENTIAL_REVOKED" {
		t.Fatal("external credential revocation was not compensated")
	}
	// Late outbox failure rolls back the instance/access/audit writes as a unit.
	late, owner := f.instance(t)
	f.exec(t, `UPDATE memberships SET status='DISABLED' WHERE org_id=$1 AND user_id=$2`, f.org, owner)
	f.exec(t, `CREATE OR REPLACE FUNCTION monitor_reject_outbox() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture'; END $$; CREATE TRIGGER monitor_reject BEFORE INSERT ON outbox_events FOR EACH ROW EXECUTE FUNCTION monitor_reject_outbox()`)
	if _, err = store.Scan(f.ctx, "", 10); err == nil {
		t.Fatal("late outbox failure was ignored")
	}
	f.exec(t, `DROP TRIGGER monitor_reject ON outbox_events; DROP FUNCTION monitor_reject_outbox()`)
	var lateVersion int64
	if err = f.admin.QueryRow(f.ctx, `SELECT version FROM instances WHERE id=$1`, late).Scan(&lateVersion); err != nil || lateVersion != 1 {
		t.Fatal("failed compensation partially committed security intent")
	}
}
func ptrInt64(n int64) *int64 { return &n }
func mustSnapshot(t *testing.T, f *monitorFixture, id string) json.RawMessage {
	t.Helper()
	var raw json.RawMessage
	if err := f.admin.QueryRow(f.ctx, `SELECT config_snapshot FROM instances WHERE id=$1`, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestHealthRefreshDiscardsStaleResultsAndNeverReopensRoutes(t *testing.T) {
	f := newMonitorFixture(t)
	id, owner := f.instance(t)
	store := NewHealthStore(f.pool, f.cipher, "test", 0)
	plan, err := store.Load(f.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Destroy()
	healthy := HealthResult{Checks: map[instances.Dimension]HealthObservation{instances.Application: {Status: instances.Healthy}, instances.Storage: {Status: instances.Healthy}, instances.Configuration: {Status: instances.Healthy}, instances.Access: {Status: instances.Healthy}}}
	if string(plan.ModelKey()) != "synthetic-monitor-key" || len(plan.Cookie) == 0 {
		t.Fatal("trusted health plan did not load private credentials")
	}
	if raw, err := json.Marshal(plan); err == nil || bytes.Contains(raw, []byte("synthetic")) {
		t.Fatal("health plan could be serialized with credentials")
	}
	if saved, err := store.Publish(f.ctx, plan, healthy); err != nil || !saved {
		t.Fatalf("fresh health not published: %v", err)
	}
	var checked, expires time.Time
	var version int64
	if err = f.admin.QueryRow(f.ctx, `SELECT h.checked_at,h.expires_at,i.version FROM instance_health h JOIN instances i ON i.id=h.instance_id WHERE i.id=$1 AND h.dimension='application'`, id).Scan(&checked, &expires, &version); err != nil || version != plan.Version || !checked.Equal(plan.Started) || expires.Sub(checked) != 60*time.Second {
		t.Fatal("refresh changed business version or extended freshness beyond probe start")
	}
	newer, err := store.Load(f.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer newer.Destroy()
	degraded := HealthResult{Checks: map[instances.Dimension]HealthObservation{instances.Application: {Status: instances.Unhealthy, Reason: "APPLICATION_UNAVAILABLE"}, instances.Storage: {Status: instances.Healthy}, instances.Configuration: {Status: instances.Healthy}, instances.Access: {Status: instances.Unhealthy, Reason: "ACCESS_UNAVAILABLE"}}}
	if saved, err := store.Publish(f.ctx, newer, degraded); err != nil || !saved {
		t.Fatal("new health degradation was not published")
	}
	if saved, err := store.Publish(f.ctx, plan, healthy); err != nil || saved {
		t.Fatal("older success overwrote a newer failure")
	}
	late, err := store.Load(f.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer late.Destroy()
	f.exec(t, `UPDATE instances SET version=version+1 WHERE id=$1`, id)
	if saved, err := store.Publish(f.ctx, late, healthy); err != nil || saved {
		t.Fatal("a superseded instance accepted late success")
	}
	current, err := store.Load(f.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Destroy()
	f.exec(t, `UPDATE access_routes SET runtime_generation=$2 WHERE instance_id=$1`, id, webapi.ID())
	if saved, err := store.Publish(f.ctx, current, healthy); err != nil || saved {
		t.Fatal("old runtime generation accepted late success")
	}
	f.exec(t, `UPDATE access_routes SET runtime_generation=$2,enabled=false WHERE instance_id=$1`, id, current.RuntimeGeneration)
	if saved, err := store.Publish(f.ctx, current, healthy); err != nil || saved {
		t.Fatal("health refresh reopened a disabled route")
	}
	var enabled bool
	if err = f.admin.QueryRow(f.ctx, `SELECT enabled FROM access_routes WHERE instance_id=$1`, id).Scan(&enabled); err != nil || enabled {
		t.Fatal("route was silently reopened")
	}
	f.exec(t, `UPDATE access_routes SET enabled=true WHERE instance_id=$1`, id)
	revoked, err := store.Load(f.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer revoked.Destroy()
	f.exec(t, `UPDATE secrets SET revoked_at=clock_timestamp() WHERE id=$1`, f.secret)
	if saved, err := store.Publish(f.ctx, revoked, healthy); err != nil || saved {
		t.Fatal("credential revocation accepted stale success")
	}
	if _, err = store.Load(f.ctx, id); !errors.Is(err, ErrExecutionRevoked) {
		t.Fatal("revoked credentials were still loaded")
	}
	f.exec(t, `UPDATE secrets SET revoked_at=NULL WHERE id=$1`, f.secret)
	disabled, err := store.Load(f.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer disabled.Destroy()
	f.exec(t, `UPDATE memberships SET status='DISABLED' WHERE org_id=$1 AND user_id=$2`, f.org, owner)
	if saved, err := store.Publish(f.ctx, disabled, healthy); err != nil || saved {
		t.Fatal("disabled Owner accepted stale success")
	}
	f.exec(t, `UPDATE memberships SET status='ACTIVE' WHERE org_id=$1 AND user_id=$2`, f.org, owner)
	malformed, err := store.Load(f.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer malformed.Destroy()
	unsafe := healthy
	unsafe.Checks = map[instances.Dimension]HealthObservation{instances.Application: {Status: instances.Unhealthy, Reason: "raw-secret-body"}}
	if _, err = store.Publish(f.ctx, malformed, unsafe); !errors.Is(err, ErrInvalidEvidence) {
		t.Fatal("incomplete or unsafe diagnostics were accepted")
	}
	f.exec(t, `UPDATE model_validations SET status='UNHEALTHY',reason_code='MODEL_TRANSPORT_UNAVAILABLE' WHERE config_id=$1 AND config_version=$2`, f.model, f.version)
	diagnostic, err := store.Load(f.ctx, id)
	if err != nil {
		t.Fatal("model degradation blocked diagnostic health checks")
	}
	defer diagnostic.Destroy()
	if saved, err := store.Publish(f.ctx, diagnostic, healthy); err != nil || !saved {
		t.Fatal("model degradation incorrectly disabled native diagnostics")
	}
	var modelStatus string
	if err = f.admin.QueryRow(f.ctx, `SELECT status FROM instance_health WHERE instance_id=$1 AND dimension='model'`, id).Scan(&modelStatus); err != nil || modelStatus != "UNHEALTHY" {
		t.Fatal("refresh hid degraded model health")
	}
	// Native session renewal is encrypted and bound to the unchanged runtime.
	renewed, err := store.Load(f.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer renewed.Destroy()
	renewal := healthy
	renewal.cookie = []byte(nativeCookieName(renewed.Host) + "=v1.synthetic.signature")
	renewal.expires = time.Now().Add(time.Hour)
	if saved, err := store.Publish(f.ctx, renewed, renewal); err != nil || !saved {
		t.Fatal("native renewal was not published")
	}
	var cipher []byte
	if err = f.admin.QueryRow(f.ctx, `SELECT cookie_ciphertext FROM native_sessions WHERE instance_id=$1`, id).Scan(&cipher); err != nil || bytes.Contains(cipher, renewal.cookie) {
		t.Fatal("native renewal was persisted in plaintext")
	}
}

func TestSafetyStopKeepsUnconfirmedStorageReservationHeld(t *testing.T) {
	f := newMonitorFixture(t)
	id, owner := f.instance(t)
	f.exec(t, `UPDATE storage_assets SET status='PREPARING',init_version=0,filesystem_id=NULL WHERE instance_id=$1`, id)
	f.exec(t, `UPDATE quota_reservations SET status='HELD' WHERE instance_id=$1 AND resource_type='STORAGE'`, id)
	f.exec(t, `UPDATE memberships SET status='DISABLED' WHERE org_id=$1 AND user_id=$2`, f.org, owner)
	if _, err := NewSafetyStore(f.pool).Scan(f.ctx, "", 10); err != nil {
		t.Fatal(err)
	}
	leases, err := NewLeaseStore(f.pool).Claim(f.ctx, "monitor-safety", 1, true)
	if err != nil || len(leases) != 1 {
		t.Fatal("safety task missing")
	}
	if err = NewExecutionStore(f.pool, f.cipher, "test").Complete(f.ctx, leases[0], Completion{ComputeAbsent: true}); err != nil {
		t.Fatal(err)
	}
	var status string
	if err = f.admin.QueryRow(f.ctx, `SELECT status FROM quota_reservations WHERE instance_id=$1 AND resource_type='STORAGE'`, id).Scan(&status); err != nil || status != "HELD" {
		t.Fatal("safety cleanup falsely confirmed allocation of an unverified original asset")
	}
}

func TestHealthLoadRetainsFrozenPrivateModelApproval(t *testing.T) {
	f := newMonitorFixture(t)
	id, _ := f.instance(t)
	policy, err := settings.ParsePolicy([]byte(`{"schema_version":1,"gateways":[{"endpoint":"https://models.enterprise.example:14000","revision":"v1","addresses":["10.50.0.10/32"],"models":[],"probe_retries_disabled":true,"probe_fallbacks_disabled":true}]}`))
	if err != nil {
		t.Fatal(err)
	}
	gateway, _ := policy.Gateway(policy.Gateways[0].Endpoint)
	manifest := gateway.Manifest(settings.GatewayModel{ID: "owner-private-alias", ResponseModels: []string{"upstream-model"}, ContextWindow: 32768, MaxOutputTokens: 4096, Streaming: true, ToolUse: true})
	raw, _ := json.Marshal(manifest)
	f.exec(t, `UPDATE model_configurations SET provider='platform-models',protocol='anthropic-messages',endpoint=$3,model=$4,manifest=$5 WHERE id=$1 AND version=$2`, f.model, f.version, gateway.Endpoint, manifest.ID, raw)
	f.exec(t, `UPDATE instances SET config_snapshot=config_snapshot || jsonb_build_object('provider','platform-models','protocol','anthropic-messages','model',$2::text,'model_approval_digest',$3::text) WHERE id=$1`, id, manifest.ID, manifest.Digest)
	store := NewHealthStore(f.pool, f.cipher, "test", 0)
	plan, err := store.Load(f.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Destroy()
	if plan.modelManifest == nil || plan.modelManifest.Digest != manifest.Digest {
		t.Fatal("health load discarded the approved private model manifest")
	}
	root := t.TempDir()
	os.Chmod(root, 0700)
	host, err := NewLocalHost(HostOptions{RuntimeRoot: root, AssetRoot: root, RuntimeImage: plan.Snapshot.ImageDigest, StorageImage: plan.Snapshot.ImageDigest, NetworkImage: plan.Snapshot.ImageDigest, Domain: "harness.test", StorageBudget: 100 << 30})
	if err != nil {
		t.Fatal(err)
	}
	host.WithModelPolicy(policy)
	if configuration, err := host.runtimeConfiguration(plan.ExecutionPlan); err != nil || configuration.SchemaVersion != 2 || configuration.Model != manifest.ID {
		t.Fatal("healthy private workspace cannot reconstruct its pinned runtime configuration", err)
	}
	f.exec(t, `UPDATE model_configurations SET manifest=manifest || '{"unknown":true}'::jsonb WHERE id=$1 AND version=$2`, f.model, f.version)
	if bad, err := store.Load(f.ctx, id); err == nil {
		bad.Destroy()
		t.Fatal("malformed private approval accepted by health loader")
	}
}
