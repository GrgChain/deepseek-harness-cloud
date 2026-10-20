package settings

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
)

type controlTestTransport struct{ target *url.URL }

func (t controlTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	copy := r.Clone(r.Context())
	u := *r.URL
	u.Scheme = t.target.Scheme
	u.Host = t.target.Host
	copy.URL = &u
	return http.DefaultTransport.RoundTrip(copy)
}
func exercisePersonalSync(t *testing.T, ctx context.Context, admin, api *pgxpool.Pool, cipher *security.SecretCipher, policy *Policy, owner, other identity.Principal, id, providerAccountKey string) {
	t.Helper()
	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if err = devlocal.GrantWorker(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(os.Getenv("HC_TEST_DB_DSN_FILE")), "worker-dsn"))
	if err != nil {
		t.Fatal("test worker identity missing")
	}
	u, _ := url.Parse(strings.TrimSpace(string(raw)))
	u.Path = "/hc_test_settings"
	worker, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal("test worker unavailable")
	}
	defer worker.Close()
	// Prior test attempts cannot run as unrelated jobs in this fixture.
	if _, err = admin.Exec(ctx, `UPDATE model_sync_operations SET status='FAILED',lease_expires_at=NULL WHERE connection_id<>$1 AND org_id=$2`, id, owner.OrganizationID); err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, `UPDATE user_model_connection_versions SET sync_status='REVOKED' WHERE connection_id<>$1 AND org_id=$2`, id, owner.OrganizationID); err != nil {
		t.Fatal(err)
	}
	var catalogID string
	if err = admin.QueryRow(ctx, `SELECT catalog_entry_id::text FROM user_model_connection_versions WHERE connection_id=$1 AND version=1`, id).Scan(&catalogID); err != nil {
		t.Fatal(err)
	}
	catalogInput := CatalogInput{Provider: "moonshot", Model: "kimi-k2.5", Name: "Kimi CN", APIBase: "https://api.moonshot.cn/v1", ResponseModels: []string{"kimi-k2.5"}, ContextWindow: 32768, MaxOutputTokens: 4096, Streaming: true, ToolUse: true, Enabled: true}
	catalogService := NewService(api, cipher, policy, "test", false)
	if _, e := catalogService.SaveCatalog(ctx, owner, "", catalogInput, ""); problemCode(e) != "FORBIDDEN" && e != identity.ErrForbidden {
		t.Fatal("member acquired catalog governance", e)
	}
	catalogInput.Model = "fixture-" + id
	added, e := catalogService.SaveCatalog(ctx, other, "", catalogInput, "")
	if e != nil {
		t.Fatal("admin cannot manage database catalog", e)
	}
	defer admin.Exec(context.Background(), `DELETE FROM model_catalog_entries WHERE id=$1`, added.ID)
	catalogInput.APIBase = "https://attacker.example/v1"
	catalogInput.ExpectedVersion = added.Version
	if _, e = catalogService.SaveCatalog(ctx, other, added.ID, catalogInput, ""); problemCode(e) != "MODEL_ENDPOINT_NOT_ALLOWED" {
		t.Fatal("arbitrary upstream admitted", e)
	}
	var catalogModel string
	if err = admin.QueryRow(ctx, `SELECT model_id FROM model_catalog_entries WHERE id=$1`, catalogID).Scan(&catalogModel); err != nil {
		t.Fatal(err)
	}
	var route map[string]any
	var virtualKey, alias string
	creates, keys, paid := 0, 0, 0
	probeRound, rejectProbe := 0, false
	driftAfterKey := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/key/list" {
			w.WriteHeader(403)
			return
		}
		if r.URL.Path == "/v1/messages" {
			paid++
			if rejectProbe {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			probeRound++
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(strings.ReplaceAll(messagesFixture(probeRound%2 == 1, true), "actual-provider-model", catalogModel)))
			return
		}
		if r.Header.Get("Authorization") != "Bearer synthetic-control-key" {
			t.Error("wrong control authorization")
			w.WriteHeader(403)
			return
		}
		switch r.URL.Path {
		case "/v2/model/info":
			if route == nil {
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
			} else {
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{route}})
			}
		case "/model/new":
			creates++
			if json.NewDecoder(r.Body).Decode(&route) != nil {
				t.Error("invalid route")
			}
			params := route["provider_params"].(map[string]any)
			if params["api_key"] != providerAccountKey || params["model"] != "moonshot/"+catalogModel || params["max_retries"] != float64(0) {
				t.Error("wrong provider recipe")
			}
			alias = route["model_name"].(string)
			// The route exists remotely but its successful response is lost.
			w.WriteHeader(502)
		case "/key/info":
			if virtualKey == "" {
				w.WriteHeader(404)
				return
			}
			hash := sha256.Sum256([]byte(virtualKey))
			if r.URL.Query().Get("key") != hex.EncodeToString(hash[:]) {
				t.Error("plaintext virtual key in URL")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"info": map[string]any{"models": []string{alias}, "allowed_routes": []string{"/v1/messages", "/v1/models"}, "status": "active"}})
		case "/key/generate":
			keys++
			var value struct {
				Key    string   `json:"key"`
				Models []string `json:"models"`
				Routes []string `json:"allowed_routes"`
			}
			if json.NewDecoder(r.Body).Decode(&value) != nil || len(value.Models) != 1 || value.Models[0] != alias || len(value.Routes) != 2 {
				t.Error("unrestricted virtual key")
			}
			virtualKey = value.Key
			if driftAfterKey {
				route["provider_params"].(map[string]any)["extra_body"] = map[string]any{"thinking": map[string]any{"type": "enabled"}}
				driftAfterKey = false
			}
		case "/key/delete":
			virtualKey = ""
		case "/model/delete":
			route = nil
		default:
			t.Error("unexpected management path", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	target, _ := url.Parse(server.URL)
	client := &http.Client{Transport: controlTestTransport{target}}
	service := NewService(worker, cipher, policy, "test", false)
	service.gatewayClientOverride = func(Gateway) *http.Client { return client }
	syncer := NewModelSync(service, []byte("synthetic-control-key"))
	syncer.client = client
	ran, err := syncer.RunOnce(ctx)
	if !ran || err == nil {
		t.Fatal("ambiguous write was marked successful")
	}
	item, err := catalogService.GetPersonalModel(ctx, owner, id)
	if err != nil || item.SyncStatus != "UNCERTAIN" {
		t.Fatal("ambiguous remote result hidden from owner", err, item.SyncStatus)
	}
	// A prolonged outage must not permanently strand remote compensation.
	params := route["provider_params"].(map[string]any)
	approvedBase := params["api_base"]
	params["api_base"] = "https://api.moonshot.cn/v1"
	if approvedBase == params["api_base"] {
		params["api_base"] = "https://api.moonshot.ai/v1"
	}
	if _, err = admin.Exec(ctx, `UPDATE model_sync_operations SET retry_at=clock_timestamp(),attempts=100 WHERE connection_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	ran, err = syncer.RunOnce(ctx)
	if !ran || err == nil || keys != 0 || creates != 1 || paid != 0 {
		t.Fatal("drifted regional route acquired a virtual key or was rewritten")
	}
	item, err = catalogService.GetPersonalModel(ctx, owner, id)
	if err != nil || item.SyncStatus != "UNCERTAIN" || item.Reason != "MODEL_ROUTE_DRIFT" {
		t.Fatal("regional route drift was hidden from owner", err)
	}
	// Restore the original approved region; simulate a second mutation while
	// issuing the virtual key, after the first successful route readback.
	params["api_base"] = approvedBase
	if _, err = admin.Exec(ctx, `UPDATE model_sync_operations SET retry_at=clock_timestamp() WHERE connection_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	ran, err = syncer.RunOnce(ctx)
	if !ran || err == nil || keys != 1 || creates != 1 || paid != 0 {
		t.Fatal("route mutation during key registration was published")
	}
	item, err = catalogService.GetPersonalModel(ctx, owner, id)
	if err != nil || item.SyncStatus != "UNCERTAIN" || item.Reason != "MODEL_ROUTE_DRIFT" {
		t.Fatal("late route drift was hidden from owner", err)
	}
	var published int
	if err = admin.QueryRow(ctx, `SELECT count(*) FROM model_configurations WHERE id=$1`, id).Scan(&published); err != nil || published != 0 {
		t.Fatal("unverified route became a runtime configuration", err)
	}
	params["extra_body"] = map[string]any{"thinking": map[string]any{"type": "disabled"}}
	if _, err = admin.Exec(ctx, `UPDATE model_sync_operations SET retry_at=clock_timestamp() WHERE connection_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	ran, err = syncer.RunOnce(ctx)
	if !ran || err != nil {
		t.Fatal("durable synchronization failed", err)
	}
	if creates != 1 || keys != 1 || paid != 0 {
		t.Fatal("recovery duplicated a remote route or issued paid inference", creates, keys, paid)
	}
	apiService := NewService(api, cipher, policy, "test", false)
	apiService.gatewayClientOverride = func(Gateway) *http.Client { return client }
	item, err = apiService.GetPersonalModel(ctx, owner, id)
	if err != nil || item.SyncStatus != "SYNCED" || item.Validation.Status != "UNKNOWN" {
		t.Fatal("synchronization fabricated validation", err, item.SyncStatus)
	}
	if _, err = apiService.SetPersonalDefault(ctx, owner, id, 1, 0, ""); problemCode(err) != "CONFIGURATION_INVALID" {
		t.Fatal("unvalidated personal default accepted", err)
	}
	auto := NewAutoValidator(service)
	if _, _, claimed, e := auto.acquire(ctx); !claimed || e != nil {
		t.Fatal("initial personal validation lease unavailable", e)
	}
	if ran, e := NewAutoValidator(service).RunOnce(ctx); ran || e != nil || paid != 0 {
		t.Fatal("second worker stole initial validation lease", ran, e, paid)
	}
	if _, e := apiService.ValidatePersonalModel(ctx, owner, id, 1, ""); problemCode(e) != "STATE_CONFLICT" || paid != 0 {
		t.Fatal("manual retry duplicated initial automatic validation", e, paid)
	}
	if _, e := admin.Exec(ctx, `UPDATE model_validations SET attempted_at=clock_timestamp()-interval '5 minutes',lease_expires_at=clock_timestamp()-interval '1 second' WHERE config_id=$1`, id); e != nil {
		t.Fatal(e)
	}
	if ran, err = auto.RunOnce(ctx); !ran || err != nil {
		t.Fatal("new saved personal model was not automatically validated", ran, err)
	}
	item, err = apiService.GetPersonalModel(ctx, owner, id)
	if err != nil || item.Validation.Status != "HEALTHY" || paid != 2 {
		t.Fatal("automatic personal validation failed", err, item.Validation.Status, paid)
	}
	if ran, err = NewAutoValidator(service).RunOnce(ctx); ran || err != nil || paid != 2 {
		t.Fatal("restart repeated unused personal model validation", ran, err, paid)
	}
	// A successful, enabled model must renew without being a default or having
	// an instance reference. Reloading its account must not strand expired evidence.
	if _, err = admin.Exec(ctx, `UPDATE model_validations SET checked_at=clock_timestamp()-interval '6 minutes',expires_at=clock_timestamp()-interval '1 minute',attempted_at=clock_timestamp()-interval '6 minutes' WHERE config_id=$1 AND config_version=1`, id); err != nil {
		t.Fatal(err)
	}
	item, err = apiService.GetPersonalModel(ctx, owner, id)
	if err != nil || item.Validation.Status != "UNKNOWN" {
		t.Fatal("expired evidence was trusted", err, item.Validation.Status)
	}
	if ran, err = NewAutoValidator(service).RunOnce(ctx); !ran || err != nil || paid != 4 {
		t.Fatal("unused successful model was not renewed", ran, err, paid)
	}
	item, err = apiService.GetPersonalModel(ctx, owner, id)
	if err != nil || item.Validation.Status != "HEALTHY" {
		t.Fatal("renewal was not visible after account reload", err, item.Validation.Status)
	}
	if ran, err = NewAutoValidator(service).RunOnce(ctx); ran || err != nil || paid != 4 {
		t.Fatal("renewed evidence was probed twice", ran, err, paid)
	}
	if _, err = apiService.SetPersonalDefault(ctx, owner, id, 1, 0, ""); err != nil {
		t.Fatal("validated personal default rejected", err)
	}
	exerciseGatewayChecks(t, ctx, admin, apiService, syncer, owner, id, func(changed bool) {
		value := "disabled"
		if changed {
			value = "enabled"
		}
		params["extra_body"] = map[string]any{"thinking": map[string]any{"type": value}}
	}, func() (int, int, int) { return creates, keys, paid })
	if _, err = apiService.SetPersonalDefault(ctx, other, id, 1, 0, ""); problemCode(err) != "NOT_FOUND" {
		t.Fatal("cross-owner default accepted", err)
	}
	var available bool
	if err = admin.QueryRow(ctx, `SELECT public.hc_model_available($1,$2,$3,1)`, owner.OrganizationID, other.UserID, id).Scan(&available); err != nil || available {
		t.Fatal("cross-owner execution permitted")
	}
	if _, err = api.Exec(ctx, `SELECT ciphertext FROM external_provider_secrets`); err == nil {
		t.Fatal("API acquired provider-secret read permission")
	}
	def, e := apiService.GetPersonalDefault(ctx, owner)
	if e != nil || def == nil {
		t.Fatal("personal default missing", e)
	}
	cleared, e := apiService.ClearPersonalDefault(ctx, owner, def.Version, "")
	if e != nil || cleared.ConnectionID != nil || cleared.Version <= def.Version {
		t.Fatal("personal default clear lost CAS receipt", e)
	}
	if _, e = apiService.SetPersonalDefault(ctx, owner, id, 1, def.Version, ""); problemCode(e) != "VERSION_CONFLICT" {
		t.Fatal("stale default write crossed clear", e)
	}
	if _, err = admin.Exec(ctx, `UPDATE model_gateway_checks SET next_check_at=clock_timestamp() WHERE connection_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	lateLease, claimed, err := syncer.claimCheck(ctx)
	if err != nil || !claimed {
		t.Fatal("cannot prepare late check after revocation", err)
	}
	item, err = apiService.RevokePersonalModel(ctx, owner, id, 1, "")
	if err != nil || item.Enabled || item.SyncStatus != "REVOKING" {
		t.Fatal("local revocation failed", err)
	}
	if err = syncer.publishCheck(ctx, lateLease, ""); err != nil {
		t.Fatal("late check publication failed", err)
	}
	if err = admin.QueryRow(ctx, `SELECT public.hc_model_available($1,$2,$3,1)`, owner.OrganizationID, owner.UserID, id).Scan(&available); err != nil || available {
		t.Fatal("late remote success reopened a revoked connection", err)
	}
	ran, err = syncer.RunOnce(ctx)
	if !ran || err != nil || route != nil || virtualKey != "" {
		t.Fatal("remote revocation failed", err)
	}
	item, err = apiService.GetPersonalModel(ctx, owner, id)
	if err != nil || item.SyncStatus != "REVOKED" {
		t.Fatal("remote revocation state missing", err)
	}
	rotated, e := apiService.UpdatePersonalModel(ctx, owner, id, PersonalModelUpdate{Name: "rotated private connection", CatalogEntryID: item.CatalogEntryID, APIKey: &providerAccountKey, ExpectedVersion: 1}, "")
	if e != nil || rotated.Version != 2 || !rotated.Enabled || rotated.SyncStatus != "PENDING_SYNC" {
		t.Fatal("replacement key did not create new version", e)
	}
	if _, e = apiService.UpdatePersonalModel(ctx, owner, id, PersonalModelUpdate{Name: "stale", CatalogEntryID: item.CatalogEntryID, APIKey: &providerAccountKey, ExpectedVersion: 1}, ""); problemCode(e) != "VERSION_CONFLICT" {
		t.Fatal("stale key rotation accepted", e)
	}
	_, _ = syncer.RunOnce(ctx)
	_, _ = admin.Exec(ctx, `UPDATE model_sync_operations SET retry_at=clock_timestamp() WHERE connection_id=$1`, id)
	if ran, e = syncer.RunOnce(ctx); !ran || e != nil {
		t.Fatal("rotated version failed to synchronize", e)
	}
	rejectProbe = true
	if ran, e = auto.RunOnce(ctx); !ran || e != nil {
		t.Fatal("rotated version was not automatically validated", e)
	}
	item, e = apiService.GetPersonalModel(ctx, owner, id)
	if e != nil || item.Validation.Status != "UNHEALTHY" || paid != 5 {
		t.Fatal("automatic failure was not visible", e, item.Validation.Status, paid)
	}
	_, _ = admin.Exec(ctx, `UPDATE model_validations SET attempted_at=clock_timestamp()-interval '5 minutes' WHERE config_id=$1`, id)
	if ran, e = NewAutoValidator(service).RunOnce(ctx); ran || e != nil || paid != 5 {
		t.Fatal("unused failed model was repeatedly charged", ran, e, paid)
	}
	rejectProbe = false
	item, e = apiService.ValidatePersonalModel(ctx, owner, id, 2, "")
	if e != nil || item.Validation.Status != "HEALTHY" || paid != 7 {
		t.Fatal("failed automatic validation could not be retried manually", e, item.Validation.Status, paid)
	}
	if _, e = apiService.RevokePersonalModel(ctx, owner, id, 2, ""); e != nil {
		t.Fatal(e)
	}
	ran, e = syncer.RunOnce(ctx)
	if !ran || e != nil {
		t.Fatal("unsynchronized version cannot revoke", e)
	}
	_, e = apiService.UpdatePersonalModel(ctx, owner, id, PersonalModelUpdate{Name: "account revocation", CatalogEntryID: item.CatalogEntryID, APIKey: &providerAccountKey, ExpectedVersion: 2}, "")
	if e != nil {
		t.Fatal("cannot prepare account revocation test", e)
	}
	// This fixture loses only the first write response.
	_, _ = syncer.RunOnce(ctx)
	_, _ = admin.Exec(ctx, `UPDATE model_sync_operations SET retry_at=clock_timestamp() WHERE connection_id=$1`, id)
	if ran, e = syncer.RunOnce(ctx); !ran || e != nil || virtualKey == "" {
		t.Fatal("cannot synchronize account revocation fixture", e)
	}
	if _, e = admin.Exec(ctx, `UPDATE users SET status='DISABLED' WHERE id=$1`, owner.UserID); e != nil {
		t.Fatal(e)
	}
	defer admin.Exec(context.Background(), `UPDATE users SET status='ACTIVE' WHERE id=$1`, owner.UserID)
	if ran, e = syncer.RunOnce(ctx); !ran || e != nil || route != nil || virtualKey != "" {
		var state string
		_ = admin.QueryRow(ctx, `SELECT string_agg(connection_version::text||':'||action||':'||status,',' ORDER BY connection_version,action) FROM model_sync_operations WHERE connection_id=$1`, id).Scan(&state)
		t.Fatal("disabled owner retained remote personal credentials", e, state, "route present", route != nil, "credential present", virtualKey != "")
	}
	if _, err = admin.Exec(ctx, `DELETE FROM user_model_defaults WHERE connection_id=$1`, id); err != nil {
		t.Fatal(err)
	}
}
