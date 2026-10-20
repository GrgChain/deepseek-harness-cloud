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
	workspaceengine "github.com/jonluo94/deepseek-harness-cloud/services/internal/workspace"
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

// Metadata never appears in a command report: it contains encrypted secret rows
// as well as immutable original instance, operation and reservation identities.
type adoptionMetadata struct {
	Instance     json.RawMessage   `json:"instance"`
	Asset        json.RawMessage   `json:"asset"`
	Model        json.RawMessage   `json:"model"`
	Secret       json.RawMessage   `json:"secret"`
	Template     json.RawMessage   `json:"template"`
	Spec         json.RawMessage   `json:"spec"`
	Owner        json.RawMessage   `json:"owner"`
	Operations   []json.RawMessage `json:"operations"`
	Reservations []json.RawMessage `json:"reservations"`
	Checkpoint   string            `json:"checkpoint"`
}

func readAdoptionMetadata(ctx context.Context, q inventoryQuerier, org, id string) (adoptionMetadata, error) {
	var m adoptionMetadata
	var raw []byte
	err := q.QueryRow(ctx, `SELECT jsonb_build_object('instance',to_jsonb(i),'asset',to_jsonb(a),'model',to_jsonb(c),'secret',to_jsonb(s),'template',to_jsonb(t),'spec',to_jsonb(spec),
 'owner',jsonb_build_object('id',u.id,'identity_issuer',u.identity_issuer,'identity_subject',u.identity_subject,'workspace_owner_id',u.workspace_owner_id),
 'operations',(SELECT coalesce(jsonb_agg(to_jsonb(o) ORDER BY o.created_at,o.id),'[]') FROM (SELECT * FROM operations WHERE instance_id=i.id ORDER BY created_at,id LIMIT 1001) o),
 'reservations',(SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY r.id),'[]') FROM (SELECT * FROM quota_reservations WHERE instance_id=i.id ORDER BY id LIMIT 6001) r),
 'checkpoint',(SELECT details->>'backup_sha256' FROM audit_events WHERE org_id=$1 AND action='RESTORE_ISOLATED' AND outcome='SUCCEEDED' ORDER BY timestamp DESC,id DESC LIMIT 1))
 FROM instances i JOIN storage_assets a ON a.id=i.storage_asset_id AND a.instance_id=i.id AND a.org_id=i.org_id AND a.owner_id=i.owner_id
 JOIN users u ON u.id=i.owner_id JOIN memberships mem ON mem.org_id=i.org_id AND mem.user_id=i.owner_id
 JOIN model_configurations c ON c.org_id=i.org_id AND c.id=i.model_config_id AND c.version=i.model_config_version JOIN secrets s ON s.org_id=c.org_id AND s.id=c.credential_ref
 JOIN approved_templates t ON t.id=(i.config_snapshot->>'template_id')::uuid AND t.version=(i.config_snapshot->>'template_version')::uuid
 JOIN approved_specs spec ON spec.id=(i.config_snapshot->'spec'->>'id')::uuid
 WHERE i.id=$2 AND i.org_id=$1 AND a.maintenance_intent IS NULL AND NOT EXISTS(SELECT 1 FROM recovery_quota_holds WHERE org_id=$1 AND instance_id=i.id)`, org, id).Scan(&raw)
	if err != nil || len(raw) > 2<<20 || json.Unmarshal(raw, &m) != nil {
		return m, ErrRecoveryAdoption
	}
	return m, nil
}

func validateAdoptionMetadata(m adoptionMetadata, org, id string) (InventoryInstance, instances.Snapshot, runtimeapproval.Manifest, error) {
	var e InventoryInstance
	var snapshot instances.Snapshot
	var manifest runtimeapproval.Manifest
	fail := func() (InventoryInstance, instances.Snapshot, runtimeapproval.Manifest, error) {
		return e, snapshot, manifest, ErrRecoveryAdoption
	}
	var i struct {
		ID               string          `json:"id"`
		Org              string          `json:"org_id"`
		Owner            string          `json:"owner_id"`
		CreateKey        string          `json:"external_create_key"`
		Name             string          `json:"workspace_name"`
		Workspace        string          `json:"workspace_id"`
		Asset            string          `json:"storage_asset_id"`
		Desired          string          `json:"desired_state"`
		Observed         string          `json:"observed_state"`
		Model            string          `json:"model_config_id"`
		ModelVersion     int64           `json:"model_config_version"`
		AccessGeneration int64           `json:"access_generation"`
		Snapshot         json.RawMessage `json:"config_snapshot"`
	}
	var a struct {
		ID         string `json:"id"`
		Org        string `json:"org_id"`
		Owner      string `json:"owner_id"`
		Instance   string `json:"instance_id"`
		Filesystem string `json:"filesystem_id"`
		Resource   string `json:"resource_name"`
		Capacity   int64  `json:"capacity_bytes"`
		Init       int    `json:"init_version"`
		Status     string `json:"status"`
	}
	var owner struct {
		ID               string `json:"id"`
		WorkspaceOwnerID string `json:"workspace_owner_id"`
	}
	if json.Unmarshal(m.Instance, &i) != nil || json.Unmarshal(m.Asset, &a) != nil || json.Unmarshal(m.Owner, &owner) != nil {
		return fail()
	}
	name, err := workspaceengine.WorkspaceName(id)
	if err != nil || i.ID != id || i.Org != org || !uuid.MatchString(i.Owner) || i.Owner != owner.ID || !uuid.MatchString(owner.WorkspaceOwnerID) || i.CreateKey != id || i.Name != name || !uuid.MatchString(i.Workspace) || i.Desired != "STOPPED" || i.Observed != "STOPPED" || i.AccessGeneration < 1 || i.AccessGeneration >= 1<<62 || a.ID != i.Asset || !uuid.MatchString(a.ID) || a.Org != org || a.Owner != i.Owner || a.Instance != id || a.Filesystem != a.ID || a.Resource != "hc-"+a.ID || !runtimeapproval.SupportedStorage(a.Capacity) || a.Init != 1 || a.Status != "READY" {
		return fail()
	}
	snapshot, err = instances.DecodeSnapshot(i.Snapshot)
	if err != nil || snapshot.ModelID != i.Model || snapshot.ModelVersion != i.ModelVersion || !runtimeapproval.SupportedResources(snapshot.Spec.CPU, snapshot.Spec.Memory, snapshot.Spec.Storage) || snapshot.Spec.Storage != a.Capacity {
		return fail()
	}
	var template struct {
		ID       string          `json:"id"`
		Version  string          `json:"version"`
		Image    string          `json:"image_digest"`
		Manifest json.RawMessage `json:"runtime_manifest"`
	}
	var spec instances.ResourceSpec
	if json.Unmarshal(m.Template, &template) != nil || json.Unmarshal(m.Spec, &spec) != nil || template.ID != snapshot.TemplateID || template.Version != snapshot.TemplateVersion || template.Image != snapshot.ImageDigest || spec != snapshot.Spec {
		return fail()
	}
	manifest, err = runtimeapproval.Parse(template.Manifest)
	if err != nil || !manifest.Matches(snapshot.ImageDigest, snapshot.TemplateVersion) || len(m.Checkpoint) != 64 {
		return fail()
	}
	for _, char := range m.Checkpoint {
		if !(char >= 'a' && char <= 'f') && !(char >= '0' && char <= '9') {
			return fail()
		}
	}
	var model struct {
		ID         string     `json:"id"`
		Org        string     `json:"org_id"`
		Version    int64      `json:"version"`
		Credential string     `json:"credential_ref"`
		Provider   string     `json:"provider"`
		Protocol   string     `json:"protocol"`
		Model      string     `json:"model"`
		Status     string     `json:"status"`
		Revoked    *time.Time `json:"revoked_at"`
	}
	var secret struct {
		ID      string     `json:"id"`
		Org     string     `json:"org_id"`
		Revoked *time.Time `json:"revoked_at"`
	}
	if json.Unmarshal(m.Model, &model) != nil || json.Unmarshal(m.Secret, &secret) != nil || model.ID != snapshot.ModelID || model.Version != snapshot.ModelVersion || model.Org != org || model.Provider != snapshot.Provider || model.Protocol != snapshot.Protocol || model.Model != snapshot.Model || model.Status != "REVOKED" || model.Revoked == nil || secret.ID != model.Credential || secret.Org != org || secret.Revoked == nil {
		return fail()
	}
	if len(m.Operations) == 0 || len(m.Operations) > 1000 || len(m.Reservations) > 6000 {
		return fail()
	}
	ops := map[string]bool{}
	created := false
	for _, raw := range m.Operations {
		var op struct {
			ID       string `json:"id"`
			Instance string `json:"instance_id"`
			Actor    string `json:"actor_id"`
			Kind     string `json:"actor_kind"`
			Type     string `json:"type"`
			Status   string `json:"status"`
		}
		if json.Unmarshal(raw, &op) != nil || !uuid.MatchString(op.ID) || ops[op.ID] || op.Instance != id || (op.Kind == "USER" && op.Actor != i.Owner) || (op.Status != "SUCCEEDED" && op.Status != "FAILED") {
			return fail()
		}
		ops[op.ID] = true
		created = created || (op.Type == "CREATE" && op.Status == "SUCCEEDED")
	}
	var reserved InventoryQuota
	for _, raw := range m.Reservations {
		var r struct {
			Org       string `json:"org_id"`
			Owner     string `json:"owner_id"`
			Instance  string `json:"instance_id"`
			Operation string `json:"operation_id"`
			Resource  string `json:"resource_type"`
			Amount    int64  `json:"amount"`
			Status    string `json:"status"`
		}
		if json.Unmarshal(raw, &r) != nil || r.Org != org || r.Owner != i.Owner || r.Instance != id || !ops[r.Operation] || r.Amount < 1 || r.Amount > 1<<50 || (r.Status != "USED" && r.Status != "RELEASED") || quotaForResource(r.Resource, r.Amount) == (InventoryQuota{}) {
			return fail()
		}
		if r.Status == "USED" && !reserved.add(quotaForResource(r.Resource, r.Amount)) {
			return fail()
		}
	}
	if !created || reserved != (InventoryQuota{Instances: 1, Storage: a.Capacity}) {
		return fail()
	}
	e = InventoryInstance{ID: id, AssetID: a.ID, OrganizationID: org, OwnerID: i.Owner, WorkspaceOwner: owner.WorkspaceOwnerID, WorkspaceID: i.Workspace, TemplateVersion: snapshot.TemplateVersion, State: "STOPPED", StorageStatus: "READY", Storage: a.Capacity, Reserved: reserved}
	return e, snapshot, manifest, nil
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

func importAdoptionMetadata(ctx context.Context, tx pgx.Tx, m adoptionMetadata, org string) error {
	// Existing approval flags, revocations and defaults are preserved. Missing
	// template/spec metadata is retained disabled, never made a new default.
	for _, item := range []struct {
		table        string
		raw          json.RawMessage
		key, ignored string
	}{
		{"approved_templates", m.Template, "id,version", "enabled"}, {"approved_specs", m.Spec, "id", "enabled"}, {"secrets", m.Secret, "id", "revoked_at"}, {"model_configurations", m.Model, "id,version", "created_at,revoked_at"},
	} {
		var compatible bool
		keys := []string{item.ignored}
		if item.table == "secrets" {
			// The same credential may have been reencrypted after the older
			// checkpoint. Preserve the target's revoked material; adoption
			// neither decrypts it nor makes it usable again.
			keys = []string{"created_at", "revoked_at", "key_id", "nonce", "ciphertext"}
		}
		if item.table == "model_configurations" {
			keys = []string{"created_at", "revoked_at"}
		}
		condition := "a.id=b.id"
		if item.table == "approved_templates" || item.table == "model_configurations" {
			condition += " AND a.version=b.version"
		}
		query := `SELECT NOT EXISTS(SELECT 1 FROM ` + item.table + ` a CROSS JOIN jsonb_populate_record(NULL::` + item.table + `,$1::jsonb) b WHERE ` + condition + ` AND (to_jsonb(a)-$2::text[])<>(to_jsonb(b)-$2::text[]))`
		if tx.QueryRow(ctx, query, item.raw, keys).Scan(&compatible) != nil || !compatible {
			return ErrRecoveryAdoption
		}
		raw := item.raw
		if item.table == "approved_templates" || item.table == "approved_specs" {
			var row map[string]json.RawMessage
			_ = json.Unmarshal(raw, &row)
			row["enabled"] = json.RawMessage("false")
			raw, _ = json.Marshal(row)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO `+item.table+` SELECT * FROM jsonb_populate_record(NULL::`+item.table+`,$1::jsonb) ON CONFLICT(`+item.key+`) DO NOTHING`, raw); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE model_configuration_heads SET current_version=greatest(current_version,($1::jsonb->>'version')::bigint) WHERE org_id=$2 AND id=($1::jsonb->>'id')::uuid`, m.Model, org); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO instances SELECT * FROM jsonb_populate_record(NULL::instances,$1::jsonb || jsonb_build_object('storage_asset_id',NULL,'access_generation',($1::jsonb->>'access_generation')::bigint+1))`, m.Instance); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO storage_assets SELECT * FROM jsonb_populate_record(NULL::storage_assets,$1::jsonb)`, m.Asset); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE instances SET storage_asset_id=($1::jsonb->>'storage_asset_id')::uuid WHERE id=($1::jsonb->>'id')::uuid`, m.Instance); err != nil {
		return err
	}
	for _, op := range m.Operations {
		if _, err := tx.Exec(ctx, `INSERT INTO operations SELECT * FROM jsonb_populate_record(NULL::operations,$1::jsonb || jsonb_build_object('retry_of',NULL,'lease_owner',NULL,'lease_expires_at',NULL,'lease_token',($1::jsonb->>'lease_token')::bigint+1))`, op); err != nil {
			return err
		}
	}
	for _, op := range m.Operations {
		if _, err := tx.Exec(ctx, `UPDATE operations SET retry_of=($1::jsonb->>'retry_of')::uuid WHERE id=($1::jsonb->>'id')::uuid`, op); err != nil {
			return err
		}
	}
	for _, quota := range m.Reservations {
		if _, err := tx.Exec(ctx, `INSERT INTO quota_reservations SELECT * FROM jsonb_populate_record(NULL::quota_reservations,$1::jsonb)`, quota); err != nil {
			return err
		}
	}
	return nil
}
