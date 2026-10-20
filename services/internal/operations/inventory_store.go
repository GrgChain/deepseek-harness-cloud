package operations

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/instances"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type RecoveryInventory struct {
	pool                   *pgxpool.Pool
	observer               InventoryObserver
	organization, operator string
}

func NewRecoveryInventory(pool *pgxpool.Pool, observer InventoryObserver, organization, operator string) *RecoveryInventory {
	return &RecoveryInventory{pool, observer, organization, operator}
}

type inventoryQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func (s *RecoveryInventory) guard(ctx context.Context, q inventoryQuerier, confirmed string) error {
	var database string
	var safe bool
	err := q.QueryRow(ctx, `SELECT current_database(),
 (SELECT count(*)=1 AND bool_and(id=$1::uuid AND status='DISABLED') FROM organizations)
 AND EXISTS(SELECT 1 FROM audit_events WHERE org_id=$1 AND action='RESTORE_ISOLATED' AND outcome='SUCCEEDED')
 AND NOT EXISTS(SELECT 1 FROM access_routes WHERE enabled)
 AND NOT EXISTS(SELECT 1 FROM native_sessions)
 AND NOT EXISTS(SELECT 1 FROM portal_sessions WHERE revoked_at IS NULL)
 AND NOT EXISTS(SELECT 1 FROM access_sessions WHERE revoked_at IS NULL)
 AND NOT EXISTS(SELECT 1 FROM secrets WHERE revoked_at IS NULL)
 AND NOT EXISTS(SELECT 1 FROM model_configurations WHERE revoked_at IS NULL OR status<>'REVOKED')
 AND NOT has_database_privilege('hc_api',current_database(),'CONNECT')
 AND NOT has_database_privilege('hc_worker',current_database(),'CONNECT')
 AND NOT has_database_privilege('hc_gateway',current_database(),'CONNECT')
 AND NOT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND usename IN ('hc_api','hc_worker','hc_gateway','hc_model_gateway'))`, s.organization).Scan(&database, &safe)
	if err != nil || !safe || database != confirmed || !databaseName.MatchString(database) || (!strings.HasPrefix(database, "hc_recovery_") && !strings.HasPrefix(database, "hc_test_recovery_")) {
		return ErrInventory
	}
	return nil
}
func (s *RecoveryInventory) expected(ctx context.Context, q inventoryQuerier) ([]InventoryInstance, error) {
	rows, err := q.Query(ctx, `SELECT i.id::text,coalesce(i.storage_asset_id::text,''),i.org_id::text,i.owner_id::text,u.workspace_owner_id::text,coalesce(i.workspace_id::text,''),i.observed_state,coalesce(a.status,''),coalesce(a.capacity_bytes,0),i.config_snapshot,
 EXISTS(SELECT 1 FROM operations op WHERE op.instance_id=i.id AND op.status IN ('ACCEPTED','RUNNING','UNCERTAIN'))
 OR i.desired_state<>CASE WHEN i.observed_state='DELETED' THEN 'DELETED' ELSE 'STOPPED' END
 OR (a.status<>'DELETED' AND a.filesystem_id IS DISTINCT FROM a.id::text),
 coalesce((SELECT jsonb_object_agg(resource_type,amount) FROM(SELECT resource_type,sum(amount) amount FROM quota_reservations WHERE instance_id=i.id AND status IN ('HELD','USED') GROUP BY resource_type) r),'{}')
 FROM instances i JOIN users u ON u.id=i.owner_id LEFT JOIN storage_assets a ON a.id=i.storage_asset_id AND a.instance_id=i.id AND a.org_id=i.org_id AND a.owner_id=i.owner_id WHERE i.org_id=$1 ORDER BY i.id LIMIT 1001`, s.organization)
	if err != nil {
		return nil, ErrInventory
	}
	defer rows.Close()
	out := []InventoryInstance{}
	for rows.Next() {
		var e InventoryInstance
		var snapshot, reservations []byte
		if err = rows.Scan(&e.ID, &e.AssetID, &e.OrganizationID, &e.OwnerID, &e.WorkspaceOwner, &e.WorkspaceID, &e.State, &e.StorageStatus, &e.Storage, &snapshot, &e.ActiveOperation, &reservations); err != nil {
			return nil, ErrInventory
		}
		frozen, err := instances.DecodeSnapshot(snapshot)
		if err != nil {
			return nil, ErrInventory
		}
		e.TemplateVersion = frozen.TemplateVersion
		var amounts map[string]int64
		if json.Unmarshal(reservations, &amounts) != nil {
			return nil, ErrInventory
		}
		e.Reserved = InventoryQuota{Instances: amounts["INSTANCE"], Running: amounts["RUNNING"], CPU: amounts["CPU"], Memory: amounts["MEMORY"], Storage: amounts["STORAGE"], Concurrent: amounts["CONCURRENT"]}
		out = append(out, e)
	}
	if rows.Err() != nil || len(out) > 1000 {
		return nil, ErrInventory
	}
	return out, nil
}
func observationHash(v []ResourceObservation) string {
	v = append([]ResourceObservation(nil), v...)
	sort.Slice(v, func(i, j int) bool { return v[i].Kind+":"+v[i].ExternalID < v[j].Kind+":"+v[j].ExternalID })
	return inventoryHash(v)
}

// Run only appends a quarantine register in the isolated restored database.
// The complete report and audit are one append-only record, so neither can
// survive without the other. It never modifies the original quota ledger or
// any external resource, and never permits promotion (even with zero blockers).
func (s *RecoveryInventory) Run(ctx context.Context, confirmed string, apply bool) (InventoryReport, error) {
	var report InventoryReport
	if s.pool == nil || s.observer == nil || !uuid.MatchString(s.organization) || !operatorName.MatchString(s.operator) {
		return report, ErrInput
	}
	if err := db.Check(ctx, s.pool); err != nil {
		return report, err
	}
	if err := s.guard(ctx, s.pool, confirmed); err != nil {
		return report, err
	}
	expected, err := s.expected(ctx, s.pool)
	if err != nil {
		return report, err
	}
	holds, err := recoveryHolds(ctx, s.pool, s.organization)
	if err != nil {
		return report, err
	}
	first, err := s.observer.Observe(ctx)
	if err != nil {
		return report, err
	}
	second, err := s.observer.Observe(ctx)
	if err != nil {
		return report, err
	}
	if observationHash(first) != observationHash(second) {
		return report, ErrInventory
	}
	report, err = compareInventoryReport(expected, second, holds)
	if err != nil {
		return report, err
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.pool.Begin(bounded)
	if err != nil {
		return report, ErrInventory
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(bounded, `SET LOCAL lock_timeout='1s'; SET LOCAL statement_timeout='4s'; SELECT pg_advisory_xact_lock(731804527); LOCK TABLE organizations,users,memberships,instances,storage_assets,quota_reservations,recovery_quota_holds,operations,access_routes,native_sessions,portal_sessions,access_sessions,secrets,model_configurations IN SHARE MODE`); err != nil {
		return report, ErrInventory
	}
	if err = s.guard(bounded, tx, confirmed); err != nil {
		return report, err
	}
	current, err := s.expected(bounded, tx)
	if err != nil || inventoryHash(current) != inventoryHash(expected) {
		return report, ErrInventory
	}
	currentHolds, err := recoveryHolds(bounded, tx, s.organization)
	if err != nil || inventoryHash(currentHolds) != inventoryHash(holds) {
		return report, ErrInventory
	}
	if !apply {
		return report, nil
	}
	var previous []byte
	err = tx.QueryRow(bounded, `SELECT details->'report' FROM audit_events WHERE org_id=$1 AND action='RECOVERY_INVENTORY' AND details->'report'->>'snapshot_sha256'=$2 ORDER BY timestamp DESC,id DESC LIMIT 1`, s.organization, report.SnapshotSHA256).Scan(&previous)
	if err == nil {
		var saved InventoryReport
		if json.Unmarshal(previous, &saved) != nil || !saved.Applied || saved.PromotionAllowed || !saved.Quarantined {
			return report, ErrInventory
		}
		return saved, nil
	}
	if err != pgx.ErrNoRows {
		return report, ErrInventory
	}
	report.RunID = webapi.ID()
	report.Applied = true
	raw, err := json.Marshal(map[string]any{"schema_version": 1, "operator_ref": s.operator, "report": report})
	if err != nil || len(raw) > 2<<20 {
		return report, ErrInventory
	}
	if _, err = tx.Exec(bounded, `INSERT INTO audit_events(id,org_id,action,outcome,trace_id,details) VALUES($1::uuid,$2,'RECOVERY_INVENTORY','SUCCEEDED',$1::text,$3)`, report.RunID, s.organization, raw); err != nil {
		report.Applied = false
		return report, ErrInventory
	}
	if err = tx.Commit(bounded); err != nil {
		report.Applied = false
		return report, ErrInventory
	}
	return report, nil
}
