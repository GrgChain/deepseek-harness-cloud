package operations

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func exerciseRecoveryLedger(t *testing.T, ctx context.Context, pool *pgxpool.Pool, observer *inventoryFixtureObserver, database, org, owner string) {
	t.Helper()
	must := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	must(`REVOKE CONNECT ON DATABASE ` + pgx.Identifier{database}.Sanitize() + ` FROM PUBLIC,hc_api,hc_worker,hc_gateway,hc_model_gateway`)
	// The prior inventory test appended an unowned network deliberately.
	observer.resources = observer.resources[:4]
	observer.hook = nil
	inventory := NewRecoveryInventory(pool, observer, org, "recovery-ledger-fixture")
	reviewed, err := inventory.Run(ctx, database, true)
	if err != nil {
		t.Fatal(err)
	}
	service := NewRecoveryLedger(pool, observer, org, "recovery-ledger-fixture")
	run := func(id, reason string, apply bool) (RecoveryRetentionReport, error) {
		return service.Retain(ctx, database, id, reason, apply)
	}
	check := func(wantHolds, wantAudits int, wantStorage int64) {
		t.Helper()
		var holds, audits, instances, usedInstances int
		var storage int64
		var quarantined, revoked bool
		err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM recovery_quota_holds),(SELECT count(*) FROM audit_events WHERE action='RECOVERY_LEDGER_RETAIN'),(SELECT count(*) FROM instances),(SELECT coalesce(sum(amount),0) FROM quota_commitments WHERE resource_type='INSTANCE'),(SELECT coalesce(sum(amount),0) FROM quota_commitments WHERE resource_type='STORAGE'),(SELECT status='DISABLED' FROM organizations),(SELECT bool_and(revoked_at IS NOT NULL) FROM secrets)`).Scan(&holds, &audits, &instances, &usedInstances, &storage, &quarantined, &revoked)
		if err != nil || holds != wantHolds || audits != wantAudits || instances != 1 || usedInstances != max(0, wantHolds-1) || storage != wantStorage || !quarantined || !revoked {
			t.Fatalf("partial ledger or authorization change: %v holds=%d instances=%d charged=%d storage=%d", err, holds, instances, usedInstances, storage)
		}
	}
	preview, err := run(reviewed.RunID, "RECOVERY_RETAIN", false)
	if err != nil || preview.Applied || len(preview.Changes) != 3 || preview.After.Storage != 20<<30 || preview.After.Instances != 2 {
		t.Fatal("retention preview lost original instance deficit or orphan", err, preview)
	}
	// Existing fixture deliberately omitted its INSTANCE reservation.
	check(0, 0, 10<<30)
	if _, err = run("00000000-0000-4000-8000-000000000001", "RECOVERY_RETAIN", true); err == nil {
		t.Fatal("unreviewed inventory accepted")
	}
	if _, err = run(reviewed.RunID, "unsafe reason", true); err == nil {
		t.Fatal("unsafe reason accepted")
	}
	must(`CREATE FUNCTION reject_retention_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='RECOVERY_LEDGER_RETAIN' THEN RAISE EXCEPTION 'fixture'; END IF; RETURN NEW; END $$;CREATE TRIGGER reject_retention_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_retention_audit()`)
	if r, err := run(reviewed.RunID, "RECOVERY_RETAIN", true); err == nil || r.Applied {
		t.Fatal("late audit failure left holds")
	}
	check(0, 0, 10<<30)
	must(`DROP TRIGGER reject_retention_audit ON audit_events`)
	observer.calls = 0
	observer.hook = func(call int) {
		if call == 2 {
			observer.resources[3].Capacity++
		}
	}
	if _, err := run(reviewed.RunID, "RECOVERY_RETAIN", true); err == nil {
		t.Fatal("changing physical inventory accepted")
	}
	observer.resources[3].Capacity--
	observer.hook = nil
	observer.calls = 0
	observer.hook = func(call int) {
		if call == 1 {
			must(`UPDATE quota_reservations SET amount=amount+1 WHERE resource_type='STORAGE'`)
		}
	}
	if _, err := run(reviewed.RunID, "RECOVERY_RETAIN", true); err == nil {
		t.Fatal("database changed during observation but adopted")
	}
	must(`UPDATE quota_reservations SET amount=10737418240 WHERE resource_type='STORAGE'`)
	observer.hook = nil
	result, err := run(reviewed.RunID, "RECOVERY_RETAIN", true)
	if err != nil || !result.Applied || result.PromotionAllowed {
		t.Fatal("retention was not committed in quarantine", err)
	}
	check(3, 1, 20<<30)
	replay, err := run(reviewed.RunID, "RECOVERY_RETAIN", true)
	if err != nil || replay.RunID != result.RunID {
		t.Fatal("retention replay duplicated charge", err)
	}
	check(3, 1, 20<<30)
	if _, err := run(reviewed.RunID, "DIFFERENT_INTENT", true); err == nil {
		t.Fatal("different intent treated as exact replay")
	}
	updated, err := inventory.Run(ctx, database, false)
	if err != nil || updated.Ledger.Storage != 20<<30 || updated.Conservative.Storage != 20<<30 || updated.Ledger.Instances != 2 {
		t.Fatal("subsequent inventory undercounted adopted holds", err)
	}
	// Granting read-only quota usage cannot grant alteration of trusted holds.
	must(`GRANT SELECT ON quota_commitments TO hc_api`)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SET LOCAL ROLE hc_api`); err != nil {
		t.Fatal(err)
	}
	var amount int64
	if err = tx.QueryRow(ctx, `SELECT sum(amount) FROM quota_commitments WHERE org_id=$1 AND owner_id=$2 AND resource_type='STORAGE'`, org, owner).Scan(&amount); err != nil || amount != 20<<30 {
		t.Fatal("restricted API quota excluded retained orphan", err)
	}
	if _, err = tx.Exec(ctx, `UPDATE recovery_quota_holds SET amount=1`); err == nil {
		t.Fatal("API modified recovery holds")
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var original int64
	if err = pool.QueryRow(ctx, `SELECT sum(amount) FROM quota_reservations WHERE resource_type='STORAGE'`).Scan(&original); err != nil || original != 10<<30 {
		t.Fatal("retention rewrote original reservations", err)
	}
}
