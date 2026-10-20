package settings

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/modelgateway"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	"slices"
	"strings"
	"time"
)

type ModelSelection struct {
	Provider        string   `json:"provider"`
	APIBase         string   `json:"api_base"`
	Model           string   `json:"model"`
	ResponseModels  []string `json:"response_models,omitempty"`
	ContextWindow   int64    `json:"context_window,omitempty"`
	MaxOutputTokens int64    `json:"max_output_tokens,omitempty"`
}

func customProvider(provider string) bool {
	return provider == "custom-openai" || provider == "custom-openai-responses" || provider == "custom-anthropic"
}
func validSelection(selection *ModelSelection, catalogID string) bool {
	if selection == nil {
		return uuid.MatchString(catalogID)
	}
	if catalogID != "" || !modelPattern.MatchString(selection.Model) || len(selection.ResponseModels) > 16 {
		return false
	}
	seen := map[string]bool{}
	for _, model := range selection.ResponseModels {
		if !modelPattern.MatchString(model) || seen[model] {
			return false
		}
		seen[model] = true
	}
	return (selection.ContextWindow == 0 || (selection.ContextWindow >= 1024 && selection.ContextWindow <= 10000000)) && (selection.MaxOutputTokens == 0 || (selection.MaxOutputTokens >= 128 && selection.MaxOutputTokens <= 1000000))
}
func (s *Service) selectionProvider(ctx context.Context, tx pgx.Tx, provider, base string) (string, error) {
	var prefix string
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT adapter_prefix,api_bases FROM model_providers WHERE id=$1 AND enabled FOR SHARE`, provider).Scan(&prefix, &raw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", invalid()
		}
		return "", err
	}
	var bases []string
	if json.Unmarshal(raw, &bases) != nil {
		return "", invalid()
	}
	customBases := []string{}
	if s.policy != nil {
		customBases = s.policy.CustomAPIBases
	}
	if (!customProvider(provider) && !slices.Contains(bases, base)) || !modelgateway.AllowsEndpoint(prefix, base, customBases) {
		return "", webapi.Fail(422, "MODEL_ENDPOINT_NOT_ALLOWED", "模型地址未配置，请使用提供商官方地址或联系管理员配置自定义服务地址")
	}
	return prefix, nil
}
func (s *Service) resolvePersonalEntry(ctx context.Context, tx pgx.Tx, p identity.Principal, id string, selection *ModelSelection) (ModelCatalogEntry, error) {
	if selection == nil {
		entry, err := scanCatalog(tx.QueryRow(ctx, catalogSelect+` WHERE e.org_id=$1 AND e.id=$2 AND (e.owner_id IS NULL OR e.owner_id=$3) AND e.enabled AND p.enabled FOR SHARE OF e,p`, p.OrganizationID, id, p.UserID))
		if errors.Is(err, pgx.ErrNoRows) {
			return entry, invalid()
		}
		return entry, err
	}
	if _, err := s.selectionProvider(ctx, tx, selection.Provider, selection.APIBase); err != nil {
		return ModelCatalogEntry{}, err
	}
	// Upserts by model root are owner-scoped. Existing metadata is never rewritten.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "personal-catalog:"+p.OrganizationID+":"+p.UserID); err != nil {
		return ModelCatalogEntry{}, err
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM model_catalog_entries WHERE org_id=$1 AND owner_id=$2`, p.OrganizationID, p.UserID).Scan(&count); err != nil {
		return ModelCatalogEntry{}, err
	}
	responses := selection.ResponseModels
	if len(responses) == 0 {
		responses = []string{selection.Model}
	}
	contextWindow, output := selection.ContextWindow, selection.MaxOutputTokens
	if contextWindow == 0 {
		contextWindow = 131072
	}
	if output == 0 {
		output = 16384
	}
	if output > contextWindow {
		return ModelCatalogEntry{}, invalid()
	}
	raw, _ := json.Marshal(responses)
	existing, existingErr := scanCatalog(tx.QueryRow(ctx, catalogSelect+` WHERE e.org_id=$1 AND e.owner_id=$2 AND e.provider_id=$3 AND e.model_id=$4 AND e.api_base=$5 AND e.response_models=$6 AND e.context_window=$7 AND e.max_output_tokens=$8 AND e.enabled AND p.enabled LIMIT 1 FOR SHARE OF e,p`, p.OrganizationID, p.UserID, selection.Provider, selection.Model, selection.APIBase, raw, contextWindow, output))
	if existingErr == nil {
		return existing, nil
	}
	if !errors.Is(existingErr, pgx.ErrNoRows) {
		return ModelCatalogEntry{}, existingErr
	}
	if count >= 2000 {
		return ModelCatalogEntry{}, webapi.Fail(429, "QUOTA_EXCEEDED", "个人模型目录已达上限")
	}
	name := selection.Model
	if len(name) > 100 {
		name = name[:100]
	}
	id = webapi.ID()
	_, err := tx.Exec(ctx, `INSERT INTO model_catalog_entries(id,org_id,owner_id,provider_id,model_id,name,api_base,response_models,context_window,max_output_tokens,streaming,tool_use) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,true,true)`, id, p.OrganizationID, p.UserID, selection.Provider, selection.Model, name, selection.APIBase, raw, contextWindow, output)
	if err != nil {
		return ModelCatalogEntry{}, err
	}
	entry, err := scanCatalog(tx.QueryRow(ctx, catalogSelect+` WHERE e.org_id=$1 AND e.owner_id=$2 AND e.id=$3`, p.OrganizationID, p.UserID, id))
	return entry, err
}

type ModelDiscoveryInput struct {
	APIBase string `json:"api_base"`
	APIKey  string `json:"api_key"`
}

func (s *Service) DiscoverProviderModels(ctx context.Context, p identity.Principal, provider string, input ModelDiscoveryInput) ([]modelgateway.DiscoveredModel, error) {
	if len(input.APIKey) > 4096 || (!customProvider(provider) && input.APIKey == "") || strings.ContainsAny(input.APIKey, " \t\r\n") {
		return nil, invalid()
	}
	for _, b := range []byte(input.APIKey) {
		if b < 0x21 || b > 0x7e {
			return nil, invalid()
		}
	}
	tx, err := s.begin(ctx, p, "personal-model:write", false)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	prefix, err := s.selectionProvider(ctx, tx, provider, input.APIBase)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	customBases := []string{}
	if s.policy != nil {
		customBases = s.policy.CustomAPIBases
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	items, err := modelgateway.DiscoverModels(ctx, prefix, input.APIBase, input.APIKey, customBases)
	if err != nil {
		return nil, webapi.Fail(502, "MODEL_DISCOVERY_FAILED", "无法同步模型列表，请检查服务地址、密钥及模型列表接口")
	}
	return items, nil
}
