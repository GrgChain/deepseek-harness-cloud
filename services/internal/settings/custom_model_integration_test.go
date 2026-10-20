package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func exerciseCustomSelections(t *testing.T, ctx context.Context, admin, api *pgxpool.Pool, cipher *security.SecretCipher, owner, other identity.Principal) {
	t.Helper()
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.Header.Get("Authorization") != "" || r.Header.Get("x-api-key") != "" {
			t.Error("no-auth model discovery leaked authentication or initiated inference")
		}
		fmt.Fprint(w, `{"data":[{"id":"local-current"},{"id":"local-next"}],"has_more":false}`)
	}))
	defer upstream.Close()
	policy, err := ParsePolicy([]byte(`{"schema_version":1,"gateways":[{"endpoint":"https://models.enterprise.example:14000","revision":"native-v1","addresses":["10.50.0.10/32"],"models":[],"probe_retries_disabled":true,"probe_fallbacks_disabled":true}]}`))
	if err != nil {
		t.Fatal(err)
	}
	policy.CustomAPIBases = []string{upstream.URL + "/v1"}
	service := NewService(api, cipher, policy, "test", false)
	for _, provider := range []string{"custom-openai", "custom-openai-responses", "custom-anthropic"} {
		selection := &ModelSelection{Provider: provider, APIBase: policy.CustomAPIBases[0], Model: "fixture-" + webapi.ID(), MaxOutputTokens: 4096}
		receipt := "custom-create-" + webapi.ID()
		input := PersonalModelInput{Name: "Personal local model", Selection: selection}
		model, err := service.CreatePersonalModel(ctx, owner, input, receipt, "")
		if err != nil {
			t.Fatal("custom model not saved", err)
		}
		defer admin.Exec(context.Background(), `UPDATE user_model_connections SET enabled=false,archived_at=clock_timestamp() WHERE id=$1`, model.ID)
		replay, err := service.CreatePersonalModel(ctx, owner, input, receipt, "")
		if err != nil || replay.ID != model.ID {
			t.Fatal("custom create receipt not replayed", err)
		}
		items, err := service.ModelCatalog(ctx, owner)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, item := range items {
			found = found || item.ID == model.CatalogEntryID
		}
		if !found {
			t.Fatal("own private catalog missing")
		}
		items, err = service.ModelCatalog(ctx, other)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range items {
			if item.ID == model.CatalogEntryID {
				t.Fatal("private endpoint disclosed to another owner")
			}
		}
		_, err = service.CreatePersonalModel(ctx, other, PersonalModelInput{Name: "steal catalog", CatalogEntryID: model.CatalogEntryID, APIKey: "synthetic"}, "cross-owner-"+webapi.ID(), "")
		if problemCode(err) != "CONFIGURATION_INVALID" {
			t.Fatal("cross-owner catalog accepted", err)
		}
		_, err = service.SaveCatalog(ctx, other, model.CatalogEntryID, CatalogInput{Provider: provider, Model: selection.Model, Name: "overwrite private", APIBase: selection.APIBase, ResponseModels: []string{selection.Model}, ContextWindow: 32768, MaxOutputTokens: 4096, Streaming: true, ToolUse: true, Enabled: true, ExpectedVersion: 1}, "")
		if problemCode(err) != "NOT_FOUND" {
			t.Fatal("administrator mutated private catalog", err)
		}
		if _, err = admin.Exec(ctx, `UPDATE model_sync_operations SET status='FAILED' WHERE connection_id=$1`, model.ID); err != nil {
			t.Fatal(err)
		}
		revised := *selection
		revised.MaxOutputTokens = 8192
		updated, err := service.UpdatePersonalModel(ctx, owner, model.ID, PersonalModelUpdate{Name: "Updated local model", Selection: &revised, ExpectedVersion: 1}, "")
		if err != nil || updated.Version != 2 || updated.CatalogEntryID == model.CatalogEntryID {
			t.Fatal("new custom capabilities did not freeze a separate catalog", err)
		}
		var unchanged bool
		if err = admin.QueryRow(ctx, `SELECT (manifest->'catalog'->>'max_output_tokens')::bigint=4096 FROM user_model_connection_versions WHERE connection_id=$1 AND version=1`, model.ID).Scan(&unchanged); err != nil || !unchanged {
			t.Fatal("old frozen version changed", err)
		}
		discovered, err := service.DiscoverProviderModels(ctx, owner, provider, ModelDiscoveryInput{APIBase: selection.APIBase})
		if err != nil || len(discovered) != 2 {
			t.Fatal("no-auth custom discovery failed", err)
		}
		selection.APIBase = upstream.URL + "/unconfigured"
		before := calls
		if _, err = service.DiscoverProviderModels(ctx, owner, provider, ModelDiscoveryInput{APIBase: selection.APIBase}); problemCode(err) != "MODEL_ENDPOINT_NOT_ALLOWED" || calls != before {
			t.Fatal("unapproved root was contacted", err)
		}
		if _, err = service.CreatePersonalModel(ctx, owner, PersonalModelInput{Name: "unapproved", Selection: selection}, "unapproved-"+webapi.ID(), ""); problemCode(err) != "MODEL_ENDPOINT_NOT_ALLOWED" {
			t.Fatal("unapproved custom model saved", err)
		}
	}
	// A failed batch cannot leave credentials, routes, audit records or partial models.
	batch := []PersonalModelInput{{Name: "batch fixture", Selection: &ModelSelection{Provider: "custom-openai", APIBase: policy.CustomAPIBases[0], Model: "batch-" + webapi.ID()}}, {Name: "batch fixture", Selection: &ModelSelection{Provider: "custom-openai", APIBase: policy.CustomAPIBases[0], Model: "batch-" + webapi.ID()}}}
	broken := append([]PersonalModelInput(nil), batch...)
	broken[1].Name = ""
	receipt := "batch-receipt-" + webapi.ID()
	var before, after int
	if err = admin.QueryRow(ctx, `SELECT count(*) FROM user_model_connections WHERE org_id=$1 AND owner_id=$2`, owner.OrganizationID, owner.UserID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err = service.CreatePersonalModels(ctx, owner, broken, receipt, ""); problemCode(err) != "CONFIGURATION_INVALID" {
		t.Fatal("invalid batch accepted", err)
	}
	if err = admin.QueryRow(ctx, `SELECT count(*) FROM user_model_connections WHERE org_id=$1 AND owner_id=$2`, owner.OrganizationID, owner.UserID).Scan(&after); err != nil || before != after {
		t.Fatal("batch left partial connections", err)
	}
	saved, err := service.CreatePersonalModels(ctx, owner, batch, receipt, "")
	if err != nil || len(saved) != 2 {
		t.Fatal("multi-model batch failed", err)
	}
	for _, model := range saved {
		defer admin.Exec(context.Background(), `UPDATE user_model_connections SET enabled=false,archived_at=clock_timestamp() WHERE id=$1`, model.ID)
	}
	replayed, err := service.CreatePersonalModels(ctx, owner, batch, receipt, "")
	if err != nil || len(replayed) != 2 || saved[0].ID != replayed[0].ID || saved[1].ID != replayed[1].ID {
		t.Fatal("batch receipt did not replay", err)
	}
	if _, err = service.CreatePersonalModels(ctx, owner, batch[:1], receipt, ""); problemCode(err) != "IDEMPOTENCY_CONFLICT" {
		t.Fatal("altered batch reused receipt", err)
	}
	// A newly available official model can be frozen privately without changing organization metadata.
	selection := &ModelSelection{Provider: "deepseek", APIBase: "https://api.deepseek.com", Model: "fixture-official-" + webapi.ID()}
	created, err := service.CreatePersonalModel(ctx, owner, PersonalModelInput{Name: "Discovered model", Selection: selection, APIKey: "synthetic-official-key"}, "discovered-"+webapi.ID(), "")
	if err != nil {
		t.Fatal("official selection cannot be saved", err)
	}
	defer admin.Exec(context.Background(), `UPDATE user_model_connections SET enabled=false,archived_at=clock_timestamp() WHERE id=$1`, created.ID)
	var frozen []byte
	if err = admin.QueryRow(ctx, `SELECT manifest FROM user_model_connection_versions WHERE connection_id=$1 AND version=1`, created.ID).Scan(&frozen); err != nil || strings.Contains(string(frozen), "synthetic-official-key") {
		t.Fatal("secret in frozen metadata", err)
	}
	var manifest personalManifest
	if json.Unmarshal(frozen, &manifest) != nil || manifest.Catalog.Model != selection.Model {
		t.Fatal("wrong discovered model frozen")
	}
}
