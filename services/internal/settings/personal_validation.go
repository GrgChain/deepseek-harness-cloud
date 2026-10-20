package settings

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type personalManifest struct {
	Catalog         ModelCatalogEntry `json:"catalog"`
	Prefix          string            `json:"prefix"`
	Parameters      json.RawMessage   `json:"parameters"`
	GatewayRevision string            `json:"gateway_revision"`
}

func readModelVersion(ctx context.Context, q dbQuery, org, id string, version int64) (Model, error) {
	if !uuid.MatchString(id) || version < 1 {
		return Model{}, notFound()
	}
	var m Model
	var manifest []byte
	err := q.QueryRow(ctx, `SELECT c.id::text,c.version,c.provider,c.endpoint,c.protocol,c.model,c.status,s.revoked_at IS NULL,c.manifest,
 CASE WHEN v.status='CHECKING' AND v.lease_expires_at>clock_timestamp() THEN 'CHECKING' WHEN v.expires_at>clock_timestamp() AND v.checked_at<=clock_timestamp() THEN v.status ELSE 'UNKNOWN' END,
 v.checked_at,v.expires_at,COALESCE(v.reason_code,'VALIDATION_REQUIRED')
 FROM model_configurations c JOIN secrets s ON s.org_id=c.org_id AND s.id=c.credential_ref JOIN model_validations v ON v.config_id=c.id AND v.config_version=c.version WHERE c.org_id=$1 AND c.id=$2 AND c.version=$3`, org, id, version).Scan(&m.ID, &m.Version, &m.Provider, &m.Endpoint, &m.Protocol, &m.Model, &m.Status, &m.CredentialConfigured, &manifest, &m.Validation.Status, &m.Validation.CheckedAt, &m.Validation.ExpiresAt, &m.Validation.Reason)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, notFound()
	}
	if err == nil && m.Provider == "platform-models" {
		m.Manifest = &ModelManifest{}
		err = webapi.StrictUnmarshal(manifest, m.Manifest)
	}
	return m, err
}

func (s *Service) ValidatePersonalModel(ctx context.Context, p identity.Principal, id string, version int64, trace string) (PersonalModel, error) {
	if !uuid.MatchString(id) || version < 1 {
		return PersonalModel{}, notFound()
	}
	tx, err := s.begin(ctx, p, "personal-model:validate", true)
	if err != nil {
		return PersonalModel{}, err
	}
	defer tx.Rollback(context.Background())
	var ignored *string
	if err = tx.QueryRow(ctx, `SELECT public.hc_worker_lock_model($1)::text`, p.OrganizationID).Scan(&ignored); err != nil {
		return PersonalModel{}, err
	}
	var current int64
	err = tx.QueryRow(ctx, `SELECT current_version FROM user_model_connections WHERE id=$1 AND org_id=$2 AND owner_id=$3 AND enabled AND archived_at IS NULL FOR UPDATE`, id, p.OrganizationID, p.UserID).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return PersonalModel{}, notFound()
	}
	if err != nil {
		return PersonalModel{}, err
	}
	if current != version {
		return PersonalModel{}, versionConflict()
	}
	lease := probeLease{}
	lease.model, err = readModelVersion(ctx, tx, p.OrganizationID, id, version)
	if err != nil {
		return PersonalModel{}, err
	}
	if !s.personalApproved(ctx, tx, p.OrganizationID, lease.model) {
		return PersonalModel{}, invalid()
	}
	err = tx.QueryRow(ctx, `SELECT c.credential_ref::text,s.key_id,s.nonce,s.ciphertext FROM model_configurations c JOIN secrets s ON s.org_id=c.org_id AND s.id=c.credential_ref WHERE c.id=$1 AND c.version=$2 AND c.revoked_at IS NULL AND s.revoked_at IS NULL`, id, version).Scan(&lease.credential, &lease.envelope.KeyID, &lease.envelope.Nonce, &lease.envelope.Ciphertext)
	if err != nil {
		return PersonalModel{}, err
	}
	err = tx.QueryRow(ctx, `UPDATE model_validations SET status='CHECKING',lease_token=lease_token+1,attempted_at=clock_timestamp(),lease_expires_at=clock_timestamp()+interval '45 seconds' WHERE config_id=$1 AND config_version=$2 AND (lease_expires_at IS NULL OR lease_expires_at<=clock_timestamp()) RETURNING lease_token,attempted_at`, id, version).Scan(&lease.token, &lease.started)
	if errors.Is(err, pgx.ErrNoRows) {
		return PersonalModel{}, webapi.Fail(409, "STATE_CONFLICT", "该连接正在校验")
	}
	if err != nil {
		return PersonalModel{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return PersonalModel{}, err
	}
	probe, cancel := context.WithTimeout(ctx, 30*time.Second)
	reason, budgetErr, err := s.runProbe(probe, p.OrganizationID, &lease)
	cancel()
	if err != nil {
		return PersonalModel{}, err
	}
	tx, err = s.begin(ctx, p, "personal-model:validate", true)
	if err != nil {
		return PersonalModel{}, err
	}
	defer tx.Rollback(context.Background())
	if err = tx.QueryRow(ctx, `SELECT public.hc_worker_lock_model($1)::text`, p.OrganizationID).Scan(&ignored); err != nil {
		return PersonalModel{}, err
	}
	if err = tx.QueryRow(ctx, `SELECT current_version FROM user_model_connections WHERE id=$1 AND org_id=$2 AND owner_id=$3 FOR UPDATE`, id, p.OrganizationID, p.UserID).Scan(&current); err != nil {
		return PersonalModel{}, err
	}
	if current != version {
		return PersonalModel{}, versionConflict()
	}
	if !s.personalApproved(ctx, tx, p.OrganizationID, lease.model) {
		return PersonalModel{}, invalid()
	}
	if err = s.publishProbe(ctx, tx, p.OrganizationID, id, lease, reason); err != nil {
		return PersonalModel{}, err
	}
	if err = s.audit(ctx, tx, p, id, "PERSONAL_MODEL_VALIDATED", trace); err != nil {
		return PersonalModel{}, err
	}
	result, err := personalGet(ctx, tx, p, id)
	if err != nil {
		return result, err
	}
	if err = tx.Commit(ctx); err != nil {
		return result, err
	}
	return result, budgetErr
}

func (s *Service) personalApproved(ctx context.Context, q dbQuery, org string, m Model) bool {
	if m.Provider != "platform-models" || m.Protocol != "anthropic-messages" || m.Manifest == nil {
		return false
	}
	var raw []byte
	var alias string
	err := q.QueryRow(ctx, `SELECT v.manifest,v.route_alias FROM user_model_connection_versions v WHERE v.org_id=$1 AND v.connection_id=$2 AND v.version=$3 AND public.hc_model_available(v.org_id,v.owner_id,v.connection_id,v.version)`, org, m.ID, m.Version).Scan(&raw, &alias)
	var frozen personalManifest
	if err != nil || webapi.StrictUnmarshal(raw, &frozen) != nil || alias != m.Model {
		return false
	}
	gateway, ok := s.policy.Gateway(m.Endpoint)
	if !ok || gateway.Revision != frozen.GatewayRevision {
		return false
	}
	approved := gateway.Manifest(GatewayModel{ID: alias, ResponseModels: frozen.Catalog.ResponseModels, ContextWindow: frozen.Catalog.ContextWindow, MaxOutputTokens: frozen.Catalog.MaxOutputTokens, Streaming: frozen.Catalog.Streaming, ToolUse: frozen.Catalog.ToolUse})
	return approved.Digest == m.Manifest.Digest
}
