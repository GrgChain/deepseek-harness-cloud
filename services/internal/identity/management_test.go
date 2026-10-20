package identity

import "testing"

func TestCustomRoleAllowsAllPermissionGroupsAndRejectsInvalidSets(t *testing.T) {
	for _, permissions := range [][]string{
		{"users:read", "users:manage"},
		{"roles:read", "roles:manage"},
		{"configuration:read", "models:catalog:manage"},
		{"audit:read", "audit:organization"},
	} {
		if err := validateRoleInput(RoleInput{Code: "developer", Name: "开发者", Permissions: permissions}); err != nil {
			t.Fatalf("valid permission set rejected: %v: %v", permissions, err)
		}
	}
	for _, permissions := range [][]string{
		{"users:manage"}, {"roles:manage"}, {"*"}, {"unknown:read"},
		{"instances:create"}, {"model:write"}, {"audit:organization"},
		{"instances:read", "instances:read"},
	} {
		if err := validateRoleInput(RoleInput{Code: "developer", Name: "开发者", Permissions: permissions}); err == nil {
			t.Fatalf("unsafe permission set accepted: %v", permissions)
		}
	}
}

func TestEmptyCustomRoleIsValidAndForgedRoleDoesNotGrantPermissions(t *testing.T) {
	if err := validateRoleInput(RoleInput{Code: "suspended", Name: "暂不授权", Permissions: []string{}}); err != nil {
		t.Fatal(err)
	}
	if (Principal{Role: "ADMIN"}).Has("users:manage") {
		t.Fatal("role name alone grants permissions")
	}
}
