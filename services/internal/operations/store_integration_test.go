package operations

import (
	"bytes"
	"context"
	"crypto/sha256"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/devlocal"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func TestRotationAndBoundedCleanupOnRestrictedPostgres(t *testing.T) {
	path := os.Getenv("HC_TEST_DB_DSN_FILE")
	if path == "" {
		t.Skip("isolated operations PostgreSQL required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("private test connection missing")
	}
	u, err := url.Parse(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal("invalid test connection")
	}
	u.Path = "/hc_test_operations"
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
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
	must(`TRUNCATE organizations,oidc_transactions CASCADE`)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = devlocal.GrantWorker(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	org, user, instance, session, secret, oidc, generation := webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID()
	must(`INSERT INTO organizations(id,name,status) VALUES($1,'Operations fixture','ACTIVE')`, org)
	must(`INSERT INTO users(id,identity_issuer,identity_subject,workspace_owner_id,status) VALUES($1::uuid,'https://ops.test',$1::text,$1::uuid,'ACTIVE')`, user)
	must(`INSERT INTO memberships(org_id,user_id,role,status) VALUES($1,$2,'MEMBER','ACTIVE')`, org, user)
	oldKey, newKey := bytes.Repeat([]byte{5}, 32), bytes.Repeat([]byte{6}, 32)
	old, _ := security.NewSecretCipher("old", oldKey)
	ring, _ := security.NewKeyring("new", map[string][]byte{"old": oldKey, "new": newKey})
	encrypt := func(id, purpose string) security.Envelope {
		t.Helper()
		e, err := old.Encrypt([]byte("private rotation fixture"), security.Context{Environment: "test", OrganizationID: org, RecordID: id, Purpose: purpose})
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	e := encrypt(secret, "model-key")
	must(`INSERT INTO secrets(id,org_id,key_id,nonce,ciphertext,revoked_at) VALUES($1,$2,$3,$4,$5,clock_timestamp())`, secret, org, e.KeyID, e.Nonce, e.Ciphertext)
	hash := sha256.Sum256([]byte(session))
	e = encrypt(session, "portal-csrf")
	must(`INSERT INTO portal_sessions(id,secret_sha256,org_id,user_id,auth_generation,csrf_sha256,csrf_cipher,csrf_key_id,csrf_nonce,expires_at) VALUES($1,$2,$3,$4,1,$2,$5,$6,$7,clock_timestamp()+interval '1 hour')`, session, hash[:], org, user, e.Ciphertext, e.KeyID, e.Nonce)
	e = encrypt(oidc, "oidc-pkce")
	must(`INSERT INTO oidc_transactions(id,state_sha256,browser_sha256,nonce_sha256,pkce_cipher,pkce_nonce,key_id,return_path,expires_at) VALUES($1,$2,$2,$2,$3,$4,$5,'/app/instances',clock_timestamp()+interval '10 minutes')`, oidc, hash[:], e.Ciphertext, e.Nonce, e.KeyID)
	model := webapi.ID()
	must(`INSERT INTO model_configuration_heads(id,org_id) VALUES($1,$2)`, model, org)
	must(`INSERT INTO model_configurations(org_id,id,version,provider,endpoint,protocol,model,credential_ref,status) VALUES($1,$2,1,'deepseek-official','https://api.deepseek.com/anthropic','deepseek-messages','fixture',$3,'REVOKED')`, org, model, secret)
	must(`INSERT INTO instances(id,org_id,owner_id,name,external_create_key,workspace_name,desired_state,observed_state,model_config_id,model_config_version,config_snapshot) VALUES($1,$2,$3,'ops-fixture',$1,'ops-fixture','STOPPED','STOPPED',$4,1,'{}')`, instance, org, user, model)
	e = encrypt(instance+":"+generation, "native-cookie")
	must(`INSERT INTO native_sessions(instance_id,runtime_generation,key_id,nonce,cookie_ciphertext,expires_at) VALUES($1,$2,$3,$4,$5,clock_timestamp()+interval '1 hour')`, instance, generation, e.KeyID, e.Nonce, e.Ciphertext)
	connection, providerSecret, reset := webapi.ID(), webapi.ID(), webapi.ID()
	must(`INSERT INTO model_connection_registry(id,org_id,owner_id) VALUES($1,$2,$3)`, connection, org, user)
	must(`INSERT INTO user_model_connections(id,org_id,owner_id,name,create_key_sha256,create_request_sha256) VALUES($1,$2,$3,'Rotation fixture',$4,$4)`, connection, org, user, hash[:])
	providerRecord := user + "/" + connection + "/" + providerSecret + "/1"
	e = encrypt(providerRecord, "provider-key")
	must(`INSERT INTO external_provider_secrets(id,org_id,owner_id,connection_id,bound_version,key_id,nonce,ciphertext,revoked_at) VALUES($1,$2,$3,$4,1,$5,$6,$7,clock_timestamp())`, providerSecret, org, user, connection, e.KeyID, e.Nonce, e.Ciphertext)
	resetAAD := security.Context{Environment: "test", OrganizationID: org, RecordID: user + "/" + reset, Purpose: "identity-password-hash"}
	e, err = old.Encrypt([]byte(strings.Repeat("h", 60)), resetAAD)
	if err != nil {
		t.Fatal(err)
	}
	must(`INSERT INTO identity_password_resets(id,org_id,user_id,actor_id,key_id,nonce,ciphertext) VALUES($1,$2,$3,$3,$4,$5,$6)`, reset, org, user, e.KeyID, e.Nonce, e.Ciphertext)
	rotator := NewRotator(pool, ring, "test", org, "integration-operator")
	counts, err := rotator.Count(ctx, "old")
	if err != nil || counts != 6 {
		t.Fatalf("rotation preview: %d %v", counts, err)
	}
	if changed, err := rotator.Batch(ctx, "old", 1); err != nil || changed != 1 {
		t.Fatalf("bounded batch: %d %v", changed, err)
	}
	if changed, err := rotator.Batch(ctx, "old", 500); err != nil || changed != 5 {
		t.Fatalf("resume: %d %v", changed, err)
	}
	if changed, err := rotator.Batch(ctx, "old", 500); err != nil || changed != 0 {
		t.Fatalf("replay: %d %v", changed, err)
	}
	var revoked bool
	if err := pool.QueryRow(ctx, `SELECT key_id,nonce,ciphertext,revoked_at IS NOT NULL FROM secrets WHERE id=$1`, secret).Scan(&e.KeyID, &e.Nonce, &e.Ciphertext, &revoked); err != nil {
		t.Fatal(err)
	}
	newOnly, _ := security.NewSecretCipher("new", newKey)
	plain, err := newOnly.Decrypt(e, security.Context{Environment: "test", OrganizationID: org, RecordID: secret, Purpose: "model-key"})
	clear(plain)
	if err != nil || !revoked {
		t.Fatal("rotation damaged value or resurrected revoked credential")
	}
	if err := pool.QueryRow(ctx, `SELECT key_id,nonce,ciphertext,revoked_at IS NOT NULL FROM external_provider_secrets WHERE id=$1`, providerSecret).Scan(&e.KeyID, &e.Nonce, &e.Ciphertext, &revoked); err != nil {
		t.Fatal(err)
	}
	plain, err = newOnly.Decrypt(e, security.Context{Environment: "test", OrganizationID: org, RecordID: providerRecord, Purpose: "provider-key"})
	clear(plain)
	if err != nil || !revoked {
		t.Fatal("provider-secret rotation lost its owner/version context or restored revocation", err)
	}
	if err := pool.QueryRow(ctx, `SELECT key_id,nonce,ciphertext FROM identity_password_resets WHERE id=$1`, reset).Scan(&e.KeyID, &e.Nonce, &e.Ciphertext); err != nil {
		t.Fatal(err)
	}
	plain, err = newOnly.Decrypt(e, resetAAD)
	if err != nil || string(plain) != strings.Repeat("h", 60) {
		t.Fatal("pending password reset was stranded by key rotation", err)
	}
	clear(plain)
	if remaining, err := rotator.Count(ctx, "old"); err != nil || remaining != 0 {
		t.Fatal("old key remains in database")
	}
	// Late audit failure must roll back ciphertext together with the audit.
	e = encrypt(secret, "model-key")
	must(`UPDATE secrets SET key_id=$2,nonce=$3,ciphertext=$4 WHERE id=$1`, secret, e.KeyID, e.Nonce, e.Ciphertext)
	must(`CREATE OR REPLACE FUNCTION ops_reject_rotation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='SECRET_REENCRYPT' THEN RAISE EXCEPTION 'injected audit failure'; END IF; RETURN NEW; END $$`)
	must(`CREATE TRIGGER ops_rotation_failure BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION ops_reject_rotation()`)
	if _, err := rotator.Batch(ctx, "old", 500); err == nil {
		t.Fatal("injected failure committed")
	}
	must(`DROP TRIGGER ops_rotation_failure ON audit_events`)
	if remaining, err := rotator.Count(ctx, "old"); err != nil || remaining != 1 {
		t.Fatal("failed transaction changed ciphertext")
	}
	if _, err := rotator.Batch(ctx, "old", 500); err != nil {
		t.Fatal(err)
	}
	workerRaw, err := os.ReadFile(filepath.Join(filepath.Dir(path), "worker-dsn"))
	if err != nil {
		t.Fatal("worker identity missing")
	}
	w, _ := url.Parse(strings.TrimSpace(string(workerRaw)))
	w.Path = u.Path
	restricted, err := pgxpool.New(ctx, w.String())
	if err != nil {
		t.Fatal(err)
	}
	defer restricted.Close()
	if _, err := restricted.Exec(ctx, `DELETE FROM portal_sessions`); err == nil {
		t.Fatal("worker has unrestricted session deletion")
	}
	challenge, ticket := webapi.ID(), webapi.ID()
	must(`INSERT INTO access_challenges(id,instance_id,host,secret_sha256,expires_at) VALUES($1,$2,'fixture.test',$3,clock_timestamp()-interval '1 minute')`, challenge, instance, hash[:])
	must(`INSERT INTO access_tickets(id,challenge_id,ticket_sha256,portal_session_id,instance_id,host,auth_generation,access_generation,expires_at) VALUES($1,$2,$3,$4,$5,'fixture.test',1,1,clock_timestamp()+interval '1 hour')`, ticket, challenge, hash[:], session, instance)
	must(`UPDATE portal_sessions SET revoked_at=clock_timestamp() WHERE id=$1`, session)
	cleaner := NewCleaner(restricted)
	if _, err := cleaner.Once(ctx, 500); err != nil {
		t.Fatal(err)
	}
	var kept bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM portal_sessions WHERE id=$1) AND EXISTS(SELECT 1 FROM access_challenges WHERE id=$2)`, session, challenge).Scan(&kept); err != nil || !kept {
		t.Fatal("referenced rows removed")
	}
	must(`UPDATE access_tickets SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, ticket)
	if _, err := cleaner.Once(ctx, 500); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM portal_sessions WHERE id=$1) OR EXISTS(SELECT 1 FROM access_challenges WHERE id=$2)`, session, challenge).Scan(&kept); err != nil || kept {
		t.Fatal("expired dependency chain retained")
	}
	must(`INSERT INTO oidc_transactions(id,state_sha256,browser_sha256,nonce_sha256,pkce_cipher,pkce_nonce,key_id,return_path,expires_at) SELECT gen_random_uuid(),decode(md5(n::text)||md5(n::text),'hex'),decode(md5(n::text)||md5(n::text),'hex'),decode(md5(n::text)||md5(n::text),'hex'),'x',decode(repeat('00',12),'hex'),'fixture','/app/instances',clock_timestamp()-interval '1 minute' FROM generate_series(1,501) n`)
	if result, err := cleaner.Once(ctx, 500); err != nil || result.Total() != 500 {
		t.Fatalf("cleanup batch exceeded or missed bound: %+v %v", result, err)
	}
	if result, err := cleaner.Once(ctx, 500); err != nil || result.Total() != 1 {
		t.Fatalf("cleanup resume: %+v %v", result, err)
	}
	if _, err := cleaner.Once(ctx, 501); err == nil {
		t.Fatal("unbounded cleanup accepted")
	}
	var preserved int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM instances)+(SELECT count(*) FROM secrets)+(SELECT count(*) FROM audit_events)`).Scan(&preserved); err != nil || preserved < 3 {
		t.Fatal("cleanup removed durable business records")
	}
}
