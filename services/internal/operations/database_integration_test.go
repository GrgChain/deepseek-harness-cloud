package operations

import (
	"bytes"
	"context"
	"crypto/sha256"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	"golang.org/x/crypto/bcrypt"
)

func TestDatabaseBackupRestoresOnlyToIsolatedQuarantine(t *testing.T) {
	path := os.Getenv("HC_TEST_DB_DSN_FILE")
	if path == "" {
		t.Skip("isolated PostgreSQL and pg_dump/pg_restore/psql required")
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
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
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
	org, user, secret, session := webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID()
	must(`INSERT INTO organizations(id,name,status) VALUES($1,'Backup fixture','ACTIVE')`, org)
	must(`INSERT INTO users(id,identity_issuer,identity_subject,workspace_owner_id,status) VALUES($1::uuid,'https://backup.test',$1::text,$1::uuid,'ACTIVE')`, user)
	must(`INSERT INTO memberships(org_id,user_id,role,status) VALUES($1,$2,'ADMIN','ACTIVE')`, org, user)
	cipher, _ := security.NewSecretCipher("fixture", bytes.Repeat([]byte{3}, 32))
	e, err := cipher.Encrypt([]byte("private database fixture"), security.Context{Environment: "test", OrganizationID: org, RecordID: secret, Purpose: "model-key"})
	if err != nil {
		t.Fatal(err)
	}
	must(`INSERT INTO secrets(id,org_id,key_id,nonce,ciphertext) VALUES($1,$2,$3,$4,$5)`, secret, org, e.KeyID, e.Nonce, e.Ciphertext)
	e, _ = cipher.Encrypt([]byte("private csrf fixture"), security.Context{Environment: "test", OrganizationID: org, RecordID: session, Purpose: "portal-csrf"})
	hash := sha256.Sum256([]byte(session))
	must(`INSERT INTO portal_sessions(id,secret_sha256,org_id,user_id,auth_generation,csrf_sha256,csrf_cipher,csrf_key_id,csrf_nonce,expires_at) VALUES($1,$2,$3,$4,1,$2,$5,$6,$7,clock_timestamp()+interval '1 hour')`, session, hash[:], org, user, e.Ciphertext, e.KeyID, e.Nonce)
	// Restores must quarantine outstanding provider grants as well as BFF sessions.
	authorization, codeID, tokenID := webapi.ID(), webapi.ID(), webapi.ID()
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("synthetic-backup-identity-password"), 10)
	if err != nil {
		t.Fatal(err)
	}
	must(`INSERT INTO identity_accounts(id,org_id,user_id,login_email,display_name,password_hash) VALUES($1,$2,$1,'backup@example.test','Backup identity',$3)`, user, org, passwordHash)
	must(`INSERT INTO identity_authorizations(id,browser_sha256,csrf_sha256,client_id,redirect_uri,state,nonce,challenge,scope,expires_at)
 VALUES($1,$2,$2,'platform','https://backup.test/auth/callback','synthetic-state','synthetic-nonce',$3,'openid',clock_timestamp()+interval '5 minutes')`, authorization, hash[:], strings.Repeat("a", 43))
	must(`INSERT INTO identity_codes(id,code_sha256,account_id,client_id,redirect_uri,nonce,challenge,scope,auth_generation,credential_version,expires_at)
 VALUES($1,$2,$3,'platform','https://backup.test/auth/callback','synthetic-nonce',$4,'openid',1,1,clock_timestamp()+interval '1 minute')`, codeID, hash[:], user, strings.Repeat("a", 43))
	must(`INSERT INTO identity_access_tokens(id,token_sha256,code_id,account_id,scope,auth_generation,credential_version,expires_at)
 VALUES($1,$2,$3,$4,'openid',1,1,clock_timestamp()+interval '5 minutes')`, tokenID, hash[:], codeID, user)
	dir := t.TempDir()
	if err = os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	output := dir + "/snapshot.hcbk"
	backupKey := bytes.Repeat([]byte{12}, 32)
	proof, err := BackupDatabase(ctx, pool, u.String(), output, "integration-operator", backupKey)
	if err != nil {
		t.Fatal(err)
	}
	if proof.Bytes == 0 || len(proof.SHA256) != 64 {
		t.Fatal("missing backup checksum")
	}
	if _, err = BackupDatabase(ctx, pool, u.String(), output, "integration-operator", backupKey); err == nil {
		t.Fatal("backup silently overwritten")
	}
	if _, err = RestoreDatabase(ctx, u.String(), output, "hc_test_operations", "integration-operator", backupKey, true); err == nil {
		t.Fatal("serving/source database accepted for restore")
	}
	database := "hc_test_recovery_" + strings.ReplaceAll(webapi.ID(), "-", "")
	must(`CREATE DATABASE ` + pgx.Identifier{database}.Sanitize())
	defer func() {
		bounded, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, err := pool.Exec(bounded, `DROP DATABASE `+pgx.Identifier{database}.Sanitize()+` WITH (FORCE)`); err != nil {
			t.Error("isolated recovery fixture cleanup failed")
		}
	}()
	target := *u
	target.Path = "/" + database
	report, err := RestoreDatabase(ctx, target.String(), output, database, "integration-operator", backupKey, false)
	if err != nil || report.Applied || report.Proof != proof {
		t.Fatal("dry-run changed target or missed integrity", err)
	}
	if _, err := RestoreDatabase(ctx, target.String(), output, "hc_recovery_wrong", "integration-operator", backupKey, true); err == nil {
		t.Fatal("wrong target confirmation accepted")
	}
	report, err = RestoreDatabase(ctx, target.String(), output, database, "integration-operator", backupKey, true)
	if err != nil || !report.Applied || !report.Quarantined || report.Proof != proof {
		t.Fatal("isolated restore failed", err)
	}
	restored, err := pgx.Connect(ctx, target.String())
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close(context.Background())
	var disabled, revoked, rolesBlocked bool
	var count int
	if err = restored.QueryRow(ctx, `SELECT (SELECT status='DISABLED' FROM organizations),(SELECT revoked_at IS NOT NULL FROM portal_sessions WHERE id=$1),(SELECT count(*) FROM audit_events WHERE action='RESTORE_ISOLATED'),NOT has_database_privilege('hc_api',current_database(),'CONNECT') AND NOT has_database_privilege('hc_worker',current_database(),'CONNECT') AND NOT has_database_privilege('hc_gateway',current_database(),'CONNECT')`, session).Scan(&disabled, &revoked, &count, &rolesBlocked); err != nil || !disabled || !revoked || count != 1 || !rolesBlocked {
		t.Fatal("restored authorization was not quarantined", err)
	}
	var identityQuarantined bool
	var restoredHash []byte
	if err = restored.QueryRow(ctx, `SELECT
 (SELECT consumed_at IS NOT NULL FROM identity_authorizations WHERE id=$1) AND
 (SELECT consumed_at IS NOT NULL FROM identity_codes WHERE id=$2) AND
 (SELECT revoked_at IS NOT NULL FROM identity_access_tokens WHERE id=$3),
 (SELECT password_hash FROM identity_accounts WHERE id=$4)`, authorization, codeID, tokenID, user).Scan(&identityQuarantined, &restoredHash); err != nil || !identityQuarantined || !bytes.Equal(passwordHash, restoredHash) {
		t.Fatal("restored provider grants were reopened or account credential was lost", err)
	}
	clear(passwordHash)
	clear(restoredHash)
	var remainsRevoked bool
	if err = restored.QueryRow(ctx, `SELECT key_id,nonce,ciphertext,revoked_at IS NOT NULL FROM secrets WHERE id=$1`, secret).Scan(&e.KeyID, &e.Nonce, &e.Ciphertext, &remainsRevoked); err != nil {
		t.Fatal(err)
	}
	plain, err := cipher.Decrypt(e, security.Context{Environment: "test", OrganizationID: org, RecordID: secret, Purpose: "model-key"})
	match := string(plain) == "private database fixture"
	clear(plain)
	if err != nil || !match || !remainsRevoked {
		t.Fatal("restored encryption context changed or credential was not quarantined")
	}
	var publicFunction, protectedBudget bool
	if err = restored.QueryRow(ctx, `SELECT has_function_privilege('hc_api','public.hc_cleanup_expired(integer)','EXECUTE'),(SELECT attempts=1000 FROM probe_budgets WHERE org_id=$1 AND day=(clock_timestamp() AT TIME ZONE 'UTC')::date)`, org).Scan(&publicFunction, &protectedBudget); err != nil || publicFunction || !protectedBudget {
		t.Fatal("restore reopened function privilege or forgot paid-probe budget")
	}
	if _, err = RestoreDatabase(ctx, target.String(), output, database, "integration-operator", backupKey, true); err == nil {
		t.Fatal("nonempty recovery database overwritten")
	}
	// An authenticated archive with mismatched organization metadata fails at
	// the final quarantine audit; the preceding restore and revocations roll back.
	database2 := "hc_test_recovery_" + strings.ReplaceAll(webapi.ID(), "-", "")
	must(`CREATE DATABASE ` + pgx.Identifier{database2}.Sanitize())
	defer func() {
		bounded, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_, _ = pool.Exec(bounded, `DROP DATABASE `+pgx.Identifier{database2}.Sanitize()+` WITH (FORCE)`)
	}()
	broken := dir + "/broken.hcbk"
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	target.Path = "/" + database2
	var dump bytes.Buffer
	manifest, _, err := OpenArchive(&dump, bytes.NewReader(data), backupKey)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(dump.Bytes())
	manifest.OrganizationID = webapi.ID()
	mismatched := dir + "/mismatched.hcbk"
	sealed, err := os.OpenFile(mismatched, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, sealErr := SealArchive(sealed, &dump, backupKey, manifest)
	closeErr := sealed.Close()
	if sealErr != nil || closeErr != nil {
		t.Fatal("failed to seal quarantine rollback fixture")
	}
	if _, err = RestoreDatabase(ctx, target.String(), mismatched, database2, "integration-operator", backupKey, true); err == nil {
		t.Fatal("failed final quarantine audit committed a restore")
	}
	empty, err := pgx.Connect(ctx, target.String())
	if err != nil {
		t.Fatal(err)
	}
	defer empty.Close(context.Background())
	if err = empty.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema='public'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed quarantine left restored tables")
	}
	// The restore guard requires no other target connections.
	if err = empty.Close(ctx); err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 1
	if err = os.WriteFile(broken, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = RestoreDatabase(ctx, target.String(), broken, database2, "integration-operator", backupKey, true); err == nil {
		t.Fatal("corrupt backup accepted")
	}
	empty, err = pgx.Connect(ctx, target.String())
	if err != nil {
		t.Fatal(err)
	}
	defer empty.Close(context.Background())
	if err = empty.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema='public'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("corrupt archive modified target")
	}
}
