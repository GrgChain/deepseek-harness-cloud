package settings

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type PersonalModelInput struct {
	Name           string          `json:"name"`
	CatalogEntryID string          `json:"catalog_entry_id"`
	APIKey         string          `json:"api_key"`
	Selection      *ModelSelection `json:"selection,omitempty"`
}
type PersonalModel struct {
	ID                   string    `json:"id"`
	Name                 string    `json:"name"`
	Version              int64     `json:"version"`
	CatalogEntryID       string    `json:"catalog_entry_id"`
	Provider             string    `json:"provider"`
	Model                string    `json:"model"`
	Enabled              bool      `json:"enabled"`
	CredentialConfigured bool      `json:"credential_configured"`
	SyncStatus           string    `json:"sync_status"`
	OperationID          string    `json:"operation_id"`
	OperationStatus      string    `json:"operation_status"`
	Reason               string    `json:"reason_code"`
	Validation           Health    `json:"validation"`
	GatewayCheck         Health    `json:"gateway_check"`
	CreatedAt            time.Time `json:"created_at"`
}
type ModelCatalogEntry struct {
	ID              string   `json:"id"`
	Provider        string   `json:"provider"`
	Model           string   `json:"model"`
	Name            string   `json:"name"`
	APIBase         string   `json:"api_base"`
	Version         int64    `json:"version"`
	ResponseModels  []string `json:"response_models"`
	ContextWindow   int64    `json:"context_window"`
	MaxOutputTokens int64    `json:"max_output_tokens"`
	Streaming       bool     `json:"streaming"`
	ToolUse         bool     `json:"tool_use"`
}
type ModelProvider struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	APIBases []string `json:"api_bases"`
	Version  int64    `json:"version"`
}

var personalCreateKey = regexp.MustCompile(`^[A-Za-z0-9._:-]{16,128}$`)

const personalSelect = `SELECT h.id::text,h.name,h.current_version,e.id::text,v.manifest->'catalog'->>'provider',v.manifest->'catalog'->>'model',h.enabled,s.revoked_at IS NULL,
 CASE WHEN NOT h.enabled AND EXISTS(SELECT 1 FROM user_model_connection_versions pending WHERE pending.connection_id=h.id AND pending.sync_status<>'REVOKED') THEN 'REVOKING' ELSE v.sync_status END,
 op.id::text,op.status,op.reason_code,h.created_at,
 COALESCE(CASE WHEN c.status='REVOKED' OR c.revoked_at IS NOT NULL THEN 'UNHEALTHY' WHEN mv.status='CHECKING' AND mv.lease_expires_at>clock_timestamp() THEN 'CHECKING' WHEN mv.checked_at<=clock_timestamp() AND mv.expires_at>clock_timestamp() THEN mv.status ELSE 'UNKNOWN' END,'UNKNOWN'),mv.checked_at,mv.expires_at,COALESCE(mv.reason_code,'VALIDATION_REQUIRED'),
 CASE WHEN NOT h.enabled OR v.sync_status<>'SYNCED' OR c.revoked_at IS NOT NULL THEN 'UNKNOWN' WHEN g.status='UNHEALTHY' THEN 'UNHEALTHY' WHEN g.status='HEALTHY' AND g.checked_at<=clock_timestamp() AND g.expires_at>clock_timestamp() THEN 'HEALTHY' ELSE 'UNKNOWN' END,g.checked_at,g.expires_at,
 CASE WHEN NOT h.enabled OR v.sync_status IN ('REVOKING','REVOKED') THEN 'CREDENTIAL_REVOKED' WHEN g.status='HEALTHY' AND (g.checked_at>clock_timestamp() OR g.expires_at<=clock_timestamp()) THEN 'MODEL_GATEWAY_CHECK_EXPIRED' ELSE COALESCE(g.reason_code,'MODEL_GATEWAY_UNVERIFIED') END
 FROM user_model_connections h JOIN user_model_connection_versions v ON v.connection_id=h.id AND v.version=h.current_version AND v.org_id=h.org_id AND v.owner_id=h.owner_id
 JOIN model_catalog_entries e ON e.org_id=v.org_id AND e.id=v.catalog_entry_id
 JOIN external_provider_secrets s ON s.id=v.provider_secret_id AND s.org_id=v.org_id AND s.owner_id=v.owner_id AND s.connection_id=v.connection_id
 LEFT JOIN model_configurations c ON c.id=v.connection_id AND c.version=v.version
 LEFT JOIN model_validations mv ON mv.config_id=c.id AND mv.config_version=c.version
 LEFT JOIN model_gateway_checks g ON g.connection_id=v.connection_id AND g.connection_version=v.version AND g.org_id=v.org_id AND g.owner_id=v.owner_id
 JOIN LATERAL (SELECT id,status,reason_code FROM model_sync_operations o WHERE o.org_id=h.org_id AND o.owner_id=h.owner_id AND o.connection_id=h.id AND (o.connection_version=v.version OR (NOT h.enabled AND o.action='REVOKE' AND o.status<>'SUCCEEDED')) ORDER BY CASE WHEN NOT h.enabled AND o.action='REVOKE' AND o.status<>'SUCCEEDED' THEN 0 ELSE 1 END,o.created_at DESC,o.id DESC LIMIT 1) op ON true`

func scanPersonal(row pgx.Row) (PersonalModel, error) {
	var m PersonalModel
	err := row.Scan(&m.ID, &m.Name, &m.Version, &m.CatalogEntryID, &m.Provider, &m.Model, &m.Enabled, &m.CredentialConfigured, &m.SyncStatus, &m.OperationID, &m.OperationStatus, &m.Reason, &m.CreatedAt, &m.Validation.Status, &m.Validation.CheckedAt, &m.Validation.ExpiresAt, &m.Validation.Reason, &m.GatewayCheck.Status, &m.GatewayCheck.CheckedAt, &m.GatewayCheck.ExpiresAt, &m.GatewayCheck.Reason)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, notFound()
	}
	return m, err
}
func personalGet(ctx context.Context, tx pgx.Tx, p identity.Principal, id string) (PersonalModel, error) {
	return scanPersonal(tx.QueryRow(ctx, personalSelect+` WHERE h.org_id=$1 AND h.owner_id=$2 AND h.id=$3`, p.OrganizationID, p.UserID, id))
}
func (s *Service) GetPersonalModel(ctx context.Context, p identity.Principal, id string) (PersonalModel, error) {
	if !uuid.MatchString(id) {
		return PersonalModel{}, notFound()
	}
	tx, err := s.begin(ctx, p, "personal-model:read", false)
	if err != nil {
		return PersonalModel{}, err
	}
	defer tx.Rollback(context.Background())
	return personalGet(ctx, tx, p, id)
}
func (s *Service) ListPersonalModels(ctx context.Context, p identity.Principal) ([]PersonalModel, error) {
	tx, err := s.begin(ctx, p, "personal-model:read", false)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	// The enforced owner limit is 20 live connections. Archived records remain
	// queryable by their owner and cannot silently reappear in this live list.
	rows, err := tx.Query(ctx, personalSelect+` WHERE h.org_id=$1 AND h.owner_id=$2 AND h.archived_at IS NULL AND NOT h.provider_retired ORDER BY h.created_at DESC,h.id DESC LIMIT 20`, p.OrganizationID, p.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []PersonalModel{}
	for rows.Next() {
		item, e := scanPersonal(rows)
		if e != nil {
			return nil, e
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
func (s *Service) ModelProviders(ctx context.Context, p identity.Principal) ([]ModelProvider, error) {
	tx, err := s.begin(ctx, p, "personal-model:read", false)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	rows, err := tx.Query(ctx, `SELECT id,name,api_bases,version FROM model_providers WHERE enabled ORDER BY name LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ModelProvider{}
	for rows.Next() {
		var item ModelProvider
		var raw []byte
		if err := rows.Scan(&item.ID, &item.Name, &raw, &item.Version); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &item.APIBases); err != nil {
			return nil, err
		}
		if customProvider(item.ID) {
			item.APIBases = []string{}
			if s.policy != nil {
				item.APIBases = append(item.APIBases, s.policy.CustomAPIBases...)
			}
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

const catalogSelect = `SELECT e.id::text,e.provider_id,e.model_id,e.name,e.api_base,e.version,e.response_models,e.context_window,e.max_output_tokens,e.streaming,e.tool_use FROM model_catalog_entries e JOIN model_providers p ON p.id=e.provider_id`

func scanCatalog(row pgx.Row) (ModelCatalogEntry, error) {
	var item ModelCatalogEntry
	var raw []byte
	err := row.Scan(&item.ID, &item.Provider, &item.Model, &item.Name, &item.APIBase, &item.Version, &raw, &item.ContextWindow, &item.MaxOutputTokens, &item.Streaming, &item.ToolUse)
	if err == nil {
		err = json.Unmarshal(raw, &item.ResponseModels)
	}
	return item, err
}
func (s *Service) ModelCatalog(ctx context.Context, p identity.Principal) ([]ModelCatalogEntry, error) {
	tx, err := s.begin(ctx, p, "personal-model:read", false)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	rows, err := tx.Query(ctx, catalogSelect+` WHERE e.org_id=$1 AND e.owner_id IS NULL AND e.enabled AND p.enabled ORDER BY e.name,e.id LIMIT 101`, p.OrganizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ModelCatalogEntry{}
	for rows.Next() {
		item, e := scanCatalog(rows)
		if e != nil {
			return nil, e
		}
		items = append(items, item)
	}
	if len(items) > 100 {
		return nil, webapi.Fail(503, "MODEL_CATALOG_LIMIT", "批准模型目录超限，请联系管理员")
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	privateRows, err := tx.Query(ctx, catalogSelect+` WHERE e.org_id=$1 AND e.owner_id=$2 AND EXISTS(SELECT 1 FROM user_model_connections h JOIN user_model_connection_versions v ON v.connection_id=h.id AND v.version=h.current_version WHERE h.org_id=e.org_id AND h.owner_id=e.owner_id AND h.archived_at IS NULL AND NOT h.provider_retired AND v.catalog_entry_id=e.id) ORDER BY e.name,e.id LIMIT 20`, p.OrganizationID, p.UserID)
	if err != nil {
		return nil, err
	}
	defer privateRows.Close()
	for privateRows.Next() {
		item, err := scanCatalog(privateRows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, privateRows.Err()
}
func (s *Service) CreatePersonalModel(ctx context.Context, p identity.Principal, input PersonalModelInput, key, trace string) (PersonalModel, error) {
	tx, err := s.begin(ctx, p, "personal-model:write", true)
	if err != nil {
		return PersonalModel{}, err
	}
	defer tx.Rollback(context.Background())
	result, err := s.createPersonalModelTx(ctx, tx, p, input, key, trace, nil)
	if err != nil {
		return result, err
	}
	if err = adoptProviderAccountModelsTx(ctx, tx, p, []PersonalModel{result}); err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}

// Batch creation shares one authority check and transaction, including encrypted
// secrets, owner quotas, audit events and idempotency receipts for every model.
func (s *Service) CreatePersonalModels(ctx context.Context, p identity.Principal, inputs []PersonalModelInput, key, trace string) ([]PersonalModel, error) {
	if len(inputs) < 1 || len(inputs) > 20 || !personalCreateKey.MatchString(key) {
		return nil, invalid()
	}
	tx, err := s.begin(ctx, p, "personal-model:write", true)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	raw, _ := json.Marshal(inputs)
	batchHash := sha256.Sum256(raw)
	clear(raw)
	items := []PersonalModel{}
	var batchProvider, batchBase string
	seen := map[string]bool{}
	for i, input := range inputs {
		var provider, base, model string
		if input.Selection != nil {
			provider, base, model = input.Selection.Provider, input.Selection.APIBase, input.Selection.Model
		} else {
			if !uuid.MatchString(input.CatalogEntryID) {
				return nil, invalid()
			}
			if err = tx.QueryRow(ctx, `SELECT provider_id,api_base,model_id FROM model_catalog_entries WHERE org_id=$1 AND id=$2 AND (owner_id IS NULL OR owner_id=$3) AND enabled`, p.OrganizationID, input.CatalogEntryID, p.UserID).Scan(&provider, &base, &model); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return nil, invalid()
				}
				return nil, err
			}
		}
		if i == 0 {
			batchProvider, batchBase = provider, base
		} else if provider != batchProvider || base != batchBase || input.APIKey != inputs[0].APIKey || strings.TrimSpace(input.Name) != strings.TrimSpace(inputs[0].Name) {
			return nil, invalid()
		}
		target := provider + "|" + base + "|" + model
		if seen[target] {
			return nil, invalid()
		}
		seen[target] = true
		hash := sha256.Sum256([]byte(fmt.Sprintf("%s/%d", key, i)))
		item, err := s.createPersonalModelTx(ctx, tx, p, input, fmt.Sprintf("batch-%x", hash), trace, batchHash[:])
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err = adoptProviderAccountModelsTx(ctx, tx, p, items); err != nil {
		return nil, err
	}
	return items, tx.Commit(ctx)
}

func (s *Service) createPersonalModelTx(ctx context.Context, tx pgx.Tx, p identity.Principal, input PersonalModelInput, key, trace string, batchHash []byte) (PersonalModel, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Selection != nil && customProvider(input.Selection.Provider) && input.APIKey == "" {
		input.APIKey = "-"
	}
	if !personalCreateKey.MatchString(key) || !validSelection(input.Selection, input.CatalogEntryID) || !utf8.ValidString(input.Name) || utf8.RuneCountInString(input.Name) < 1 || utf8.RuneCountInString(input.Name) > 100 || strings.ContainsFunc(input.Name, unicode.IsControl) || len(input.APIKey) < 1 || len(input.APIKey) > 4096 {
		return PersonalModel{}, invalid()
	}
	for _, b := range []byte(input.APIKey) {
		if b < 0x21 || b > 0x7e {
			return PersonalModel{}, invalid()
		}
	}
	var err error
	// Serialize only this owner's creation/limit/receipt checks. Current identity
	// locks are held first, so governance cannot race an accepted private write.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "personal-model:"+p.OrganizationID+":"+p.UserID); err != nil {
		return PersonalModel{}, err
	}
	keyHash := sha256.Sum256([]byte(key))
	raw, _ := json.Marshal(input)
	requestBody := append(raw, batchHash...)
	requestHash := sha256.Sum256(requestBody)
	clear(requestBody)
	clear(raw)
	var existing string
	var previous []byte
	err = tx.QueryRow(ctx, `SELECT id::text,create_request_sha256 FROM user_model_connections WHERE org_id=$1 AND owner_id=$2 AND create_key_sha256=$3`, p.OrganizationID, p.UserID, keyHash[:]).Scan(&existing, &previous)
	if err == nil {
		if !bytes.Equal(previous, requestHash[:]) {
			return PersonalModel{}, webapi.Fail(409, "IDEMPOTENCY_CONFLICT", "此创建请求已用于其他参数")
		}
		return personalGet(ctx, tx, p, existing)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return PersonalModel{}, err
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM user_model_connections WHERE org_id=$1 AND owner_id=$2 AND archived_at IS NULL AND NOT provider_retired`, p.OrganizationID, p.UserID).Scan(&count); err != nil {
		return PersonalModel{}, err
	}
	if count >= 20 {
		return PersonalModel{}, webapi.Fail(429, "QUOTA_EXCEEDED", "本人模型连接已达20个上限")
	}
	entry, err := s.resolvePersonalEntry(ctx, tx, p, input.CatalogEntryID, input.Selection)
	if errors.Is(err, pgx.ErrNoRows) {
		return PersonalModel{}, invalid()
	}
	if err != nil {
		return PersonalModel{}, err
	}
	if s.policy == nil || len(s.policy.Gateways) == 0 {
		return PersonalModel{}, webapi.Fail(503, "MODEL_GATEWAY_NOT_CONFIGURED", "模型网关尚未配置，请联系管理员")
	}
	gateway := s.policy.Gateways[0]
	var prefix string
	var parameters []byte
	if err = tx.QueryRow(ctx, `SELECT adapter_prefix,parameters FROM model_providers WHERE id=$1`, entry.Provider).Scan(&prefix, &parameters); err != nil {
		return PersonalModel{}, err
	}
	id, secretID, operationID := webapi.ID(), webapi.ID(), webapi.ID()
	alias := "hc-" + strings.ReplaceAll(id, "-", "") + "-v1"
	frozen, _ := json.Marshal(struct {
		Catalog         ModelCatalogEntry `json:"catalog"`
		Prefix          string            `json:"prefix"`
		Parameters      json.RawMessage   `json:"parameters"`
		GatewayRevision string            `json:"gateway_revision"`
	}{entry, prefix, parameters, gateway.Revision})
	context := security.Context{Environment: s.environment, OrganizationID: p.OrganizationID, RecordID: fmt.Sprintf("%s/%s/%s/1", p.UserID, id, secretID), Purpose: "provider-key"}
	plaintext := []byte(input.APIKey)
	envelope, err := s.cipher.Encrypt(plaintext, context)
	clear(plaintext)
	if err != nil {
		return PersonalModel{}, err
	}
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO model_connection_registry(id,org_id,owner_id) VALUES($1,$2,$3)`, []any{id, p.OrganizationID, p.UserID}},
		{`INSERT INTO user_model_connections(id,org_id,owner_id,name,current_version,create_key_sha256,create_request_sha256) VALUES($1,$2,$3,$4,1,$5,$6)`, []any{id, p.OrganizationID, p.UserID, input.Name, keyHash[:], requestHash[:]}},
		{`INSERT INTO external_provider_secrets(id,org_id,owner_id,connection_id,bound_version,key_id,nonce,ciphertext) VALUES($1,$2,$3,$4,1,$5,$6,$7)`, []any{secretID, p.OrganizationID, p.UserID, id, envelope.KeyID, envelope.Nonce, envelope.Ciphertext}},
		{`INSERT INTO user_model_connection_versions(connection_id,org_id,owner_id,version,catalog_entry_id,catalog_version,provider_secret_id,gateway_endpoint,route_alias,manifest) VALUES($1,$2,$3,1,$4,$5,$6,$7,$8,$9)`, []any{id, p.OrganizationID, p.UserID, entry.ID, entry.Version, secretID, gateway.Endpoint, alias, frozen}},
		{`INSERT INTO model_sync_operations(id,org_id,owner_id,connection_id,connection_version,action,remote_model_id) VALUES($1,$2,$3,$4,1,'UPSERT',$5)`, []any{operationID, p.OrganizationID, p.UserID, id, webapi.ID()}},
	}
	for _, statement := range statements {
		if _, err = tx.Exec(ctx, statement.sql, statement.args...); err != nil {
			return PersonalModel{}, err
		}
	}
	if err = s.audit(ctx, tx, p, id, "PERSONAL_MODEL_CREATED", trace); err != nil {
		return PersonalModel{}, err
	}
	result, err := personalGet(ctx, tx, p, id)
	if err != nil {
		return PersonalModel{}, err
	}
	return result, nil
}
