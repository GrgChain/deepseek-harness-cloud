package operations

import (
	"context"
	"encoding/json"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

var recoveryReason = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

type RecoveryLedger struct{ inventory *RecoveryInventory }

func NewRecoveryLedger(pool *pgxpool.Pool, observer InventoryObserver, org, operator string) *RecoveryLedger {
	return &RecoveryLedger{NewRecoveryInventory(pool, observer, org, operator)}
}

func recoveryHolds(ctx context.Context, q inventoryQuerier, org string) ([]RecoveryQuotaHold, error) {
	rows, err := q.Query(ctx, `SELECT owner_id::text,instance_id::text,resource_type,amount FROM recovery_quota_holds WHERE org_id=$1 ORDER BY instance_id,resource_type LIMIT 6001`, org)
	if err != nil {
		return nil, ErrRecoveryLedger
	}
	defer rows.Close()
	holds := []RecoveryQuotaHold{}
	for rows.Next() {
		var h RecoveryQuotaHold
		if rows.Scan(&h.OwnerID, &h.InstanceID, &h.Resource, &h.Amount) != nil {
			return nil, ErrRecoveryLedger
		}
		holds = append(holds, h)
	}
	if rows.Err() != nil || len(holds) > 6000 {
		return nil, ErrRecoveryLedger
	}
	return holds, nil
}
func recoveryOwners(ctx context.Context, q inventoryQuerier, org string) (map[string]string, error) {
	rows, err := q.Query(ctx, `SELECT u.workspace_owner_id::text,u.id::text FROM users u JOIN memberships m ON m.user_id=u.id WHERE m.org_id=$1 ORDER BY u.id LIMIT 1001`, org)
	if err != nil {
		return nil, ErrRecoveryLedger
	}
	defer rows.Close()
	owners := map[string]string{}
	for rows.Next() {
		var workspaceOwner, owner string
		if rows.Scan(&workspaceOwner, &owner) != nil {
			return nil, ErrRecoveryLedger
		}
		owners[workspaceOwner] = owner
	}
	if rows.Err() != nil || len(owners) > 1000 {
		return nil, ErrRecoveryLedger
	}
	return owners, nil
}

// Retain adds actual quota commitments to a restored database. External calls
// finish before a bounded transaction. Every mutation and its final audit share
// one commit; apply always rechecks quarantine, ownership and captured DB state.
func (s *RecoveryLedger) Retain(ctx context.Context, confirmed, inventoryID, reason string, apply bool) (RecoveryRetentionReport, error) {
	r := RecoveryRetentionReport{InventoryRunID: inventoryID, Quarantined: true, Changes: []RecoveryQuotaChange{}}
	if s == nil || s.inventory == nil || s.inventory.pool == nil || s.inventory.observer == nil || !uuid.MatchString(s.inventory.organization) || !operatorName.MatchString(s.inventory.operator) || !uuid.MatchString(inventoryID) || !recoveryReason.MatchString(reason) {
		return r, ErrInput
	}
	i := s.inventory
	if err := db.Check(ctx, i.pool); err != nil {
		return r, err
	}
	if err := i.guard(ctx, i.pool, confirmed); err != nil {
		return r, err
	}
	expected, err := i.expected(ctx, i.pool)
	if err != nil {
		return r, err
	}
	holds, err := recoveryHolds(ctx, i.pool, i.organization)
	if err != nil {
		return r, err
	}
	owners, err := recoveryOwners(ctx, i.pool, i.organization)
	if err != nil {
		return r, err
	}
	first, err := i.observer.Observe(ctx)
	if err != nil {
		return r, err
	}
	second, err := i.observer.Observe(ctx)
	if err != nil || observationHash(first) != observationHash(second) {
		return r, ErrRecoveryLedger
	}
	changes, report, err := PlanRecoveryRetention(i.organization, expected, second, holds, owners)
	if err != nil {
		return r, err
	}
	r.SnapshotSHA256, r.Before, r.After, r.Changes = report.SnapshotSHA256, report.Ledger, report.Conservative, changes
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := i.pool.Begin(bounded)
	if err != nil {
		return r, ErrRecoveryLedger
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(bounded, `SET LOCAL lock_timeout='1s';SET LOCAL statement_timeout='4s';SET LOCAL idle_in_transaction_session_timeout='5s';SELECT pg_advisory_xact_lock(731804527);LOCK TABLE organizations,users,memberships,instances,storage_assets,quota_policies,quota_reservations,recovery_quota_holds,operations,access_routes,native_sessions,portal_sessions,access_sessions,secrets,model_configurations IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return r, ErrRecoveryLedger
	}
	if err = i.guard(bounded, tx, confirmed); err != nil {
		return r, err
	}
	current, err := i.expected(bounded, tx)
	if err != nil || inventoryHash(current) != inventoryHash(expected) {
		return r, ErrRecoveryLedger
	}
	currentHolds, err := recoveryHolds(bounded, tx, i.organization)
	if err != nil || inventoryHash(currentHolds) != inventoryHash(holds) {
		return r, ErrRecoveryLedger
	}
	currentOwners, err := recoveryOwners(bounded, tx, i.organization)
	if err != nil || inventoryHash(currentOwners) != inventoryHash(owners) {
		return r, ErrRecoveryLedger
	}
	var savedRaw []byte
	err = tx.QueryRow(bounded, `SELECT details FROM audit_events WHERE org_id=$1 AND action='RECOVERY_LEDGER_RETAIN' AND outcome='SUCCEEDED' AND details->>'inventory_run_id'=$2 ORDER BY timestamp DESC,id DESC LIMIT 1`, i.organization, inventoryID).Scan(&savedRaw)
	if err == nil {
		var saved retentionAudit
		if json.Unmarshal(savedRaw, &saved) != nil || saved.Operator != i.operator || saved.Reason != reason || saved.ExpectedHash != inventoryHash(expected) || saved.ObservedHash != observationHash(second) || saved.OwnersHash != inventoryHash(owners) || saved.HoldsHash != inventoryHash(holds) || !saved.Report.Applied || saved.Report.PromotionAllowed || !saved.Report.Quarantined {
			return r, ErrRecoveryLedger
		}
		if !apply {
			saved.Report.Applied = false
		}
		return saved.Report, nil
	}
	if err != pgx.ErrNoRows {
		return r, ErrRecoveryLedger
	}
	var reviewedRaw []byte
	if err = tx.QueryRow(bounded, `SELECT details->'report' FROM audit_events WHERE id=$1 AND org_id=$2 AND action='RECOVERY_INVENTORY' AND outcome='SUCCEEDED'`, inventoryID, i.organization).Scan(&reviewedRaw); err != nil {
		return r, ErrRecoveryLedger
	}
	var reviewed InventoryReport
	if json.Unmarshal(reviewedRaw, &reviewed) != nil || reviewed.RunID != inventoryID || !reviewed.Applied || !reviewed.Quarantined || reviewed.PromotionAllowed || reviewed.SnapshotSHA256 != report.SnapshotSHA256 {
		return r, ErrRecoveryLedger
	}
	if !apply {
		return r, nil
	}
	r.RunID = webapi.ID()
	for _, change := range changes {
		var tag pgconn.CommandTag
		if tag, err = tx.Exec(bounded, `INSERT INTO recovery_quota_holds(org_id,owner_id,instance_id,resource_type,amount,inventory_run_id,inventory_sha256,reason_code,operator_ref) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(org_id,instance_id,resource_type) DO UPDATE SET amount=EXCLUDED.amount,inventory_run_id=EXCLUDED.inventory_run_id,inventory_sha256=EXCLUDED.inventory_sha256,reason_code=EXCLUDED.reason_code,operator_ref=EXCLUDED.operator_ref,updated_at=clock_timestamp() WHERE recovery_quota_holds.owner_id=EXCLUDED.owner_id AND recovery_quota_holds.amount<EXCLUDED.amount`, i.organization, change.OwnerID, change.InstanceID, change.Resource, change.After, inventoryID, report.SnapshotSHA256, reason, i.operator); err != nil || tag.RowsAffected() != 1 {
			return r, ErrRecoveryLedger
		}
	}
	postHolds, err := recoveryHolds(bounded, tx, i.organization)
	if err != nil {
		return r, err
	}
	r.Applied = true
	audit := retentionAudit{1, i.operator, reason, inventoryID, inventoryHash(expected), observationHash(second), inventoryHash(owners), inventoryHash(postHolds), r}
	raw, err := json.Marshal(audit)
	if err != nil || len(raw) > 2<<20 {
		r.Applied = false
		return r, ErrRecoveryLedger
	}
	if _, err = tx.Exec(bounded, `INSERT INTO audit_events(id,org_id,action,outcome,trace_id,details) VALUES($1::uuid,$2,'RECOVERY_LEDGER_RETAIN','SUCCEEDED',$1::text,$3)`, r.RunID, i.organization, raw); err != nil {
		r.Applied = false
		return r, ErrRecoveryLedger
	}
	if err = tx.Commit(bounded); err != nil {
		r.Applied = false
		return r, ErrRecoveryLedger
	}
	return r, nil
}

type retentionAudit struct {
	Schema         int                     `json:"schema_version"`
	Operator       string                  `json:"operator_ref"`
	Reason         string                  `json:"reason_code"`
	InventoryRunID string                  `json:"inventory_run_id"`
	ExpectedHash   string                  `json:"expected_sha256"`
	ObservedHash   string                  `json:"observed_sha256"`
	OwnersHash     string                  `json:"owners_sha256"`
	HoldsHash      string                  `json:"holds_sha256"`
	Report         RecoveryRetentionReport `json:"report"`
}
