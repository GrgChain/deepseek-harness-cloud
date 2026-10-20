package settings

import (
	"bytes"
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/devlocal"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type automaticProbe struct {
	calls atomic.Int32
	check func(context.Context) (string, bool)
}

func (p *automaticProbe) Check(ctx context.Context, _ Model, key string) (string, bool) {
	p.calls.Add(1)
	if key != "synthetic-automatic-probe" {
		return "MODEL_AUTH_REJECTED", false
	}
	if p.check != nil {
		return p.check(ctx)
	}
	return "", false
}

func TestAutomaticProbeSharesVersionLeaseAndDailyBudget(t *testing.T) {
	path := os.Getenv("HC_TEST_DB_DSN_FILE")
	if path == "" {
		t.Skip("isolated PostgreSQL automatic-probe database required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("test connection unavailable")
	}
	u, err := url.Parse(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal("invalid test connection")
	}
	u.Path = "/hc_test_probe"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = db.Apply(ctx, u.String()); err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if err = devlocal.GrantWorker(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(filepath.Join(filepath.Dir(path), "worker-dsn"))
	if err != nil {
		t.Fatal("restricted worker connection unavailable")
	}
	appURL, _ := url.Parse(strings.TrimSpace(string(raw)))
	appURL.Path = u.Path
	pool, err := pgxpool.New(ctx, appURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	org := "00000000-0000-4000-8000-000000000081"
	must := func(sql string, args ...any) {
		t.Helper()
		if _, err := admin.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	must(`INSERT INTO organizations(id,name,status) VALUES($1,'Automatic probe test','ACTIVE') ON CONFLICT(id) DO UPDATE SET status='ACTIVE'`, org)
	must(`UPDATE instances SET desired_state='DELETED',observed_state='DELETED',deleted_at=clock_timestamp() WHERE org_id=$1`, org)
	must(`DELETE FROM probe_budgets WHERE org_id=$1`, org)
	model, secret, owner := webapi.ID(), webapi.ID(), webapi.ID()
	must(`INSERT INTO model_configuration_heads(id,org_id,current_version) VALUES($1,$2,0) ON CONFLICT(org_id) DO NOTHING`, model, org)
	var version int64
	if err = admin.QueryRow(ctx, `UPDATE model_configuration_heads SET current_version=current_version+1 WHERE org_id=$1 RETURNING id::text,current_version`, org).Scan(&model, &version); err != nil {
		t.Fatal(err)
	}
	cipher, _ := security.NewSecretCipher("test-key", bytes.Repeat([]byte{8}, 32))
	envelope, _ := cipher.Encrypt([]byte("synthetic-automatic-probe"), security.Context{Environment: "test", OrganizationID: org, RecordID: secret, Purpose: "model-key"})
	must(`INSERT INTO secrets(id,org_id,key_id,nonce,ciphertext) VALUES($1,$2,$3,$4,$5)`, secret, org, envelope.KeyID, envelope.Nonce, envelope.Ciphertext)
	must(`INSERT INTO model_configurations(org_id,id,version,provider,endpoint,protocol,model,credential_ref,status) VALUES($1,$2,$3,'deepseek-official','https://api.deepseek.com/anthropic','deepseek-messages','test-model',$4,'VALID')`, org, model, version, secret)
	must(`INSERT INTO model_validations(config_id,config_version,status) VALUES($1,$2,'UNKNOWN')`, model, version)
	must(`INSERT INTO users(id,identity_issuer,identity_subject,workspace_owner_id,status) VALUES($1,'https://probe.test',$1::uuid::text,$1,'ACTIVE')`, owner)
	must(`INSERT INTO memberships(org_id,user_id,role,status) VALUES($1,$2,'MEMBER','ACTIVE')`, org, owner)
	for range 2 {
		id := webapi.ID()
		must(`INSERT INTO instances(id,org_id,owner_id,name,external_create_key,workspace_name,desired_state,observed_state,model_config_id,model_config_version,config_snapshot) VALUES($1,$2,$3,$4,$1,$1::uuid::text,'STOPPED','STOPPED',$5,$6,'{}')`, id, org, owner, "w-"+id[:12], model, version)
	}
	policy, _ := ParsePolicy([]byte(`{"schema_version":1,"endpoints":["https://api.deepseek.com/anthropic"],"models":["test-model"]}`))
	service := NewService(pool, cipher, policy, "test", false)
	probe := &automaticProbe{}
	service.probe = probe
	auto := NewAutoValidator(service)
	must(`UPDATE model_configurations SET status='UNVALIDATED' WHERE id=$1 AND version=$2`, model, version)
	if ran, err := auto.RunOnce(ctx); err != nil || ran {
		t.Fatal("saving an unvalidated credential caused automatic requests")
	}
	must(`UPDATE model_configurations SET status='VALID' WHERE id=$1 AND version=$2`, model, version)
	must(`UPDATE organizations SET status='DISABLED' WHERE id=$1`, org)
	if ran, err := auto.RunOnce(ctx); err != nil || ran {
		t.Fatal("disabled organization remained scheduled")
	}
	must(`UPDATE organizations SET status='ACTIVE' WHERE id=$1`, org)
	must(`UPDATE instances SET desired_state='DELETED' WHERE org_id=$1`, org)
	if ran, err := auto.RunOnce(ctx); err != nil || ran {
		t.Fatal("unreferenced model caused automatic requests")
	}
	must(`UPDATE instances SET desired_state='STOPPED' WHERE org_id=$1 AND deleted_at IS NULL`, org)
	service.policy = &Policy{SchemaVersion: 1, Endpoints: policy.Endpoints, Models: []string{"different-model"}}
	if ran, err := auto.RunOnce(ctx); err != nil || ran || probe.calls.Load() != 0 {
		t.Fatal("unapproved model was probed")
	}
	service.policy = policy
	must(`UPDATE model_validations SET attempted_at=NULL WHERE config_id=$1 AND config_version=$2`, model, version)
	lease, leaseOrg, claimed, err := auto.acquire(ctx)
	if err != nil || !claimed {
		t.Fatalf("test lease unavailable: %v", err)
	}
	must(`UPDATE organizations SET status='DISABLED' WHERE id=$1`, org)
	if err = service.reserveProbe(ctx, leaseOrg, lease); problemCode(err) != "PROBE_BUDGET_EXCEEDED" {
		t.Fatalf("organization disabled after claim still reserved a request: %v", err)
	}
	must(`UPDATE organizations SET status='ACTIVE' WHERE id=$1`, org)
	must(`UPDATE model_validations SET status='UNKNOWN',attempted_at=NULL,lease_expires_at=NULL WHERE config_id=$1 AND config_version=$2`, model, version)
	results := make(chan error, 2)
	for range 2 {
		go func() { _, err := auto.RunOnce(ctx); results <- err }()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if probe.calls.Load() != 1 {
		t.Fatal("shared version issued duplicate model requests")
	}
	var status, health string
	var checked, expires, attempted time.Time
	var attempts int
	if err = admin.QueryRow(ctx, `SELECT c.status,v.status,v.checked_at,v.expires_at,v.attempted_at,b.attempts FROM model_configurations c JOIN model_validations v ON v.config_id=c.id AND v.config_version=c.version JOIN probe_budgets b ON b.org_id=c.org_id AND b.day=(clock_timestamp() AT TIME ZONE 'UTC')::date WHERE c.id=$1 AND c.version=$2`, model, version).Scan(&status, &health, &checked, &expires, &attempted, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != "VALID" || health != "HEALTHY" || attempts != 1 || expires.Sub(checked) != 5*time.Minute || !checked.Equal(attempted) {
		t.Fatal("automatic probe did not publish original evidence and budget")
	}
	if ran, err := NewAutoValidator(service).RunOnce(ctx); err != nil || ran || probe.calls.Load() != 1 {
		t.Fatal("restart bypassed persisted probe cadence")
	}
	var audits int
	if err = admin.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE org_id=$1 AND action='MODEL_PROBE' AND actor_id IS NULL`, org).Scan(&audits); err != nil || audits < 1 {
		t.Fatal("automatic probe lacks a system audit")
	}
	// A live manual/other-worker lease suppresses the scheduler even when due.
	must(`UPDATE model_validations SET attempted_at=clock_timestamp()-interval '5 minutes',status='CHECKING',lease_expires_at=clock_timestamp()+interval '40 seconds' WHERE config_id=$1 AND config_version=$2`, model, version)
	if ran, err := auto.RunOnce(ctx); err != nil || ran {
		t.Fatal("automatic probe stole a live version lease")
	}
	must(`UPDATE model_validations SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE config_id=$1 AND config_version=$2`, model, version)
	probe.check = func(context.Context) (string, bool) { return "MODEL_UPSTREAM_UNAVAILABLE", true }
	if ran, err := auto.RunOnce(ctx); err != nil || !ran {
		t.Fatalf("expired lease did not recover: %v", err)
	}
	if probe.calls.Load() != 3 {
		t.Fatal("automatic probe must stop after two attempts")
	}
	// A canceled process leaves its lease recoverable, never publishes success.
	must(`UPDATE model_validations SET attempted_at=clock_timestamp()-interval '5 minutes' WHERE config_id=$1 AND config_version=$2`, model, version)
	entered := make(chan struct{})
	probe.check = func(ctx context.Context) (string, bool) { close(entered); <-ctx.Done(); return "", false }
	stopping, stop := context.WithCancel(ctx)
	defer stop()
	go func() { results <- auto.Run(stopping) }()
	<-entered
	stop()
	if err := <-results; err != nil {
		t.Fatalf("canceled automatic probe finalized: %v", err)
	}
	if err = admin.QueryRow(ctx, `SELECT status FROM model_validations WHERE config_id=$1 AND config_version=$2`, model, version).Scan(&health); err != nil || health != "CHECKING" {
		t.Fatal("shutdown published a model result")
	}
	// A success that arrives after its deadline cannot publish either.
	must(`UPDATE model_validations SET attempted_at=clock_timestamp()-interval '5 minutes',lease_expires_at=clock_timestamp()-interval '1 second' WHERE config_id=$1 AND config_version=$2`, model, version)
	probe.check = func(context.Context) (string, bool) {
		must(`UPDATE model_validations SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE config_id=$1 AND config_version=$2`, model, version)
		return "", false
	}
	if _, err = auto.RunOnce(ctx); problemCode(err) != "STATE_CONFLICT" {
		t.Fatalf("expired automatic probe published success: %v", err)
	}
	if err = admin.QueryRow(ctx, `SELECT status FROM model_validations WHERE config_id=$1 AND config_version=$2`, model, version).Scan(&health); err != nil || health != "CHECKING" {
		t.Fatal("expired success changed validation evidence")
	}
	must(`UPDATE model_validations SET attempted_at=clock_timestamp()-interval '5 minutes' WHERE config_id=$1 AND config_version=$2`, model, version)
	must(`UPDATE probe_budgets SET attempts=1000 WHERE org_id=$1 AND day=(clock_timestamp() AT TIME ZONE 'UTC')::date`, org)
	before := probe.calls.Load()
	probe.check = nil
	if _, err = auto.RunOnce(ctx); problemCode(err) != "PROBE_BUDGET_EXCEEDED" || probe.calls.Load() != before {
		t.Fatalf("exhausted automatic budget sent a request: %v", err)
	}
	must(`UPDATE probe_budgets SET attempts=0 WHERE org_id=$1`, org)
	// Revocation after the request starts fences the eventual successful reply.
	must(`UPDATE model_validations SET attempted_at=clock_timestamp()-interval '5 minutes',lease_expires_at=clock_timestamp()-interval '1 second' WHERE config_id=$1 AND config_version=$2`, model, version)
	probe.check = func(context.Context) (string, bool) {
		must(`UPDATE secrets SET revoked_at=clock_timestamp() WHERE id=$1`, secret)
		must(`UPDATE model_configurations SET status='REVOKED',revoked_at=clock_timestamp() WHERE id=$1 AND version=$2`, model, version)
		must(`UPDATE model_validations SET status='UNHEALTHY',reason_code='CREDENTIAL_REVOKED',lease_expires_at=NULL WHERE config_id=$1 AND config_version=$2`, model, version)
		return "", false
	}
	if _, err = auto.RunOnce(ctx); problemCode(err) != "STATE_CONFLICT" {
		t.Fatalf("late probe overwrote revocation: %v", err)
	}
	if err = admin.QueryRow(ctx, `SELECT status FROM model_configurations WHERE id=$1 AND version=$2`, model, version).Scan(&status); err != nil || status != "REVOKED" {
		t.Fatal("automatic probe resurrected a revoked version")
	}
	if ran, err := auto.RunOnce(ctx); err != nil || ran {
		t.Fatal("revoked version remained scheduled")
	}
	for _, sql := range []string{`UPDATE users SET status='DISABLED'`, `UPDATE memberships SET role='ADMIN'`, `UPDATE model_configuration_heads SET current_version=0`, `UPDATE approved_templates SET enabled=true`, `CREATE TABLE probe_role_escape(id integer)`} {
		if _, err = pool.Exec(ctx, sql); err == nil {
			t.Fatal("automatic probe role gained forbidden write privilege")
		}
	}
}
