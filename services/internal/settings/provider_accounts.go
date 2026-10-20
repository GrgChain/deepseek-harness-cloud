package settings

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/modelgateway"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type ProviderAccountModel struct {
	PersonalModel
	Selection ModelSelection `json:"selection"`
	InUse     int64          `json:"in_use"`
}
type ProviderAccount struct {
	DefaultModelID       *string                `json:"default_model_id"`
	ID                   string                 `json:"id"`
	Name                 string                 `json:"name"`
	Provider             string                 `json:"provider"`
	APIBase              string                 `json:"api_base"`
	Version              int64                  `json:"version"`
	Status               string                 `json:"status"`
	CredentialConfigured bool                   `json:"credential_configured"`
	Models               []ProviderAccountModel `json:"models"`
}
type ProviderAccountInput struct {
	DefaultModel    string           `json:"default_model,omitempty"`
	Name            string           `json:"name"`
	Provider        string           `json:"provider"`
	APIBase         string           `json:"api_base"`
	APIKey          *string          `json:"api_key"`
	Models          []ModelSelection `json:"models"`
	ExpectedVersion int64            `json:"expected_version"`
}

// Preserve an account's selected model through rename/key rotation. If an edit
// removes it, the first explicitly retained model becomes the account default.
func updateProviderDefault(ctx context.Context, tx pgx.Tx, account, selected string, models []ModelSelection) error {
	if selected == "" {
		var current string
		err := tx.QueryRow(ctx, `SELECT COALESCE(v.manifest->'catalog'->>'model','') FROM personal_provider_accounts g LEFT JOIN user_model_connections h ON h.id=g.default_model_id AND h.provider_account_id=g.id AND h.archived_at IS NULL AND h.enabled AND NOT h.provider_retired LEFT JOIN user_model_connection_versions v ON v.connection_id=h.id AND v.version=h.current_version WHERE g.id=$1`, account).Scan(&current)
		if err != nil {
			return err
		}
		for _, model := range models {
			if model.Model == current {
				selected = current
				break
			}
		}
		if selected == "" {
			selected = models[0].Model
		}
	}
	result, err := tx.Exec(ctx, `UPDATE personal_provider_accounts g SET default_model_id=h.id FROM user_model_connections h JOIN user_model_connection_versions v ON v.connection_id=h.id AND v.version=h.current_version WHERE g.id=$1 AND h.provider_account_id=g.id AND h.org_id=g.org_id AND h.owner_id=g.owner_id AND h.archived_at IS NULL AND h.enabled AND NOT h.provider_retired AND v.manifest->'catalog'->>'model'=$2`, account, selected)
	if err == nil && result.RowsAffected() != 1 {
		return invalid()
	}
	return err
}

func validProviderInput(input ProviderAccountInput) bool {
	if !utf8.ValidString(input.Name) || utf8.RuneCountInString(input.Name) < 1 || utf8.RuneCountInString(input.Name) > 100 || strings.ContainsFunc(input.Name, unicode.IsControl) || len(input.Models) < 1 || len(input.Models) > 20 {
		return false
	}
	seen := map[string]bool{}
	for _, selection := range input.Models {
		if selection.Provider != input.Provider || selection.APIBase != input.APIBase || !validSelection(&selection, "") || seen[selection.Model] {
			return false
		}
		seen[selection.Model] = true
	}
	return input.DefaultModel == "" || seen[input.DefaultModel]
}
func providerAccountGet(ctx context.Context, tx pgx.Tx, p identity.Principal, id string, lock bool) (ProviderAccount, error) {
	var value ProviderAccount
	query := `SELECT id::text,name,provider_id,api_base,version,status,default_model_id::text FROM personal_provider_accounts WHERE org_id=$1 AND owner_id=$2 AND id=$3`
	if lock {
		query += ` FOR UPDATE`
	}
	err := tx.QueryRow(ctx, query, p.OrganizationID, p.UserID, id).Scan(&value.ID, &value.Name, &value.Provider, &value.APIBase, &value.Version, &value.Status, &value.DefaultModelID)
	if errors.Is(err, pgx.ErrNoRows) {
		return value, notFound()
	}
	if err != nil {
		return value, err
	}
	rows, err := tx.Query(ctx, `SELECT h.id::text,v.manifest->'catalog',(SELECT count(*) FROM instances i WHERE i.org_id=h.org_id AND i.model_config_id=h.id AND i.deleted_at IS NULL) FROM user_model_connections h JOIN user_model_connection_versions v ON v.connection_id=h.id AND v.version=h.current_version WHERE h.provider_account_id=$1 AND h.org_id=$2 AND h.owner_id=$3 AND h.archived_at IS NULL AND NOT h.provider_retired ORDER BY h.created_at,h.id`, id, p.OrganizationID, p.UserID)
	if err != nil {
		return value, err
	}
	type ref struct {
		id    string
		entry ModelCatalogEntry
		usage int64
	}
	refs := []ref{}
	for rows.Next() {
		var item ref
		var raw []byte
		if err = rows.Scan(&item.id, &raw, &item.usage); err != nil {
			rows.Close()
			return value, err
		}
		if err = json.Unmarshal(raw, &item.entry); err != nil {
			rows.Close()
			return value, err
		}
		refs = append(refs, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return value, err
	}
	value.Models = []ProviderAccountModel{}
	for _, r := range refs {
		model, err := personalGet(ctx, tx, p, r.id)
		if err != nil {
			return value, err
		}
		entry := r.entry
		value.Models = append(value.Models, ProviderAccountModel{model, ModelSelection{Provider: entry.Provider, APIBase: entry.APIBase, Model: entry.Model, ResponseModels: entry.ResponseModels, ContextWindow: entry.ContextWindow, MaxOutputTokens: entry.MaxOutputTokens}, r.usage})
		value.CredentialConfigured = value.CredentialConfigured || (model.Enabled && model.CredentialConfigured)
	}
	return value, nil
}
func (s *Service) ListProviderAccounts(ctx context.Context, p identity.Principal) ([]ProviderAccount, error) {
	tx, err := s.begin(ctx, p, "personal-model:read", false)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	rows, err := tx.Query(ctx, `SELECT id::text FROM personal_provider_accounts WHERE org_id=$1 AND owner_id=$2 AND status<>'DELETED' ORDER BY created_at DESC,id DESC LIMIT 20`, p.OrganizationID, p.UserID)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	result := []ProviderAccount{}
	for _, id := range ids {
		v, err := providerAccountGet(ctx, tx, p, id, false)
		if err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, nil
}
func lockProviderOwner(ctx context.Context, tx pgx.Tx, p identity.Principal) error {
	var ignored *string
	if err := tx.QueryRow(ctx, `SELECT public.hc_worker_lock_model($1)::text`, p.OrganizationID).Scan(&ignored); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "personal-model:"+p.OrganizationID+":"+p.UserID)
	return err
}

func lockPersonalProviderAccount(ctx context.Context, tx pgx.Tx, p identity.Principal, id string) (*string, error) {
	if err := lockProviderOwner(ctx, tx, p); err != nil {
		return nil, err
	}
	var groupID string
	err := tx.QueryRow(ctx, `SELECT g.id::text FROM personal_provider_accounts g JOIN user_model_connections h ON h.provider_account_id=g.id WHERE h.id=$1 AND h.org_id=$2 AND h.owner_id=$3 FOR UPDATE OF g`, id, p.OrganizationID, p.UserID).Scan(&groupID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &groupID, err
}
func touchPersonalProviderAccount(ctx context.Context, tx pgx.Tx, id *string, name *string) error {
	if id == nil {
		return nil
	}
	_, err := tx.Exec(ctx, `UPDATE personal_provider_accounts SET name=COALESCE($2,name),version=version+1,updated_at=clock_timestamp() WHERE id=$1`, *id, name)
	return err
}

// Legacy single-model clients remain supported; a batch adopts one stable group.
func adoptProviderAccountModelsTx(ctx context.Context, tx pgx.Tx, p identity.Principal, items []PersonalModel) error {
	if len(items) == 0 {
		return nil
	}
	id := items[0].ID
	_, err := tx.Exec(ctx, `INSERT INTO personal_provider_accounts(id,org_id,owner_id,name,provider_id,api_base) SELECT $1,$2,$3,h.name,e.provider_id,e.api_base FROM user_model_connections h JOIN user_model_connection_versions v ON v.connection_id=h.id AND v.version=h.current_version JOIN model_catalog_entries e ON e.id=v.catalog_entry_id WHERE h.id=$1 AND h.provider_account_id IS NULL ON CONFLICT DO NOTHING`, id, p.OrganizationID, p.UserID)
	if err != nil {
		return err
	}
	for _, item := range items {
		if _, err = tx.Exec(ctx, `UPDATE user_model_connections SET provider_account_id=$2 WHERE id=$1 AND provider_account_id IS NULL`, item.ID, id); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE personal_provider_accounts SET default_model_id=$1 WHERE id=$1 AND default_model_id IS NULL`, id)
	return err
}
func (s *Service) CreateProviderAccount(ctx context.Context, p identity.Principal, input ProviderAccountInput, key, trace string) (ProviderAccount, error) {
	input.Name = strings.TrimSpace(input.Name)
	if !validProviderInput(input) || input.APIKey == nil || input.ExpectedVersion != 0 || !personalCreateKey.MatchString(key) {
		return ProviderAccount{}, invalid()
	}
	tx, err := s.begin(ctx, p, "personal-model:write", true)
	if err != nil {
		return ProviderAccount{}, err
	}
	defer tx.Rollback(context.Background())
	if err = lockProviderOwner(ctx, tx, p); err != nil {
		return ProviderAccount{}, err
	}
	keyHash := sha256.Sum256([]byte(key))
	raw, _ := json.Marshal(input)
	bodyHash := sha256.Sum256(raw)
	clear(raw)
	var id string
	var previous []byte
	err = tx.QueryRow(ctx, `SELECT id::text,create_request_sha256 FROM personal_provider_accounts WHERE org_id=$1 AND owner_id=$2 AND create_key_sha256=$3`, p.OrganizationID, p.UserID, keyHash[:]).Scan(&id, &previous)
	if err == nil {
		if !slices.Equal(previous, bodyHash[:]) {
			return ProviderAccount{}, webapi.Fail(409, "IDEMPOTENCY_CONFLICT", "该提交键已用于其他提供商配置")
		}
		v, err := providerAccountGet(ctx, tx, p, id, false)
		return v, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ProviderAccount{}, err
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM personal_provider_accounts WHERE org_id=$1 AND owner_id=$2 AND status<>'DELETED'`, p.OrganizationID, p.UserID).Scan(&count); err != nil {
		return ProviderAccount{}, err
	}
	if count >= 20 {
		return ProviderAccount{}, webapi.Fail(429, "QUOTA_EXCEEDED", "最多保存20个模型提供商账号")
	}
	if _, err = s.selectionProvider(ctx, tx, input.Provider, input.APIBase); err != nil {
		return ProviderAccount{}, err
	}
	id = webapi.ID()
	if _, err = tx.Exec(ctx, `INSERT INTO personal_provider_accounts(id,org_id,owner_id,name,provider_id,api_base,create_key_sha256,create_request_sha256) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, id, p.OrganizationID, p.UserID, input.Name, input.Provider, input.APIBase, keyHash[:], bodyHash[:]); err != nil {
		return ProviderAccount{}, err
	}
	for i, selection := range input.Models {
		item, err := s.createPersonalModelTx(ctx, tx, p, PersonalModelInput{Name: input.Name, Selection: &selection, APIKey: *input.APIKey}, fmt.Sprintf("provider-%s-%d", id, i), trace, bodyHash[:])
		if err != nil {
			return ProviderAccount{}, err
		}
		if _, err = tx.Exec(ctx, `UPDATE user_model_connections SET provider_account_id=$2 WHERE id=$1`, item.ID, id); err != nil {
			return ProviderAccount{}, err
		}
	}
	if err = updateProviderDefault(ctx, tx, id, input.DefaultModel, input.Models); err != nil {
		return ProviderAccount{}, err
	}
	if err = s.audit(ctx, tx, p, id, "PERSONAL_PROVIDER_ACCOUNT_CREATED", trace); err != nil {
		return ProviderAccount{}, err
	}
	value, err := providerAccountGet(ctx, tx, p, id, false)
	if err != nil {
		return value, err
	}
	return value, tx.Commit(ctx)
}
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
func clearProviderDefault(ctx context.Context, tx pgx.Tx, id string) error {
	_, err := tx.Exec(ctx, `UPDATE user_model_defaults SET connection_id=NULL,connection_version=NULL,version=version+1,updated_at=clock_timestamp() WHERE connection_id=$1`, id)
	return err
}
func (s *Service) retireProviderAccountModel(ctx context.Context, tx pgx.Tx, p identity.Principal, item ProviderAccountModel, trace string) error {
	if item.InUse > 0 {
		return webapi.Fail(409, "MODEL_IN_USE", "请先更换或删除使用此模型的工作空间")
	}
	if err := s.authorize(ctx, tx, p, "personal-model:revoke"); err != nil {
		return err
	}
	if err := clearProviderDefault(ctx, tx, item.ID); err != nil {
		return err
	}
	if _, err := s.revokePersonalModelTx(ctx, tx, p, item.ID, item.Version, trace); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE user_model_connections SET provider_retired=true WHERE id=$1`, item.ID)
	return err
}
func normalizeSelection(v ModelSelection) ModelSelection {
	if v.ContextWindow == 0 {
		v.ContextWindow = 131072
	}
	if v.MaxOutputTokens == 0 {
		v.MaxOutputTokens = 16384
	}
	if len(v.ResponseModels) == 0 {
		v.ResponseModels = []string{v.Model}
	}
	return v
}
func sameSelection(a, b ModelSelection) bool {
	a = normalizeSelection(a)
	b = normalizeSelection(b)
	return a.Provider == b.Provider && a.APIBase == b.APIBase && a.Model == b.Model && a.ContextWindow == b.ContextWindow && a.MaxOutputTokens == b.MaxOutputTokens && slices.Equal(a.ResponseModels, b.ResponseModels)
}
func (s *Service) UpdateProviderAccount(ctx context.Context, p identity.Principal, id string, input ProviderAccountInput, trace string) (ProviderAccount, error) {
	input.Name = strings.TrimSpace(input.Name)
	if !uuid.MatchString(id) || !validProviderInput(input) || input.ExpectedVersion < 1 {
		return ProviderAccount{}, invalid()
	}
	tx, err := s.begin(ctx, p, "personal-model:write", true)
	if err != nil {
		return ProviderAccount{}, err
	}
	defer tx.Rollback(context.Background())
	if err = lockProviderOwner(ctx, tx, p); err != nil {
		return ProviderAccount{}, err
	}
	current, err := providerAccountGet(ctx, tx, p, id, true)
	if err != nil {
		return current, err
	}
	if current.Version != input.ExpectedVersion {
		return current, versionConflict()
	}
	if current.Status != "ACTIVE" || current.Provider != input.Provider || current.APIBase != input.APIBase {
		return current, webapi.Fail(409, "STATE_CONFLICT", "请新增连接来更换提供商或服务地址")
	}
	desired := map[string]ModelSelection{}
	for _, v := range input.Models {
		desired[v.Model] = v
	}
	existing := map[string]ProviderAccountModel{}
	for _, v := range current.Models {
		existing[v.Model] = v
	}
	var retainedKey []byte
	for _, selection := range input.Models {
		if _, ok := existing[selection.Model]; !ok && input.APIKey == nil {
			retainedKey, err = s.providerAccountKey(ctx, tx, p, id)
			if err != nil {
				return current, err
			}
			defer clear(retainedKey)
			break
		}
	}
	for _, item := range current.Models {
		if _, ok := desired[item.Model]; !ok {
			if err = s.retireProviderAccountModel(ctx, tx, p, item, trace); err != nil {
				return current, err
			}
		}
	}
	for _, selection := range input.Models {
		if item, ok := existing[selection.Model]; ok {
			if input.APIKey != nil || !sameSelection(item.Selection, selection) || !item.Enabled {
				if _, err = s.updatePersonalModelTx(ctx, tx, p, item.ID, PersonalModelUpdate{Name: input.Name, Selection: &selection, APIKey: input.APIKey, ExpectedVersion: item.Version}, trace); err != nil {
					return current, err
				}
			} else if _, err = tx.Exec(ctx, `UPDATE user_model_connections SET name=$2,updated_at=clock_timestamp() WHERE id=$1`, item.ID, input.Name); err != nil {
				return current, err
			}
		} else {
			key := string(retainedKey)
			if input.APIKey != nil {
				key = *input.APIKey
			}
			if customProvider(input.Provider) && key == "-" {
				key = ""
			}
			item, err := s.createPersonalModelTx(ctx, tx, p, PersonalModelInput{Name: input.Name, Selection: &selection, APIKey: key}, "provider-add-"+webapi.ID(), trace, nil)
			if err != nil {
				return current, err
			}
			if _, err = tx.Exec(ctx, `UPDATE user_model_connections SET provider_account_id=$2 WHERE id=$1`, item.ID, id); err != nil {
				return current, err
			}
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE personal_provider_accounts SET name=$2,version=version+1,updated_at=clock_timestamp() WHERE id=$1`, id, input.Name); err != nil {
		return current, err
	}
	if err = updateProviderDefault(ctx, tx, id, input.DefaultModel, input.Models); err != nil {
		return current, err
	}
	if err = s.audit(ctx, tx, p, id, "PERSONAL_PROVIDER_ACCOUNT_UPDATED", trace); err != nil {
		return current, err
	}
	value, err := providerAccountGet(ctx, tx, p, id, false)
	if err != nil {
		return value, err
	}
	return value, tx.Commit(ctx)
}
func (s *Service) DeleteProviderAccount(ctx context.Context, p identity.Principal, id string, version int64, trace string) (ProviderAccount, error) {
	if !uuid.MatchString(id) || version < 1 {
		return ProviderAccount{}, invalid()
	}
	tx, err := s.begin(ctx, p, "personal-model:revoke", true)
	if err != nil {
		return ProviderAccount{}, err
	}
	defer tx.Rollback(context.Background())
	if err = lockProviderOwner(ctx, tx, p); err != nil {
		return ProviderAccount{}, err
	}
	value, err := providerAccountGet(ctx, tx, p, id, true)
	if err != nil {
		return value, err
	}
	if value.Version != version {
		return value, versionConflict()
	}
	if value.Status != "ACTIVE" {
		return value, nil
	}
	for _, item := range value.Models {
		if err = s.retireProviderAccountModel(ctx, tx, p, item, trace); err != nil {
			return value, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE personal_provider_accounts SET status='DELETING',version=version+1,updated_at=clock_timestamp() WHERE id=$1`, id); err != nil {
		return value, err
	}
	if err = s.audit(ctx, tx, p, id, "PERSONAL_PROVIDER_ACCOUNT_DELETE_REQUESTED", trace); err != nil {
		return value, err
	}
	value.Status = "DELETING"
	value.Version++
	return value, tx.Commit(ctx)
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
