package identity

import (
	"errors"
	"testing"
)

func delegateGovernance(t *testing.T, f *managementFixture, admin Principal, subject, code string, permissions []string) Principal {
	t.Helper()
	role, err := f.service.SaveRole(f.ctx, admin, "", "delegate-"+code+"-request", RoleInput{Code: code, Name: code, Permissions: permissions}, "delegate-create")
	if err != nil {
		t.Fatal(err)
	}
	previous := f.login(t, subject)
	user, err := f.service.GetUser(f.ctx, admin, previous.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.UserAction(f.ctx, admin, user.ID, "USER_ROLE", UserActionInput{ExpectedVersion: user.Version, RoleID: role.ID}, "delegate-assign"); err != nil {
		t.Fatal(err)
	}
	return f.login(t, subject)
}

func TestDelegatedGovernanceSeparatesUserAndRoleWrites(t *testing.T) {
	f := managementDatabase(t)
	admin := f.login(t, "admin-a")
	users := delegateGovernance(t, f, admin, "member", "user_manager", []string{"users:read", "users:manage"})
	roles := delegateGovernance(t, f, admin, "admin-b", "role_manager", []string{"roles:read", "roles:manage"})
	input := RoleInput{Code: "delegated_role", Name: "委派角色", Permissions: []string{"instances:read"}}
	if _, err := f.service.SaveRole(f.ctx, users, "", "denied-delegate-role", input, "denied-role"); !errors.Is(err, ErrForbidden) {
		t.Fatal("user manager obtained role write access", err)
	}
	if _, err := f.service.UserAction(f.ctx, roles, users.UserID, "USER_DISABLE", UserActionInput{ExpectedVersion: 2}, "denied-user"); !errors.Is(err, ErrForbidden) {
		t.Fatal("role manager obtained user write access", err)
	}
	role, err := f.service.SaveRole(f.ctx, roles, "", "allowed-delegate-role", input, "create-role")
	if err != nil {
		t.Fatal("delegated role create failed", err)
	}
	input.Name, input.ExpectedVersion = "修改后的委派角色", role.Version
	updated, err := f.service.SaveRole(f.ctx, roles, role.ID, "", input, "edit-role")
	if err != nil || updated.Version != role.Version+1 {
		t.Fatal("delegated role update failed", err)
	}
	if _, err = f.service.SaveRole(f.ctx, roles, role.ID, "", input, "stale-edit"); problemCode(err) != "VERSION_CONFLICT" {
		t.Fatal("delegation bypassed optimistic role versions", err)
	}
	if err = f.service.DeleteRole(f.ctx, roles, role.ID, updated.Version, "delete-role"); err != nil {
		t.Fatal("delegated role delete failed", err)
	}
	var builtin string
	if err = f.root.QueryRow(f.ctx, `SELECT id::text FROM roles WHERE org_id=$1 AND code='ADMIN'`, f.org).Scan(&builtin); err != nil {
		t.Fatal(err)
	}
	if err = f.service.DeleteRole(f.ctx, roles, builtin, 1, "delete-builtin"); problemCode(err) != "BUILTIN_ROLE" {
		t.Fatal("delegation bypassed built-in role protection", err)
	}
	if _, err = f.service.UserAction(f.ctx, users, users.UserID, "USER_ROLE", UserActionInput{ExpectedVersion: 2, RoleID: builtin}, "self-role"); problemCode(err) != "SELF_CHANGE_FORBIDDEN" {
		t.Fatal("delegation bypassed self role protection", err)
	}
	if _, err = f.service.UserAction(f.ctx, users, admin.UserID, "USER_DISABLE", UserActionInput{ExpectedVersion: 1}, "disable-last-admin"); problemCode(err) != "LAST_ADMIN_REQUIRED" {
		t.Fatal("delegation bypassed last administrator protection", err)
	}
	var count int
	if err = f.root.QueryRow(f.ctx, `SELECT count(*) FROM audit_events WHERE actor_id=$1 AND action IN ('ROLE_CREATE','ROLE_UPDATE','ROLE_DELETE')`, roles.UserID).Scan(&count); err != nil || count != 3 {
		t.Fatal("delegated role writes were not audited", count, err)
	}
}

func TestDelegatedUserManagerCanResetOwnPasswordAndArchiveUsers(t *testing.T) {
	f, admin, client, target := localResetFixture(t)
	manager := delegateGovernance(t, f, admin, target.Subject, "account_manager", []string{"users:read", "users:manage"})
	self, err := f.service.GetUser(f.ctx, admin, manager.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.UserAction(f.ctx, manager, self.ID, "USER_PASSWORD_RESET", UserActionInput{ExpectedVersion: self.Version, Password: "delegated-reset-password"}, "delegated-reset"); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("self reset did not invalidate the delegated manager session", err)
	}
	self, err = f.service.GetUser(f.ctx, admin, manager.UserID)
	if err != nil || self.PasswordReset == nil || self.PasswordReset.Status != "SUCCEEDED" || client.updates != 1 {
		t.Fatal("delegated self reset failed durable confirmation", err)
	}
	manager = f.login(t, target.Subject)
	member := f.login(t, "member")
	user, err := f.service.GetUser(f.ctx, manager, member.UserID)
	if err != nil {
		t.Fatal(err)
	}
	confirmedName := user.DisplayName
	if confirmedName == "" {
		confirmedName = user.ID
	}
	if err = f.service.DeleteUser(f.ctx, manager, user.ID, UserDeleteInput{ExpectedVersion: user.Version, ConfirmedName: confirmedName}, "delegated-archive"); err != nil {
		t.Fatal("delegated user archive failed", err)
	}
	user, err = f.service.GetUser(f.ctx, manager, user.ID)
	if err != nil || user.Status != "ARCHIVED" {
		t.Fatal("delegated archive did not revoke the account", err)
	}
}

func TestDelegatedPasswordResetRechecksPermissionsAfterProviderWrite(t *testing.T) {
	f, admin, _, target := localResetFixture(t)
	manager := delegateGovernance(t, f, admin, "member", "reset_manager", []string{"users:read", "users:manage"})
	role, err := f.service.GetRole(f.ctx, admin, manager.RoleID)
	if err != nil {
		t.Fatal(err)
	}
	var revokeError error
	f.service.local = passwordResetHook{f.service.local, func() {
		_, revokeError = f.service.SaveRole(f.ctx, admin, role.ID, "", RoleInput{Code: role.Code, Name: role.Name, Permissions: []string{"users:read"}, ExpectedVersion: role.Version}, "revoke-reset-permission")
	}}
	if _, err = f.service.UserAction(f.ctx, manager, target.ID, "USER_PASSWORD_RESET", UserActionInput{ExpectedVersion: target.Version, Password: "delegated-uncertain-reset"}, "reset-then-revoke"); !errors.Is(err, ErrForbidden) || revokeError != nil {
		t.Fatal("lost management permission published reset confirmation", err, revokeError)
	}
	pending, err := f.service.GetUser(f.ctx, admin, target.ID)
	if err != nil || pending.PasswordReset == nil || pending.PasswordReset.Status != "PENDING" {
		t.Fatal("unconfirmed reset intent was not retained", err)
	}
}
