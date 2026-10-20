package access

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
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/devlocal"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/instances"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	workspaceengine "github.com/jonluo94/deepseek-harness-cloud/services/internal/workspace"
)

func TestChallengeTicketConsumptionAndLiveSessionWithRestrictedRoles(t *testing.T) {
	path := os.Getenv("HC_TEST_DB_DSN_FILE")
	if path == "" {
		t.Skip("isolated PostgreSQL access database required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("test connection unavailable")
	}
	u, err := url.Parse(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal("invalid test connection")
	}
	u.Path = "/hc_test_access"
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	if err = db.Apply(ctx, u.String()); err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if err = devlocal.GrantAPI(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err = devlocal.GrantGateway(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	restricted := func(role string) *pgxpool.Pool {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(filepath.Dir(path), role+"-dsn"))
		if err != nil {
			t.Fatal("restricted connection unavailable")
		}
		app, _ := url.Parse(strings.TrimSpace(string(raw)))
		app.Path = u.Path
		pool, err := pgxpool.New(ctx, app.String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pool.Close)
		return pool
	}
	rules, _ := NewRules("https://portal.harness.test:42013", "harness.test")
	cipher, _ := security.NewSecretCipher("test-key", bytes.Repeat([]byte{9}, 32))
	apiPool, gatewayPool := restricted("api"), restricted("gateway")
	org := "00000000-0000-4000-8000-000000000091"
	apiStore, gatewayStore := NewStore(apiPool, cipher, "test", org, rules), NewStore(gatewayPool, cipher, "test", org, rules)
	must := func(sql string, args ...any) {
		t.Helper()
		if _, err := admin.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	must(`INSERT INTO organizations(id,name,status) VALUES($1,'Access test','ACTIVE') ON CONFLICT(id) DO UPDATE SET status='ACTIVE'`, org)
	principal := func(role string) identity.Principal {
		t.Helper()
		id, session := webapi.ID(), webapi.ID()
		must(`INSERT INTO users(id,identity_issuer,identity_subject,workspace_owner_id,status) VALUES($1,'https://access.test',$1::uuid::text,$1,'ACTIVE')`, id)
		must(`INSERT INTO memberships(org_id,user_id,role,status) VALUES($1,$2,$3,'ACTIVE')`, org, id, role)
		must(`INSERT INTO portal_sessions(id,org_id,user_id,auth_generation,secret_sha256,csrf_sha256,csrf_cipher,csrf_nonce,csrf_key_id,expires_at) VALUES($1,$2,$3,1,$4,$4,'synthetic-cipher',$5,'test-key',clock_timestamp()+interval '1 hour')`, session, org, id, hash(newSecret()), bytes.Repeat([]byte{0}, 12))
		return identity.Principal{UserID: id, OrganizationID: org, SessionID: session, Generation: 1, Role: role}
	}
	owner, other := principal("MEMBER"), principal("ADMIN")
	model, secret := webapi.ID(), webapi.ID()
	must(`INSERT INTO model_configuration_heads(id,org_id,current_version) VALUES($1,$2,0) ON CONFLICT(org_id) DO NOTHING`, model, org)
	var version int64
	if err = admin.QueryRow(ctx, `UPDATE model_configuration_heads SET current_version=current_version+1 WHERE org_id=$1 RETURNING id::text,current_version`, org).Scan(&model, &version); err != nil {
		t.Fatal(err)
	}
	must(`INSERT INTO secrets(id,org_id,key_id,nonce,ciphertext) VALUES($1,$2,'test-key',$3,'synthetic-model-cipher')`, secret, org, bytes.Repeat([]byte{0}, 12))
	must(`INSERT INTO model_configurations(id,org_id,version,provider,endpoint,protocol,model,credential_ref,status) VALUES($1,$2,$3,'deepseek-official','https://api.deepseek.com/anthropic','deepseek-messages','test-model',$4,'INVALID')`, model, org, version, secret)
	must(`INSERT INTO model_validations(config_id,config_version,status) VALUES($1,$2,'UNHEALTHY')`, model, version)
	id, asset, workspace, generation := webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID()
	name, _ := workspaceengine.WorkspaceName(id)
	snapshot := instances.Snapshot{DefaultsVersion: 1, TemplateID: webapi.ID(), TemplateVersion: webapi.ID(), ImageDigest: "sha256:" + strings.Repeat("a", 64), Spec: instances.ResourceSpec{ID: webapi.ID(), CPU: 2000, Memory: 4 << 30, Storage: 10 << 30}, ModelID: model, ModelVersion: version, Provider: "deepseek-official", Protocol: "deepseek-messages", Model: "test-model"}
	frozen, _ := json.Marshal(struct {
		SchemaVersion int `json:"schema_version"`
		instances.Snapshot
	}{1, snapshot})
	must(`INSERT INTO instances(id,org_id,owner_id,name,external_create_key,workspace_name,workspace_id,desired_state,observed_state,model_config_id,model_config_version,config_snapshot) VALUES($1,$2,$3,$4,$1,$5,$6,'RUNNING','RUNNING',$7,$8,$9)`, id, org, owner.UserID, "w-"+id[:12], name, workspace, model, version, frozen)
	must(`INSERT INTO storage_assets(id,org_id,owner_id,instance_id,resource_name,filesystem_id,capacity_bytes,init_version,status) VALUES($1,$2,$3,$4,$5,$1::uuid::text,10737418240,1,'READY')`, asset, org, owner.UserID, id, "hc-"+asset)
	must(`UPDATE instances SET storage_asset_id=$2 WHERE id=$1`, id, asset)
	must(`INSERT INTO operations(id,instance_id,actor_id,type,status,stage,intent,build_id,trace_id) VALUES($1,$2,$3,'CREATE','SUCCEEDED','COMPLETE','{}',$4,'access-fixture')`, generation, id, owner.UserID, webapi.ID())
	host := rules.Host(id)
	must(`INSERT INTO access_routes(instance_id,host,upstream_ip,runtime_generation,enabled) VALUES($1,$2,'172.18.0.5',$3,true)`, id, host, generation)
	for _, dimension := range []string{"application", "storage", "configuration", "access"} {
		must(`INSERT INTO instance_health(instance_id,dimension,status,checked_at,expires_at) VALUES($1,$2,'HEALTHY',clock_timestamp(),clock_timestamp()+interval '1 minute')`, id, dimension)
	}
	native := []byte("dsh-auth-" + base64Host(rules.authority(host)) + "=v1.synthetic.signature")
	envelope, _ := cipher.Encrypt(native, security.Context{Environment: "test", OrganizationID: org, RecordID: id + ":" + generation, Purpose: "native-cookie"})
	must(`INSERT INTO native_sessions(instance_id,runtime_generation,key_id,nonce,cookie_ciphertext,expires_at) VALUES($1,$2,$3,$4,$5,clock_timestamp()+interval '1 hour')`, id, generation, envelope.KeyID, envelope.Nonce, envelope.Ciphertext)
	// A frozen, available model does not require an organization default head.
	// Personal provider accounts operate without that optional configuration.
	must(`DELETE FROM model_configuration_heads WHERE org_id=$1`, org)
	plan, err := apiStore.Prepare(ctx, owner, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = apiStore.Prepare(ctx, other, id); code(err) != "NOT_FOUND" {
		t.Fatal("administrator inherited another owner's workspace")
	}
	challenge, browser, err := gatewayStore.Challenge(ctx, host)
	if err != nil {
		t.Fatal(err)
	}
	handoff, err := apiStore.Issue(ctx, owner, plan, challenge.ID, "test-access")
	if err != nil {
		t.Fatal(err)
	}
	if handoff.URL != rules.URL(host)+"/__dsh/handoff" || !opaque.MatchString(handoff.Ticket) || handoff.ExpiresAt.After(challenge.ExpiresAt) {
		t.Fatal("handoff authority or expiry escaped challenge")
	}
	if _, _, err = gatewayStore.Consume(ctx, host, handoff.Ticket, challenge.ID, newSecret(), plan, "test"); code(err) != "FORBIDDEN" {
		t.Fatal("copied ticket worked in another browser")
	}
	if _, _, err = gatewayStore.Consume(ctx, host, handoff.Ticket, challenge.ID, browser, Target{}, "test"); code(err) != "FORBIDDEN" {
		t.Fatal("unverified runtime plan consumed a ticket")
	}
	var wg sync.WaitGroup
	results := make(chan string, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			session, _, err := gatewayStore.Consume(ctx, host, handoff.Ticket, challenge.ID, browser, plan, "test")
			if err != nil {
				results <- ""
				return
			}
			results <- session
		}()
	}
	wg.Wait()
	close(results)
	wins := 0
	var accessSecret string
	for secret := range results {
		if secret != "" {
			wins++
			accessSecret = secret
		}
	}
	if wins != 1 {
		t.Fatal("ticket/challenge consumption was not exactly once")
	}
	authorized, err := gatewayStore.Authorize(ctx, host, accessSecret, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	defer authorized.Destroy()
	if !bytes.Equal(authorized.Cookie, native) || authorized.Principal.UserID != owner.UserID || authorized.ExpiresAt.After(time.Now().Add(30*time.Minute)) {
		t.Fatal("session did not retain correct Owner, native identity or absolute expiry")
	}
	serialized, _ := json.Marshal(authorized)
	if bytes.Contains(serialized, native) || strings.Contains(fmt.Sprintf("%+v", authorized), string(native)) {
		t.Fatal("native secret crossed diagnostics")
	}
	// Every request checks live state; degraded model health remains diagnostic.
	for _, change := range []struct {
		sql      string
		args     []any
		undo     string
		undoArgs []any
	}{
		{`UPDATE access_routes SET enabled=false WHERE instance_id=$1`, []any{id}, `UPDATE access_routes SET enabled=true WHERE instance_id=$1`, []any{id}},
		{`UPDATE memberships SET auth_generation=2 WHERE org_id=$1 AND user_id=$2`, []any{org, owner.UserID}, `UPDATE memberships SET auth_generation=1 WHERE org_id=$1 AND user_id=$2`, []any{org, owner.UserID}},
		{`UPDATE secrets SET revoked_at=clock_timestamp() WHERE id=$1`, []any{secret}, `UPDATE secrets SET revoked_at=NULL WHERE id=$1`, []any{secret}},
		{`UPDATE portal_sessions SET last_seen_at=clock_timestamp()-interval '31 minutes' WHERE id=$1`, []any{owner.SessionID}, `UPDATE portal_sessions SET last_seen_at=clock_timestamp() WHERE id=$1`, []any{owner.SessionID}},
		{`UPDATE access_sessions SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, []any{authorized.SessionID}, `UPDATE access_sessions SET expires_at=clock_timestamp()+interval '10 minutes' WHERE id=$1`, []any{authorized.SessionID}},
		{`UPDATE instance_health SET expires_at=clock_timestamp()-interval '1 second',checked_at=clock_timestamp()-interval '1 minute' WHERE instance_id=$1`, []any{id}, `UPDATE instance_health SET checked_at=clock_timestamp(),expires_at=clock_timestamp()+interval '1 minute' WHERE instance_id=$1`, []any{id}},
	} {
		must(change.sql, change.args...)
		if result, err := gatewayStore.Authorize(ctx, host, accessSecret, nil, true); err == nil {
			result.Destroy()
			t.Fatal("stale authorization remained usable")
		}
		must(change.undo, change.undoArgs...)
	}
	old := authorized.Target
	must(`UPDATE access_routes SET runtime_generation=$2 WHERE instance_id=$1`, id, webapi.ID())
	if result, err := gatewayStore.Authorize(ctx, host, accessSecret, &old, true); err == nil {
		result.Destroy()
		t.Fatal("stale external ownership check authorized another generation")
	}
	must(`UPDATE access_routes SET runtime_generation=$2 WHERE instance_id=$1`, id, generation)
	// The initial session lookup is only a hint. A changed generation in the
	// locked row must still be rejected after the intervening lock wait.
	sessionLock, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer sessionLock.Rollback(context.Background())
	if _, err = sessionLock.Exec(ctx, `SELECT id FROM access_sessions WHERE id=$1 FOR UPDATE`, authorized.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err = sessionLock.Exec(ctx, `UPDATE access_sessions SET auth_generation=2 WHERE id=$1`, authorized.SessionID); err != nil {
		t.Fatal(err)
	}
	checked := make(chan error, 1)
	go func() {
		result, err := gatewayStore.Authorize(ctx, host, accessSecret, nil, true)
		result.Destroy()
		checked <- err
	}()
	waitLock := func(fragment string) {
		t.Helper()
		var blocked bool
		for range 100 {
			if err := admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%'||$1||'%')`, fragment).Scan(&blocked); err != nil {
				t.Fatal(err)
			}
			if blocked {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal("access check did not reach the controlled row lock")
	}
	waitLock("access_sessions")
	if err = sessionLock.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-checked; code(err) != "FORBIDDEN" {
		t.Fatal("session generation changed while waiting but remained authorized")
	}
	must(`UPDATE access_sessions SET auth_generation=1 WHERE id=$1`, authorized.SessionID)
	// A wait on the challenge row must not extend its eligibility.
	short, _, err := gatewayStore.Challenge(ctx, host)
	if err != nil {
		t.Fatal(err)
	}
	must(`UPDATE access_challenges SET expires_at=clock_timestamp()+interval '6 seconds' WHERE id=$1`, short.ID)
	hold, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Rollback(context.Background())
	if _, err = hold.Exec(ctx, `SELECT id FROM access_challenges WHERE id=$1 FOR UPDATE`, short.ID); err != nil {
		t.Fatal(err)
	}
	issued := make(chan error, 1)
	go func() { _, err := apiStore.Issue(ctx, owner, plan, short.ID, "expired-wait"); issued <- err }()
	waitLock("access_challenges")
	time.Sleep(1200 * time.Millisecond)
	if err = hold.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-issued; code(err) != "FORBIDDEN" {
		t.Fatal("expired challenge became a ticket after waiting")
	}
	rollback, browser, err := gatewayStore.Challenge(ctx, host)
	if err != nil {
		t.Fatal(err)
	}
	retryTicket, err := apiStore.Issue(ctx, owner, plan, rollback.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	must(`CREATE OR REPLACE FUNCTION hc_test_reject_access_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.trace_id='rollback-consume' THEN RAISE EXCEPTION 'injected access audit failure'; END IF; RETURN NEW; END; $$;
 DROP TRIGGER IF EXISTS hc_test_access_audit_failure ON audit_events;
 CREATE TRIGGER hc_test_access_audit_failure BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION hc_test_reject_access_audit()`)
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DROP TRIGGER IF EXISTS hc_test_access_audit_failure ON audit_events; DROP FUNCTION IF EXISTS hc_test_reject_access_audit()`)
	})
	if _, _, err = gatewayStore.Consume(ctx, host, retryTicket.Ticket, rollback.ID, browser, plan, "rollback-consume"); err == nil {
		t.Fatal("late audit failure did not abort consumption")
	}
	var unconsumed bool
	if err = admin.QueryRow(ctx, `SELECT c.consumed_at IS NULL AND t.consumed_at IS NULL FROM access_challenges c JOIN access_tickets t ON t.challenge_id=c.id WHERE c.id=$1`, rollback.ID).Scan(&unconsumed); err != nil || !unconsumed {
		t.Fatal("partial consumption survived rollback")
	}
	must(`DROP TRIGGER hc_test_access_audit_failure ON audit_events; DROP FUNCTION hc_test_reject_access_audit()`)
	if _, _, err = gatewayStore.Consume(ctx, host, retryTicket.Ticket, rollback.ID, browser, plan, "retry-after-rollback"); err != nil {
		t.Fatal("rolled-back ticket could not be consumed", err)
	}
	apiService := NewService(apiStore, ownershipFunc(func(context.Context, Target) error { return nil }), nil)
	service := NewService(gatewayStore, ownershipFunc(func(context.Context, Target) error { return nil }), nativeFunc(func(context.Context, Authorization) error { return nil }))
	// Native failure spends the ticket and revokes the just-created session.
	failed, failedBrowser, err := gatewayStore.Challenge(ctx, host)
	if err != nil {
		t.Fatal(err)
	}
	failedTicket, err := apiService.Issue(ctx, owner, id, failed.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	service.native = nativeFunc(func(context.Context, Authorization) error { return errors.New("synthetic native failure") })
	if _, _, err = service.Redeem(ctx, host, failedTicket.Ticket, failed.ID, failedBrowser, "native-failure"); code(err) != "NATIVE_AUTH_FAILED" {
		t.Fatal("native failure did not fail the handoff")
	}
	if _, _, err = service.Redeem(ctx, host, failedTicket.Ticket, failed.ID, failedBrowser, "native-replay"); code(err) != "FORBIDDEN" {
		t.Fatal("failed native handoff ticket was replayable")
	}
	var active int
	if err = admin.QueryRow(ctx, `SELECT count(*) FROM access_sessions WHERE portal_session_id=$1 AND revoked_at IS NULL`, owner.SessionID).Scan(&active); err != nil || active != 2 {
		t.Fatal("native failure left a usable Gateway session")
	}
	oldChallenge, oldBrowser, err := gatewayStore.Challenge(ctx, host)
	if err != nil {
		t.Fatal(err)
	}
	oldTicket, err := apiService.Issue(ctx, owner, id, oldChallenge.ID, "replaced-challenge")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = gatewayStore.ReplaceChallenge(ctx, host, oldBrowser); err != nil {
		t.Fatal(err)
	}
	if _, err = gatewayStore.PrepareHandoff(ctx, host, oldTicket.Ticket, oldChallenge.ID, oldBrowser); code(err) != "FORBIDDEN" {
		t.Fatal("replacing a challenge left its old ticket usable")
	}
	// Live Owner changes during native I/O are checked before a Cookie is returned.
	late, lateBrowser, err := gatewayStore.Challenge(ctx, host)
	if err != nil {
		t.Fatal(err)
	}
	lateTicket, err := apiService.Issue(ctx, owner, id, late.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	service.native = nativeFunc(func(context.Context, Authorization) error {
		must(`UPDATE memberships SET status='DISABLED',auth_generation=auth_generation+1 WHERE org_id=$1 AND user_id=$2`, org, owner.UserID)
		return nil
	})
	if _, _, err = service.Redeem(ctx, host, lateTicket.Ticket, late.ID, lateBrowser, "revoked-after-native"); err == nil {
		t.Fatal("late native success bypassed Owner revocation")
	}
	if err = admin.QueryRow(ctx, `SELECT count(*) FROM access_sessions WHERE portal_session_id=$1 AND revoked_at IS NULL`, owner.SessionID).Scan(&active); err != nil || active != 2 {
		t.Fatal("revocation left the new handoff session usable")
	}
	// The Gateway may lock current state and touch activity, not mutate it.
	for _, sql := range []string{`SELECT ciphertext FROM secrets`, `SELECT csrf_cipher FROM portal_sessions`, `UPDATE memberships SET auth_generation=auth_generation+1`, `UPDATE instances SET version=version+1`, `UPDATE quota_reservations SET status='RELEASED'`, `UPDATE access_routes SET enabled=true`, `UPDATE model_configuration_heads SET current_version=0`, `UPDATE approved_templates SET enabled=true`, `CREATE TABLE gateway_escape(id integer)`} {
		if _, err = gatewayPool.Exec(ctx, sql); err == nil {
			t.Fatalf("Gateway role escaped its boundary: %s", sql)
		}
	}
	if _, err = apiPool.Exec(ctx, `SELECT cookie_ciphertext FROM native_sessions`); err == nil {
		t.Fatal("API role can read native sessions")
	}
}

type ownershipFunc func(context.Context, Target) error

func (f ownershipFunc) Check(ctx context.Context, target Target) error { return f(ctx, target) }

type nativeFunc func(context.Context, Authorization) error

func (f nativeFunc) Verify(ctx context.Context, authorization Authorization) error {
	return f(ctx, authorization)
}

func code(err error) string {
	var problem *webapi.Problem
	if errors.As(err, &problem) {
		return problem.Code
	}
	return ""
}
