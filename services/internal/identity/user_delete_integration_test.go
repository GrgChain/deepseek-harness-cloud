package identity

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/userquota"
)

func TestUserDeleteConfirmationAssetsAuditAndSessionRevocation(t *testing.T) {
	f := managementDatabase(t)
	admin, member := f.login(t, "admin-a"), f.login(t, "member")
	target, err := f.service.GetUser(f.ctx, admin, member.UserID)
	if err != nil {
		t.Fatal(err)
	}
	name := target.DisplayName
	if name == "" {
		name = target.ID
	}
	input := UserDeleteInput{ExpectedVersion: target.Version, ConfirmedName: name}
	request := func(body string, p Principal) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodDelete, "/v1/users/"+target.ID, strings.NewReader(body)).WithContext(f.ctx)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		w.Header().Set("X-Request-ID", "delete-user-test")
		NewManagementHandler(f.service).ServeAuthenticated(w, r, p)
		return w
	}
	data, _ := json.Marshal(input)
	if w := request(string(data), member); w.Code != http.StatusForbidden {
		t.Fatal("member can delete user", w.Code)
	}
	for _, body := range []string{`{"expected_version":1}`, `{"expected_version":1,"confirmed_name":"wrong"}`, `{"expected_version":1,"confirmed_name":"wrong","role":"ADMIN"}`} {
		if w := request(body, admin); w.Code < 400 || w.Code >= 500 {
			t.Fatal("invalid confirmation accepted", w.Code)
		}
	}
	stale := input
	stale.ExpectedVersion++
	if err = f.service.DeleteUser(f.ctx, admin, target.ID, stale, "stale"); problemCode(err) != "VERSION_CONFLICT" {
		t.Fatal("stale deletion accepted", err)
	}
	self, err := f.service.GetUser(f.ctx, admin, admin.UserID)
	if err != nil {
		t.Fatal(err)
	}
	selfName := self.DisplayName
	if selfName == "" {
		selfName = self.ID
	}
	if err = f.service.DeleteUser(f.ctx, admin, self.ID, UserDeleteInput{ExpectedVersion: self.Version, ConfirmedName: selfName}, "self"); problemCode(err) != "SELF_CHANGE_FORBIDDEN" {
		t.Fatal("self deletion accepted", err)
	}
	if err = f.service.DeleteUser(f.ctx, admin, newID(), input, "foreign"); problemCode(err) != "NOT_FOUND" {
		t.Fatal("foreign identity exposed", err)
	}
	must := func(sql string, args ...any) {
		t.Helper()
		if _, e := f.root.Exec(f.ctx, sql, args...); e != nil {
			t.Fatal(e)
		}
	}
	must(`INSERT INTO quota_policies(org_id) VALUES($1)`, f.org)
	limits := userquota.Limits{Instances: 1, CPU: 1000, Memory: 1 << 30, Storage: 10 << 30}
	if _, err = f.service.SaveUserQuota(f.ctx, admin, target.ID, quotaInput(t, 0, limits), "quota"); err != nil {
		t.Fatal(err)
	}
	hold := newID()
	must(`INSERT INTO recovery_quota_holds(org_id,owner_id,instance_id,resource_type,amount,inventory_run_id,inventory_sha256,reason_code,operator_ref) VALUES($1,$2,$3,'STORAGE',1,$4,$5,'TEST_RETAIN','delete-test')`, f.org, target.ID, hold, newID(), strings.Repeat("b", 64))
	if w := request(string(data), admin); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "USER_HAS_ASSETS") {
		t.Fatal("recovery asset allowed deletion", w.Code, w.Body.String())
	}
	must(`DELETE FROM recovery_quota_holds WHERE org_id=$1 AND instance_id=$2`, f.org, hold)
	must(`CREATE FUNCTION reject_user_delete_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='USER_ARCHIVE' THEN RAISE EXCEPTION 'test audit failure'; END IF; RETURN NEW; END $$;
CREATE TRIGGER reject_user_delete_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_user_delete_audit()`)
	if err = f.service.DeleteUser(f.ctx, admin, target.ID, input, "rollback"); err == nil {
		t.Fatal("audit failure allowed deletion")
	}
	after, err := f.service.GetUser(f.ctx, admin, target.ID)
	if err != nil || after.Status != "ACTIVE" || after.Version != target.Version {
		t.Fatal("failed deletion changed user", err)
	}
	tx, err := f.app.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = RequirePermission(f.ctx, tx, member, "instances:read")
	tx.Rollback(f.ctx)
	if err != nil {
		t.Fatal("failed deletion revoked session", err)
	}
	must(`DROP TRIGGER reject_user_delete_audit ON audit_events`)
	if w := request(string(data), admin); w.Code != http.StatusNoContent || w.Body.Len() != 0 {
		t.Fatal("delete endpoint failed", w.Code, w.Body.String())
	}
	after, err = f.service.GetUser(f.ctx, admin, target.ID)
	if err != nil || after.Status != "ARCHIVED" || after.Version != target.Version+1 {
		t.Fatal("deleted identity tombstone missing", err)
	}
	page, err := f.service.ListUsers(f.ctx, admin, ManagementListInput{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Items {
		if item.ID == target.ID {
			t.Fatal("deleted user appears in default user/quota list")
		}
	}
	page, err = f.service.ListUsers(f.ctx, admin, ManagementListInput{Limit: 100, Status: "ARCHIVED"})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != target.ID {
		t.Fatal("deleted history cannot be queried", err)
	}
	quota, err := f.service.GetUserQuota(f.ctx, admin, target.ID)
	if err != nil || !quota.Configured || quota.Limits != limits {
		t.Fatal("deletion lost quota history", err)
	}
	if _, err = f.service.SaveUserQuota(f.ctx, admin, target.ID, quotaInput(t, quota.Version, limits), "deleted-quota"); problemCode(err) != "STATE_CONFLICT" {
		t.Fatal("deleted quota can be changed", err)
	}
	tx, err = f.app.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = RequirePermission(f.ctx, tx, member, "instances:read")
	tx.Rollback(f.ctx)
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("deleted user session remains authorized", err)
	}
	var count int
	if err = f.root.QueryRow(f.ctx, `SELECT count(*) FROM audit_events WHERE action='USER_ARCHIVE' AND trace_id='delete-user-test'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("deletion audit missing or duplicated", count, err)
	}
}
