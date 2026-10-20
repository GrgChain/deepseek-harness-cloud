package settings

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type ModelDirectory struct {
	Version int64           `json:"version"`
	Items   []ModelManifest `json:"items"`
	Health  Health          `json:"health"`
}

func readDirectory(ctx context.Context, tx pgx.Tx, m Model) (ModelDirectory, error) {
	result := ModelDirectory{Version: m.Version, Items: []ModelManifest{}, Health: Health{Status: "UNKNOWN", Reason: "DIRECTORY_REFRESH_REQUIRED"}}
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT checked_at,expires_at,status,reason_code,items FROM model_directories WHERE config_id=$1 AND config_version=$2`, m.ID, m.Version).Scan(&result.Health.CheckedAt, &result.Health.ExpiresAt, &result.Health.Status, &result.Health.Reason, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if webapi.StrictUnmarshal(raw, &result.Items) != nil {
		return result, errors.New("invalid persisted model directory")
	}
	if result.Health.ExpiresAt.Before(time.Now()) {
		result.Health.Status = "UNKNOWN"
		result.Health.Reason = "DIRECTORY_REFRESH_REQUIRED"
	}
	if !m.CredentialConfigured || m.Status == "REVOKED" {
		result.Items = []ModelManifest{}
		result.Health = Health{Status: "UNHEALTHY", Reason: "CREDENTIAL_REVOKED"}
	}
	return result, nil
}
func (s *Service) GetDirectory(ctx context.Context, p identity.Principal, version int64) (ModelDirectory, error) {
	if version < 1 {
		return ModelDirectory{}, invalid()
	}
	tx, err := s.begin(ctx, p, "configuration:read", false)
	if err != nil {
		return ModelDirectory{}, err
	}
	defer tx.Rollback(context.Background())
	m, err := readModel(ctx, tx, p.OrganizationID, version)
	if err != nil {
		return ModelDirectory{}, err
	}
	if m.Provider != "platform-models" {
		return ModelDirectory{}, invalid()
	}
	return readDirectory(ctx, tx, m)
}
func gatewayDirectory(ctx context.Context, client *http.Client, gateway Gateway, key string, policy *Policy) ([]ModelManifest, string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, gateway.Endpoint+"/v1/models", nil)
	if err != nil {
		return nil, "MODEL_PROTOCOL_INVALID"
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, "MODEL_TRANSPORT_UNAVAILABLE"
	}
	defer resp.Body.Close()
	if reason := upstreamReason(resp.StatusCode); reason != "" {
		return nil, reason
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (256<<10)+1))
	if err != nil || len(raw) > 256<<10 {
		return nil, "MODEL_DIRECTORY_LIMIT"
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &body) != nil || body.Data == nil {
		return nil, "MODEL_PROTOCOL_INVALID"
	}
	if len(body.Data) > 100 {
		return nil, "MODEL_DIRECTORY_LIMIT"
	}
	items := []ModelManifest{}
	seen := map[string]bool{}
	for _, entry := range body.Data {
		if !modelPattern.MatchString(entry.ID) || seen[entry.ID] {
			return nil, "MODEL_PROTOCOL_INVALID"
		}
		seen[entry.ID] = true
		if manifest, ok := policy.Manifest(gateway.Endpoint, entry.ID); ok {
			items = append(items, manifest)
		}
	}
	return items, ""
}
func (s *Service) RefreshDirectory(ctx context.Context, p identity.Principal, version int64, trace string) (ModelDirectory, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if version < 1 {
		return ModelDirectory{}, invalid()
	}
	tx, err := s.begin(ctx, p, "model:validate", true)
	if err != nil {
		return ModelDirectory{}, err
	}
	defer tx.Rollback(context.Background())
	m, err := readModel(ctx, tx, p.OrganizationID, version)
	if err != nil {
		return ModelDirectory{}, err
	}
	if m.Provider != "platform-models" || !m.CredentialConfigured || m.Status == "REVOKED" || !s.policy.AllowsModel(m) {
		return ModelDirectory{}, invalid()
	}
	var credential string
	var envelope security.Envelope
	err = tx.QueryRow(ctx, `SELECT c.credential_ref::text,sec.key_id,sec.nonce,sec.ciphertext FROM model_configuration_heads h JOIN model_configurations c ON c.id=h.id AND c.version=h.current_version JOIN secrets sec ON sec.id=c.credential_ref WHERE h.org_id=$1 AND c.version=$2 AND c.revoked_at IS NULL AND sec.revoked_at IS NULL`, p.OrganizationID, version).Scan(&credential, &envelope.KeyID, &envelope.Nonce, &envelope.Ciphertext)
	if errors.Is(err, pgx.ErrNoRows) {
		return ModelDirectory{}, versionConflict()
	}
	if err != nil {
		return ModelDirectory{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return ModelDirectory{}, err
	}
	key, err := s.cipher.Decrypt(envelope, s.aad(p.OrganizationID, credential))
	if err != nil {
		return ModelDirectory{}, err
	}
	defer clear(key)
	gateway, _ := s.policy.Gateway(m.Endpoint)
	started := time.Now().UTC()
	items, reason := gatewayDirectory(ctx, gateway.HTTPClient(), gateway, string(key), s.policy)
	if items == nil {
		items = []ModelManifest{}
	}
	raw, _ := json.Marshal(items)
	finish, cancelFinish := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancelFinish()
	tx, err = s.begin(finish, p, "model:validate", false)
	if err != nil {
		return ModelDirectory{}, err
	}
	defer tx.Rollback(context.Background())
	var current int64
	if err = tx.QueryRow(finish, `SELECT current_version FROM model_configuration_heads WHERE org_id=$1 FOR UPDATE`, p.OrganizationID).Scan(&current); err != nil {
		return ModelDirectory{}, err
	}
	if current != version {
		return ModelDirectory{}, versionConflict()
	}
	currentModel, err := readModel(finish, tx, p.OrganizationID, version)
	if err != nil {
		return ModelDirectory{}, err
	}
	if !currentModel.CredentialConfigured || currentModel.Status == "REVOKED" || !s.policy.AllowsModel(currentModel) {
		return ModelDirectory{}, invalid()
	}
	status := "HEALTHY"
	if reason != "" {
		status = "UNHEALTHY"
	}
	_, err = tx.Exec(finish, `INSERT INTO model_directories(config_id,config_version,checked_at,expires_at,status,reason_code,items) VALUES($1,$2,$3,$3::timestamptz+interval '5 minutes',$4,$5,$6) ON CONFLICT(config_id,config_version) DO UPDATE SET checked_at=EXCLUDED.checked_at,expires_at=EXCLUDED.expires_at,status=EXCLUDED.status,reason_code=EXCLUDED.reason_code,items=EXCLUDED.items WHERE model_directories.checked_at<=EXCLUDED.checked_at`, m.ID, m.Version, started, status, reason, raw)
	if err != nil {
		return ModelDirectory{}, err
	}
	if err = s.audit(finish, tx, p, m.ID, "MODEL_DIRECTORY_REFRESH", trace); err != nil {
		return ModelDirectory{}, err
	}
	result, err := readDirectory(finish, tx, currentModel)
	if err != nil {
		return result, err
	}
	return result, tx.Commit(finish)
}
