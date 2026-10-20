package worker

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type releaseHostFixture struct {
	calls int
	fail  bool
}

func (h *releaseHostFixture) Acquire(context.Context, string) (func(), error) { return func() {}, nil }
func (h *releaseHostFixture) ReclaimAsset(context.Context, ExecutionPlan) error {
	h.calls++
	if h.fail {
		return ErrStorageUnavailable
	}
	return nil
}
func TestOwnerStorageReleasePreservesQuotaUntilPhysicalAndAuditProof(t *testing.T) {
	f := newMonitorFixture(t)
	id, owner := f.instance(t)
	request := webapi.ID()
	var asset, name string
	if err := f.admin.QueryRow(f.ctx, `SELECT storage_asset_id::text,name FROM instances WHERE id=$1`, id).Scan(&asset, &name); err != nil {
		t.Fatal(err)
	}
	intent, _ := json.Marshal(map[string]any{"schema_version": 1, "action": "OWNER_RELEASE", "request_id": request})
	f.exec(t, `UPDATE instances SET desired_state='DELETED',observed_state='DELETED',deleted_at=clock_timestamp() WHERE id=$1`, id)
	f.exec(t, `UPDATE storage_assets SET status='RECLAIM_PENDING',maintenance_intent=$2,retention_until=clock_timestamp()+interval '7 days' WHERE id=$1`, asset, intent)
	f.exec(t, `INSERT INTO storage_release_requests(id,org_id,owner_id,instance_id,asset_id,confirmed_name,status) VALUES($1,$2,$3,$4,$5,$6,'ACCEPTED')`, request, f.org, owner, id, asset, name)
	host := &releaseHostFixture{}
	runner := NewStorageReleaser(f.pool, host)
	retained := func() {
		t.Helper()
		var amount int64
		var status string
		if err := f.admin.QueryRow(f.ctx, `SELECT a.status,(SELECT sum(amount) FROM quota_reservations WHERE instance_id=a.instance_id AND resource_type='STORAGE' AND status='USED') FROM storage_assets a WHERE a.id=$1`, asset).Scan(&status, &amount); err != nil || status != "RECLAIM_PENDING" || amount != 10<<30 {
			t.Fatal("unconfirmed release lost retained quota", status, amount, err)
		}
	}
	f.exec(t, `UPDATE storage_assets SET legal_hold=true WHERE id=$1`, asset)
	if err := runner.release(f.ctx, request); err == nil || host.calls != 0 {
		t.Fatal("legal hold reached physical deletion")
	}
	retained()
	f.exec(t, `UPDATE storage_assets SET legal_hold=false WHERE id=$1`, asset)
	f.exec(t, `UPDATE memberships SET status='DISABLED' WHERE org_id=$1 AND user_id=$2`, f.org, owner)
	if err := runner.release(f.ctx, request); err == nil || host.calls != 0 {
		t.Fatal("disabled owner reached physical deletion")
	}
	retained()
	f.exec(t, `UPDATE memberships SET status='ACTIVE' WHERE org_id=$1 AND user_id=$2`, f.org, owner)
	host.fail = true
	if err := runner.release(f.ctx, request); err == nil {
		t.Fatal("uncertain physical response declared success")
	}
	retained()
	host.fail = false
	f.exec(t, `CREATE OR REPLACE FUNCTION reject_owner_release_publish() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='STORAGE_OWNER_RELEASE' THEN RAISE EXCEPTION 'injected final failure'; END IF; RETURN NEW; END $$`)
	f.exec(t, `CREATE TRIGGER reject_owner_release_publish BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_owner_release_publish()`)
	if err := runner.release(f.ctx, request); err == nil {
		t.Fatal("missing final audit declared success")
	}
	retained()
	f.exec(t, `DROP TRIGGER reject_owner_release_publish ON audit_events`)
	if err := runner.release(f.ctx, request); err != nil {
		t.Fatal("same accepted early release could not reconcile", err)
	}
	calls := host.calls
	if err := runner.release(f.ctx, request); err != nil || host.calls != calls {
		t.Fatal("successful replay repeated physical deletion", err)
	}
	var amount, audits int64
	if err := f.admin.QueryRow(f.ctx, `SELECT coalesce(sum(amount),0) FROM quota_reservations WHERE instance_id=$1 AND resource_type='STORAGE' AND status IN ('HELD','USED')`, id).Scan(&amount); err != nil || amount != 0 {
		t.Fatal("confirmed storage not released", err)
	}
	if err := f.admin.QueryRow(f.ctx, `SELECT count(*) FROM audit_events WHERE resource_id=$1 AND action='STORAGE_OWNER_RELEASE' AND outcome='SUCCEEDED'`, asset).Scan(&audits); err != nil || audits != 1 {
		t.Fatal("release did not audit exactly once", audits, err)
	}
}
