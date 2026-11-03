package operations

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/instances"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/runtimeapproval"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

var ErrRecoveryAdoption = errors.New("resource adoption requires a verified isolated checkpoint, exact ownership, stopped original resources and retained quota; authorization remains quarantined")

type RecoveryAdoptionReport struct {
	RunID            string         `json:"run_id,omitempty"`
	InventoryRunID   string         `json:"inventory_run_id"`
	InstanceID       string         `json:"instance_id"`
	AssetID          string         `json:"asset_id"`
	CheckpointSHA256 string         `json:"checkpoint_sha256"`
	Transferred      InventoryQuota `json:"transferred"`
	Applied          bool           `json:"applied"`
	Quarantined      bool           `json:"quarantined"`
	PromotionAllowed bool           `json:"promotion_allowed"`
}

type RecoveryRuntimeVerifier func(context.Context, instances.Snapshot, runtimeapproval.Manifest) error

type RecoveryAdoption struct {
	target, source *RecoveryInventory
	verify         RecoveryRuntimeVerifier
}

func NewRecoveryAdoption(target, source *pgxpool.Pool, observer InventoryObserver, verify RecoveryRuntimeVerifier, org, operator string) *RecoveryAdoption {
	return &RecoveryAdoption{NewRecoveryInventory(target, observer, org, operator), NewRecoveryInventory(source, observer, org, operator), verify}
}

const adoptionTables = `organizations,users,memberships,instances,storage_assets,quota_policies,quota_reservations,recovery_quota_holds,operations,access_routes,native_sessions,portal_sessions,access_sessions,secrets,model_configuration_heads,model_configurations,approved_templates,approved_specs`

// Adopt imports original metadata from a separately authenticated and isolated
// checkpoint. Retained commitments become original USED reservations in the
// same transaction, so neither a credit gap nor double charging is visible.
func (s *RecoveryAdoption) Adopt(ctx context.Context, targetDB, sourceDB, id, inventoryID, reason string, apply bool) (RecoveryAdoptionReport, error) {
	r := RecoveryAdoptionReport{InstanceID: id, InventoryRunID: inventoryID, Quarantined: true}
	if s == nil || s.target == nil || s.source == nil || s.target.pool == nil || s.source.pool == nil || s.verify == nil || s.target.observer == nil || !uuid.MatchString(s.target.organization) || !operatorName.MatchString(s.target.operator) || !uuid.MatchString(id) || !uuid.MatchString(inventoryID) || !recoveryReason.MatchString(reason) || targetDB == sourceDB {
		return r, ErrInput
	}
	t, source := s.target, s.source
	if db.Check(ctx, t.pool) != nil || db.Check(ctx, source.pool) != nil || t.guard(ctx, t.pool, targetDB) != nil || source.guard(ctx, source.pool, sourceDB) != nil {
		return r, ErrRecoveryAdoption
	}
	metadata, err := readAdoptionMetadata(ctx, source.pool, t.organization, id)
	if err != nil {
		return r, err
	}
	e, snapshot, manifest, err := validateAdoptionMetadata(metadata, t.organization, id)
	if err != nil {
		return r, err
	}
	r.AssetID, r.CheckpointSHA256, r.Transferred = e.AssetID, metadata.Checkpoint, e.Reserved
	var owner []byte
	readOwner := func(q inventoryQuerier) error {
		return q.QueryRow(ctx, `SELECT jsonb_build_object('id',u.id,'identity_issuer',u.identity_issuer,'identity_subject',u.identity_subject,'workspace_owner_id',u.workspace_owner_id) FROM users u JOIN memberships m ON m.user_id=u.id WHERE m.org_id=$1 AND u.id=$2`, t.organization, e.OwnerID).Scan(&owner)
	}
	if readOwner(t.pool) != nil || inventoryHash(json.RawMessage(owner)) != inventoryHash(metadata.Owner) {
		return r, ErrRecoveryAdoption
	}
	expected, err := t.expected(ctx, t.pool)
	if err != nil {
		return r, err
	}
	holds, err := recoveryHolds(ctx, t.pool, t.organization)
	if err != nil {
		return r, err
	}
	owners, err := recoveryOwners(ctx, t.pool, t.organization)
	if err != nil {
		return r, err
	}
	if err = s.verify(ctx, snapshot, manifest); err != nil {
		return r, ErrRecoveryAdoption
	}
	first, err := t.observer.Observe(ctx)
	if err != nil {
		return r, err
	}
	observed, err := t.observer.Observe(ctx)
	if err != nil || observationHash(first) != observationHash(observed) {
		return r, ErrRecoveryAdoption
	}
	changes, report, err := PlanRecoveryRetention(t.organization, expected, observed, holds, owners)
	if err != nil || len(changes) != 0 {
		return r, ErrRecoveryAdoption
	}
	selected := []ResourceObservation{}
	for _, v := range observed {
		if v.InstanceID == id {
			selected = append(selected, v)
		}
	}
	physical, err := CompareInventory([]InventoryInstance{e}, selected)
	if err != nil || physical.Blockers != 0 || len(selected) != 3 || physical.Conservative != e.Reserved {
		return r, ErrRecoveryAdoption
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	srcTx, err := source.pool.Begin(bounded)
	if err != nil {
		return r, ErrRecoveryAdoption
	}
	defer srcTx.Rollback(context.Background())
	if _, err = srcTx.Exec(bounded, `SET LOCAL lock_timeout='1s';SET LOCAL statement_timeout='4s';SET LOCAL idle_in_transaction_session_timeout='5s';LOCK TABLE `+adoptionTables+` IN SHARE MODE`); err != nil {
		return r, ErrRecoveryAdoption
	}
	if source.guard(bounded, srcTx, sourceDB) != nil {
		return r, ErrRecoveryAdoption
	}
	currentSource, err := readAdoptionMetadata(bounded, srcTx, t.organization, id)
	if err != nil || inventoryHash(currentSource) != inventoryHash(metadata) {
		return r, ErrRecoveryAdoption
	}
	tx, err := t.pool.Begin(bounded)
	if err != nil {
		return r, ErrRecoveryAdoption
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(bounded, `SET LOCAL lock_timeout='1s';SET LOCAL statement_timeout='4s';SET LOCAL idle_in_transaction_session_timeout='5s';SELECT pg_advisory_xact_lock(731804527);LOCK TABLE `+adoptionTables+` IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return r, ErrRecoveryAdoption
	}
	if t.guard(bounded, tx, targetDB) != nil || readOwner(tx) != nil || inventoryHash(json.RawMessage(owner)) != inventoryHash(metadata.Owner) {
		return r, ErrRecoveryAdoption
	}
	current, err := t.expected(bounded, tx)
	if err != nil || inventoryHash(current) != inventoryHash(expected) {
		return r, ErrRecoveryAdoption
	}
	currentHolds, err := recoveryHolds(bounded, tx, t.organization)
	if err != nil || inventoryHash(currentHolds) != inventoryHash(holds) {
		return r, ErrRecoveryAdoption
	}
	var savedRaw []byte
	err = tx.QueryRow(bounded, `SELECT details FROM audit_events WHERE org_id=$1 AND action='RECOVERY_RESOURCE_ADOPT' AND outcome='SUCCEEDED' AND resource_id=$2 AND details->>'inventory_run_id'=$3 ORDER BY timestamp DESC,id DESC LIMIT 1`, t.organization, id, inventoryID).Scan(&savedRaw)
	if err == nil {
		var saved adoptionAudit
		post, postErr := readAdoptionMetadata(bounded, tx, t.organization, id)
		if json.Unmarshal(savedRaw, &saved) != nil || saved.Operator != t.operator || saved.Reason != reason || saved.SourceDB != sourceDB || saved.SourceHash != inventoryHash(metadata) || saved.ObservedHash != observationHash(observed) || saved.PostExpectedHash != inventoryHash(current) || saved.PostHoldsHash != inventoryHash(currentHolds) || postErr != nil || saved.PostMetadataHash != inventoryHash(post) || !saved.Report.Applied || !saved.Report.Quarantined || saved.Report.PromotionAllowed {
			return r, ErrRecoveryAdoption
		}
		if !apply {
			saved.Report.Applied = false
		}
		return saved.Report, nil
	}
	if err != pgx.ErrNoRows {
		return r, ErrRecoveryAdoption
	}
	for _, known := range current {
		if known.ID == id {
			return r, ErrRecoveryAdoption
		}
	}
	var retained InventoryQuota
	for _, h := range holds {
		if h.InstanceID == id {
			if h.OwnerID != e.OwnerID || !retained.add(quotaForResource(h.Resource, h.Amount)) {
				return r, ErrRecoveryAdoption
			}
		}
	}
	if retained != e.Reserved {
		return r, ErrRecoveryAdoption
	}
	var reviewedRaw []byte
	if tx.QueryRow(bounded, `SELECT details->'report' FROM audit_events WHERE id=$1 AND org_id=$2 AND action='RECOVERY_INVENTORY' AND outcome='SUCCEEDED'`, inventoryID, t.organization).Scan(&reviewedRaw) != nil {
		return r, ErrRecoveryAdoption
	}
	var reviewed InventoryReport
	if json.Unmarshal(reviewedRaw, &reviewed) != nil || reviewed.RunID != inventoryID || !reviewed.Applied || !reviewed.Quarantined || reviewed.PromotionAllowed || reviewed.SnapshotSHA256 != report.SnapshotSHA256 {
		return r, ErrRecoveryAdoption
	}
	if !apply {
		return r, nil
	}
	if err = importAdoptionMetadata(bounded, tx, metadata, t.organization); err != nil {
		return r, ErrRecoveryAdoption
	}
	deleted, err := tx.Exec(bounded, `DELETE FROM recovery_quota_holds WHERE org_id=$1 AND owner_id=$2 AND instance_id=$3`, t.organization, e.OwnerID, id)
	if err != nil || deleted.RowsAffected() != 2 {
		return r, ErrRecoveryAdoption
	}
	postExpected, err := t.expected(bounded, tx)
	if err != nil {
		return r, ErrRecoveryAdoption
	}
	postHolds, err := recoveryHolds(bounded, tx, t.organization)
	if err != nil {
		return r, ErrRecoveryAdoption
	}
	postReport, err := compareInventoryReport(postExpected, observed, postHolds)
	if err != nil || postReport.Ledger != report.Ledger || postReport.Conservative != report.Conservative {
		return r, ErrRecoveryAdoption
	}
	post, err := readAdoptionMetadata(bounded, tx, t.organization, id)
	if err != nil {
		return r, ErrRecoveryAdoption
	}
	r.RunID, r.Applied = webapi.ID(), true
	audit := adoptionAudit{1, t.operator, reason, inventoryID, sourceDB, inventoryHash(metadata), observationHash(observed), inventoryHash(postExpected), inventoryHash(postHolds), inventoryHash(post), r}
	raw, err := json.Marshal(audit)
	if err != nil {
		r.Applied = false
		return r, ErrRecoveryAdoption
	}
	if _, err = tx.Exec(bounded, `INSERT INTO audit_events(id,org_id,owner_id,resource_id,action,outcome,trace_id,details) VALUES($1::uuid,$2,$3,$4,'RECOVERY_RESOURCE_ADOPT','SUCCEEDED',$1::text,$5)`, r.RunID, t.organization, e.OwnerID, id, raw); err != nil {
		r.Applied = false
		return r, ErrRecoveryAdoption
	}
	if err = tx.Commit(bounded); err != nil {
		r.Applied = false
		return r, ErrRecoveryAdoption
	}
	return r, nil
}

type adoptionAudit struct {
	Schema           int                    `json:"schema_version"`
	Operator         string                 `json:"operator_ref"`
	Reason           string                 `json:"reason_code"`
	InventoryRunID   string                 `json:"inventory_run_id"`
	SourceDB         string                 `json:"source_database"`
	SourceHash       string                 `json:"source_metadata_sha256"`
	ObservedHash     string                 `json:"observed_sha256"`
	PostExpectedHash string                 `json:"post_expected_sha256"`
	PostHoldsHash    string                 `json:"post_holds_sha256"`
	PostMetadataHash string                 `json:"post_metadata_sha256"`
	Report           RecoveryAdoptionReport `json:"report"`
}
