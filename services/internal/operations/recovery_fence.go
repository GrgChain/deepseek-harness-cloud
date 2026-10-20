package operations

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

var ErrRecoveryFence = errors.New("source fence requires a reconciled isolated target and stable stopped resources; if source fencing committed, replay the same intent to finish connection draining")

type RecoverySourceFenceReport struct {
	RunID             string `json:"run_id,omitempty"`
	DrainRunID        string `json:"drain_run_id,omitempty"`
	SourceDatabase    string `json:"source_database"`
	TargetDatabase    string `json:"target_database"`
	InventoryRunID    string `json:"inventory_run_id"`
	Applied           bool   `json:"applied"`
	Drained           bool   `json:"drained"`
	TargetQuarantined bool   `json:"target_quarantined"`
}

type RecoverySourceFence struct {
	source, target *RecoveryInventory
	drain          func(context.Context, *pgxpool.Pool) error
}

func NewRecoverySourceFence(source, target *pgxpool.Pool, observer InventoryObserver, org, operator string) *RecoverySourceFence {
	return &RecoverySourceFence{NewRecoveryInventory(source, observer, org, operator), NewRecoveryInventory(target, observer, org, operator), drainSourceConnections}
}

const fenceTables = adoptionTables + `,model_gateway_routes,model_gateway_keys,identity_accounts,identity_authorizations,identity_codes,identity_access_tokens,model_validations,probe_budgets,oidc_transactions,access_tickets,access_challenges,instance_health`

// This digest covers durable authorization and original instance metadata, not
// expiring session history or plaintext/ciphertext credential material.
func recoveryPolicyHash(ctx context.Context, q inventoryQuerier, org string) (string, error) {
	var raw []byte
	err := q.QueryRow(ctx, `SELECT jsonb_build_object(
 'org',(SELECT to_jsonb(o) FROM organizations o WHERE id=$1),
 'identities',(SELECT coalesce(jsonb_agg(jsonb_build_array(u.id,u.identity_issuer,u.identity_subject,u.workspace_owner_id,u.status,m.role,m.status,m.auth_generation) ORDER BY u.id),'[]') FROM users u JOIN memberships m ON m.user_id=u.id WHERE m.org_id=$1),
 'instances',(SELECT coalesce(jsonb_agg(to_jsonb(i) ORDER BY i.id),'[]') FROM instances i WHERE i.org_id=$1),
 'assets',(SELECT coalesce(jsonb_agg(to_jsonb(a) ORDER BY a.id),'[]') FROM storage_assets a WHERE a.org_id=$1),
 'quota',(SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY r.id),'[]') FROM quota_reservations r WHERE r.org_id=$1),
 'holds',(SELECT coalesce(jsonb_agg(to_jsonb(h) ORDER BY h.instance_id,h.resource_type),'[]') FROM recovery_quota_holds h WHERE h.org_id=$1),
 'heads',(SELECT coalesce(jsonb_agg(to_jsonb(h) ORDER BY h.id),'[]') FROM model_configuration_heads h WHERE org_id=$1),
 'models',(SELECT coalesce(jsonb_agg(to_jsonb(c) ORDER BY c.id,c.version),'[]') FROM model_configurations c WHERE org_id=$1),
 'secrets',(SELECT coalesce(jsonb_agg(jsonb_build_array(id,org_id,key_id,revoked_at) ORDER BY id),'[]') FROM secrets WHERE org_id=$1),
 'defaults',(SELECT coalesce(jsonb_agg(to_jsonb(d)),'[]') FROM organization_defaults d WHERE org_id=$1))`, org).Scan(&raw)
	if err != nil || len(raw) > 4<<20 {
		return "", ErrRecoveryFence
	}
	return inventoryHash(json.RawMessage(raw)), nil
}

func recoveryOwnerIdentities(ctx context.Context, q inventoryQuerier, org string) (string, error) {
	var raw []byte
	err := q.QueryRow(ctx, `SELECT coalesce(jsonb_agg(jsonb_build_array(u.id,u.identity_issuer,u.identity_subject,u.workspace_owner_id) ORDER BY u.id),'[]') FROM users u JOIN memberships m ON m.user_id=u.id WHERE m.org_id=$1 AND EXISTS(SELECT 1 FROM instances i WHERE i.org_id=m.org_id AND i.owner_id=u.id)`, org).Scan(&raw)
	if err != nil {
		return "", ErrRecoveryFence
	}
	return inventoryHash(json.RawMessage(raw)), nil
}

func (s *RecoverySourceFence) sourceGuard(ctx context.Context, q inventoryQuerier, confirmed string) (bool, error) {
	var database string
	var safe, active bool
	err := q.QueryRow(ctx, `SELECT current_database(),
 (SELECT count(*)=1 AND bool_and(id=$1::uuid) FROM organizations)
 AND NOT EXISTS(SELECT 1 FROM instances WHERE NOT ((desired_state='STOPPED' AND observed_state='STOPPED') OR (desired_state='DELETED' AND observed_state='DELETED')))
 AND NOT EXISTS(SELECT 1 FROM operations WHERE status IN ('ACCEPTED','RUNNING','UNCERTAIN'))
 AND NOT EXISTS(SELECT 1 FROM storage_assets WHERE status NOT IN ('READY','RETAINED','DELETED') OR maintenance_intent IS NOT NULL)
 AND NOT EXISTS(SELECT 1 FROM recovery_quota_holds)
 AND NOT EXISTS(SELECT 1 FROM quota_reservations WHERE status IN ('HELD','USED') AND resource_type NOT IN ('INSTANCE','STORAGE'))
 AND NOT EXISTS(SELECT 1 FROM access_routes WHERE enabled)
 AND current_user NOT IN ('hc_api','hc_worker','hc_gateway','hc_model_gateway')
 AND (pg_has_role(current_user,'pg_signal_backend','USAGE') OR (SELECT rolsuper FROM pg_roles WHERE rolname=current_user)),
 (SELECT status='ACTIVE' FROM organizations WHERE id=$1)`, s.source.organization).Scan(&database, &safe, &active)
	if err != nil || !safe || confirmed != database || !databaseName.MatchString(database) {
		return false, ErrRecoveryFence
	}
	return active, nil
}

func sourceIsFenced(ctx context.Context, q inventoryQuerier) error {
	var safe bool
	err := q.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM organizations WHERE status<>'DISABLED')
 AND NOT EXISTS(SELECT 1 FROM secrets WHERE revoked_at IS NULL)
 AND NOT EXISTS(SELECT 1 FROM model_configurations WHERE status<>'REVOKED' OR revoked_at IS NULL)
 AND NOT EXISTS(SELECT 1 FROM native_sessions)
 AND NOT EXISTS(SELECT 1 FROM portal_sessions WHERE revoked_at IS NULL)
 AND NOT EXISTS(SELECT 1 FROM access_sessions WHERE revoked_at IS NULL)
 AND NOT EXISTS(SELECT 1 FROM access_routes WHERE enabled)
 AND NOT has_database_privilege('hc_api',current_database(),'CONNECT')
 AND NOT has_database_privilege('hc_worker',current_database(),'CONNECT')
 AND NOT has_database_privilege('hc_gateway',current_database(),'CONNECT')`).Scan(&safe)
	if err != nil || !safe {
		return ErrRecoveryFence
	}
	return nil
}

func drainSourceConnections(ctx context.Context, pool *pgxpool.Pool) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if sourceIsFenced(ctx, pool) != nil {
		return ErrRecoveryFence
	}
	rows, err := pool.Query(ctx, `SELECT pg_terminate_backend(pid,1000) FROM pg_stat_activity WHERE datname=current_database() AND usename IN ('hc_api','hc_worker','hc_gateway','hc_model_gateway') AND pid<>pg_backend_pid()`)
	if err != nil {
		return ErrRecoveryFence
	}
	for rows.Next() {
		var done bool
		if rows.Scan(&done) != nil || !done {
			rows.Close()
			return ErrRecoveryFence
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return ErrRecoveryFence
	}
	return sourceConnectionsAbsent(ctx, pool)
}

func sourceConnectionsAbsent(ctx context.Context, q inventoryQuerier) error {
	var empty bool
	if q.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND usename IN ('hc_api','hc_worker','hc_gateway','hc_model_gateway'))`).Scan(&empty) != nil || !empty {
		return ErrRecoveryFence
	}
	return nil
}

type sourceFenceAudit struct {
	Schema               int                       `json:"schema_version"`
	Operator             string                    `json:"operator_ref"`
	Reason               string                    `json:"reason_code"`
	TargetDatabase       string                    `json:"target_database"`
	InventoryRunID       string                    `json:"inventory_run_id"`
	SourceExpectedHash   string                    `json:"source_expected_sha256"`
	SourcePostPolicyHash string                    `json:"source_post_policy_sha256"`
	TargetPolicyHash     string                    `json:"target_policy_sha256"`
	ObservedHash         string                    `json:"observed_sha256"`
	Report               RecoverySourceFenceReport `json:"report"`
}

// Fence commits source authorization revocation before killing old pooled
// connections. A crash between those steps is safe and resumes the same fence;
// the independent target remains quarantined throughout this operation.
func (s *RecoverySourceFence) Fence(ctx context.Context, sourceDB, targetDB, inventoryID, reason string, apply bool) (RecoverySourceFenceReport, error) {
	r := RecoverySourceFenceReport{SourceDatabase: sourceDB, TargetDatabase: targetDB, InventoryRunID: inventoryID, TargetQuarantined: true}
	if s == nil || s.source == nil || s.target == nil || s.source.pool == nil || s.target.pool == nil || s.source.observer == nil || s.drain == nil || !uuid.MatchString(s.source.organization) || !operatorName.MatchString(s.source.operator) || !uuid.MatchString(inventoryID) || !recoveryReason.MatchString(reason) || sourceDB == targetDB || (!strings.HasPrefix(targetDB, "hc_recovery_") && !strings.HasPrefix(targetDB, "hc_test_recovery_")) {
		return r, ErrInput
	}
	src, target := s.source, s.target
	if db.Check(ctx, src.pool) != nil || db.Check(ctx, target.pool) != nil || target.guard(ctx, target.pool, targetDB) != nil {
		return r, ErrRecoveryFence
	}
	active, err := s.sourceGuard(ctx, src.pool, sourceDB)
	if err != nil {
		return r, err
	}
	sourceExpected, err := src.expected(ctx, src.pool)
	if err != nil {
		return r, err
	}
	targetExpected, err := target.expected(ctx, target.pool)
	if err != nil || inventoryHash(sourceExpected) != inventoryHash(targetExpected) {
		return r, ErrRecoveryFence
	}
	holds, err := recoveryHolds(ctx, target.pool, target.organization)
	if err != nil || len(holds) != 0 {
		return r, ErrRecoveryFence
	}
	sourceOwners, err := recoveryOwnerIdentities(ctx, src.pool, src.organization)
	if err != nil {
		return r, err
	}
	targetOwners, err := recoveryOwnerIdentities(ctx, target.pool, target.organization)
	if err != nil || sourceOwners != targetOwners {
		return r, ErrRecoveryFence
	}
	sourcePolicy, err := recoveryPolicyHash(ctx, src.pool, src.organization)
	if err != nil {
		return r, err
	}
	targetPolicy, err := recoveryPolicyHash(ctx, target.pool, target.organization)
	if err != nil {
		return r, err
	}
	first, err := src.observer.Observe(ctx)
	if err != nil {
		return r, err
	}
	observed, err := src.observer.Observe(ctx)
	if err != nil || observationHash(first) != observationHash(observed) {
		return r, ErrRecoveryFence
	}
	report, err := compareInventoryReport(targetExpected, observed, holds)
	if err != nil || report.Blockers != 0 || report.Ledger != report.Conservative {
		return r, ErrRecoveryFence
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	targetTx, err := target.pool.Begin(bounded)
	if err != nil {
		return r, ErrRecoveryFence
	}
	defer targetTx.Rollback(context.Background())
	if _, err = targetTx.Exec(bounded, `SET LOCAL lock_timeout='1s';SET LOCAL statement_timeout='4s';SET LOCAL idle_in_transaction_session_timeout='5s';LOCK TABLE `+fenceTables+` IN SHARE MODE`); err != nil {
		return r, ErrRecoveryFence
	}
	if target.guard(bounded, targetTx, targetDB) != nil {
		return r, ErrRecoveryFence
	}
	currentTarget, err := recoveryPolicyHash(bounded, targetTx, target.organization)
	if err != nil || currentTarget != targetPolicy {
		return r, ErrRecoveryFence
	}
	var reviewedRaw []byte
	if targetTx.QueryRow(bounded, `SELECT details->'report' FROM audit_events WHERE id=$1 AND org_id=$2 AND action='RECOVERY_INVENTORY' AND outcome='SUCCEEDED'`, inventoryID, target.organization).Scan(&reviewedRaw) != nil {
		return r, ErrRecoveryFence
	}
	var reviewed InventoryReport
	if json.Unmarshal(reviewedRaw, &reviewed) != nil || reviewed.RunID != inventoryID || !reviewed.Applied || !reviewed.Quarantined || reviewed.PromotionAllowed || reviewed.Blockers != 0 || reviewed.SnapshotSHA256 != report.SnapshotSHA256 {
		return r, ErrRecoveryFence
	}
	tx, err := src.pool.Begin(bounded)
	if err != nil {
		return r, ErrRecoveryFence
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(bounded, `SET LOCAL lock_timeout='1s';SET LOCAL statement_timeout='4s';SET LOCAL idle_in_transaction_session_timeout='5s';SELECT pg_advisory_xact_lock(731804527);LOCK TABLE `+fenceTables+` IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return r, ErrRecoveryFence
	}
	currentActive, err := s.sourceGuard(bounded, tx, sourceDB)
	if err != nil || currentActive != active {
		return r, ErrRecoveryFence
	}
	currentSource, err := recoveryPolicyHash(bounded, tx, src.organization)
	if err != nil || currentSource != sourcePolicy {
		return r, ErrRecoveryFence
	}
	var savedRaw []byte
	err = tx.QueryRow(bounded, `SELECT details FROM audit_events WHERE org_id=$1 AND action='RECOVERY_SOURCE_FENCE' AND outcome='SUCCEEDED' AND details->>'target_database'=$2 AND details->>'inventory_run_id'=$3 ORDER BY timestamp DESC,id DESC LIMIT 1`, src.organization, targetDB, inventoryID).Scan(&savedRaw)
	if err == nil {
		var saved sourceFenceAudit
		if active || json.Unmarshal(savedRaw, &saved) != nil || saved.Operator != src.operator || saved.Reason != reason || saved.SourceExpectedHash != inventoryHash(sourceExpected) || saved.SourcePostPolicyHash != sourcePolicy || saved.TargetPolicyHash != targetPolicy || saved.ObservedHash != observationHash(observed) || !saved.Report.Applied || !saved.Report.TargetQuarantined || sourceIsFenced(bounded, tx) != nil {
			return r, ErrRecoveryFence
		}
		r = saved.Report
	} else {
		if err != pgx.ErrNoRows || !active {
			return r, ErrRecoveryFence
		}
		if !apply {
			return r, nil
		}
		if _, err = tx.Exec(bounded, `UPDATE organizations SET status='DISABLED';
UPDATE memberships SET auth_generation=auth_generation+1;
UPDATE portal_sessions SET revoked_at=coalesce(revoked_at,clock_timestamp());
UPDATE access_sessions SET revoked_at=coalesce(revoked_at,clock_timestamp());
UPDATE access_tickets SET consumed_at=coalesce(consumed_at,clock_timestamp());
UPDATE access_challenges SET consumed_at=coalesce(consumed_at,clock_timestamp());
UPDATE oidc_transactions SET consumed_at=coalesce(consumed_at,clock_timestamp()),expires_at=least(expires_at,clock_timestamp());
UPDATE secrets SET revoked_at=coalesce(revoked_at,clock_timestamp());
UPDATE model_configurations SET status='REVOKED',revoked_at=coalesce(revoked_at,clock_timestamp());
UPDATE model_validations SET status='UNKNOWN',checked_at=NULL,expires_at=NULL,reason_code='VALIDATION_REQUIRED',lease_token=lease_token+1,lease_expires_at=NULL;
UPDATE instance_health SET status='UNKNOWN',checked_at=NULL,expires_at=NULL,reason_code='HEALTH_EXPIRED';
UPDATE access_routes SET enabled=false;
DELETE FROM native_sessions;
UPDATE instances SET access_generation=access_generation+1,version=version+1;
REVOKE CONNECT ON DATABASE `+pgx.Identifier{sourceDB}.Sanitize()+` FROM PUBLIC,hc_api,hc_worker,hc_gateway,hc_model_gateway`); err != nil {
			return r, ErrRecoveryFence
		}
		if sourceIsFenced(bounded, tx) != nil {
			return r, ErrRecoveryFence
		}
		post, err := recoveryPolicyHash(bounded, tx, src.organization)
		if err != nil {
			return r, err
		}
		r.RunID, r.Applied = webapi.ID(), true
		audit := sourceFenceAudit{1, src.operator, reason, targetDB, inventoryID, inventoryHash(sourceExpected), post, targetPolicy, observationHash(observed), r}
		raw, err := json.Marshal(audit)
		if err != nil {
			r.Applied = false
			return r, ErrRecoveryFence
		}
		if _, err = tx.Exec(bounded, `INSERT INTO audit_events(id,org_id,action,outcome,trace_id,details) VALUES($1::uuid,$2,'RECOVERY_SOURCE_FENCE','SUCCEEDED',$1::text,$3)`, r.RunID, src.organization, raw); err != nil {
			r.Applied = false
			return r, ErrRecoveryFence
		}
	}
	if !apply {
		r.Applied = false
		r.Drained = sourceConnectionsAbsent(bounded, tx) == nil
		return r, nil
	}
	if err = tx.Commit(bounded); err != nil {
		r.Applied = false
		return r, ErrRecoveryFence
	}
	// Release both transaction connections before the separately replayable
	// drain. Source org/credentials are already disabled if draining crashes.
	_ = targetTx.Rollback(context.Background())
	if err = s.drain(ctx, src.pool); err != nil {
		return r, ErrRecoveryFence
	}
	drainCtx, drainCancel := context.WithTimeout(ctx, 5*time.Second)
	defer drainCancel()
	ctx = drainCtx
	drainTx, err := src.pool.Begin(ctx)
	if err != nil {
		return r, ErrRecoveryFence
	}
	defer drainTx.Rollback(context.Background())
	if _, err = drainTx.Exec(ctx, `SET LOCAL lock_timeout='1s';SET LOCAL statement_timeout='4s';SELECT pg_advisory_xact_lock(731804527)`); err != nil || sourceIsFenced(ctx, drainTx) != nil || sourceConnectionsAbsent(ctx, drainTx) != nil {
		return r, ErrRecoveryFence
	}
	err = drainTx.QueryRow(ctx, `SELECT id::text FROM audit_events WHERE org_id=$1 AND action='RECOVERY_SOURCE_DRAIN' AND outcome='SUCCEEDED' AND details->>'fence_run_id'=$2 ORDER BY timestamp DESC,id DESC LIMIT 1`, src.organization, r.RunID).Scan(&r.DrainRunID)
	if err == pgx.ErrNoRows {
		r.DrainRunID = webapi.ID()
		if _, err = drainTx.Exec(ctx, `INSERT INTO audit_events(id,org_id,action,outcome,trace_id,details) VALUES($1::uuid,$2,'RECOVERY_SOURCE_DRAIN','SUCCEEDED',$1::text,jsonb_build_object('schema_version',1,'operator_ref',$3::text,'fence_run_id',$4::text,'target_database',$5::text))`, r.DrainRunID, src.organization, src.operator, r.RunID, targetDB); err != nil {
			return r, ErrRecoveryFence
		}
	} else if err != nil {
		return r, ErrRecoveryFence
	}
	if err = drainTx.Commit(ctx); err != nil {
		return r, ErrRecoveryFence
	}
	r.Drained = true
	return r, nil
}
