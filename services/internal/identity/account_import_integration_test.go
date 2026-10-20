package identity

import (
	"encoding/json"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func importFixture(t *testing.T, f *managementFixture) AccountImport {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("synthetic-import-password"), 10)
	if err != nil {
		t.Fatal(err)
	}
	in := AccountImport{SchemaVersion: 1, OrganizationID: f.org, PreviousIssuer: "https://idp.example"}
	rows, err := f.root.Query(f.ctx, `SELECT u.identity_subject,u.workspace_owner_id::text,m.role FROM users u JOIN memberships m ON m.user_id=u.id WHERE m.org_id=$1 ORDER BY u.identity_subject`, f.org)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var a ImportedAccount
		if rows.Scan(&a.PreviousSubject, &a.WorkspaceOwnerID, &a.Role) != nil {
			t.Fatal("import source unavailable")
		}
		a.ID = newID()
		a.Email = a.PreviousSubject + "@example.com"
		a.Name = a.PreviousSubject
		a.PasswordHash = string(hash)
		in.Accounts = append(in.Accounts, a)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return in
}
func TestAccountImportPreservesIdentityOwnerAndRoleAndDoesNotResetOnReplay(t *testing.T) {
	f := managementDatabase(t)
	old := f.login(t, "admin-a")
	in := importFixture(t, f)
	raw, _ := json.Marshal(in)
	parsed, err := ParseAccountImport(raw, f.org)
	if err != nil {
		t.Fatal(err)
	}
	issuer := "https://portal.example/oidc"
	if err = ImportAccounts(f.ctx, f.root, parsed, issuer, "test-operator"); err != nil {
		t.Fatal(err)
	}
	var id, owner, role, source string
	var generation int64
	err = f.root.QueryRow(f.ctx, `SELECT u.id::text,u.workspace_owner_id::text,m.role,u.identity_source,m.auth_generation FROM users u JOIN memberships m ON m.user_id=u.id WHERE m.org_id=$1 AND u.identity_subject=$2 AND u.identity_issuer=$3`, f.org, in.Accounts[0].ID, issuer).Scan(&id, &owner, &role, &source, &generation)
	if err != nil || id != old.UserID || owner != in.Accounts[0].WorkspaceOwnerID || role != "ADMIN" || source != "local" || generation <= old.Generation {
		t.Fatal("migration replaced user/owner/role or retained old generation", err)
	}
	var revoked bool
	if err = f.root.QueryRow(f.ctx, `SELECT revoked_at IS NOT NULL FROM portal_sessions WHERE id=$1`, old.SessionID).Scan(&revoked); err != nil || !revoked {
		t.Fatal("migration retained old portal session", err)
	}
	original := in.Accounts[0].PasswordHash
	replacement, _ := bcrypt.GenerateFromPassword([]byte("synthetic-replay-different-password"), 10)
	in.Accounts[0].PasswordHash = string(replacement)
	if err = ImportAccounts(f.ctx, f.root, in, issuer, "replay"); err != nil {
		t.Fatal(err)
	}
	var hash []byte
	if err = f.root.QueryRow(f.ctx, `SELECT password_hash FROM identity_accounts WHERE id=$1`, in.Accounts[0].ID).Scan(&hash); err != nil || string(hash) != original {
		t.Fatal("import replay overwrote password", err)
	}
}
func TestAccountImportConflictingOwnerRollsBackEntireIssuerChange(t *testing.T) {
	f := managementDatabase(t)
	in := importFixture(t, f)
	in.Accounts[1].WorkspaceOwnerID = newID()
	if err := ImportAccounts(f.ctx, f.root, in, "https://portal.example/oidc", "test-operator"); err == nil {
		t.Fatal("import changed original Workspace Owner")
	}
	var credentials, changed int
	if err := f.root.QueryRow(f.ctx, `SELECT count(*) FROM identity_accounts`).Scan(&credentials); err != nil {
		t.Fatal(err)
	}
	if err := f.root.QueryRow(f.ctx, `SELECT count(*) FROM users WHERE identity_issuer<>'https://idp.example'`).Scan(&changed); err != nil || credentials != 0 || changed != 0 {
		t.Fatal("conflicting batch partly committed", err)
	}
}

func TestAccountMigrationMissingSubjectDoesNotCreateReplacementUser(t *testing.T) {
	f := managementDatabase(t)
	in := importFixture(t, f)
	in.Accounts[0].PreviousSubject = "unregistered-subject"
	if err := ImportAccounts(f.ctx, f.root, in, "https://portal.example/oidc", "test-operator"); err == nil {
		t.Fatal("missing trusted subject created a replacement user")
	}
	var users, credentials int
	if err := f.root.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM users),(SELECT count(*) FROM identity_accounts)`).Scan(&users, &credentials); err != nil || users != 3 || credentials != 0 {
		t.Fatal("failed issuer migration changed users or credentials", err)
	}
}
func TestAccountImportBootstrapsOnlyAnAuthorizedAdmin(t *testing.T) {
	f := managementDatabase(t)
	in := importFixture(t, f)
	// This fixture owns an isolated temporary database; no application data is used.
	if _, err := f.root.Exec(f.ctx, `TRUNCATE organizations,users CASCADE`); err != nil {
		t.Fatal(err)
	}
	in.PreviousIssuer = "https://portal.example/oidc"
	for i := range in.Accounts {
		in.Accounts[i].PreviousSubject = in.Accounts[i].ID
	}
	if err := ImportAccounts(f.ctx, f.root, in, in.PreviousIssuer, "initial-operator"); err != nil {
		t.Fatal(err)
	}
	var accounts, admins int
	if err := f.root.QueryRow(f.ctx, `SELECT count(*) FROM identity_accounts`).Scan(&accounts); err != nil {
		t.Fatal(err)
	}
	if err := f.root.QueryRow(f.ctx, `SELECT count(*) FROM memberships WHERE role='ADMIN' AND status='ACTIVE'`).Scan(&admins); err != nil || accounts != 3 || admins != 2 {
		t.Fatal("initial import lost accounts/roles", err)
	}
}
