package identity

import (
	"fmt"
	"testing"
)

func TestManagementNumberedPagesTotalsFiltersAndDeletion(t *testing.T) {
	f := managementDatabase(t)
	admin := f.login(t, "admin-a")
	var roleID string
	if err := f.root.QueryRow(f.ctx, `SELECT id::text FROM roles WHERE org_id=$1 AND code='MEMBER'`, f.org).Scan(&roleID); err != nil {
		t.Fatal(err)
	}
	for i := range 7 {
		_, err := f.service.CreateUser(f.ctx, admin, fmt.Sprintf("pagination-user-key-%02d", i), UserCreateInput{DisplayName: fmt.Sprintf("分页成员 %02d", i), Email: fmt.Sprintf("page%d@example.com", i), IdentitySource: "oidc", Subject: fmt.Sprintf("page-user-%d", i), RoleID: roleID}, "page-create")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.service.SaveRole(f.ctx, admin, "", fmt.Sprintf("pagination-role-key-%02d", i), RoleInput{Code: fmt.Sprintf("page_%02d", i), Name: fmt.Sprintf("分页角色 %02d", i), Permissions: []string{}}, "role-page-create"); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	var tail ManagedUser
	for n := 1; n <= 4; n++ {
		page, err := f.service.ListUsers(f.ctx, admin, ManagementListInput{Limit: 2, Page: n, Query: "分页成员", Status: "ACTIVE", RoleID: roleID})
		if err != nil || page.Total != 7 || len(page.Items) != min(2, 9-2*n) {
			t.Fatalf("numbered users page %d: %+v %v", n, page, err)
		}
		for _, user := range page.Items {
			if seen[user.ID] {
				t.Fatal("duplicate user across numbered pages")
			}
			seen[user.ID] = true
			tail = user
		}
	}
	last, err := f.service.ListUsers(f.ctx, admin, ManagementListInput{Limit: 2, Page: 4, Query: "分页成员"})
	if err != nil || last.Total != 7 || len(last.Items) != 1 || last.Items[0].ID != tail.ID || last.NextCursor != nil {
		t.Fatal("direct user tail differs from page traversal", err)
	}
	if err = f.service.DeleteUser(f.ctx, admin, tail.ID, UserDeleteInput{ExpectedVersion: tail.Version, ConfirmedName: tail.DisplayName}, "delete-tail"); err != nil {
		t.Fatal(err)
	}
	last, err = f.service.ListUsers(f.ctx, admin, ManagementListInput{Limit: 2, Page: 3, Query: "分页成员"})
	if err != nil || last.Total != 6 || len(last.Items) != 2 || last.NextCursor != nil {
		t.Fatal("user deletion did not update filtered total", err)
	}
	archived, err := f.service.ListUsers(f.ctx, admin, ManagementListInput{Limit: 2, Page: 1, Query: "分页成员", Status: "ARCHIVED"})
	if err != nil || archived.Total != 1 || len(archived.Items) != 1 || archived.Items[0].ID != tail.ID {
		t.Fatal("status total differs from filter", err)
	}
	roles, err := f.service.ListRoles(f.ctx, admin, ManagementListInput{Limit: 2, Page: 4, Query: "分页角色"})
	if err != nil || roles.Total != 7 || len(roles.Items) != 1 || roles.NextCursor != nil {
		t.Fatal("direct role tail or total invalid", err)
	}
	empty, err := f.service.ListRoles(f.ctx, admin, ManagementListInput{Limit: 2, Page: 1, Query: "不存在"})
	if err != nil || empty.Total != 0 || len(empty.Items) != 0 {
		t.Fatal("empty total invalid", err)
	}
	for _, input := range []ManagementListInput{{Limit: 2, Page: -1}, {Limit: 2, Page: 1_000_000_001}, {Limit: 2, Page: 1, Cursor: "unexpected"}} {
		if _, err = f.service.ListUsers(f.ctx, admin, input); problemCode(err) != "IDENTITY_INVALID" {
			t.Fatal("invalid page accepted", err)
		}
	}
}
