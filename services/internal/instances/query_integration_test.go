package instances

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/devlocal"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/pagination"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/runtimeapproval"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	workspaceengine "github.com/jonluo94/deepseek-harness-cloud/services/internal/workspace"
)

func TestOwnerQueriesWithRestrictedPostgres(t *testing.T) {
	path := os.Getenv("HC_TEST_DB_DSN_FILE")
	if path == "" {
		t.Skip("isolated PostgreSQL query database required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("test connection unavailable")
	}
	connection, err := url.Parse(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal("invalid test connection")
	}
	connection.Path = "/hc_test_instances"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.Apply(ctx, connection.String()); err != nil {
		t.Fatal(err)
	}
	database, err := pgxpool.New(ctx, connection.String())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	tx, err := database.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = devlocal.GrantAPI(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	appRaw, err := os.ReadFile(filepath.Join(filepath.Dir(path), "api-dsn"))
	if err != nil {
		t.Fatal("restricted connection unavailable")
	}
	appURL, _ := url.Parse(strings.TrimSpace(string(appRaw)))
	appURL.Path = connection.Path
	appDB, err := pgxpool.New(ctx, appURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer appDB.Close()
	t.Run("explicit model application selects current approved adapter while start stays frozen", func(t *testing.T) {
		tx, e := database.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback(ctx)
		digest := "sha256:" + strings.Repeat("a", 64)
		old := runtimeapproval.Manifest{SchemaVersion: 1, UpstreamCommit: runtimeapproval.ReviewCommit, Image: digest, StorageImage: digest, NetworkImage: digest, WorkspaceSourceVersion: runtimeapproval.WorkspaceSourceVersion, TerraformVersion: runtimeapproval.TerraformVersion, ProviderLock: digest, TemplateVersion: webapi.ID(), ProfileDigest: digest, ConfigFormat: runtimeapproval.ConfigFormat, Bootstrap: runtimeapproval.BootstrapAdapter, NativeAuth: runtimeapproval.NativeAdapter, Paths: []string{"/data/workspace", "/data/dsh"}, ToolDigest: digest, PackageVersion: runtimeapproval.PackageVersion, PackageIntegrity: runtimeapproval.PackageIntegrity, AdapterDigest: digest, ExternalModels: runtimeapproval.ExternalModelAdapter}
		next := old
		next.TemplateVersion = webapi.ID()
		next.Image = "sha256:" + strings.Repeat("b", 64)
		if _, e = tx.Exec(ctx, `UPDATE approved_templates SET enabled=false WHERE enabled`); e != nil {
			t.Fatal(e)
		}
		template := webapi.ID()
		for i, m := range []runtimeapproval.Manifest{old, next} {
			raw, _ := json.Marshal(m)
			if _, e = tx.Exec(ctx, `INSERT INTO approved_templates(id,version,name,image_digest,runtime_manifest,enabled) VALUES($1,$2,'adapter-test',$3,$4,$5)`, template, m.TemplateVersion, m.Image, raw, i == 1); e != nil {
				t.Fatal(e)
			}
		}
		frozen := Snapshot{Spec: ResourceSpec{Storage: 10 << 30}, TemplateID: template, TemplateVersion: old.TemplateVersion, ImageDigest: old.Image, Provider: "platform-models"}
		if e = modelRuntime(ctx, tx, &frozen, false); e != nil || frozen.TemplateVersion != old.TemplateVersion {
			t.Fatal("start changed frozen runtime", e)
		}
		if e = modelRuntime(ctx, tx, &frozen, true); e != nil || frozen.TemplateVersion != next.TemplateVersion || frozen.ImageDigest != next.Image {
			t.Fatal("explicit application did not select current adapter", e)
		}
	})
	org := "00000000-0000-4000-8000-000000000051"
	must := func(sql string, args ...any) {
		t.Helper()
		if _, err := database.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	must(`INSERT INTO organizations(id,name,status) VALUES($1,'Query test','ACTIVE') ON CONFLICT(id) DO NOTHING`, org)
	principal := func(role string) identity.Principal {
		t.Helper()
		id := webapi.ID()
		session := webapi.ID()
		hash := sha256.Sum256([]byte(session))
		must(`INSERT INTO users(id,identity_issuer,identity_subject,workspace_owner_id,status) VALUES($1,'https://query.test',$2,$1,'ACTIVE')`, id, id)
		must(`INSERT INTO memberships(org_id,user_id,role,status) VALUES($1,$2,$3,'ACTIVE')`, org, id, role)
		must(`INSERT INTO portal_sessions(id,secret_sha256,org_id,user_id,auth_generation,csrf_sha256,csrf_cipher,csrf_key_id,csrf_nonce,expires_at) VALUES($1,$2,$3,$4,1,$2,'x','test',$5,clock_timestamp()+interval '1 hour')`, session, hash[:], org, id, bytes.Repeat([]byte{0}, 12))
		return identity.Principal{UserID: id, OrganizationID: org, Role: role, Generation: 1, SessionID: session}
	}
	owner, other, admin := principal("MEMBER"), principal("MEMBER"), principal("ADMIN")
	model, secret := webapi.ID(), webapi.ID()
	must(`INSERT INTO secrets(id,org_id,key_id,nonce,ciphertext) VALUES($1,$2,'test',$3,'x')`, secret, org, bytes.Repeat([]byte{0}, 12))
	// The organization keeps one configuration head across repeated test runs.
	must(`INSERT INTO model_configuration_heads(id,org_id) VALUES($1,$2) ON CONFLICT(org_id) DO NOTHING`, model, org)
	if err := database.QueryRow(ctx, `SELECT id::text FROM model_configuration_heads WHERE org_id=$1`, org).Scan(&model); err != nil {
		t.Fatal(err)
	}
	must(`INSERT INTO model_configurations(org_id,id,version,provider,endpoint,protocol,model,credential_ref,status) VALUES($1,$2,1,'deepseek-official','https://api.deepseek.com/anthropic','deepseek-messages','test-model',$3,'VALID') ON CONFLICT(id,version) DO NOTHING`, org, model, secret)
	must(`INSERT INTO model_validations(config_id,config_version,status,checked_at,expires_at) VALUES($1,1,'HEALTHY',clock_timestamp(),clock_timestamp()+interval '5 minutes') ON CONFLICT(config_id,config_version) DO UPDATE SET status=EXCLUDED.status,checked_at=EXCLUDED.checked_at,expires_at=EXCLUDED.expires_at,lease_expires_at=NULL,reason_code=''`, model)
	must(`UPDATE secrets SET revoked_at=NULL WHERE id=(SELECT credential_ref FROM model_configurations WHERE id=$1 AND version=1)`, model)
	snapshot := map[string]any{"schema_version": 1, "defaults_version": 1, "template_id": webapi.ID(), "template_version": webapi.ID(), "image_digest": "sha256:" + strings.Repeat("a", 64), "spec": map[string]any{"id": webapi.ID(), "cpu_millicores": 2000, "memory_bytes": 4 << 30, "storage_bytes": 10 << 30}, "model_configuration_id": model, "model_configuration_version": 1, "provider": "deepseek-official", "protocol": "deepseek-messages", "model": "test-model"}
	encoded, _ := json.Marshal(snapshot)
	create := func(p identity.Principal) string {
		t.Helper()
		id := webapi.ID()
		name, _ := workspaceengine.WorkspaceName(id)
		must(`INSERT INTO instances(id,org_id,owner_id,name,external_create_key,workspace_name,desired_state,observed_state,model_config_id,model_config_version,config_snapshot,created_at) VALUES($1,$2,$3,$4,$1,$5,'STOPPED','STOPPED',$6,1,$7,'2026-10-10T00:00:00Z')`, id, org, p.UserID, "w-"+id[:12], name, model, encoded)
		return id
	}
	ids := map[string]bool{}
	for range 5 {
		ids[create(owner)] = true
	}
	foreign := create(other)
	codec, _ := pagination.NewCodec(bytes.Repeat([]byte{9}, 32), "test")
	service := NewQueryService(appDB, codec)
	t.Run("Chinese prefix and underscore search are literal and owner scoped", func(t *testing.T) {
		var fixture string
		for id := range ids {
			fixture = id
			break
		}
		must(`UPDATE instances SET name='研发_工作空间' WHERE id=$1`, fixture)
		defer must(`UPDATE instances SET name=$2 WHERE id=$1`, fixture, "w-"+fixture[:12])
		for _, prefix := range []string{"研发", "研发_"} {
			result, err := service.List(ctx, owner, ListInput{Query: prefix})
			if err != nil || result.Total != 1 || len(result.Items) != 1 || result.Items[0].ID != fixture {
				t.Fatal("Chinese name search failed", err)
			}
		}
		result, err := service.List(ctx, owner, ListInput{Query: "_"})
		if err != nil || result.Total != 0 {
			t.Fatal("underscore matched unrelated names", err)
		}
	})
	page, err := service.List(ctx, owner, ListInput{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.NextCursor == nil || page.Total != 5 {
		t.Fatalf("first page: %+v", page)
	}
	seen := map[string]bool{}
	var ordered []string
	for {
		for _, item := range page.Items {
			if !ids[item.ID] || seen[item.ID] || item.Ready || item.CanEnter {
				t.Fatalf("owner page leaked/duplicated/fabricated readiness: %+v", item)
			}
			seen[item.ID] = true
			ordered = append(ordered, item.ID)
		}
		if page.NextCursor == nil {
			break
		}
		page, err = service.List(ctx, owner, ListInput{Limit: 2, Cursor: *page.NextCursor})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 5 {
		t.Fatalf("keyset omitted equal-time rows: %d", len(seen))
	}
	tail, err := service.List(ctx, owner, ListInput{Limit: 2, Page: 3})
	if err != nil || tail.Total != 5 || len(tail.Items) != 1 || tail.Items[0].ID != ordered[4] || tail.NextCursor != nil {
		t.Fatalf("direct instance tail: %+v %v", tail, err)
	}
	otherPage, err := service.List(ctx, other, ListInput{Limit: 2, Page: 1})
	if err != nil || otherPage.Total != 1 || len(otherPage.Items) != 1 || otherPage.Items[0].ID != foreign {
		t.Fatal("numbered totals leaked another owner", err)
	}
	filtered, err := service.List(ctx, owner, ListInput{Limit: 2, Page: 1, Query: "absent"})
	if err != nil || filtered.Total != 0 || len(filtered.Items) != 0 {
		t.Fatal("instance count ignored name filter", err)
	}
	first, err := service.List(ctx, owner, ListInput{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.List(ctx, other, ListInput{Limit: 2, Cursor: *first.NextCursor}); queryCode(err) != "INVALID_CURSOR" {
		t.Fatalf("cross-owner cursor: %v", err)
	}
	for _, p := range []identity.Principal{owner, admin} {
		if _, err := service.Get(ctx, p, foreign); queryCode(err) != "NOT_FOUND" {
			t.Fatalf("admin inherited foreign access: %v", err)
		}
	}
	own := first.Items[0].ID
	operation := webapi.ID()
	must(`INSERT INTO operations(id,instance_id,actor_id,type,status,stage,intent,trace_id,error_code,error_message) VALUES($1,$2,$3,'START','FAILED','COMPLETE','{}','accepted-trace','WORKSPACE_UNAVAILABLE','private upstream raw diagnostic')`, operation, own, owner.UserID)
	result, err := service.Operation(ctx, owner, operation)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(result)
	if strings.Contains(string(out), "private upstream") || result.Error == nil || result.Error.RequestID != "accepted-trace" {
		t.Fatal("asynchronous error leaked or lost original request ID")
	}
	if _, err := service.Operation(ctx, other, operation); queryCode(err) != "NOT_FOUND" {
		t.Fatalf("foreign operation: %v", err)
	}
	for _, eventOwner := range []string{owner.UserID, owner.UserID, other.UserID} {
		must(`INSERT INTO audit_events(id,org_id,owner_id,resource_id,action,outcome,trace_id) VALUES($1,$2,$3,$4,'QUERY_PROOF','SUCCEEDED','safe-trace')`, webapi.ID(), org, eventOwner, own)
	}
	forged := owner
	forged.Role = "ADMIN"
	operationLogs, err := service.OperationLogs(ctx, forged, OperationLogInput{Action: "QUERY_PROOF", ResourceID: own})
	if err != nil || len(operationLogs.Items) != 2 || operationLogs.Total != 2 {
		t.Fatalf("operation log scope trusted forged role: %+v %v", operationLogs, err)
	}
	operationLogs, err = service.OperationLogs(ctx, admin, OperationLogInput{Action: "QUERY_PROOF", ResourceID: own, Limit: 1})
	if err != nil || len(operationLogs.Items) != 1 || operationLogs.NextCursor == nil {
		t.Fatalf("admin operation log pagination: %+v %v", operationLogs, err)
	}
	operationLogTail, err := service.OperationLogs(ctx, admin, OperationLogInput{Action: "QUERY_PROOF", ResourceID: own, Limit: 1, Page: 3})
	if err != nil || operationLogTail.Total != 3 || len(operationLogTail.Items) != 1 || operationLogTail.NextCursor != nil {
		t.Fatalf("direct operation log tail: %+v %v", operationLogTail, err)
	}
	if _, err := service.OperationLogs(ctx, owner, OperationLogInput{Action: "QUERY_PROOF", ResourceID: own, Cursor: *operationLogs.NextCursor}); queryCode(err) != "INVALID_CURSOR" {
		t.Fatalf("admin operation log cursor crossed subject: %v", err)
	}
	t.Run("operation log renders names and typed changes within the existing owner scope", func(t *testing.T) {
		must(`UPDATE users SET display_name='Log operator' WHERE id=$1`, admin.UserID)
		must(`INSERT INTO audit_events(id,org_id,actor_id,owner_id,resource_id,action,outcome,trace_id,details) VALUES($1,$2,$3,$4,$4,'USER_QUOTA_UPDATE','SUCCEEDED','safe-quota-trace','{"before":{"max_instances":1,"api_key":"never-expose"},"after":{"max_instances":2},"request_body":"never-expose"}')`, webapi.ID(), org, admin.UserID, owner.UserID)
		page, err := service.OperationLogs(ctx, owner, OperationLogInput{Action: "USER_QUOTA_UPDATE", Outcome: "SUCCEEDED"})
		if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ActorName != "Log operator" || page.Items[0].ResourceType != "user" || page.Items[0].OwnerID == nil {
			t.Fatal("operation log names or detail scope missing", err)
		}
		encoded, _ := json.Marshal(page)
		if strings.Contains(string(encoded), "never-expose") || len(page.Items[0].Details) != 2 {
			t.Fatal("unsafe operation log detail returned")
		}
		for _, input := range []OperationLogInput{{Action: "USER_QUOTA_UPDATE", Outcome: "FAILED"}, {Action: "USER_QUOTA_UPDATE", Outcome: "unknown"}} {
			page, err := service.OperationLogs(ctx, owner, input)
			if input.Outcome == "FAILED" && (err != nil || page.Total != 0) || input.Outcome == "unknown" && err == nil {
				t.Fatal("outcome filter bypassed", err)
			}
		}
		foreign, err := service.OperationLogs(ctx, other, OperationLogInput{Action: "USER_QUOTA_UPDATE"})
		if err != nil || foreign.Total != 0 {
			t.Fatal("foreign operation log changes exposed", err)
		}
	})

	asset := webapi.ID()
	must(`INSERT INTO storage_assets(id,org_id,owner_id,instance_id,resource_name,filesystem_id,capacity_bytes,init_version,status) VALUES($1,$2,$3,$4,$5,$1::uuid::text,10737418240,1,'READY')`, asset, org, owner.UserID, own, "hc-"+asset)
	must(`UPDATE instances SET storage_asset_id=$2,observed_state='RUNNING',desired_state='RUNNING' WHERE id=$1`, own, asset)
	must(`INSERT INTO access_routes(instance_id,host,upstream_ip,runtime_generation,enabled) VALUES($1,$2,'172.18.0.5',$3,true)`, own, "i-"+own+".dsh.test", webapi.ID())
	for _, dimension := range []Dimension{Application, Storage, Configuration, Access} {
		must(`INSERT INTO instance_health(instance_id,dimension,status,checked_at,expires_at) VALUES($1,$2,'HEALTHY',clock_timestamp(),clock_timestamp()+interval '1 minute')`, own, dimension)
	}
	ready, err := service.Get(ctx, owner, own)
	if err != nil || !ready.Ready || !ready.CanEnter {
		t.Fatalf("fresh persisted health: %+v %v", ready, err)
	}
	must(`UPDATE model_validations SET status='CHECKING',checked_at=NULL,expires_at=NULL,lease_expires_at=clock_timestamp()+interval '40 seconds' WHERE config_id=$1 AND config_version=1`, model)
	checking, err := service.Get(ctx, owner, own)
	if err != nil || checking.Ready || !checking.CanEnter || checking.Health[Model].Status != "CHECKING" || checking.Health[Model].ExpiresAt == nil {
		t.Fatalf("active model validation did not expose its bounded lease: %+v %v", checking.Health[Model], err)
	}
	must(`UPDATE model_validations SET checked_at=clock_timestamp(),expires_at=clock_timestamp()+interval '5 minutes',lease_expires_at=clock_timestamp()-interval '1 second' WHERE config_id=$1 AND config_version=1`, model)
	checking, err = service.Get(ctx, owner, own)
	if err != nil || checking.Health[Model].Status != "UNKNOWN" || checking.Ready || !checking.CanEnter {
		t.Fatalf("expired validation lease remained current: %+v %v", checking.Health[Model], err)
	}
	must(`UPDATE model_validations SET status='HEALTHY',checked_at=clock_timestamp(),expires_at=clock_timestamp()+interval '5 minutes',lease_expires_at=NULL WHERE config_id=$1 AND config_version=1`, model)
	must(`UPDATE instance_health SET status='UNHEALTHY',checked_at=clock_timestamp()-interval '2 minutes',expires_at=clock_timestamp()-interval '1 minute',reason_code='HEALTH_TIMEOUT' WHERE instance_id=$1 AND dimension='application'`, own)
	stale, err := service.Get(ctx, owner, own)
	if err != nil || stale.Health[Application].Status != "UNKNOWN" || stale.CanEnter {
		t.Fatalf("stale application failure remained a current observation: %+v %v", stale.Health[Application], err)
	}
	must(`UPDATE instance_health SET status='HEALTHY',checked_at=clock_timestamp(),expires_at=clock_timestamp()+interval '1 minute',reason_code='' WHERE instance_id=$1 AND dimension='application'`, own)
	output, _ := json.Marshal(ready)
	if strings.Contains(string(output), "172.18.0.5") || strings.Contains(string(output), "credential_ref") {
		t.Fatal("query leaked internal routing or credential reference")
	}
	must(`UPDATE model_validations SET checked_at=clock_timestamp()-interval '10 minutes',expires_at=clock_timestamp()-interval '5 minutes' WHERE config_id=$1 AND config_version=1`, model)
	degraded, err := service.Get(ctx, owner, own)
	if err != nil || degraded.Ready || !degraded.CanEnter || degraded.Health[Model].Status != "UNKNOWN" {
		t.Fatalf("model health expiry blocked safe diagnostic entry: %+v %v", degraded, err)
	}
	must(`UPDATE secrets SET revoked_at=clock_timestamp() WHERE id=(SELECT credential_ref FROM model_configurations WHERE id=$1 AND version=1)`, model)
	revoked, err := service.Get(ctx, owner, own)
	if err != nil || revoked.Ready || revoked.CanEnter || revoked.Health[Configuration].Reason != "CREDENTIAL_REVOKED" {
		t.Fatalf("revoked credential allowed entry: %+v %v", revoked, err)
	}
	caller := principal("MEMBER")
	closed := NewCommandService(appDB, nil, false)
	if _, err := closed.Create(ctx, caller, webapi.ID(), CreateInput{}, "closed"); queryCode(err) != "DEPENDENCY_UNAVAILABLE" {
		t.Fatalf("missing runtime gate accepted: %v", err)
	}
	must(`UPDATE secrets SET revoked_at=NULL WHERE id=(SELECT credential_ref FROM model_configurations WHERE id=$1 AND version=1)`, model)
	must(`UPDATE model_validations SET status='HEALTHY',checked_at=clock_timestamp(),expires_at=clock_timestamp()+interval '5 minutes' WHERE config_id=$1 AND config_version=1`, model)
	template, version, spec := webapi.ID(), webapi.ID(), webapi.ID()
	must(`UPDATE approved_templates SET enabled=false WHERE enabled`)
	must(`UPDATE approved_specs SET enabled=false WHERE enabled`)
	must(`INSERT INTO approved_templates(id,version,name,image_digest,runtime_manifest,enabled) VALUES($1,$2,'Fixture-only template',$3,'{}',true)`, template, version, "sha256:"+strings.Repeat("a", 64))
	must(`INSERT INTO approved_specs(id,cpu_millicores,memory_bytes,storage_bytes,enabled) VALUES($1,2000,4294967296,10737418240,true)`, spec)
	must(`INSERT INTO organization_defaults(org_id,version,template_id,template_version,spec_id,model_config_id,model_config_version) VALUES($1,1,$2,$3,$4,$5,1) ON CONFLICT(org_id) DO UPDATE SET version=organization_defaults.version+1,template_id=EXCLUDED.template_id,template_version=EXCLUDED.template_version,spec_id=EXCLUDED.spec_id,model_config_id=EXCLUDED.model_config_id,model_config_version=1`, org, template, version, spec, model)
	must(`INSERT INTO quota_policies(org_id,max_instances,max_user_instances,cpu_millicores,memory_bytes,storage_bytes,max_concurrent_creates) VALUES($1,10000,2,20000000,42949672960000,107374182400000,10000) ON CONFLICT(org_id) DO UPDATE SET max_instances=10000,cpu_millicores=20000000,memory_bytes=42949672960000,storage_bytes=107374182400000,max_concurrent_creates=10000`, org)
	commands := NewCommandService(appDB, fixtureCreationGate{}, false)
	t.Run("selected resources are frozen and reserved exactly", func(t *testing.T) {
		p := principal("MEMBER")
		small := webapi.ID()
		must(`INSERT INTO approved_specs(id,cpu_millicores,memory_bytes,storage_bytes,enabled) VALUES($1,1000,2147483648,21474836480,true)`, small)
		defer must(`UPDATE approved_specs SET enabled=false WHERE id=$1`, small)
		if _, e := commands.Create(ctx, p, webapi.ID(), CreateInput{SpecID: &small}, "legacy-storage"); queryCode(e) != "CONFIGURATION_INVALID" {
			t.Fatal("legacy adapter accepted configurable storage", e)
		}
		must(`UPDATE approved_templates SET runtime_manifest='{"storage_adapter":"bounded-ext4-v1"}' WHERE id=$1 AND version=$2`, template, version)
		defer must(`UPDATE approved_templates SET runtime_manifest='{}' WHERE id=$1 AND version=$2`, template, version)
		key := webapi.ID()
		accepted, e := commands.Create(ctx, p, key, CreateInput{SpecID: &small}, "selected-spec")
		if e != nil {
			t.Fatal("approved nondefault resources rejected", e)
		}
		var cpu, memory, storage, storedCPU, storedMemory int64
		if e = database.QueryRow(ctx, `SELECT (config_snapshot->'spec'->>'cpu_millicores')::bigint,(config_snapshot->'spec'->>'memory_bytes')::bigint,(SELECT sum(amount) FROM quota_reservations WHERE instance_id=i.id AND resource_type='CPU'),(SELECT sum(amount) FROM quota_reservations WHERE instance_id=i.id AND resource_type='MEMORY'),(SELECT sum(amount) FROM quota_reservations WHERE instance_id=i.id AND resource_type='STORAGE') FROM instances i WHERE id=$1`, accepted.InstanceID).Scan(&storedCPU, &storedMemory, &cpu, &memory, &storage); e != nil || storage != 20<<30 || cpu != 1000 || storedCPU != 1000 || memory != 2<<30 || storedMemory != 2<<30 {
			t.Fatal("selected resources differ from frozen quota", e)
		}
		if _, e = commands.Create(ctx, p, key, CreateInput{SpecID: &spec}, "changed-spec"); queryCode(e) != "IDEMPOTENCY_CONFLICT" {
			t.Fatal("changed spec reused request", e)
		}
		must(`UPDATE approved_specs SET enabled=false WHERE id=$1`, small)
		if replay, e := commands.Create(ctx, p, key, CreateInput{SpecID: &small}, "replay"); e != nil || replay != accepted {
			t.Fatal("accepted spec receipt lost after disable", e)
		}
		if _, e = commands.Create(ctx, p, webapi.ID(), CreateInput{SpecID: &small}, "disabled-spec"); queryCode(e) != "INVALID_SPEC" {
			t.Fatal("disabled resource tuple accepted", e)
		}
		// Release only this fixture's held reservations; later quota tests own
		// independent users and should not inherit its running allowance.
		must(`UPDATE quota_reservations SET status='RELEASED' WHERE instance_id=$1`, accepted.InstanceID)
	})

	t.Run("individual resource limits reject without partial writes", func(t *testing.T) {
		p := principal("MEMBER")
		defer database.Exec(context.Background(), `DELETE FROM user_quota_policies WHERE org_id=$1 AND owner_id=$2`, org, p.UserID)
		for index := range 4 {
			limits := []int64{3, 6000, 12 << 30, 30 << 30}
			limits[index] = 0
			must(`INSERT INTO user_quota_policies(org_id,owner_id,max_instances,cpu_millicores,memory_bytes,storage_bytes) VALUES($1,$2,$3,$4,$5,$6)
              ON CONFLICT(org_id,owner_id) DO UPDATE SET max_instances=EXCLUDED.max_instances,cpu_millicores=EXCLUDED.cpu_millicores,memory_bytes=EXCLUDED.memory_bytes,storage_bytes=EXCLUDED.storage_bytes`, org, p.UserID, limits[0], limits[1], limits[2], limits[3])
			if _, err := commands.Create(ctx, p, webapi.ID(), CreateInput{}, "user-quota"); queryCode(err) != "QUOTA_EXCEEDED" {
				t.Fatal("individual cap bypassed", index, err)
			}
		}
		var count int
		if err := database.QueryRow(ctx, `SELECT count(*) FROM instances WHERE org_id=$1 AND owner_id=$2`, org, p.UserID).Scan(&count); err != nil || count != 0 {
			t.Fatal("rejected quota left an instance", count, err)
		}
		must(`UPDATE user_quota_policies SET storage_bytes=32212254720 WHERE org_id=$1 AND owner_id=$2`, org, p.UserID)
		orphan := webapi.ID()
		must(`INSERT INTO recovery_quota_holds(org_id,owner_id,instance_id,resource_type,amount,inventory_run_id,inventory_sha256,reason_code,operator_ref) VALUES($1,$2,$3,'STORAGE',22548578304,$4,$5,'TEST_RETAIN','quota-test')`, org, p.UserID, orphan, webapi.ID(), strings.Repeat("c", 64))
		if _, err := commands.Create(ctx, p, webapi.ID(), CreateInput{}, "personal-recovery-quota"); queryCode(err) != "QUOTA_EXCEEDED" {
			t.Fatal("recovery storage bypassed personal quota", err)
		}
		must(`DELETE FROM recovery_quota_holds WHERE org_id=$1 AND instance_id=$2`, org, orphan)
		// Personalized instance limits may exceed the default two instances.
		for range 3 {
			if _, err := commands.Create(ctx, p, webapi.ID(), CreateInput{}, "individual-three"); err != nil {
				t.Fatal("individual instance cap did not override default", err)
			}
		}
		if _, err := commands.Create(ctx, p, webapi.ID(), CreateInput{}, "individual-four"); queryCode(err) != "QUOTA_EXCEEDED" {
			t.Fatal("individual cap exceeded", err)
		}
	})
	t.Run("quota adjustment and creation share the admission lock", func(t *testing.T) {
		administrator, p := principal("ADMIN"), principal("MEMBER")
		must(`INSERT INTO user_quota_policies(org_id,owner_id,max_instances,cpu_millicores,memory_bytes,storage_bytes) VALUES($1,$2,2,4000,8589934592,21474836480)`, org, p.UserID)
		defer database.Exec(context.Background(), `DELETE FROM user_quota_policies WHERE org_id=$1 AND owner_id=$2`, org, p.UserID)
		management := identity.NewManagementService(appDB, nil, "", nil, nil, "test")
		var input identity.UserQuotaInput
		if err := json.Unmarshal([]byte(`{"expected_version":1,"limits":{"max_instances":1,"cpu_millicores":2000,"memory_bytes":4294967296,"storage_bytes":10737418240}}`), &input); err != nil {
			t.Fatal(err)
		}
		type result struct {
			adjustment bool
			err        error
		}
		results := make(chan result, 3)
		var workers sync.WaitGroup
		workers.Add(3)
		go func() {
			defer workers.Done()
			_, err := management.SaveUserQuota(ctx, administrator, p.UserID, input, "admission-race")
			results <- result{true, err}
		}()
		for range 2 {
			go func() {
				defer workers.Done()
				_, err := commands.Create(ctx, p, webapi.ID(), CreateInput{}, "admission-race")
				results <- result{false, err}
			}()
		}
		workers.Wait()
		close(results)
		for outcome := range results {
			if outcome.err == nil {
				continue
			}
			if outcome.adjustment && queryCode(outcome.err) == "QUOTA_BELOW_USAGE" {
				continue
			}
			if !outcome.adjustment && queryCode(outcome.err) == "QUOTA_EXCEEDED" {
				continue
			}
			t.Fatal("unexpected admission race failure", outcome.err)
		}
		view, err := management.GetUserQuota(ctx, administrator, p.UserID)
		if err != nil {
			t.Fatal(err)
		}
		caps := view.Limits.Resources()
		for resource, used := range view.Usage.Resources() {
			if used > caps[resource] {
				t.Fatal("adjustment race overcommitted", resource, used, caps[resource])
			}
		}
	})
	t.Run("private model bindings require owner and enabled version", func(t *testing.T) { testPersonalBinding(t, ctx, database, appDB, caller, other) })
	t.Run("recovery holds block normal restricted command acceptance", func(t *testing.T) {
		orphan := webapi.ID()
		defer database.Exec(context.Background(), `DELETE FROM recovery_quota_holds WHERE org_id=$1 AND instance_id=$2`, org, orphan)
		for _, test := range []struct {
			resource string
			amount   int64
		}{{"INSTANCE", 2}, {"RUNNING", 10000}, {"STORAGE", 107374182400000}} {
			must(`INSERT INTO recovery_quota_holds(org_id,owner_id,instance_id,resource_type,amount,inventory_run_id,inventory_sha256,reason_code,operator_ref) VALUES($1,$2,$3,$4,$5,$6,$7,'TEST_RETAIN','fixture')`, org, caller.UserID, orphan, test.resource, test.amount, webapi.ID(), strings.Repeat("a", 64))
			if _, err := commands.Create(ctx, caller, webapi.ID(), CreateInput{}, "recovery-held-quota"); queryCode(err) != "QUOTA_EXCEEDED" {
				t.Fatalf("retained %s did not block creation: %v", test.resource, err)
			}
			must(`DELETE FROM recovery_quota_holds WHERE org_id=$1 AND instance_id=$2`, org, orphan)
		}
		var count int
		if err := database.QueryRow(ctx, `SELECT count(*) FROM instances WHERE owner_id=$1`, caller.UserID).Scan(&count); err != nil || count != 0 {
			t.Fatal("quota rejection created a replacement instance", err)
		}
	})
	key := webapi.ID()
	chineseName := "研发工作空间_" + webapi.ID()[:8]
	var wg sync.WaitGroup
	acceptedResults := make(chan Accepted, 6)
	commandErrors := make(chan error, 6)
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			accepted, err := commands.Create(ctx, caller, key, CreateInput{Name: &chineseName}, "command-trace")
			acceptedResults <- accepted
			commandErrors <- err
		}()
	}
	wg.Wait()
	close(acceptedResults)
	close(commandErrors)
	for err := range commandErrors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var accepted Accepted
	for result := range acceptedResults {
		if accepted.InstanceID == "" {
			accepted = result
		}
		if result != accepted {
			t.Fatal("same-key race created different operations")
		}
	}
	var operations, assets, outboxes, ledger int
	if err := database.QueryRow(ctx, `SELECT (SELECT count(*) FROM operations WHERE instance_id=$1),(SELECT count(*) FROM storage_assets WHERE instance_id=$1),(SELECT count(*) FROM outbox_events WHERE operation_id=$2),(SELECT count(*) FROM quota_reservations WHERE instance_id=$1 AND status='HELD')`, accepted.InstanceID, accepted.OperationID).Scan(&operations, &assets, &outboxes, &ledger); err != nil {
		t.Fatal(err)
	}
	if operations != 1 || assets != 1 || outboxes != 1 || ledger != 6 {
		t.Fatalf("acceptance was not atomic: %d %d %d %d", operations, assets, outboxes, ledger)
	}
	name := "changed-name"
	if _, err := commands.Create(ctx, caller, key, CreateInput{Name: &name}, "conflict"); queryCode(err) != "IDEMPOTENCY_CONFLICT" {
		t.Fatalf("same key changed input: %v", err)
	}
	// Replays precede new-command checks and preserve their original snapshot.
	must(`UPDATE approved_templates SET enabled=false WHERE id=$1 AND version=$2`, template, version)
	replay, err := NewCommandService(appDB, nil, true).Create(ctx, caller, key, CreateInput{Name: &chineseName}, "replay")
	if err != nil || replay != accepted {
		t.Fatalf("maintenance/default change broke replay: %+v %v", replay, err)
	}
	must(`UPDATE approved_templates SET enabled=true WHERE id=$1 AND version=$2`, template, version)
	if _, err := commands.Create(ctx, caller, webapi.ID(), CreateInput{}, "second"); err != nil {
		t.Fatal(err)
	}
	if _, err := commands.Create(ctx, caller, webapi.ID(), CreateInput{}, "over-quota"); queryCode(err) != "QUOTA_EXCEEDED" {
		t.Fatalf("per-user quota exceeded: %v", err)
	}
	var count int
	if err := database.QueryRow(ctx, `SELECT count(*) FROM instances WHERE owner_id=$1`, caller.UserID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("quota rejection left partial instance: %d %v", count, err)
	}
	newCaller := principal("MEMBER")
	var originalName string
	if err := database.QueryRow(ctx, `SELECT name FROM instances WHERE id=$1`, accepted.InstanceID).Scan(&originalName); err != nil {
		t.Fatal(err)
	}
	if originalName != chineseName {
		t.Fatal("Chinese display name changed during creation")
	}
	if _, err := commands.Create(ctx, newCaller, webapi.ID(), CreateInput{Name: &originalName}, "name-conflict"); queryCode(err) != "NAME_CONFLICT" {
		t.Fatalf("duplicate name accepted: %v", err)
	}
	must(`UPDATE model_validations SET checked_at=clock_timestamp()-interval '10 minutes',expires_at=clock_timestamp()-interval '5 minutes' WHERE config_id=$1 AND config_version=1`, model)
	if _, err := commands.Create(ctx, newCaller, webapi.ID(), CreateInput{}, "expired-model"); queryCode(err) != "CONFIGURATION_INVALID" {
		t.Fatalf("expired validation accepted: %v", err)
	}
	must(`UPDATE model_validations SET checked_at=clock_timestamp(),expires_at=clock_timestamp()+interval '5 minutes' WHERE config_id=$1 AND config_version=1`, model)
	// Force a late write failure after instance, asset, operation and all ledger
	// rows have been inserted; PostgreSQL must roll the whole acceptance back.
	must(`CREATE OR REPLACE FUNCTION test_outbox_reject() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF EXISTS(SELECT 1 FROM operations WHERE id=NEW.operation_id AND trace_id='failure-injection') THEN RAISE EXCEPTION 'fixture failure'; END IF; RETURN NEW; END $$`)
	must(`CREATE TRIGGER test_outbox_reject BEFORE INSERT ON outbox_events FOR EACH ROW EXECUTE FUNCTION test_outbox_reject()`)
	if _, err := commands.Create(ctx, newCaller, webapi.ID(), CreateInput{}, "failure-injection"); err == nil {
		t.Fatal("injected late write failure accepted")
	}
	must(`DROP TRIGGER test_outbox_reject ON outbox_events`)
	if err := database.QueryRow(ctx, `SELECT count(*) FROM instances WHERE owner_id=$1`, newCaller.UserID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("late failure left partial instance: %d %v", count, err)
	}
	must(`UPDATE quota_policies SET max_concurrent_creates=1+(SELECT COALESCE(sum(amount),0) FROM quota_reservations WHERE org_id=$1 AND resource_type='CONCURRENT' AND status IN ('HELD','USED')) WHERE org_id=$1`, org)
	rivals := []identity.Principal{newCaller, principal("MEMBER")}
	quotaErrors := make(chan error, 2)
	for _, rival := range rivals {
		wg.Add(1)
		go func(p identity.Principal) {
			defer wg.Done()
			_, err := commands.Create(ctx, p, webapi.ID(), CreateInput{}, "last-slot")
			quotaErrors <- err
		}(rival)
	}
	wg.Wait()
	close(quotaErrors)
	wins, denied := 0, 0
	for err := range quotaErrors {
		if err == nil {
			wins++
		} else if queryCode(err) == "QUOTA_EXCEEDED" {
			denied++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || denied != 1 {
		t.Fatalf("cross-user quota race: %d wins, %d rejections", wins, denied)
	}
	// Safety commands revoke the route in their acceptance transaction and do
	// not require a usable model. These do not execute external compute effects.
	must(`UPDATE secrets SET revoked_at=clock_timestamp() WHERE id=(SELECT credential_ref FROM model_configurations WHERE id=$1 AND version=1)`, model)
	stopKey := webapi.ID()
	stopped, err := commands.Command(ctx, owner, own, Stop, stopKey, CommandInput{}, "stop-accepted")
	if err != nil || stopped.State != "STOPPING" {
		t.Fatalf("revoked model blocked stop: %+v %v", stopped, err)
	}
	var enabled bool
	var generation int64
	if err := database.QueryRow(ctx, `SELECT r.enabled,i.access_generation FROM instances i JOIN access_routes r ON r.instance_id=i.id WHERE i.id=$1`, own).Scan(&enabled, &generation); err != nil || enabled || generation != 2 {
		t.Fatalf("stop did not atomically revoke: %v %d %v", enabled, generation, err)
	}
	if replay, err := commands.Command(ctx, owner, own, Stop, stopKey, CommandInput{}, "stop-replay"); err != nil || replay != stopped {
		t.Fatalf("stop replay: %+v %v", replay, err)
	}
	if _, err := commands.Command(ctx, owner, own, Stop, webapi.ID(), CommandInput{}, "stop-duplicate"); queryCode(err) != "STATE_CONFLICT" {
		t.Fatalf("duplicate stop accepted: %v", err)
	}
	if _, err := commands.Command(ctx, admin, own, Stop, webapi.ID(), CommandInput{}, "foreign-stop"); queryCode(err) != "NOT_FOUND" {
		t.Fatalf("admin can stop foreign work: %v", err)
	}
	// Simulate only a worker's confirmed stop, then exercise the next command.
	must(`UPDATE operations SET status='SUCCEEDED',stage='COMPLETE',reconciled_at=clock_timestamp() WHERE id=$1`, stopped.OperationID)
	must(`UPDATE instances SET observed_state='STOPPED' WHERE id=$1`, own)
	if _, err := commands.Command(ctx, owner, own, Start, webapi.ID(), CommandInput{}, "invalid-start"); queryCode(err) != "CONFIGURATION_INVALID" {
		t.Fatalf("revoked model started: %v", err)
	}
	must(`UPDATE secrets SET revoked_at=NULL WHERE id=(SELECT credential_ref FROM model_configurations WHERE id=$1 AND version=1)`, model)
	must(`UPDATE quota_policies SET max_concurrent_creates=10000 WHERE org_id=$1`, org)
	expected := int64(1)
	modelVersion := int64(1)
	configure := CommandInput{ExpectedVersion: &expected, ModelID: &model, ModelVersion: &modelVersion}
	if _, err := commands.Command(ctx, owner, own, Configure, webapi.ID(), configure, "stale-configure"); queryCode(err) != "VERSION_CONFLICT" {
		t.Fatalf("stale configure accepted: %v", err)
	}
	expected = 2
	configured, err := commands.Command(ctx, owner, own, Configure, webapi.ID(), configure, "configure-stopped")
	if err != nil || configured.State != "STOPPED" {
		t.Fatalf("stopped configure started compute: %+v %v", configured, err)
	}
	must(`UPDATE operations SET status='SUCCEEDED',stage='COMPLETE',reconciled_at=clock_timestamp() WHERE id=$1`, configured.OperationID)
	wrong := "wrong-name"
	if _, err := commands.Command(ctx, owner, own, Delete, webapi.ID(), CommandInput{ConfirmedName: &wrong}, "delete-wrong"); queryCode(err) != "INVALID_SPEC" {
		t.Fatalf("unconfirmed delete accepted: %v", err)
	}
	startOwner := principal("MEMBER")
	startID := create(startOwner)
	startAsset := webapi.ID()
	must(`INSERT INTO storage_assets(id,org_id,owner_id,instance_id,resource_name,filesystem_id,capacity_bytes,init_version,status) VALUES($1,$2,$3,$4,$5,$1::uuid::text,9663676416,1,'READY')`, startAsset, org, startOwner.UserID, startID, "hc-"+startAsset)
	must(`UPDATE instances SET storage_asset_id=$2 WHERE id=$1`, startID, startAsset)
	if _, err := commands.Command(ctx, startOwner, startID, Start, webapi.ID(), CommandInput{}, "wrong-capacity-start"); queryCode(err) != "STORAGE_MISSING" {
		t.Fatalf("different original capacity was accepted: %v", err)
	}
	must(`UPDATE storage_assets SET capacity_bytes=10737418240 WHERE id=$1`, startAsset)
	must(`UPDATE storage_assets SET init_version=0 WHERE id=$1`, startAsset)
	if _, err := commands.Command(ctx, startOwner, startID, Start, webapi.ID(), CommandInput{}, "uninitialized-start"); queryCode(err) != "STORAGE_MISSING" {
		t.Fatalf("uninitialized original asset was accepted: %v", err)
	}
	must(`UPDATE storage_assets SET init_version=1,filesystem_id=NULL WHERE id=$1`, startAsset)
	if _, err := commands.Command(ctx, startOwner, startID, Start, webapi.ID(), CommandInput{}, "unidentified-start"); queryCode(err) != "STORAGE_MISSING" {
		t.Fatalf("unidentified original asset was accepted: %v", err)
	}
	if view, err := service.Get(ctx, startOwner, startID); err != nil || strings.Contains(strings.Join(view.AllowedActions, ","), "start") {
		t.Fatalf("query offered an unsafe start: %+v %v", view.AllowedActions, err)
	}
	must(`UPDATE storage_assets SET filesystem_id=$2 WHERE id=$1`, startAsset, startAsset)
	must(`INSERT INTO user_quota_policies(org_id,owner_id,max_instances,cpu_millicores,memory_bytes,storage_bytes) VALUES($1,$2,2,0,8589934592,21474836480)`, org, startOwner.UserID)
	if _, err := commands.Command(ctx, startOwner, startID, Start, webapi.ID(), CommandInput{}, "user-start-quota"); queryCode(err) != "QUOTA_EXCEEDED" {
		t.Fatal("start bypassed user compute cap", err)
	}
	must(`DELETE FROM user_quota_policies WHERE org_id=$1 AND owner_id=$2`, org, startOwner.UserID)
	started, err := commands.Command(ctx, startOwner, startID, Start, webapi.ID(), CommandInput{}, "start-stopped")
	if err != nil || started.State != "STARTING" {
		t.Fatalf("start was not accepted: %+v %v", started, err)
	}
	var startReservations int
	if err := database.QueryRow(ctx, `SELECT count(*) FROM quota_reservations WHERE operation_id=$1 AND resource_type IN ('CPU','MEMORY','RUNNING','CONCURRENT') AND status='HELD'`, started.OperationID).Scan(&startReservations); err != nil || startReservations != 4 {
		t.Fatalf("start reservations: %d %v", startReservations, err)
	}
	var duplicateStorage bool
	if err := database.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM quota_reservations WHERE operation_id=$1 AND resource_type IN ('INSTANCE','STORAGE'))`, started.OperationID).Scan(&duplicateStorage); err != nil || duplicateStorage {
		t.Fatal("start reserved retained data twice")
	}
	confirmed := first.Items[0].Name
	deleted, err := NewCommandService(appDB, nil, true).Command(ctx, owner, own, Delete, webapi.ID(), CommandInput{ConfirmedName: &confirmed}, "delete-maintenance")
	if err != nil || deleted.State != "DELETING" {
		t.Fatalf("maintenance blocked safe delete: %+v %v", deleted, err)
	}

	t.Run("error instance exposes and accepts deletion without a valid model", func(t *testing.T) {
		errorOwner := principal("MEMBER")
		broken := create(errorOwner)
		must(`UPDATE instances SET observed_state='ERROR',desired_state='RUNNING' WHERE id=$1`, broken)
		must(`UPDATE secrets SET revoked_at=clock_timestamp() WHERE id=(SELECT credential_ref FROM model_configurations WHERE id=$1 AND version=1)`, model)
		defer must(`UPDATE secrets SET revoked_at=NULL WHERE id=(SELECT credential_ref FROM model_configurations WHERE id=$1 AND version=1)`, model)
		view, err := service.Get(ctx, errorOwner, broken)
		if err != nil || strings.Join(view.AllowedActions, ",") != "delete" || view.CanEnter {
			t.Fatalf("error instance cannot delete: %+v %v", view.AllowedActions, err)
		}
		if _, err = commands.Command(ctx, admin, broken, Delete, webapi.ID(), CommandInput{ConfirmedName: &view.Name}, "error-foreign"); queryCode(err) != "NOT_FOUND" {
			t.Fatal("foreign error deletion", err)
		}
		wrong := "wrong-name"
		if _, err = commands.Command(ctx, errorOwner, broken, Delete, webapi.ID(), CommandInput{ConfirmedName: &wrong}, "error-wrong"); queryCode(err) != "INVALID_SPEC" {
			t.Fatal("error deletion accepted wrong name", err)
		}
		key := webapi.ID()
		accepted, err := NewCommandService(appDB, nil, true).Command(ctx, errorOwner, broken, Delete, key, CommandInput{ConfirmedName: &view.Name}, "error-delete")
		if err != nil || accepted.State != "DELETING" {
			t.Fatalf("error deletion: %+v %v", accepted, err)
		}
		if replay, err := commands.Command(ctx, errorOwner, broken, Delete, key, CommandInput{ConfirmedName: &view.Name}, "error-replay"); err != nil || replay != accepted {
			t.Fatal("error delete replay", err)
		}
		if _, err = commands.Command(ctx, errorOwner, broken, Delete, webapi.ID(), CommandInput{ConfirmedName: &view.Name}, "error-duplicate"); queryCode(err) != "STATE_CONFLICT" {
			t.Fatal("duplicate error cleanup", err)
		}
		after, err := service.Get(ctx, errorOwner, broken)
		if err != nil || len(after.AllowedActions) != 0 || after.ActiveOperationID == nil || after.Observed != "DELETING" || after.DeletedAt != nil {
			t.Fatal("queued cleanup claimed completion", err)
		}
	})
	var assetStatus string
	if err := database.QueryRow(ctx, `SELECT status FROM storage_assets WHERE id=$1`, asset).Scan(&assetStatus); err != nil || assetStatus != "READY" {
		t.Fatal("acceptance reclaimed original storage")
	}
	// The production HTTP adapter must preserve the accepted response on replay
	// even in maintenance with no runtime gate, and must retain owner visibility.
	for _, tc := range []struct {
		principal identity.Principal
		code      int
	}{{owner, 202}, {admin, 404}} {
		r := httptest.NewRequest("POST", "/v1/instances/"+own+":stop", nil)
		r.Header.Set("Idempotency-Key", stopKey)
		w := httptest.NewRecorder()
		NewCommandHandler(NewCommandService(appDB, nil, true), nil).ServeAuthenticated(w, r, tc.principal)
		if w.Code != tc.code {
			t.Fatalf("restricted HTTP replay status: %d", w.Code)
		}
		if tc.code == 202 {
			var result Accepted
			if json.Unmarshal(w.Body.Bytes(), &result) != nil || result != stopped {
				t.Fatal("HTTP replay lost original acceptance")
			}
		}
	}
	t.Run("disabled during later lock wait never accepts a command", func(t *testing.T) {
		blockedOwner := principal("MEMBER")
		blockedID := create(blockedOwner)
		var name string
		if err := database.QueryRow(ctx, `SELECT name FROM instances WHERE id=$1`, blockedID).Scan(&name); err != nil {
			t.Fatal(err)
		}
		lock, err := database.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer lock.Rollback(context.Background())
		if _, err = lock.Exec(ctx, `SELECT id FROM instances WHERE id=$1 FOR UPDATE`, blockedID); err != nil {
			t.Fatal(err)
		}
		finished := make(chan error, 1)
		go func() {
			_, err := commands.Command(ctx, blockedOwner, blockedID, Delete, webapi.ID(), CommandInput{ConfirmedName: &name}, "disabled-during-wait")
			finished <- err
		}()
		deadline := time.Now().Add(3 * time.Second)
		for {
			var waiting bool
			if err := database.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%SELECT i.observed_state%')`).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("command did not reach instance lock barrier")
			}
			time.Sleep(5 * time.Millisecond)
		}
		must(`UPDATE users SET status='DISABLED' WHERE id=$1`, blockedOwner.UserID)
		if err := lock.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if err := <-finished; !errors.Is(err, identity.ErrUnauthenticated) {
			t.Fatalf("disabled principal accepted after lock wait: %v", err)
		}
		var count int
		if err := database.QueryRow(ctx, `SELECT count(*) FROM operations WHERE instance_id=$1`, blockedID).Scan(&count); err != nil || count != 0 {
			t.Fatal("unauthorized operation persisted")
		}
	})
	t.Run("retry preserves identity and re-reserves only released resources", func(t *testing.T) {
		p := principal("MEMBER")
		instance := create(p)
		asset, failed := webapi.ID(), webapi.ID()
		must(`INSERT INTO storage_assets(id,org_id,owner_id,instance_id,resource_name,filesystem_id,capacity_bytes,init_version,status) VALUES($1,$2,$3,$4,$5,$1::uuid::text,10737418240,1,'READY')`, asset, org, p.UserID, instance, "hc-"+asset)
		must(`UPDATE instances SET storage_asset_id=$2,observed_state='ERROR',version=3 WHERE id=$1`, instance, asset)
		intent, _ := json.Marshal(retryIntent{Schema: 1, Expected: 2, Desired: "RUNNING", Reason: "USER_REQUEST", Request: "failed-trace", Generation: p.Generation, Target: encoded})
		must(`INSERT INTO operations(id,instance_id,actor_id,type,status,stage,intent,trace_id,error_code,reconciled_at) VALUES($1,$2,$3,'CREATE','FAILED','COMPLETE',$4,'failed-trace','WORKSPACE_UNAVAILABLE',clock_timestamp())`, failed, instance, p.UserID, intent)
		for resource, amount := range map[string]int64{"INSTANCE": 1, "STORAGE": 10 << 30} {
			must(`INSERT INTO quota_reservations(id,org_id,owner_id,instance_id,operation_id,resource_type,amount,status) VALUES($1,$2,$3,$4,$5,$6,$7,'USED')`, webapi.ID(), org, p.UserID, instance, failed, resource, amount)
		}
		limit := func(context.Context, string, int) error { return nil }
		key := webapi.ID()
		if _, err := commands.Retry(ctx, other, failed, key, "foreign", limit); queryCode(err) != "NOT_FOUND" {
			t.Fatal("foreign retry accepted", err)
		}
		must(`UPDATE quota_policies SET max_instances=10000,cpu_millicores=20000000,memory_bytes=42949672960000,storage_bytes=107374182400000,max_concurrent_creates=10000 WHERE org_id=$1`, org)
		must(`INSERT INTO user_quota_policies(org_id,owner_id,max_instances,cpu_millicores,memory_bytes,storage_bytes) VALUES($1,$2,2,0,8589934592,21474836480)`, org, p.UserID)
		if _, err := commands.Retry(ctx, p, failed, key, "user-retry-quota", limit); queryCode(err) != "QUOTA_EXCEEDED" {
			t.Fatal("retry bypassed user compute cap", err)
		}
		must(`DELETE FROM user_quota_policies WHERE org_id=$1 AND owner_id=$2`, org, p.UserID)
		accepted, err := commands.Retry(ctx, p, failed, key, "retry-trace", limit)
		if err != nil || accepted.InstanceID != instance || accepted.State != "CREATING" {
			t.Fatal("retry acceptance", accepted, err)
		}
		replay, err := NewCommandService(appDB, nil, true).Retry(ctx, p, failed, key, "replay", nil)
		if err != nil || replay != accepted {
			t.Fatal("retry replay failed", replay, err)
		}
		if _, err := commands.Retry(ctx, p, failed, webapi.ID(), "second", limit); queryCode(err) != "STATE_CONFLICT" {
			t.Fatal("parallel retry accepted", err)
		}
		var sourceID, kind, storedAsset string
		var expected, instanceCount, storageCount, cpu int64
		if err = database.QueryRow(ctx, `SELECT o.retry_of::text,o.type,(o.intent->>'expected_instance_version')::bigint,i.storage_asset_id::text,(SELECT COALESCE(sum(amount) FILTER(WHERE resource_type='INSTANCE'),0) FROM quota_reservations WHERE instance_id=i.id AND status IN ('USED','HELD')),(SELECT COALESCE(sum(amount) FILTER(WHERE resource_type='STORAGE'),0) FROM quota_reservations WHERE instance_id=i.id AND status IN ('USED','HELD')),(SELECT COALESCE(sum(amount) FILTER(WHERE resource_type='CPU'),0) FROM quota_reservations WHERE instance_id=i.id AND status IN ('USED','HELD')) FROM operations o JOIN instances i ON i.id=o.instance_id WHERE o.id=$1`, accepted.OperationID).Scan(&sourceID, &kind, &expected, &storedAsset, &instanceCount, &storageCount, &cpu); err != nil || sourceID != failed || kind != "CREATE" || expected != 4 || storedAsset != asset || instanceCount != 1 || storageCount != 10<<30 || cpu != 2000 {
			t.Fatal("retry changed identity or double charged storage", err)
		}
		must(`UPDATE operations SET status='FAILED',stage='COMPLETE',reconciled_at=clock_timestamp(),error_code='DEPENDENCY_UNAVAILABLE' WHERE id=$1`, accepted.OperationID)
		must(`UPDATE instances SET observed_state='ERROR',version=version+1 WHERE id=$1`, instance)
		must(`UPDATE quota_reservations SET status='RELEASED' WHERE instance_id=$1 AND resource_type IN ('CPU','MEMORY','RUNNING','CONCURRENT')`, instance)
		if _, err := commands.Retry(ctx, p, failed, webapi.ID(), "old-source", limit); queryCode(err) != "STATE_CONFLICT" {
			t.Fatal("older failure accepted", err)
		}
		must(`UPDATE model_validations SET expires_at=clock_timestamp()-interval '1 second',checked_at=clock_timestamp()-interval '1 minute' WHERE config_id=$1`, model)
		if _, err := commands.Retry(ctx, p, accepted.OperationID, webapi.ID(), "expired", limit); queryCode(err) != "CONFIGURATION_INVALID" {
			t.Fatal("retry silently switched invalid model", err)
		}
		must(`UPDATE model_validations SET expires_at=clock_timestamp()+interval '5 minutes',checked_at=clock_timestamp() WHERE config_id=$1`, model)
		must(`UPDATE operations SET type='STOP',intent=jsonb_build_object('schema_version',1,'expected_instance_version',5,'target_desired_state','STOPPED','reason','USER_REQUEST','request_id','stop-retry','accepted_auth_generation',1) WHERE id=$1`, accepted.OperationID)
		must(`UPDATE secrets SET revoked_at=clock_timestamp() WHERE id=(SELECT credential_ref FROM model_configurations WHERE id=$1 AND version=1)`, model)
		must(`INSERT INTO user_quota_policies(org_id,owner_id,max_instances,cpu_millicores,memory_bytes,storage_bytes) VALUES($1,$2,2,0,0,21474836480)`, org, p.UserID)
		defer database.Exec(context.Background(), `DELETE FROM user_quota_policies WHERE org_id=$1 AND owner_id=$2`, org, p.UserID)
		if _, err := NewCommandService(appDB, nil, true).Retry(ctx, p, accepted.OperationID, webapi.ID(), "safety", nil); err != nil {
			t.Fatal("revoked model or Redis blocked safety retry", err)
		}
		must(`UPDATE secrets SET revoked_at=NULL WHERE id=(SELECT credential_ref FROM model_configurations WHERE id=$1 AND version=1)`, model)
	})
	t.Run("model expiry during quota wait never reserves a new instance", func(t *testing.T) {
		caller := principal("MEMBER")
		must(`UPDATE quota_policies SET max_instances=10000 WHERE org_id=$1`, org)
		lock, err := database.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer lock.Rollback(context.Background())
		if _, err := lock.Exec(ctx, `SELECT org_id FROM quota_policies WHERE org_id=$1 FOR UPDATE`, org); err != nil {
			t.Fatal(err)
		}
		finished := make(chan error, 1)
		go func() {
			_, err := commands.Create(ctx, caller, webapi.ID(), CreateInput{}, "model-expired-during-wait")
			finished <- err
		}()
		deadline := time.Now().Add(3 * time.Second)
		for {
			var waiting bool
			if err := database.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%FROM quota_policies%')`).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("create did not reach quota lock barrier")
			}
			time.Sleep(5 * time.Millisecond)
		}
		must(`UPDATE model_validations SET checked_at=clock_timestamp()-interval '2 seconds',expires_at=clock_timestamp()-interval '1 second' WHERE config_id=$1 AND config_version=1`, model)
		if err := lock.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if err := <-finished; queryCode(err) != "CONFIGURATION_INVALID" {
			t.Fatalf("expired model accepted after quota wait: %v", err)
		}
		var count int
		if err := database.QueryRow(ctx, `SELECT count(*) FROM instances WHERE owner_id=$1`, caller.UserID).Scan(&count); err != nil || count != 0 {
			t.Fatal("expired model persisted a new instance")
		}
	})
	must(`UPDATE memberships SET auth_generation=auth_generation+1 WHERE org_id=$1 AND user_id=$2`, org, owner.UserID)
	if _, err := service.List(ctx, owner, ListInput{}); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("revoked generation returned resources: %v", err)
	}
}
func queryCode(err error) string {
	var problem *webapi.Problem
	if errors.As(err, &problem) {
		return problem.Code
	}
	return ""
}

// Only the external readiness boundary is synthesized here. All acceptance,
// authorization, snapshot, quota and idempotency behavior uses real PostgreSQL.
type fixtureCreationGate struct{}

func (fixtureCreationGate) Check(context.Context, identity.Principal) error { return nil }
