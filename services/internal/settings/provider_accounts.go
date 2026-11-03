package settings

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
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
