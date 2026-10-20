package settings

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/devlocal"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/modelgateway"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func exerciseNativeGateway(t *testing.T, ctx context.Context, admin, api *pgxpool.Pool, cipher *security.SecretCipher, owner, other identity.Principal) {
	t.Helper()
	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if err = devlocal.GrantWorker(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err = devlocal.GrantModelGateway(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	pool := func(role string) *pgxpool.Pool {
		raw, err := os.ReadFile(filepath.Join(filepath.Dir(os.Getenv("HC_TEST_DB_DSN_FILE")), role+"-dsn"))
		if err != nil {
			t.Fatal("native test role missing")
		}
		u, _ := url.Parse(strings.TrimSpace(string(raw)))
		u.Path = "/hc_test_settings"
		p, err := pgxpool.New(ctx, u.String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		return p
	}
	nativePool, worker := pool("model_gateway"), pool("worker")
	master := []byte("synthetic-native-control-credential-0000000000")
	handler, err := modelgateway.NewServer(nativePool, cipher, "test", master, false)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	target, _ := url.Parse(server.URL)
	client := &http.Client{Transport: controlTestTransport{target}}
	policy, err := ParsePolicy([]byte(`{"schema_version":1,"gateways":[{"endpoint":"https://models.enterprise.example:14000","revision":"native-v1","addresses":["10.50.0.10/32"],"models":[],"probe_retries_disabled":true,"probe_fallbacks_disabled":true}]}`))
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(api, cipher, policy, "test", false)
	syncer := NewModelSync(NewService(worker, cipher, policy, "test", false), master)
	syncer.client = client
	request := func(method, path, key string, body any) (int, []byte) {
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		r, _ := http.NewRequestWithContext(ctx, method, server.URL+path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+key)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code, w.Body.Bytes()
	}
	type bound struct {
		id, key, alias, remote string
		principal              identity.Principal
	}
	var saved []bound
	for i, provider := range []string{"openai", "anthropic", "gemini", "qwen", "deepseek", "moonshot", "zai"} {
		p := owner
		if i%2 == 1 {
			p = other
		}
		var catalog string
		if err = admin.QueryRow(ctx, `SELECT id::text FROM model_catalog_entries WHERE org_id=$1 AND provider_id=$2 AND enabled ORDER BY created_at LIMIT 1`, p.OrganizationID, provider).Scan(&catalog); err != nil {
			t.Fatal(err)
		}
		item, err := service.CreatePersonalModel(ctx, p, PersonalModelInput{Name: "native " + provider, CatalogEntryID: catalog, APIKey: "synthetic-provider-" + provider}, "native-create-"+webapi.ID(), "")
		if err != nil {
			t.Fatal(err)
		}
		if ran, e := syncer.RunOnce(ctx); !ran || e != nil {
			t.Fatal("native route registration failed", provider, e)
		}
		item, err = service.GetPersonalModel(ctx, p, item.ID)
		if err != nil || item.SyncStatus != "SYNCED" || item.Validation.Status != "UNKNOWN" {
			t.Fatal("native sync fabricated inference evidence", provider, err)
		}
		var b bound
		var env security.Envelope
		var credential string
		b.id = item.ID
		b.principal = p
		if err = admin.QueryRow(ctx, `SELECT v.route_alias,op.remote_model_id,sec.id::text,sec.key_id,sec.nonce,sec.ciphertext FROM user_model_connection_versions v JOIN secrets sec ON sec.id=v.runtime_credential_ref JOIN model_sync_operations op ON op.connection_id=v.connection_id AND op.connection_version=v.version AND op.action='UPSERT' WHERE v.connection_id=$1`, item.ID).Scan(&b.alias, &b.remote, &credential, &env.KeyID, &env.Nonce, &env.Ciphertext); err != nil {
			t.Fatal(err)
		}
		raw, e := cipher.Decrypt(env, security.Context{Environment: "test", OrganizationID: p.OrganizationID, RecordID: credential, Purpose: "model-key"})
		if e != nil {
			t.Fatal(e)
		}
		b.key = string(raw)
		clear(raw)
		saved = append(saved, b)
		status, body := request("GET", "/v1/models", b.key, nil)
		if status != 200 || !bytes.Contains(body, []byte(b.alias)) || bytes.Contains(body, []byte("synthetic-provider")) {
			t.Fatal("native model scope incorrect", provider, status)
		}
		if status, _ = request("GET", "/key/list", b.key, nil); status != 403 {
			t.Fatal("virtual key obtained management access")
		}
		if status, _ = request("GET", "/v1/models", string(master), nil); status != 403 {
			t.Fatal("management key obtained inference scope")
		}
	}
	// One selected provider account exposes all of its approved models, while
	// unrelated accounts and owners remain outside the execution credential.
	accountKey := "synthetic-native-account-provider-key"
	selection := ModelSelection{Provider: "openai", APIBase: "https://api.openai.com/v1", Model: "fixture-fast", ContextWindow: 32768, MaxOutputTokens: 1024, ResponseModels: []string{"fixture-fast"}}
	pro := selection
	pro.Model = "fixture-pro"
	pro.ContextWindow = 128000
	pro.MaxOutputTokens = 8192
	pro.ResponseModels = []string{"fixture-pro"}
	account, err := service.CreateProviderAccount(ctx, owner, ProviderAccountInput{Name: "switchable account", Provider: "openai", APIBase: selection.APIBase, APIKey: &accountKey, Models: []ModelSelection{selection, pro}}, "native-account-"+webapi.ID(), "")
	if err != nil {
		t.Fatal("account fixture", err)
	}
	var accountBound []bound
	for range account.Models {
		if ran, e := syncer.RunOnce(ctx); !ran || e != nil {
			t.Fatal("account registration", e)
		}
	}
	for _, model := range account.Models {
		var b bound
		var envelope security.Envelope
		var credential string
		b.id = model.ID
		b.principal = owner
		if err = admin.QueryRow(ctx, `SELECT v.route_alias,r.id::text,sec.id::text,sec.key_id,sec.nonce,sec.ciphertext FROM user_model_connection_versions v JOIN secrets sec ON sec.id=v.runtime_credential_ref JOIN model_gateway_routes r ON r.connection_id=v.connection_id AND r.connection_version=v.version WHERE v.connection_id=$1 AND v.version=1`, b.id).Scan(&b.alias, &b.remote, &credential, &envelope.KeyID, &envelope.Nonce, &envelope.Ciphertext); err != nil {
			t.Fatal(err)
		}
		raw, e := cipher.Decrypt(envelope, security.Context{Environment: "test", OrganizationID: owner.OrganizationID, RecordID: credential, Purpose: "model-key"})
		if e != nil {
			t.Fatal(e)
		}
		b.key = string(raw)
		clear(raw)
		accountBound = append(accountBound, b)
		saved = append(saved, b)
		if _, err = admin.Exec(ctx, `UPDATE model_validations SET status='HEALTHY',checked_at=clock_timestamp(),expires_at=clock_timestamp()+interval '1 hour' WHERE config_id=$1`, b.id); err != nil {
			t.Fatal(err)
		}
		if _, err = admin.Exec(ctx, `UPDATE model_gateway_checks SET status='HEALTHY',reason_code='',checked_at=clock_timestamp(),expires_at=clock_timestamp()+interval '59 seconds' WHERE connection_id=$1`, b.id); err != nil {
			t.Fatal(err)
		}
	}
	anchor, alternate := accountBound[0], accountBound[1]
	listAccount := func(want int) {
		t.Helper()
		status, body := request("GET", "/v1/models?scope=provider-account", anchor.key, nil)
		var directory struct {
			Data []struct {
				ID, Name        string
				MaxOutputTokens int64 `json:"max_output_tokens"`
			}
		}
		if status != 200 || json.Unmarshal(body, &directory) != nil || len(directory.Data) != want || bytes.Contains(body, []byte("synthetic-")) {
			t.Fatal("account directory scope", status, string(body))
		}
		for _, model := range directory.Data {
			if model.ID != anchor.alias && model.ID != alternate.alias {
				t.Fatal("another account leaked")
			}
			if model.Name == "" || model.MaxOutputTokens < 128 {
				t.Fatal("capabilities missing")
			}
		}
	}
	listAccount(2)
	payload := map[string]any{"model": alternate.alias, "stream": true, "max_tokens": 128, "messages": []any{map[string]any{"role": "user", "content": "hello"}}}
	if _, err = admin.Exec(ctx, `UPDATE model_gateway_keys SET requests=60 WHERE route_id=$1`, anchor.remote); err != nil {
		t.Fatal(err)
	}
	if status, _ := request("POST", "/v1/messages?scope=provider-account", anchor.key, payload); status != 429 {
		t.Fatal("approved alternate not authorized or shared budget bypassed", status)
	}
	if status, _ := request("POST", "/v1/messages", anchor.key, payload); status != 400 {
		t.Fatal("probe contract broadened", status)
	}
	payload["max_tokens"] = 8193
	if status, _ := request("POST", "/v1/messages?scope=provider-account", anchor.key, payload); status != 400 {
		t.Fatal("selected model output limit bypassed", status)
	}
	payload["max_tokens"] = 128
	for _, foreign := range []bound{saved[0], saved[1]} {
		payload["model"] = foreign.alias
		if status, _ := request("POST", "/v1/messages?scope=provider-account", anchor.key, payload); status != 403 {
			t.Fatal("cross account or owner inference", status)
		}
	}
	for _, change := range []struct{ deny, restore string }{
		{`UPDATE user_model_connections SET enabled=false WHERE id=$1`, `UPDATE user_model_connections SET enabled=true WHERE id=$1`},
		{`UPDATE model_validations SET expires_at=clock_timestamp()-interval '1 second',checked_at=clock_timestamp()-interval '1 hour' WHERE config_id=$1`, `UPDATE model_validations SET checked_at=clock_timestamp(),expires_at=clock_timestamp()+interval '1 hour' WHERE config_id=$1`},
		{`UPDATE model_gateway_checks SET status='UNHEALTHY' WHERE connection_id=$1`, `UPDATE model_gateway_checks SET status='HEALTHY' WHERE connection_id=$1`},
	} {
		if _, err = admin.Exec(ctx, change.deny, alternate.id); err != nil {
			t.Fatal(err)
		}
		listAccount(1)
		payload["model"] = alternate.alias
		if status, _ := request("POST", "/v1/messages?scope=provider-account", anchor.key, payload); status != 403 {
			t.Fatal("unavailable model retained inference", status)
		}
		if _, err = admin.Exec(ctx, change.restore, alternate.id); err != nil {
			t.Fatal(err)
		}
		listAccount(2)
	}
	if _, err = admin.Exec(ctx, `UPDATE model_gateway_keys SET requests=0 WHERE route_id=$1`, anchor.remote); err != nil {
		t.Fatal(err)
	}
	// Authorization is re-read on every request, including approved-looking drift.
	first := saved[0]
	for _, change := range []struct {
		name, deny, restore string
		arg                 any
	}{
		{"owner disabled", `UPDATE users SET status='DISABLED' WHERE id=$1`, `UPDATE users SET status='ACTIVE' WHERE id=$1`, first.principal.UserID},
		{"catalog disabled", `UPDATE model_catalog_entries SET enabled=false WHERE id=(SELECT catalog_entry_id FROM user_model_connection_versions WHERE connection_id=$1 AND version=1)`, `UPDATE model_catalog_entries SET enabled=true WHERE id=(SELECT catalog_entry_id FROM user_model_connection_versions WHERE connection_id=$1 AND version=1)`, first.id},
		{"route drift", `UPDATE model_gateway_routes SET parameters=jsonb_set(parameters,'{model}','"openai/another-model"') WHERE id=$1`, `UPDATE model_gateway_routes r SET parameters=jsonb_set(r.parameters,'{model}',to_jsonb(v.manifest->>'prefix' || '/' || (v.manifest->'catalog'->>'model'))) FROM user_model_connection_versions v WHERE r.id=$1 AND v.connection_id=r.connection_id AND v.version=r.connection_version`, first.remote},
	} {
		if _, err = admin.Exec(ctx, change.deny, change.arg); err != nil {
			t.Fatal(err)
		}
		status, _ := request("GET", "/v1/models", first.key, nil)
		if _, err = admin.Exec(ctx, change.restore, change.arg); err != nil {
			t.Fatal(err)
		}
		if status != 401 {
			t.Fatal("stale authorization admitted", change.name, status)
		}
		if status, _ = request("GET", "/v1/models", first.key, nil); status != 200 {
			t.Fatal("authorization fixture did not recover", change.name, status)
		}
	}
	for _, b := range saved {
		var secretRead bool
		if err = nativePool.QueryRow(ctx, `SELECT has_table_privilege(current_user,'identity_accounts','SELECT') OR has_table_privilege(current_user,'instances','UPDATE')`).Scan(&secretRead); err != nil || secretRead {
			t.Fatal("native role obtained identity or instance permissions", err)
		}
		payload := map[string]any{"model": "another-owner-model", "stream": true, "max_tokens": 128, "messages": []any{map[string]any{"role": "user", "content": "hello"}}}
		if status, _ := request("POST", "/v1/messages", b.key, payload); status != 400 {
			t.Fatal("cross-route credential admitted", status)
		}
		if _, err = admin.Exec(ctx, `UPDATE model_gateway_keys SET requests=60 WHERE route_id=$1`, b.remote); err != nil {
			t.Fatal(err)
		}
		payload["model"] = b.alias
		if status, _ := request("POST", "/v1/messages", b.key, payload); status != 429 {
			t.Fatal("native request limit bypassed", status)
		}
		if _, err = service.RevokePersonalModel(ctx, b.principal, b.id, 1, ""); err != nil {
			t.Fatal(err)
		}
		if status, _ := request("GET", "/v1/models", b.key, nil); status != 401 {
			t.Fatal("local revocation did not immediately reject copied key", status)
		}
		if ran, e := syncer.RunOnce(ctx); !ran || e != nil {
			t.Fatal("native revocation failed", e)
		}
		if _, err = service.ArchivePersonalModel(ctx, b.principal, b.id, 1, ""); err != nil {
			t.Fatal(err)
		}
	}
}
