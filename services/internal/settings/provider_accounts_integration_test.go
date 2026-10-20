package settings

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	"net/http"
	"net/http/httptest"
	"testing"
)

func exerciseProviderAccounts(t *testing.T, ctx context.Context, root, api *pgxpool.Pool, cipher *security.SecretCipher, owner, other identity.Principal) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Authorization") != "" {
			t.Error("discovery must be no-auth read only")
		}
		fmt.Fprint(w, `{"data":[{"id":"local-a"},{"id":"local-b"},{"id":"local-c"}]}`)
	}))
	defer upstream.Close()
	policy, err := ParsePolicy([]byte(`{"schema_version":1,"gateways":[{"endpoint":"https://models.enterprise.example:14000","revision":"native-v1","addresses":["10.50.0.10/32"],"models":[],"probe_retries_disabled":true,"probe_fallbacks_disabled":true}]}`))
	if err != nil {
		t.Fatal(err)
	}
	policy.CustomAPIBases = []string{upstream.URL + "/v1"}
	s := NewService(api, cipher, policy, "test", false)
	key := ""
	a := ModelSelection{Provider: "custom-openai", APIBase: policy.CustomAPIBases[0], Model: "local-a"}
	b := a
	b.Model = "local-b"
	c := a
	c.Model = "local-c"
	input := ProviderAccountInput{Name: "My local provider", Provider: a.Provider, APIBase: a.APIBase, APIKey: &key, Models: []ModelSelection{a, b}}
	receipt := "provider-create-" + webapi.ID()
	created, err := s.CreateProviderAccount(ctx, owner, input, receipt, "")
	if err != nil || len(created.Models) != 2 || created.DefaultModelID == nil {
		t.Fatal("provider aggregate creation", err)
	}
	for _, model := range created.Models {
		if (*created.DefaultModelID == model.ID) != (model.Model == "local-a") {
			t.Fatal("first selected model must be the initial provider default")
		}
	}
	// Two accounts for one provider retain independent defaults. An account edit
	// cannot silently select the other account's matching model name.
	secondInput := input
	secondInput.Name = "Second account"
	secondInput.DefaultModel = "local-b"
	second, err := s.CreateProviderAccount(ctx, owner, secondInput, "second-account-"+webapi.ID(), "")
	if err != nil || second.DefaultModelID == nil || *second.DefaultModelID == *created.DefaultModelID {
		t.Fatal("independent account default", err)
	}
	defer root.Exec(context.Background(), `UPDATE user_model_connections SET enabled=false,archived_at=clock_timestamp() WHERE provider_account_id=$1`, second.ID)
	defer root.Exec(context.Background(), `UPDATE personal_provider_accounts SET status='DELETED' WHERE id=$1`, second.ID)
	for _, model := range second.Models {
		if (*second.DefaultModelID == model.ID) != (model.Model == "local-b") {
			t.Fatal("explicit default was not bound to its own account")
		}
	}
	invalidDefault := input
	invalidDefault.DefaultModel = "not-selected"
	if _, err := s.CreateProviderAccount(ctx, owner, invalidDefault, "invalid-default-"+webapi.ID(), ""); problemCode(err) != "CONFIGURATION_INVALID" {
		t.Fatal("unselected default model was accepted", err)
	}
	defer root.Exec(context.Background(), `UPDATE user_model_connections SET enabled=false,archived_at=clock_timestamp() WHERE provider_account_id=$1`, created.ID)
	defer root.Exec(context.Background(), `UPDATE personal_provider_accounts SET status='DELETED' WHERE id=$1`, created.ID)
	replay, err := s.CreateProviderAccount(ctx, owner, input, receipt, "")
	if err != nil || replay.ID != created.ID || replay.Models[0].ID != created.Models[0].ID {
		t.Fatal("provider replay", err)
	}
	changed := input
	changed.Name = "Changed"
	if _, err = s.CreateProviderAccount(ctx, owner, changed, receipt, ""); problemCode(err) != "IDEMPOTENCY_CONFLICT" {
		t.Fatal("provider receipt conflict", err)
	}
	list, err := s.ListProviderAccounts(ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range list {
		if v.ID == created.ID {
			t.Fatal("cross-owner provider listed")
		}
	}
	changed = input
	changed.ExpectedVersion = created.Version
	if _, err = s.UpdateProviderAccount(ctx, other, created.ID, changed, ""); problemCode(err) != "NOT_FOUND" {
		t.Fatal("cross-owner update", err)
	}
	if _, err = s.DiscoverProviderAccountModels(ctx, other, created.ID, ProviderAccountSyncInput{ExpectedVersion: 1}); problemCode(err) != "NOT_FOUND" {
		t.Fatal("cross-owner saved credential", err)
	}
	if _, err = s.DeleteProviderAccount(ctx, other, created.ID, 1, ""); problemCode(err) != "NOT_FOUND" {
		t.Fatal("cross-owner deletion", err)
	}
	models, err := s.DiscoverProviderAccountModels(ctx, owner, created.ID, ProviderAccountSyncInput{ExpectedVersion: 1})
	if err != nil || len(models) != 3 {
		t.Fatal("saved credential discovery", err)
	}
	// Rename does not invalidate model versions or checks, even while registration is pending.
	changed.APIKey = nil
	changed.Name = "Renamed provider"
	renamed, err := s.UpdateProviderAccount(ctx, owner, created.ID, changed, "")
	if err != nil || renamed.Version != 2 || renamed.Models[0].Version != 1 || renamed.Models[0].Name != changed.Name || renamed.DefaultModelID == nil || *renamed.DefaultModelID != *created.DefaultModelID {
		t.Fatal("rename lost validation/version", err)
	}
	if _, err = s.UpdateProviderAccount(ctx, owner, created.ID, changed, ""); problemCode(err) != "VERSION_CONFLICT" {
		t.Fatal("stale provider editor accepted", err)
	}
	changed.ExpectedVersion = 2
	changed.Models = []ModelSelection{a, b, c}
	changed.DefaultModel = "local-c"
	added, err := s.UpdateProviderAccount(ctx, owner, created.ID, changed, "")
	if err != nil || len(added.Models) != 3 || added.DefaultModelID == nil || *added.DefaultModelID == *created.DefaultModelID {
		t.Fatal("add model with retained encrypted key", err)
	}
	var secondDefault string
	if err = root.QueryRow(ctx, `SELECT default_model_id::text FROM personal_provider_accounts WHERE id=$1`, second.ID).Scan(&secondDefault); err != nil || secondDefault != *second.DefaultModelID {
		t.Fatal("editing one account changed another account default", err)
	}
	// Any later invalid model rolls back all preceding writes and group version.
	invalid := changed
	invalid.ExpectedVersion = 3
	invalid.Name = "Must roll back"
	invalid.Models = append([]ModelSelection{a}, ModelSelection{Provider: a.Provider, APIBase: a.APIBase, Model: "bad model"})
	if _, err = s.UpdateProviderAccount(ctx, owner, created.ID, invalid, ""); problemCode(err) != "CONFIGURATION_INVALID" {
		t.Fatal("invalid provider update", err)
	}
	var removedID string
	for _, m := range added.Models {
		if m.Model == "local-a" {
			removedID = m.ID
		}
	}
	if removedID == "" {
		t.Fatal("missing model to remove")
	}
	// A live workspace protects both model removal and whole-provider deletion.
	// Simulate a completed registration without calling an external model.
	if _, err = root.Exec(ctx, `INSERT INTO model_configurations(org_id,id,version,provider,endpoint,protocol,model,credential_ref,status) SELECT org_id,$2,1,provider,endpoint,protocol,model,credential_ref,'UNVALIDATED' FROM model_configurations WHERE org_id=$1 ORDER BY created_at LIMIT 1`, owner.OrganizationID, removedID); err != nil {
		t.Fatal(err)
	}
	if _, err = root.Exec(ctx, `UPDATE user_model_connection_versions SET sync_status='SYNCED',model_configuration_id=connection_id,model_configuration_version=version WHERE connection_id=$1`, removedID); err != nil {
		t.Fatal(err)
	}
	if _, err = root.Exec(ctx, `INSERT INTO model_gateway_checks(connection_id,connection_version,org_id,owner_id,status,checked_at,expires_at,reason_code) VALUES($1,1,$2,$3,'HEALTHY',now(),now()+interval '60 seconds','')`, removedID, owner.OrganizationID, owner.UserID); err != nil {
		t.Fatal(err)
	}
	instanceID := webapi.ID()
	if _, err = root.Exec(ctx, `INSERT INTO instances(id,org_id,owner_id,name,external_create_key,workspace_name,desired_state,observed_state,model_config_id,model_config_version,config_snapshot) VALUES($1,$2,$3,$4,$1,$1::uuid::text,'STOPPED','STOPPED',$5,1,'{}')`, instanceID, owner.OrganizationID, owner.UserID, "w-"+instanceID[:12], removedID); err != nil {
		t.Fatal(err)
	}
	defer root.Exec(context.Background(), `DELETE FROM instances WHERE id=$1`, instanceID)
	blocked := changed
	blocked.ExpectedVersion = 3
	blocked.Models = []ModelSelection{b, c}
	if _, err = s.UpdateProviderAccount(ctx, owner, created.ID, blocked, ""); problemCode(err) != "MODEL_IN_USE" {
		t.Fatal("in-use model removal accepted", err)
	}
	if _, err = s.DeleteProviderAccount(ctx, owner, created.ID, 3, ""); problemCode(err) != "MODEL_IN_USE" {
		t.Fatal("in-use provider deletion accepted", err)
	}
	if _, err = root.Exec(ctx, `DELETE FROM instances WHERE id=$1`, instanceID); err != nil {
		t.Fatal(err)
	}
	// Default removal keeps its version tombstone. No workspace can race removal:
	// aggregate changes share the organization model lock with instance admission.
	if _, err = root.Exec(ctx, `INSERT INTO user_model_defaults(org_id,owner_id,connection_id,connection_version,version) VALUES($1,$2,$3,1,1) ON CONFLICT(org_id,owner_id) DO UPDATE SET connection_id=EXCLUDED.connection_id,connection_version=1,version=1`, owner.OrganizationID, owner.UserID, removedID); err != nil {
		t.Fatal(err)
	}
	changed.ExpectedVersion = 3
	changed.Models = []ModelSelection{b, c}
	removed, err := s.UpdateProviderAccount(ctx, owner, created.ID, changed, "")
	if err != nil || len(removed.Models) != 2 || removed.Version != 4 {
		t.Fatal("remove selected model", err)
	}
	def, err := s.GetPersonalDefault(ctx, owner)
	if err != nil || def.ConnectionID != nil || def.Version != 2 {
		t.Fatal("default not cleared atomically", err)
	}
	var retired bool
	if err = root.QueryRow(ctx, `SELECT provider_retired AND NOT enabled FROM user_model_connections WHERE id=$1`, removedID).Scan(&retired); err != nil || !retired {
		t.Fatal("removed model still grants access", err)
	}
	// New credential rotates each retained model as one transaction.
	if _, err = root.Exec(ctx, `UPDATE model_sync_operations SET status='FAILED' WHERE connection_id IN (SELECT id FROM user_model_connections WHERE provider_account_id=$1) AND action='UPSERT'`, created.ID); err != nil {
		t.Fatal(err)
	}
	replacement := "new-synthetic-key"
	changed.ExpectedVersion = 4
	changed.APIKey = &replacement
	rotated, err := s.UpdateProviderAccount(ctx, owner, created.ID, changed, "")
	if err != nil || rotated.Version != 5 {
		t.Fatal("provider key rotation", err)
	}
	for _, m := range rotated.Models {
		if m.Version != 2 {
			t.Fatal("rotation missed a selected model")
		}
	}
	deleted, err := s.DeleteProviderAccount(ctx, owner, created.ID, 5, "")
	if err != nil || deleted.Status != "DELETING" {
		t.Fatal("provider deletion not queued", err)
	}
	again, err := s.DeleteProviderAccount(ctx, owner, created.ID, deleted.Version, "")
	if err != nil || again.Status != "DELETING" {
		t.Fatal("delete retry", err)
	}
	if _, err = s.DiscoverProviderAccountModels(ctx, owner, created.ID, ProviderAccountSyncInput{ExpectedVersion: 6}); problemCode(err) != "STATE_CONFLICT" {
		t.Fatal("deleted credential reused", err)
	}
	sync := NewModelSync(NewService(root, cipher, policy, "test", false), nil)
	if err = sync.finishProviderAccountDeletes(ctx); err != nil {
		t.Fatal(err)
	}
	var status string
	root.QueryRow(ctx, `SELECT status FROM personal_provider_accounts WHERE id=$1`, created.ID).Scan(&status)
	if status != "DELETING" {
		t.Fatal("deletion reported complete before remote revocation")
	}
	if _, err = root.Exec(ctx, `UPDATE user_model_connection_versions SET sync_status='REVOKED' WHERE connection_id IN (SELECT id FROM user_model_connections WHERE provider_account_id=$1)`, created.ID); err != nil {
		t.Fatal(err)
	}
	if err = sync.finishProviderAccountDeletes(ctx); err != nil {
		t.Fatal(err)
	}
	root.QueryRow(ctx, `SELECT status FROM personal_provider_accounts WHERE id=$1`, created.ID).Scan(&status)
	if status != "DELETED" {
		t.Fatal("provider deletion not finalized")
	}
	// A late duplicate create never resurrects a deleted provider or routes.
	replay, err = s.CreateProviderAccount(ctx, owner, input, receipt, "")
	if err != nil || replay.Status != "DELETED" || len(replay.Models) != 0 {
		t.Fatal("deleted provider resurrected", err)
	}
}
