package settings

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/modelgateway"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func (s *Service) providerAccountKey(ctx context.Context, tx pgx.Tx, p identity.Principal, id string) ([]byte, error) {
	var envelope security.Envelope
	var connection, secret string
	var version int64
	err := tx.QueryRow(ctx, `SELECT connection_id::text,secret_id::text,bound_version,key_id,nonce,ciphertext FROM public.hc_personal_provider_account_credential($1,$2,$3,$4,$5)`, p.OrganizationID, p.UserID, p.SessionID, p.Generation, id).Scan(&connection, &secret, &version, &envelope.KeyID, &envelope.Nonce, &envelope.Ciphertext)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, webapi.Fail(422, "PROVIDER_KEY_REQUIRED", "请填写新的提供商密钥")
	}
	if err != nil {
		return nil, err
	}
	return s.cipher.Decrypt(envelope, security.Context{Environment: s.environment, OrganizationID: p.OrganizationID, RecordID: fmt.Sprintf("%s/%s/%s/%d", p.UserID, connection, secret, version), Purpose: "provider-key"})
}

type ProviderAccountSyncInput struct {
	ExpectedVersion int64   `json:"expected_version"`
	APIKey          *string `json:"api_key"`
}

func (s *Service) DiscoverProviderAccountModels(ctx context.Context, p identity.Principal, id string, input ProviderAccountSyncInput) ([]modelgateway.DiscoveredModel, error) {
	if !uuid.MatchString(id) {
		return nil, notFound()
	}
	tx, err := s.begin(ctx, p, "personal-model:write", false)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	value, err := providerAccountGet(ctx, tx, p, id, false)
	if err != nil {
		return nil, err
	}
	if value.Version != input.ExpectedVersion {
		return nil, versionConflict()
	}
	if value.Status != "ACTIVE" {
		return nil, webapi.Fail(409, "STATE_CONFLICT", "模型提供商账号正在删除")
	}
	var key []byte
	if input.APIKey != nil {
		key = []byte(*input.APIKey)
	} else {
		key, err = s.providerAccountKey(ctx, tx, p, id)
		if err != nil {
			return nil, err
		}
	}
	defer clear(key)
	if string(key) == "-" && customProvider(value.Provider) {
		key = nil
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.DiscoverProviderModels(ctx, p, value.Provider, ModelDiscoveryInput{APIBase: value.APIBase, APIKey: string(key)})
}

// Revocation remains durable until all versions are confirmed revoked. Only
// then can retired models release their list entries and provider deletion finish.
func (a *ModelSync) finishProviderAccountDeletes(ctx context.Context) error {
	tx, err := a.service.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	_, err = tx.Exec(ctx, `WITH retired AS (SELECT h.id FROM user_model_connections h WHERE h.provider_retired AND NOT h.enabled AND h.archived_at IS NULL AND NOT EXISTS(SELECT 1 FROM user_model_connection_versions v WHERE v.connection_id=h.id AND v.sync_status<>'REVOKED') AND NOT EXISTS(SELECT 1 FROM instances i WHERE i.model_config_id=h.id AND i.deleted_at IS NULL) AND NOT EXISTS(SELECT 1 FROM user_model_defaults d WHERE d.connection_id=h.id) ORDER BY h.id LIMIT 100 FOR UPDATE SKIP LOCKED) UPDATE user_model_connections SET archived_at=clock_timestamp() WHERE id IN (SELECT id FROM retired)`)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE personal_provider_accounts p SET status='DELETED',updated_at=clock_timestamp() WHERE p.id IN (SELECT candidate.id FROM personal_provider_accounts candidate WHERE candidate.status<>'DELETED' AND NOT EXISTS(SELECT 1 FROM user_model_connections h WHERE h.provider_account_id=candidate.id AND h.archived_at IS NULL) ORDER BY candidate.id LIMIT 100 FOR UPDATE SKIP LOCKED)`)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
