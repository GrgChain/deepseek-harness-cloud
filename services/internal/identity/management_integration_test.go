package identity

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/devlocal"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/pagination"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type managementFixture struct {
	ctx       context.Context
	root, app *pgxpool.Pool
	service   *ManagementService
	cipher    *security.SecretCipher
	org       string
}

func managementDatabase(t *testing.T) *managementFixture {
	t.Helper()
	path := os.Getenv("HC_TEST_DB_DSN_FILE")
	if path == "" {
		t.Skip("isolated PostgreSQL governance database required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("test connection file unavailable")
	}
	u, err := url.Parse(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal("invalid test connection")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	u.Path = "/postgres"
	admin, err := pgx.Connect(ctx, u.String())
	if err != nil {
		t.Fatal("test administrator unavailable")
	}
	database := "hc_test_iam_" + strings.ReplaceAll(newID(), "-", "")
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{database}.Sanitize()); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	var root, app *pgxpool.Pool
	t.Cleanup(func() {
		if app != nil {
			app.Close()
		}
		if root != nil {
			root.Close()
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanup, "DROP DATABASE "+pgx.Identifier{database}.Sanitize())
		_ = admin.Close(cleanup)
	})
	u.Path = "/" + database
	if err = db.Apply(ctx, u.String()); err != nil {
		t.Fatal(err)
	}
	root, err = pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	tx, err := root.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = devlocal.GrantAPI(ctx, tx); err != nil {
		tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(u.String())
	if err != nil {
		t.Fatal(err)
	}
	config.AfterConnect = func(ctx context.Context, c *pgx.Conn) error { _, err := c.Exec(ctx, "SET ROLE hc_api"); return err }
	app, err = pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	org := newID()
	if err = ApplyMapping(ctx, root, Mapping{SchemaVersion: 1, OrganizationID: org, Members: []MemberMapping{
		{Issuer: "https://idp.example", Subject: "admin-a", Role: "ADMIN", WorkspaceOwnerID: newID()},
		{Issuer: "https://idp.example", Subject: "admin-b", Role: "ADMIN", WorkspaceOwnerID: newID()},
		{Issuer: "https://idp.example", Subject: "member", Role: "MEMBER", WorkspaceOwnerID: newID()},
	}}, "governance-test"); err != nil {
		t.Fatal(err)
	}
	cipher, _ := security.NewSecretCipher("test-key", bytes.Repeat([]byte{7}, 32))
	cursor, _ := pagination.NewCodec(bytes.Repeat([]byte{8}, 32), "test")
	return &managementFixture{ctx: ctx, root: root, app: app, service: NewManagementService(app, cursor, "https://idp.example", nil, cipher, "test"), cipher: cipher, org: org}
}
func (f *managementFixture) login(t *testing.T, subject string) Principal {
	t.Helper()
	provider := &fixtureProvider{issuer: "https://idp.example", subject: subject, nonces: map[string]string{}}
	s := NewService(f.app, provider, f.cipher, "test", f.org, "https://portal.example")
	l, err := s.Login(f.ctx, "", "/app/instances")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(l.URL)
	state := u.Query().Get("state")
	result, err := s.Callback(f.ctx, state, l.BrowserSecret, state, "")
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Authenticate(f.ctx, result.SessionSecret)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func problemCode(err error) string {
	var p *webapi.Problem
	if errors.As(err, &p) {
		return p.Code
	}
	if err == nil {
		return ""
	}
	return err.Error()
}

func TestPermissionCatalogMatchesDatabaseAndSupportsStorageRelease(t *testing.T) {
	f := managementDatabase(t)
	admin, member := f.login(t, "admin-a"), f.login(t, "member")
	catalog, err := f.service.Permissions(f.ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := f.root.QueryRow(f.ctx, `SELECT count(*) FROM permissions`).Scan(&count); err != nil || len(catalog) != count {
		t.Fatal("permission catalog differs from migrated database", err)
	}
	seen := map[string]bool{}
	for _, permission := range catalog {
		var assignable bool
		var dependencies []string
		if err := f.root.QueryRow(f.ctx, `SELECT assignable,dependencies FROM permissions WHERE id=$1`, permission.ID).Scan(&assignable, &dependencies); err != nil {
			t.Fatal("catalog permission missing from database", permission.ID, err)
		}
		if seen[permission.ID] || permission.Name == "" || permission.Description == "" || assignable != permission.Assignable || !slices.Equal(dependencies, permission.Dependencies) {
			t.Fatal("catalog metadata or authorization dependencies differ", permission.ID)
		}
		seen[permission.ID] = true
	}
	if !seen["instances:release-storage"] {
		t.Fatal("owner storage release missing from catalog")
	}
	if _, err := f.service.Permissions(f.ctx, member); !errors.Is(err, ErrForbidden) {
		t.Fatal("catalog access bypassed roles:read", err)
	}
	input := RoleInput{Code: "storage_owner", Name: "本人数据管理", Permissions: []string{"instances:release-storage"}}
	if _, err := f.service.SaveRole(f.ctx, admin, "", "catalog-missing-dependency", input, "catalog-test"); err == nil {
		t.Fatal("storage release without read dependency accepted")
	}
	input.Permissions = []string{"instances:read", "instances:release-storage"}
	role, err := f.service.SaveRole(f.ctx, admin, "", "catalog-storage-owner-role", input, "catalog-test")
	if err != nil || !slices.Equal(role.Permissions, input.Permissions) {
		t.Fatal("explicit owner storage role grant failed", err)
	}
}

func TestManagementRoleLifecycleAndAuthoritativePermissions(t *testing.T) {
	f := managementDatabase(t)
	admin, member := f.login(t, "admin-a"), f.login(t, "member")
	if !admin.Has("users:manage") || member.Has("users:manage") || !member.Has("instances:start") {
		t.Fatal("effective permission projection differs from built-in roles")
	}
	member.Role = "ADMIN"
	if _, err := f.service.ListUsers(f.ctx, member, ManagementListInput{Limit: 20}); !errors.Is(err, ErrForbidden) {
		t.Fatal("forged role grants governance", err)
	}
	input := RoleInput{Code: "observer", Name: "只读成员", Permissions: []string{"instances:read"}}
	role, err := f.service.SaveRole(f.ctx, admin, "", "role-create-request-01", input, "role-create")
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := f.service.SaveRole(f.ctx, admin, "", "role-create-request-01", input, "role-replay")
	if err != nil || replayed.ID != role.ID {
		t.Fatal("role create duplicated", err)
	}
	input.Name = "不同参数"
	if _, err = f.service.SaveRole(f.ctx, admin, "", "role-create-request-01", input, "conflict"); problemCode(err) != "IDEMPOTENCY_CONFLICT" {
		t.Fatal("changed request accepted", err)
	}
	u, err := f.service.GetUser(f.ctx, admin, member.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.UserAction(f.ctx, admin, u.ID, "USER_ROLE", UserActionInput{ExpectedVersion: u.Version, RoleID: role.ID}, "assign"); err != nil {
		t.Fatal(err)
	}
	tx, _ := f.app.Begin(f.ctx)
	err = RequirePermission(f.ctx, tx, member, "instances:create")
	tx.Rollback(f.ctx)
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("old session survived role assignment", err)
	}
	member = f.login(t, "member")
	if !member.Has("instances:read") || member.Has("instances:create") {
		t.Fatal("custom role projection ignored")
	}
	if err = f.service.DeleteRole(f.ctx, admin, role.ID, role.Version, "delete-in-use"); problemCode(err) != "ROLE_IN_USE" {
		t.Fatal("assigned role deleted", err)
	}
	input = RoleInput{Code: role.Code, Name: role.Name, Permissions: []string{}, ExpectedVersion: role.Version}
	role, err = f.service.SaveRole(f.ctx, admin, role.ID, "", input, "empty-permissions")
	if err != nil {
		t.Fatal(err)
	}
	member = f.login(t, "member")
	if len(member.Permissions) != 0 {
		t.Fatal("empty role has business permissions")
	}
	if _, err = f.service.SaveRole(f.ctx, admin, role.ID, "", input, "stale-write"); problemCode(err) != "VERSION_CONFLICT" {
		t.Fatal("stale role write accepted", err)
	}
	if err = f.service.DeleteRole(f.ctx, admin, admin.RoleID, 1, "delete-admin"); problemCode(err) != "BUILTIN_ROLE" {
		t.Fatal("built-in role deleted", err)
	}
	for _, sql := range []string{`UPDATE users SET status='DISABLED'`, `UPDATE memberships SET role='ADMIN'`, `DELETE FROM roles`, `INSERT INTO role_permissions SELECT id,'users:manage' FROM roles`} {
		if _, err = f.app.Exec(f.ctx, sql); err == nil {
			t.Fatal("runtime role has unrestricted governance write")
		}
	}
}

func TestManagementUserIdentityPaginationAndAuditRollback(t *testing.T) {
	f := managementDatabase(t)
	admin := f.login(t, "admin-a")
	var memberRole string
	if err := f.root.QueryRow(f.ctx, `SELECT id::text FROM roles WHERE org_id=$1 AND code='MEMBER'`, f.org).Scan(&memberRole); err != nil {
		t.Fatal(err)
	}
	in := UserCreateInput{DisplayName: "新成员", Email: "new@example.com", IdentitySource: "oidc", Subject: "external-subject", RoleID: memberRole}
	u, err := f.service.CreateUser(f.ctx, admin, "user-create-request-01", in, "create-user")
	if err != nil {
		t.Fatal(err)
	}
	replay, err := f.service.CreateUser(f.ctx, admin, "user-create-request-01", in, "repeat-user")
	if err != nil || replay.ID != u.ID {
		t.Fatal("duplicate identity", err)
	}
	if _, err = f.service.CreateUser(f.ctx, admin, "user-create-request-02", in, "duplicate-subject"); problemCode(err) != "IDENTITY_CONFLICT" {
		t.Fatal("same subject rebound", err)
	}
	logged := f.login(t, "external-subject")
	if logged.UserID != u.ID {
		t.Fatal("new binding cannot log in")
	}
	page, err := f.service.ListUsers(f.ctx, admin, ManagementListInput{Limit: 1})
	if err != nil || len(page.Items) != 1 || page.NextCursor == nil {
		t.Fatal("user pagination missing", err)
	}
	if _, err = f.service.ListUsers(f.ctx, admin, ManagementListInput{Limit: 1, Cursor: *page.NextCursor, Query: "different"}); problemCode(err) != "INVALID_CURSOR" {
		t.Fatal("cursor crossed filter scope", err)
	}
	if _, err = f.root.Exec(f.ctx, `CREATE FUNCTION reject_user_change_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='USER_DISABLE' THEN RAISE EXCEPTION 'test audit failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER reject_user_change_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_user_change_audit()`); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.UserAction(f.ctx, admin, u.ID, "USER_DISABLE", UserActionInput{ExpectedVersion: u.Version}, "audit-rollback"); err == nil {
		t.Fatal("late audit failure accepted")
	}
	after, err := f.service.GetUser(f.ctx, admin, u.ID)
	if err != nil || after.Status != "ACTIVE" || after.Version != u.Version {
		t.Fatal("late failure did not roll back user", err)
	}
	tx, _ := f.app.Begin(f.ctx)
	err = RequirePermission(f.ctx, tx, logged, "instances:read")
	tx.Rollback(f.ctx)
	if err != nil {
		t.Fatal("rolled back change revoked session", err)
	}
	if _, err = f.root.Exec(f.ctx, `DROP TRIGGER reject_user_change_audit ON audit_events`); err != nil {
		t.Fatal(err)
	}
	disabled, err := f.service.UserAction(f.ctx, admin, u.ID, "USER_DISABLE", UserActionInput{ExpectedVersion: u.Version}, "disable")
	if err != nil || disabled.Status != "DISABLED" {
		t.Fatal(err)
	}
	tx, _ = f.app.Begin(f.ctx)
	err = RequirePermission(f.ctx, tx, logged, "instances:read")
	tx.Rollback(f.ctx)
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("disabled user retained authorization", err)
	}
	if _, err = f.service.UserAction(f.ctx, admin, u.ID, "USER_ENABLE", UserActionInput{ExpectedVersion: disabled.Version}, "enable"); err != nil {
		t.Fatal(err)
	}
	f.login(t, "external-subject")
}

func TestManagementConcurrentAdministratorProtection(t *testing.T) {
	f := managementDatabase(t)
	a, b := f.login(t, "admin-a"), f.login(t, "admin-b")
	if _, err := f.service.UserAction(f.ctx, a, a.UserID, "USER_DISABLE", UserActionInput{ExpectedVersion: 1}, "self-disable"); problemCode(err) != "SELF_CHANGE_FORBIDDEN" {
		t.Fatal("self-disable accepted", err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	start := make(chan struct{})
	for _, pair := range [][2]Principal{{a, b}, {b, a}} {
		wg.Add(1)
		go func(pair [2]Principal) {
			defer wg.Done()
			<-start
			_, err := f.service.UserAction(f.ctx, pair[0], pair[1].UserID, "USER_DISABLE", UserActionInput{ExpectedVersion: 1}, "concurrent-disable")
			results <- err
		}(pair)
	}
	close(start)
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	var remaining int
	if err := f.root.QueryRow(f.ctx, `SELECT count(*) FROM memberships m JOIN users u ON u.id=m.user_id WHERE m.org_id=$1 AND m.role='ADMIN' AND m.status='ACTIVE' AND u.status='ACTIVE'`, f.org).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if success != 1 || remaining != 1 {
		t.Fatalf("concurrent administrator changes: success=%d active=%d", success, remaining)
	}
}
