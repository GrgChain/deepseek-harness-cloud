package settings

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func exercisePersonalKeyReuse(t *testing.T, ctx context.Context, admin, api *pgxpool.Pool, cipher *security.SecretCipher, policy *Policy, owner, manager identity.Principal) {
	t.Helper()
	service := NewService(api, cipher, policy, "test", false)
	handler := NewHandler(service, nil, nil)
	var catalogID, otherProviderID string
	if err := admin.QueryRow(ctx, `SELECT id::text FROM model_catalog_entries WHERE org_id=$1 AND provider_id='moonshot' AND enabled ORDER BY created_at LIMIT 1`, owner.OrganizationID).Scan(&catalogID); err != nil {
		t.Fatal(err)
	}
	if err := admin.QueryRow(ctx, `SELECT id::text FROM model_catalog_entries WHERE org_id=$1 AND provider_id='zai' LIMIT 1`, owner.OrganizationID).Scan(&otherProviderID); err != nil {
		t.Fatal(err)
	}
	region, err := service.SaveCatalog(ctx, manager, "", CatalogInput{Provider: "moonshot", Model: "fixture-" + webapi.ID(), Name: "Other account region", APIBase: "https://api.moonshot.cn/v1", ResponseModels: []string{"kimi-k2.5"}, ContextWindow: 32768, MaxOutputTokens: 4096, Streaming: true, ToolUse: true, Enabled: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), `DELETE FROM model_catalog_entries WHERE id=$1`, region.ID)
	connection, err := service.CreatePersonalModel(ctx, owner, PersonalModelInput{Name: "Key reuse", CatalogEntryID: catalogID, APIKey: "synthetic-key-reuse"}, "reuse-"+webapi.ID(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), `UPDATE user_model_connections SET enabled=false,archived_at=clock_timestamp() WHERE id=$1`, connection.ID)
	settle := func() {
		t.Helper()
		// A terminal sync failure permits editing, without any remote inference.
		if _, err := admin.Exec(ctx, `UPDATE model_sync_operations SET status='FAILED',lease_expires_at=NULL WHERE connection_id=$1`, connection.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := admin.Exec(ctx, `UPDATE user_model_connection_versions SET sync_status='FAILED' WHERE connection_id=$1`, connection.ID); err != nil {
			t.Fatal(err)
		}
	}
	settle()
	update := func(entry string, version int, key any, includeKey bool) *httptest.ResponseRecorder {
		t.Helper()
		body := map[string]any{"name": "Renamed connection", "catalog_entry_id": entry, "expected_version": version}
		if includeKey {
			body["api_key"] = key
		}
		raw, _ := json.Marshal(body)
		request := httptest.NewRequest("PUT", "/v1/my-models/"+connection.ID, bytes.NewReader(raw))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeAuthenticated(response, request, owner)
		return response
	}
	for _, test := range []struct {
		name, catalog string
		key           any
		includeKey    bool
		status        int
	}{
		{"empty key", catalogID, "", true, 422},
		{"null key", catalogID, nil, true, 422},
		{"different provider", otherProviderID, nil, false, 409},
		{"different account region", region.ID, nil, false, 409},
	} {
		if response := update(test.catalog, 1, test.key, test.includeKey); response.Code != test.status {
			t.Fatalf("%s accepted: status=%d", test.name, response.Code)
		}
	}
	otherRequest := httptest.NewRequest("PUT", "/v1/my-models/"+connection.ID, bytes.NewBufferString(`{"name":"Other owner","catalog_entry_id":"`+catalogID+`","expected_version":1}`))
	otherRequest.Header.Set("Content-Type", "application/json")
	otherResponse := httptest.NewRecorder()
	handler.ServeAuthenticated(otherResponse, otherRequest, manager)
	if otherResponse.Code != 404 {
		t.Fatal("administrator edited another owner's connection", otherResponse.Code)
	}
	response := update(catalogID, 1, nil, false)
	if response.Code != 202 {
		t.Fatalf("omitted key cannot reuse credential: status=%d", response.Code)
	}
	var shared bool
	var secrets int
	if err := admin.QueryRow(ctx, `SELECT a.provider_secret_id=b.provider_secret_id FROM user_model_connection_versions a JOIN user_model_connection_versions b USING(connection_id) WHERE a.connection_id=$1 AND a.version=1 AND b.version=2`, connection.ID).Scan(&shared); err != nil || !shared {
		t.Fatal("reuse did not retain the encrypted credential reference", err)
	}
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM external_provider_secrets WHERE connection_id=$1`, connection.ID).Scan(&secrets); err != nil || secrets != 1 {
		t.Fatal("reuse duplicated provider secrets", err)
	}
	settle()
	if _, err := service.RevokePersonalModel(ctx, owner, connection.ID, 2, ""); err != nil {
		t.Fatal(err)
	}
	// Confirming the latest version cannot hide an older remote credential.
	if _, err := admin.Exec(ctx, `UPDATE user_model_connection_versions SET sync_status='REVOKED' WHERE connection_id=$1 AND version=2`, connection.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `UPDATE model_sync_operations SET status='SUCCEEDED' WHERE connection_id=$1 AND connection_version=2 AND action='REVOKE'`, connection.ID); err != nil {
		t.Fatal(err)
	}
	partial, err := service.GetPersonalModel(ctx, owner, connection.ID)
	if err != nil || partial.SyncStatus != "REVOKING" || partial.OperationStatus != "PENDING" {
		t.Fatal("latest-version confirmation hid unfinished remote revocation", err, partial.SyncStatus)
	}
	if _, err := service.ArchivePersonalModel(ctx, owner, connection.ID, 2, ""); problemCode(err) != "STATE_CONFLICT" {
		t.Fatal("connection archived before all credentials were revoked", err)
	}
	if _, err := admin.Exec(ctx, `UPDATE user_model_connection_versions SET sync_status='REVOKED' WHERE connection_id=$1`, connection.ID); err != nil {
		t.Fatal(err)
	}
	complete, err := service.GetPersonalModel(ctx, owner, connection.ID)
	if err != nil || complete.SyncStatus != "REVOKED" {
		t.Fatal("complete revocation not visible", err)
	}
	settle()
	// Even a stale enable flag must not restore a revoked encrypted credential.
	if _, err := admin.Exec(ctx, `UPDATE user_model_connections SET enabled=true WHERE id=$1`, connection.ID); err != nil {
		t.Fatal(err)
	}
	if response := update(catalogID, 2, nil, false); response.Code != 422 {
		t.Fatal("revoked credential was reused", response.Code)
	}
	if response := update(catalogID, 2, "synthetic-replacement", true); response.Code != 202 {
		t.Fatal("replacement credential cannot create a new version", response.Code)
	}
	settle()
}
