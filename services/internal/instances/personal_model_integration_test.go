package instances

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/runtimeapproval"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	workspaceengine "github.com/jonluo94/deepseek-harness-cloud/services/internal/workspace"
)

func testPersonalBinding(t *testing.T, ctx context.Context, admin, api *pgxpool.Pool, owner, other identity.Principal) {
	t.Helper()
	id, provider, runtime := webapi.ID(), webapi.ID(), webapi.ID()
	var catalog string
	if err := admin.QueryRow(ctx, `SELECT id::text FROM model_catalog_entries WHERE org_id=$1 LIMIT 1`, owner.OrganizationID).Scan(&catalog); err != nil {
		t.Fatal(err)
	}
	must := func(sql string, args ...any) {
		t.Helper()
		if _, err := admin.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	receipt := sha256.Sum256([]byte(id))
	must(`INSERT INTO model_connection_registry(id,org_id,owner_id) VALUES($1,$2,$3)`, id, owner.OrganizationID, owner.UserID)
	must(`INSERT INTO user_model_connections(id,org_id,owner_id,name,current_version,create_key_sha256,create_request_sha256) VALUES($1,$2,$3,'private fixture',1,$4,$4)`, id, owner.OrganizationID, owner.UserID, receipt[:])
	must(`INSERT INTO external_provider_secrets(id,org_id,owner_id,connection_id,bound_version,key_id,nonce,ciphertext) VALUES($1,$2,$3,$4,1,'fixture',$5,$6)`, provider, owner.OrganizationID, owner.UserID, id, bytes.Repeat([]byte{1}, 12), bytes.Repeat([]byte{1}, 17))
	must(`INSERT INTO secrets(id,org_id,key_id,nonce,ciphertext) VALUES($1,$2,'fixture',$3,$4)`, runtime, owner.OrganizationID, bytes.Repeat([]byte{1}, 12), bytes.Repeat([]byte{1}, 17))
	must(`INSERT INTO model_configurations(id,org_id,version,provider,protocol,endpoint,model,credential_ref,status,manifest) VALUES($1,$2,1,'platform-models','anthropic-messages','https://models.enterprise.example:14000','private-route',$3,'VALID','{"id":"private-route","revision":"fixture","context_window":32768,"max_output_tokens":4096,"streaming":true,"tool_use":true,"response_models":["fixture"],"digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}')`, id, owner.OrganizationID, runtime)
	must(`INSERT INTO model_validations(config_id,config_version,status,checked_at,expires_at) VALUES($1,1,'HEALTHY',clock_timestamp(),clock_timestamp()+interval '5 minutes')`, id)
	must(`INSERT INTO user_model_connection_versions(connection_id,org_id,owner_id,version,catalog_entry_id,catalog_version,provider_secret_id,gateway_endpoint,route_alias,manifest,sync_status,model_configuration_id,model_configuration_version) VALUES($1,$2,$3,1,$4,1,$5,'https://models.enterprise.example:14000',$6,'{}','SYNCED',$1,1)`, id, owner.OrganizationID, owner.UserID, catalog, provider, "hc-"+stripUUID(id)+"-v1")
	must(`INSERT INTO model_gateway_checks(connection_id,connection_version,org_id,owner_id,status,checked_at,expires_at,reason_code) VALUES($1,1,$2,$3,'HEALTHY',statement_timestamp(),statement_timestamp()+interval '60 seconds','')`, id, owner.OrganizationID, owner.UserID)
	defer must(`UPDATE user_model_connections SET enabled=false,archived_at=clock_timestamp() WHERE id=$1`, id)
	tx, err := api.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var snapshot Snapshot
	if err = bindModel(ctx, tx, owner, &snapshot, id, 1); err != nil || snapshot.ModelID != id || snapshot.Provider != "platform-models" {
		t.Fatal("owner cannot bind private model", err)
	}
	if err = bindModel(ctx, tx, other, &snapshot, id, 1); queryCode(err) != "NOT_FOUND" {
		t.Fatal("private model leaked through configuration binding", err)
	}
	_ = tx.Rollback(ctx)
	t.Run("runtime defaults accept a private model without an organization model head", func(t *testing.T) {
		must := func(sql string, args ...any) {
			t.Helper()
			if _, err := admin.Exec(ctx, sql, args...); err != nil {
				t.Fatal(err)
			}
		}
		var head, shared string
		var headVersion, sharedVersion int64
		if err := admin.QueryRow(ctx, `SELECT h.id::text,h.current_version,d.model_config_id::text,d.model_config_version FROM model_configuration_heads h JOIN organization_defaults d ON d.org_id=h.org_id WHERE h.org_id=$1`, owner.OrganizationID).Scan(&head, &headVersion, &shared, &sharedVersion); err != nil {
			t.Fatal(err)
		}
		defer func() {
			// The registry row intentionally survives head removal. Restore this
			// fixture atomically without registering that same row a second time.
			restore, err := admin.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer restore.Rollback(context.Background())
			if _, err = restore.Exec(ctx, `ALTER TABLE model_configuration_heads DISABLE TRIGGER model_heads_register`); err == nil {
				_, err = restore.Exec(ctx, `INSERT INTO model_configuration_heads(id,org_id,current_version) VALUES($1,$2,$3)`, head, owner.OrganizationID, headVersion)
			}
			if err == nil {
				_, err = restore.Exec(ctx, `ALTER TABLE model_configuration_heads ENABLE TRIGGER model_heads_register`)
			}
			if err == nil {
				err = restore.Commit(ctx)
			}
			if err != nil {
				t.Fatal(err)
			}
			must(`UPDATE organization_defaults SET model_config_id=$2,model_config_version=$3 WHERE org_id=$1`, owner.OrganizationID, shared, sharedVersion)
		}()
		must(`UPDATE organization_defaults SET model_config_id=NULL,model_config_version=NULL WHERE org_id=$1`, owner.OrganizationID)
		must(`DELETE FROM model_configuration_heads WHERE org_id=$1`, owner.OrganizationID)
		var template, templateVersion, imageDigest string
		var originalManifest []byte
		if err := admin.QueryRow(ctx, `SELECT t.id::text,t.version::text,t.image_digest,t.runtime_manifest FROM organization_defaults d JOIN approved_templates t ON t.id=d.template_id AND t.version=d.template_version WHERE d.org_id=$1`, owner.OrganizationID).Scan(&template, &templateVersion, &imageDigest, &originalManifest); err != nil {
			t.Fatal(err)
		}
		manifest := runtimeapproval.Manifest{SchemaVersion: 1, UpstreamCommit: runtimeapproval.ReviewCommit, Image: imageDigest, StorageImage: imageDigest, NetworkImage: imageDigest, WorkspaceSourceVersion: runtimeapproval.WorkspaceSourceVersion, TerraformVersion: runtimeapproval.TerraformVersion, ProviderLock: imageDigest, TemplateVersion: templateVersion, ProfileDigest: imageDigest, ConfigFormat: runtimeapproval.ConfigFormat, Bootstrap: runtimeapproval.BootstrapAdapter, NativeAuth: runtimeapproval.NativeAdapter, Paths: []string{"/data/workspace", "/data/dsh"}, ToolDigest: imageDigest, PackageVersion: runtimeapproval.PackageVersion, PackageIntegrity: runtimeapproval.PackageIntegrity, AdapterDigest: imageDigest, ExternalModels: runtimeapproval.ExternalModelAdapter}
		encoded, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		must(`UPDATE approved_templates SET runtime_manifest=$3 WHERE id=$1 AND version=$2`, template, templateVersion, encoded)
		defer must(`UPDATE approved_templates SET runtime_manifest=$3 WHERE id=$1 AND version=$2`, template, templateVersion, originalManifest)
		commands := NewCommandService(api, fixtureCreationGate{}, false)
		modelVersion := int64(1)
		accepted, err := commands.Create(ctx, owner, webapi.ID(), CreateInput{ModelID: &id, ModelVersion: &modelVersion}, "private-runtime-only")
		if err != nil {
			t.Fatal("private account required an organization model", err)
		}
		defer func() {
			must(`DELETE FROM idempotency_records WHERE operation_id=$1`, accepted.OperationID)
			must(`DELETE FROM outbox_events WHERE operation_id=$1`, accepted.OperationID)
			must(`DELETE FROM quota_reservations WHERE operation_id=$1`, accepted.OperationID)
			must(`DELETE FROM operations WHERE id=$1`, accepted.OperationID)
			must(`UPDATE instances SET storage_asset_id=NULL WHERE id=$1`, accepted.InstanceID)
			must(`DELETE FROM storage_assets WHERE instance_id=$1`, accepted.InstanceID)
			must(`DELETE FROM instances WHERE id=$1`, accepted.InstanceID)
		}()
		var bound string
		if err := admin.QueryRow(ctx, `SELECT model_config_id::text FROM instances WHERE id=$1`, accepted.InstanceID).Scan(&bound); err != nil || bound != id {
			t.Fatal("creation lost the selected private model", err)
		}
		if _, err := commands.Create(ctx, other, webapi.ID(), CreateInput{ModelID: &id, ModelVersion: &modelVersion}, "private-runtime-owner-denied"); queryCode(err) != "NOT_FOUND" {
			t.Fatal("runtime-only defaults bypassed private model ownership", err)
		}
	})
	t.Run("instance display uses its frozen private version and preserves owner isolation", func(t *testing.T) {
		must(`UPDATE user_model_connection_versions SET manifest='{"catalog":{"provider":"deepseek","model":"frozen-review-model"}}' WHERE connection_id=$1 AND version=1`, id)
		// The head may advance while an existing instance retains version 1.
		must(`UPDATE user_model_connections SET current_version=2 WHERE id=$1`, id)
		instance := webapi.ID()
		workspace, err := workspaceengine.WorkspaceName(instance)
		if err != nil {
			t.Fatal(err)
		}
		frozen := snapshot
		frozen.DefaultsVersion, frozen.TemplateID, frozen.TemplateVersion = 1, webapi.ID(), webapi.ID()
		frozen.ImageDigest = "sha256:" + strings.Repeat("a", 64)
		frozen.Spec = ResourceSpec{ID: webapi.ID(), CPU: 1000, Memory: 2 << 30, Storage: 10 << 30}
		encoded, err := json.Marshal(struct {
			SchemaVersion int `json:"schema_version"`
			Snapshot
		}{1, frozen})
		if err != nil {
			t.Fatal(err)
		}
		name := "展示-" + instance[:8]
		must(`INSERT INTO instances(id,org_id,owner_id,name,external_create_key,workspace_name,desired_state,observed_state,model_config_id,model_config_version,config_snapshot) VALUES($1,$2,$3,$4,$1,$5,'STOPPED','STOPPED',$6,1,$7)`, instance, owner.OrganizationID, owner.UserID, name, workspace, id, encoded)
		defer must(`DELETE FROM instances WHERE id=$1`, instance)
		queries := NewQueryService(api, nil)
		view, err := queries.Get(ctx, owner, instance)
		if err != nil || view.ModelDisplayName != "frozen-review-model" || view.ModelProviderName != "DeepSeek" || view.Snapshot.Model != frozen.Model {
			t.Fatal("frozen model display changed execution binding", err)
		}
		page, err := queries.List(ctx, owner, ListInput{Query: name})
		if err != nil || len(page.Items) != 1 || page.Items[0].ModelDisplayName != view.ModelDisplayName {
			t.Fatal("list and details model display disagree", err)
		}
		if _, err := queries.Get(ctx, other, instance); queryCode(err) != "NOT_FOUND" {
			t.Fatal("model display leaked across owners", err)
		}
	})
	must(`UPDATE user_model_connections SET enabled=false WHERE id=$1`, id)
	tx, err = api.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if err = bindModel(ctx, tx, owner, &snapshot, id, 1); queryCode(err) != "CONFIGURATION_INVALID" {
		t.Fatal("disabled private model accepted", err)
	}
}

func stripUUID(id string) string {
	result := ""
	for _, c := range id {
		if c != '-' {
			result += string(c)
		}
	}
	return result
}
