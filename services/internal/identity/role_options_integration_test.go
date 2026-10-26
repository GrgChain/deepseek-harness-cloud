package identity

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUserRoleOptionsRespectIndependentPermissions(t *testing.T) {
	f := managementDatabase(t)
	admin, member := f.login(t, "admin-a"), f.login(t, "member")
	in := ManagementListInput{Limit: 1}
	forged := member
	forged.Permissions = []string{"users:read", "users:manage", "roles:read"}
	if _, err := f.service.ListUserRoleOptions(f.ctx, forged, in); !errors.Is(err, ErrForbidden) {
		t.Fatal("role options trusted a forged permission projection", err)
	}
	role, err := f.service.SaveRole(f.ctx, admin, "", "user-governor-options-01", RoleInput{Code: "user_governor", Name: "用户管理员", Permissions: []string{"users:read", "users:manage"}}, "create-role")
	if err != nil {
		t.Fatal(err)
	}
	target, err := f.service.GetUser(f.ctx, admin, member.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.UserAction(f.ctx, admin, target.ID, "USER_ROLE", UserActionInput{ExpectedVersion: target.Version, RoleID: role.ID}, "assign-role"); err != nil {
		t.Fatal(err)
	}
	manager := f.login(t, "member")
	wrongOrg := manager
	wrongOrg.OrganizationID = newID()
	if _, err = f.service.ListUserRoleOptions(f.ctx, wrongOrg, ManagementListInput{Limit: 20}); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("role options accepted an unbound organization", err)
	}
	if _, err = f.service.ListRoles(f.ctx, manager, ManagementListInput{Limit: 20}); !errors.Is(err, ErrForbidden) {
		t.Fatal("user management implicitly granted full role access", err)
	}
	if _, err = f.service.Permissions(f.ctx, manager); !errors.Is(err, ErrForbidden) {
		t.Fatal("user management implicitly granted permission catalog access", err)
	}
	seen := map[string]bool{}
	for {
		page, err := f.service.ListUserRoleOptions(f.ctx, manager, in)
		if err != nil || page.Total != 3 || len(page.Items) != 1 || seen[page.Items[0].ID] {
			t.Fatal("role options pagination or total invalid", page, err)
		}
		seen[page.Items[0].ID] = true
		if page.NextCursor == nil {
			break
		}
		in.Cursor = *page.NextCursor
	}
	if len(seen) != 3 {
		t.Fatal("role options omitted an organization role")
	}
	adminOptions, err := f.service.ListUserRoleOptions(f.ctx, admin, ManagementListInput{Limit: 1})
	if err != nil || adminOptions.NextCursor == nil {
		t.Fatal("admin role option cursor missing", err)
	}
	if _, err = f.service.ListRoles(f.ctx, admin, ManagementListInput{Limit: 1, Cursor: *adminOptions.NextCursor}); problemCode(err) != "INVALID_CURSOR" {
		t.Fatal("role option cursor accepted by full role list", err)
	}
	if _, err = f.service.ListUserRoleOptions(f.ctx, manager, ManagementListInput{Limit: 1, Cursor: in.Cursor, Query: "MEMBER"}); problemCode(err) != "INVALID_CURSOR" {
		t.Fatal("cursor accepted different search scope", err)
	}
	for _, invalid := range []ManagementListInput{{Limit: 1, Status: "ACTIVE"}, {Limit: 1, RoleID: role.ID}, {Limit: 1, Page: 1, Cursor: in.Cursor}} {
		if _, err = f.service.ListUserRoleOptions(f.ctx, manager, invalid); problemCode(err) != "IDENTITY_INVALID" {
			t.Fatal("user filters or mixed pagination accepted", err)
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/users/role-options?q=MEMBER&limit=1", nil).WithContext(f.ctx)
	response := httptest.NewRecorder()
	NewManagementHandler(f.service).ServeAuthenticated(response, request, manager)
	var result struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &result); response.Code != 200 || err != nil || len(result.Items) != 1 || len(result.Items[0]) != 3 {
		t.Fatal("role options failed or exposed full role metadata", response.Code, err)
	}
	var memberRole string
	if err = json.Unmarshal(result.Items[0]["id"], &memberRole); err != nil || memberRole == "" {
		t.Fatal("role option identity missing", err)
	}
	if _, err = f.service.CreateUser(f.ctx, manager, "user-options-create-01", UserCreateInput{DisplayName: "委派创建", Email: "delegated@example.com", IdentitySource: "oidc", Subject: "delegated", RoleID: memberRole}, "create-delegated"); err != nil {
		t.Fatal("user manager cannot assign a selected role without roles:read", err)
	}
	role, err = f.service.SaveRole(f.ctx, admin, role.ID, "", RoleInput{Code: role.Code, Name: role.Name, Permissions: []string{"users:read"}, ExpectedVersion: role.Version}, "restrict-governor")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.ListUserRoleOptions(f.ctx, manager, ManagementListInput{Limit: 20}); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("old role options session survived role change", err)
	}
	reader := f.login(t, "member")
	if _, err = f.service.ListUserRoleOptions(f.ctx, reader, ManagementListInput{Limit: 20}); err != nil {
		t.Fatal("user reader cannot filter roles", err)
	}
	if _, err = f.service.CreateUser(f.ctx, reader, "user-options-create-02", UserCreateInput{DisplayName: "禁止创建", Email: "denied@example.com", IdentitySource: "oidc", Subject: "denied", RoleID: memberRole}, "denied-create"); !errors.Is(err, ErrForbidden) {
		t.Fatal("role option access granted user mutation", err)
	}
}
