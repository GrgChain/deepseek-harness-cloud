package identity

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/userquota"
)

func quotaInput(t *testing.T, version int64, limits userquota.Limits) UserQuotaInput {
	t.Helper()
	data, _ := json.Marshal(map[string]any{"expected_version": version, "limits": limits})
	var input UserQuotaInput
	if err := json.Unmarshal(data, &input); err != nil {
		t.Fatal(err)
	}
	return input
}

func TestUserQuotasDefaultIsolationUsageVersionsAndAtomicAudit(t *testing.T) {
	f := managementDatabase(t)
	admin, member := f.login(t, "admin-a"), f.login(t, "member")
	otherAdmin := f.login(t, "admin-b")
	must := func(sql string, args ...any) {
		t.Helper()
		if _, err := f.root.Exec(f.ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	must(`INSERT INTO quota_policies(org_id) VALUES($1)`, f.org)
	v, err := f.service.GetUserQuota(f.ctx, admin, member.UserID)
	if err != nil || v.Configured || v.Version != 0 || v.Limits != (userquota.Limits{Instances: 2, CPU: 10000, Memory: 20 << 30, Storage: 100 << 30}) {
		t.Fatalf("default inheritance: %+v %v", v, err)
	}
	// An override can raise the uniform instance default while staying inside
	// the shared organization budget. Concurrent editors cannot overwrite it.
	limits := userquota.Limits{Instances: 3, CPU: 4000, Memory: 8 << 30, Storage: 20 << 30}
	input := quotaInput(t, 0, limits)
	if _, err = f.service.GetUserQuota(f.ctx, member, member.UserID); !errors.Is(err, ErrForbidden) {
		t.Fatal("member read administrator quotas", err)
	}
	member.Role = "ADMIN"
	member.Permissions = admin.Permissions
	if _, err = f.service.SaveUserQuota(f.ctx, member, member.UserID, input, "forged"); !errors.Is(err, ErrForbidden) {
		t.Fatal("presentation permissions granted write", err)
	}
	if _, err = f.service.SaveUserQuota(f.ctx, admin, newID(), input, "foreign"); problemCode(err) != "NOT_FOUND" {
		t.Fatal("foreign membership revealed", err)
	}
	invalid := quotaInput(t, 0, limits)
	invalid.Limits.CPU = nil
	if _, err = f.service.SaveUserQuota(f.ctx, admin, member.UserID, invalid, "missing"); problemCode(err) != "IDENTITY_INVALID" {
		t.Fatal("missing cap treated as zero", err)
	}
	over := limits
	over.Instances = 11
	if _, err = f.service.SaveUserQuota(f.ctx, admin, member.UserID, quotaInput(t, 0, over), "over"); problemCode(err) != "IDENTITY_INVALID" {
		t.Fatal("user exceeds shared cap", err)
	}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, editor := range []Principal{admin, otherAdmin} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := f.service.SaveUserQuota(f.ctx, editor, member.UserID, input, "race")
			results <- e
		}()
	}
	wg.Wait()
	close(results)
	wins, conflicts := 0, 0
	for err := range results {
		if err == nil {
			wins++
		} else if problemCode(err) == "VERSION_CONFLICT" {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("concurrent editors: %d wins %d conflicts", wins, conflicts)
	}
	own, err := f.service.GetUserQuota(f.ctx, admin, admin.UserID)
	if err != nil || own.Configured || own.Limits.Instances != 2 {
		t.Fatal("other user quota changed", own, err)
	}
	hold := newID()
	for resource, amount := range map[string]int64{"INSTANCE": 1, "RUNNING": 1, "CPU": 2000, "MEMORY": 4 << 30, "STORAGE": 10 << 30} {
		must(`INSERT INTO recovery_quota_holds(org_id,owner_id,instance_id,resource_type,amount,inventory_run_id,inventory_sha256,reason_code,operator_ref) VALUES($1,$2,$3,$4,$5,$6,$7,'TEST_RETAIN','quota-test')`, f.org, member.UserID, hold, resource, amount, newID(), strings.Repeat("b", 64))
	}
	v, err = f.service.GetUserQuota(f.ctx, admin, member.UserID)
	if err != nil || v.Usage != (userquota.Limits{Instances: 1, CPU: 2000, Memory: 4 << 30, Storage: 10 << 30}) {
		t.Fatal("recovery holds omitted", v, err)
	}
	for resource := range limits.Resources() {
		lowered := v.Usage
		switch resource {
		case "INSTANCE":
			lowered.Instances--
		case "CPU":
			lowered.CPU--
		case "MEMORY":
			lowered.Memory--
		case "STORAGE":
			lowered.Storage--
		}
		if _, err = f.service.SaveUserQuota(f.ctx, admin, member.UserID, quotaInput(t, 1, lowered), "decrease"); problemCode(err) != "QUOTA_BELOW_USAGE" {
			t.Fatal("decrease below occupied "+resource, err)
		}
	}
	must(`DELETE FROM recovery_quota_holds WHERE org_id=$1 AND instance_id=$2`, f.org, hold)
	zero := userquota.Limits{}
	v, err = f.service.SaveUserQuota(f.ctx, admin, member.UserID, quotaInput(t, 1, zero), "zero")
	if err != nil || v.Version != 2 || v.Limits != zero {
		t.Fatal("explicit zero limits", v, err)
	}
	// Failure at the last write must roll back the entire policy update.
	must(`CREATE FUNCTION quota_audit_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='USER_QUOTA_UPDATE' THEN RAISE EXCEPTION 'injected audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER quota_audit_failure BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION quota_audit_failure()`)
	if _, err = f.service.SaveUserQuota(f.ctx, admin, member.UserID, quotaInput(t, 2, limits), "audit-failure"); err == nil {
		t.Fatal("audit failure ignored")
	}
	v, err = f.service.GetUserQuota(f.ctx, admin, member.UserID)
	if err != nil || v.Version != 2 || v.Limits != zero {
		t.Fatal("partial update after audit failure", v, err)
	}
	must(`UPDATE users SET status='ARCHIVED' WHERE id=$1`, member.UserID)
	if _, err = f.service.SaveUserQuota(f.ctx, admin, member.UserID, quotaInput(t, 2, limits), "archived"); problemCode(err) != "STATE_CONFLICT" {
		t.Fatal("archived target accepted quota change", err)
	}
	var audits int
	if err = f.root.QueryRow(f.ctx, `SELECT count(*) FROM audit_events WHERE action='USER_QUOTA_UPDATE' AND owner_id=$1 AND details ? 'before' AND details ? 'after'`, member.UserID).Scan(&audits); err != nil || audits != 2 {
		t.Fatal("quota audit coverage", audits, err)
	}
}
