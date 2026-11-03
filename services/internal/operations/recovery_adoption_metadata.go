package operations

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/instances"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/runtimeapproval"
	workspaceengine "github.com/jonluo94/deepseek-harness-cloud/services/internal/workspace"
)

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
