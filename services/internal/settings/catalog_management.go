package settings

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type CatalogInput struct {
	Provider        string   `json:"provider"`
	Model           string   `json:"model"`
	Name            string   `json:"name"`
	APIBase         string   `json:"api_base"`
	ResponseModels  []string `json:"response_models"`
	ContextWindow   int64    `json:"context_window"`
	MaxOutputTokens int64    `json:"max_output_tokens"`
	Streaming       bool     `json:"streaming"`
	ToolUse         bool     `json:"tool_use"`
	Enabled         bool     `json:"enabled"`
	ExpectedVersion int64    `json:"expected_version"`
}
type ManagedCatalogEntry struct {
	ModelCatalogEntry
	Enabled bool `json:"enabled"`
}

func (s *Service) ManagedCatalog(ctx context.Context, p identity.Principal) ([]ManagedCatalogEntry, error) {
	tx, err := s.begin(ctx, p, "models:catalog:manage", false)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	rows, err := tx.Query(ctx, strings.Replace(catalogSelect, " FROM model_catalog_entries", ",e.enabled FROM model_catalog_entries", 1)+` WHERE e.org_id=$1 AND e.owner_id IS NULL ORDER BY e.name,e.id LIMIT 100`, p.OrganizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ManagedCatalogEntry{}
	for rows.Next() {
		var item ManagedCatalogEntry
		var raw []byte
		err = rows.Scan(&item.ID, &item.Provider, &item.Model, &item.Name, &item.APIBase, &item.Version, &raw, &item.ContextWindow, &item.MaxOutputTokens, &item.Streaming, &item.ToolUse, &item.Enabled)
		if err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &item.ResponseModels); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
func (s *Service) SaveCatalog(ctx context.Context, p identity.Principal, id string, input CatalogInput, trace string) (ManagedCatalogEntry, error) {
	input.Name = strings.TrimSpace(input.Name)
	if (id != "" && !uuid.MatchString(id)) || input.ExpectedVersion < 0 || !utf8.ValidString(input.Name) || utf8.RuneCountInString(input.Name) < 1 || utf8.RuneCountInString(input.Name) > 100 || strings.ContainsFunc(input.Name, unicode.IsControl) || !modelPattern.MatchString(input.Model) || len(input.ResponseModels) < 1 || len(input.ResponseModels) > 16 || input.ContextWindow < 1024 || input.ContextWindow > 10000000 || input.MaxOutputTokens < 128 || input.MaxOutputTokens > 1000000 || input.MaxOutputTokens > input.ContextWindow {
		return ManagedCatalogEntry{}, invalid()
	}
	seen := map[string]bool{}
	for _, model := range input.ResponseModels {
		if !modelPattern.MatchString(model) || seen[model] {
			return ManagedCatalogEntry{}, invalid()
		}
		seen[model] = true
	}
	tx, err := s.begin(ctx, p, "models:catalog:manage", true)
	if err != nil {
		return ManagedCatalogEntry{}, err
	}
	defer tx.Rollback(context.Background())
	if _, err = s.selectionProvider(ctx, tx, input.Provider, input.APIBase); err != nil {
		return ManagedCatalogEntry{}, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "model-catalog:"+p.OrganizationID); err != nil {
		return ManagedCatalogEntry{}, err
	}
	var version int64
	if id == "" {
		if input.ExpectedVersion != 0 {
			return ManagedCatalogEntry{}, versionConflict()
		}
		var count int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM model_catalog_entries WHERE org_id=$1 AND owner_id IS NULL`, p.OrganizationID).Scan(&count); err != nil {
			return ManagedCatalogEntry{}, err
		}
		if count >= 100 {
			return ManagedCatalogEntry{}, webapi.Fail(429, "QUOTA_EXCEEDED", "目录已达100项上限")
		}
		id = webapi.ID()
	} else {
		err = tx.QueryRow(ctx, `SELECT version FROM model_catalog_entries WHERE org_id=$1 AND id=$2 AND owner_id IS NULL FOR UPDATE`, p.OrganizationID, id).Scan(&version)
		if errors.Is(err, pgx.ErrNoRows) {
			return ManagedCatalogEntry{}, notFound()
		}
		if err != nil {
			return ManagedCatalogEntry{}, err
		}
		if version != input.ExpectedVersion {
			return ManagedCatalogEntry{}, versionConflict()
		}
	}
	models, _ := json.Marshal(input.ResponseModels)
	version++
	_, err = tx.Exec(ctx, `INSERT INTO model_catalog_entries(id,org_id,provider_id,model_id,name,api_base,response_models,context_window,max_output_tokens,streaming,tool_use,enabled,version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) ON CONFLICT(id) DO UPDATE SET provider_id=EXCLUDED.provider_id,model_id=EXCLUDED.model_id,name=EXCLUDED.name,api_base=EXCLUDED.api_base,response_models=EXCLUDED.response_models,context_window=EXCLUDED.context_window,max_output_tokens=EXCLUDED.max_output_tokens,streaming=EXCLUDED.streaming,tool_use=EXCLUDED.tool_use,enabled=EXCLUDED.enabled,version=EXCLUDED.version,updated_at=clock_timestamp()`, id, p.OrganizationID, input.Provider, input.Model, input.Name, input.APIBase, models, input.ContextWindow, input.MaxOutputTokens, input.Streaming, input.ToolUse, input.Enabled, version)
	var postgres *pgconn.PgError
	if errors.As(err, &postgres) && postgres.Code == "23505" {
		return ManagedCatalogEntry{}, webapi.Fail(409, "MODEL_CATALOG_CONFLICT", "此提供商、模型和地区已存在")
	}
	if err != nil {
		return ManagedCatalogEntry{}, err
	}
	if err = s.audit(ctx, tx, p, id, "MODEL_CATALOG_SAVED", trace); err != nil {
		return ManagedCatalogEntry{}, err
	}
	item := ManagedCatalogEntry{ModelCatalogEntry: ModelCatalogEntry{ID: id, Provider: input.Provider, Model: input.Model, Name: input.Name, APIBase: input.APIBase, Version: version, ResponseModels: input.ResponseModels, ContextWindow: input.ContextWindow, MaxOutputTokens: input.MaxOutputTokens, Streaming: input.Streaming, ToolUse: input.ToolUse}, Enabled: input.Enabled}
	return item, tx.Commit(ctx)
}
