package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type PersonalModelUpdate struct {
	Name            string          `json:"name"`
	CatalogEntryID  string          `json:"catalog_entry_id"`
	APIKey          *string         `json:"api_key"`
	ExpectedVersion int64           `json:"expected_version"`
	Selection       *ModelSelection `json:"selection,omitempty"`
}

// Update freezes a new version. Omission retains an active credential only for
// the same provider and account region; revoked credentials require replacement.
func (s *Service) UpdatePersonalModel(ctx context.Context, p identity.Principal, id string, input PersonalModelUpdate, trace string) (PersonalModel, error) {
	tx, err := s.begin(ctx, p, "personal-model:write", true)
	if err != nil {
		return PersonalModel{}, err
	}
	defer tx.Rollback(context.Background())
	groupID, err := lockPersonalProviderAccount(ctx, tx, p, id)
	if err != nil {
		return PersonalModel{}, err
	}
	result, err := s.updatePersonalModelTx(ctx, tx, p, id, input, trace)
	if err != nil {
		return result, err
	}
	if err = touchPersonalProviderAccount(ctx, tx, groupID, &result.Name); err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}
func (s *Service) updatePersonalModelTx(ctx context.Context, tx pgx.Tx, p identity.Principal, id string, input PersonalModelUpdate, trace string) (PersonalModel, error) {
	input.Name = strings.TrimSpace(input.Name)
	if !uuid.MatchString(id) {
		return PersonalModel{}, notFound()
	}
	if input.ExpectedVersion < 1 || !validSelection(input.Selection, input.CatalogEntryID) || !utf8.ValidString(input.Name) || utf8.RuneCountInString(input.Name) < 1 || utf8.RuneCountInString(input.Name) > 100 || strings.ContainsFunc(input.Name, unicode.IsControl) {
		return PersonalModel{}, invalid()
	}
	if input.APIKey != nil && *input.APIKey == "" && input.Selection != nil && customProvider(input.Selection.Provider) {
		value := "-"
		input.APIKey = &value
	}
	if input.APIKey != nil {
		if len(*input.APIKey) < 1 || len(*input.APIKey) > 4096 {
			return PersonalModel{}, invalid()
		}
		for _, b := range []byte(*input.APIKey) {
			if b < 0x21 || b > 0x7e {
				return PersonalModel{}, invalid()
			}
		}
	}
	var err error
	var ignored *string
	if err = tx.QueryRow(ctx, `SELECT public.hc_worker_lock_model($1)::text`, p.OrganizationID).Scan(&ignored); err != nil {
		return PersonalModel{}, err
	}
	var current int64
	var enabled bool
	err = tx.QueryRow(ctx, `SELECT current_version,enabled FROM user_model_connections WHERE org_id=$1 AND owner_id=$2 AND id=$3 AND archived_at IS NULL AND NOT provider_retired FOR UPDATE`, p.OrganizationID, p.UserID, id).Scan(&current, &enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return PersonalModel{}, notFound()
	}
	if err != nil {
		return PersonalModel{}, err
	}
	if current != input.ExpectedVersion {
		return PersonalModel{}, versionConflict()
	}
	var pending bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM model_sync_operations WHERE connection_id=$1 AND status IN ('PENDING','RUNNING','UNCERTAIN'))`, id).Scan(&pending); err != nil {
		return PersonalModel{}, err
	}
	if pending {
		return PersonalModel{}, webapi.Fail(409, "STATE_CONFLICT", "请等待当前同步或撤销完成")
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM user_model_connection_versions WHERE connection_id=$1`, id).Scan(&count); err != nil {
		return PersonalModel{}, err
	}
	if count >= 100 {
		return PersonalModel{}, webapi.Fail(429, "QUOTA_EXCEEDED", "此连接已达100个版本上限，请新增连接")
	}
	entry, err := s.resolvePersonalEntry(ctx, tx, p, input.CatalogEntryID, input.Selection)
	if errors.Is(err, pgx.ErrNoRows) {
		return PersonalModel{}, invalid()
	}
	if err != nil {
		return PersonalModel{}, err
	}
	var mismatch bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_model_connections h JOIN personal_provider_accounts g ON g.id=h.provider_account_id JOIN user_model_connection_versions v ON v.connection_id=h.id AND v.version=h.current_version WHERE h.id=$1 AND (g.status<>'ACTIVE' OR g.provider_id<>$2 OR g.api_base<>$3 OR v.manifest->'catalog'->>'model'<>$4))`, id, entry.Provider, entry.APIBase, entry.Model).Scan(&mismatch); err != nil {
		return PersonalModel{}, err
	}
	if mismatch {
		return PersonalModel{}, webapi.Fail(409, "STATE_CONFLICT", "请通过模型提供商账号管理模型，或新增连接来更换服务")
	}
	if s.policy == nil || len(s.policy.Gateways) == 0 {
		return PersonalModel{}, webapi.Fail(503, "MODEL_GATEWAY_NOT_CONFIGURED", "模型网关尚未配置")
	}
	gateway := s.policy.Gateways[0]
	var prefix string
	var params []byte
	if err = tx.QueryRow(ctx, `SELECT adapter_prefix,parameters FROM model_providers WHERE id=$1`, entry.Provider).Scan(&prefix, &params); err != nil {
		return PersonalModel{}, err
	}
	version := current + 1
	var secret string
	raw, _ := json.Marshal(personalManifest{entry, prefix, params, gateway.Revision})
	if input.APIKey == nil {
		if !enabled {
			return PersonalModel{}, invalid()
		}
		var oldRaw []byte
		err = tx.QueryRow(ctx, `SELECT v.manifest,s.id::text FROM user_model_connection_versions v JOIN external_provider_secrets s ON (s.org_id,s.owner_id,s.connection_id,s.id)=(v.org_id,v.owner_id,v.connection_id,v.provider_secret_id) WHERE v.org_id=$1 AND v.owner_id=$2 AND v.connection_id=$3 AND v.version=$4 AND s.revoked_at IS NULL FOR SHARE OF s`, p.OrganizationID, p.UserID, id, current).Scan(&oldRaw, &secret)
		if errors.Is(err, pgx.ErrNoRows) {
			return PersonalModel{}, invalid()
		}
		if err != nil {
			return PersonalModel{}, err
		}
		var previous personalManifest
		if json.Unmarshal(oldRaw, &previous) != nil || previous.Catalog.Provider != entry.Provider || previous.Catalog.APIBase != entry.APIBase {
			return PersonalModel{}, invalid()
		}
	} else {
		secret = webapi.ID()
		key := []byte(*input.APIKey)
		envelope, encryptErr := s.cipher.Encrypt(key, security.Context{Environment: s.environment, OrganizationID: p.OrganizationID, RecordID: fmt.Sprintf("%s/%s/%s/%d", p.UserID, id, secret, version), Purpose: "provider-key"})
		clear(key)
		if encryptErr != nil {
			return PersonalModel{}, encryptErr
		}
		if _, err = tx.Exec(ctx, `INSERT INTO external_provider_secrets(id,org_id,owner_id,connection_id,bound_version,key_id,nonce,ciphertext) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, secret, p.OrganizationID, p.UserID, id, version, envelope.KeyID, envelope.Nonce, envelope.Ciphertext); err != nil {
			return PersonalModel{}, err
		}
	}
	alias := fmt.Sprintf("hc-%s-v%d", strings.ReplaceAll(id, "-", ""), version)
	if _, err = tx.Exec(ctx, `INSERT INTO user_model_connection_versions(connection_id,org_id,owner_id,version,catalog_entry_id,catalog_version,provider_secret_id,gateway_endpoint,route_alias,manifest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, id, p.OrganizationID, p.UserID, version, entry.ID, entry.Version, secret, gateway.Endpoint, alias, raw); err != nil {
		return PersonalModel{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO model_sync_operations(id,org_id,owner_id,connection_id,connection_version,action,remote_model_id) VALUES($1,$2,$3,$4,$5,'UPSERT',$6)`, webapi.ID(), p.OrganizationID, p.UserID, id, version, webapi.ID()); err != nil {
		return PersonalModel{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE user_model_connections SET name=$2,current_version=$3,enabled=true,updated_at=clock_timestamp() WHERE id=$1`, id, input.Name, version); err != nil {
		return PersonalModel{}, err
	}
	if err = s.audit(ctx, tx, p, id, "PERSONAL_MODEL_UPDATED", trace); err != nil {
		return PersonalModel{}, err
	}
	result, err := personalGet(ctx, tx, p, id)
	if err != nil {
		return result, err
	}
	return result, nil
}
