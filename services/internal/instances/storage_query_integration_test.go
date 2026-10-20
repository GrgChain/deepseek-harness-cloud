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
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/devlocal"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func TestStorageQueriesWithRestrictedPostgres(t *testing.T) {
	path := os.Getenv("HC_TEST_DB_DSN_FILE")
	if path == "" {
		t.Skip("isolated PostgreSQL required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(strings.TrimSpace(string(raw)))
	u.Path = "/hc_test_storage_query"
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	if err = db.Apply(ctx, u.String()); err != nil {
		t.Fatal(err)
	}
	adminDB, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer adminDB.Close()
	must := func(sql string, args ...any) {
		t.Helper()
		if _, e := adminDB.Exec(ctx, sql, args...); e != nil {
			t.Fatal(e)
		}
	}
	must(`TRUNCATE organizations CASCADE`)
	tx, _ := adminDB.Begin(ctx)
	if err = devlocal.GrantAPI(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	appRaw, err := os.ReadFile(filepath.Join(filepath.Dir(path), "api-dsn"))
	if err != nil {
		t.Fatal(err)
	}
	appURL, _ := url.Parse(strings.TrimSpace(string(appRaw)))
	appURL.Path = u.Path
	apiDB, err := pgxpool.New(ctx, appURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer apiDB.Close()
	service := NewQueryService(apiDB, nil)
	handler := NewQueryHandler(service)
	org, org2 := webapi.ID(), webapi.ID()
	must(`INSERT INTO organizations(id,name,status) VALUES($1,'Storage query','ACTIVE')`, org)
	principal := func(org, role string) identity.Principal {
		id, session := webapi.ID(), webapi.ID()
		hash := sha256.Sum256([]byte(session))
		must(`INSERT INTO users(id,identity_issuer,identity_subject,workspace_owner_id,status) VALUES($1::uuid,'https://storage.test',$1::text,$1::uuid,'ACTIVE')`, id)
		must(`INSERT INTO memberships(org_id,user_id,role,status) VALUES($1,$2,$3,'ACTIVE')`, org, id, role)
		must(`INSERT INTO portal_sessions(id,secret_sha256,org_id,user_id,auth_generation,csrf_sha256,csrf_cipher,csrf_key_id,csrf_nonce,expires_at) VALUES($1,$2,$3,$4,1,$2,'x','test',$5,clock_timestamp()+interval '1 hour')`, session, hash[:], org, id, bytes.Repeat([]byte{0}, 12))
		return identity.Principal{UserID: id, OrganizationID: org, Role: role, SessionID: session, Generation: 1}
	}
	owner, other, administrator := principal(org, "MEMBER"), principal(org, "MEMBER"), principal(org, "ADMIN")
	asset := func(p identity.Principal, name, status string) string {
		id, a, model, secret := webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID()
		must(`INSERT INTO secrets(id,org_id,key_id,nonce,ciphertext) VALUES($1,$2,'test',$3,'x')`, secret, p.OrganizationID, bytes.Repeat([]byte{0}, 12))
		must(`INSERT INTO model_configuration_heads(id,org_id) VALUES($1,$2) ON CONFLICT(org_id) DO NOTHING`, model, p.OrganizationID)
		if e := adminDB.QueryRow(ctx, `SELECT id::text FROM model_configuration_heads WHERE org_id=$1`, p.OrganizationID).Scan(&model); e != nil {
			t.Fatal(e)
		}
		must(`INSERT INTO model_configurations(org_id,id,version,provider,endpoint,protocol,model,credential_ref,status) VALUES($1,$2,1,'deepseek-official','https://api.deepseek.com/anthropic','deepseek-messages','fixture',$3,'VALID') ON CONFLICT(id,version) DO NOTHING`, p.OrganizationID, model, secret)
		must(`INSERT INTO instances(id,org_id,owner_id,name,workspace_name,external_create_key,desired_state,observed_state,model_config_id,model_config_version,config_snapshot) VALUES($1,$2,$3,$4,$5,$1,'STOPPED','STOPPED',$6,1,'{}')`, id, p.OrganizationID, p.UserID, name, "dsh-"+id[:12], model)
		must(`INSERT INTO storage_assets(id,org_id,owner_id,instance_id,resource_name,filesystem_id,capacity_bytes,init_version,status) VALUES($1::uuid,$2,$3,$4,$5,$1::text,10737418240,1,$6)`, a, p.OrganizationID, p.UserID, id, "hc-"+a, status)
		must(`UPDATE instances SET storage_asset_id=$2 WHERE id=$1`, id, a)
		return a
	}
	retained := asset(owner, "数据_保留", "RETAINED")
	stopped := asset(owner, "数据A停止", "READY")
	_ = asset(owner, "数据运行", "READY")
	foreignAsset := asset(other, "其他用户", "RETAINED")
	crossOrg := webapi.ID()
	impostor := owner
	impostor.OrganizationID = org2
	if _, e := service.StorageAsset(ctx, impostor, crossOrg); e == nil {
		t.Fatal("organization substitution accepted")
	}
	must(`UPDATE instances SET desired_state='DELETED',observed_state='DELETED',deleted_at=clock_timestamp()-interval '8 days' WHERE storage_asset_id=$1`, retained)
	must(`UPDATE storage_assets SET retention_until=clock_timestamp()-interval '1 day' WHERE id=$1`, retained)
	page, e := service.StorageAssets(ctx, owner, StorageListInput{Page: 1, Limit: 2})
	if e != nil || page.Total != 3 || len(page.Items) != 2 {
		t.Fatalf("owner pagination: %+v %v", page, e)
	}
	filtered, e := service.StorageAssets(ctx, owner, StorageListInput{Page: 1, Limit: 10, Query: "数据_", Status: "RETAINED"})
	if e != nil || filtered.Total != 1 || filtered.Items[0].ID != retained || !filtered.Items[0].RetentionExpired {
		t.Fatalf("literal prefix or retention lost: %+v %v", filtered, e)
	}
	if filtered.Items[0].Usage.Status != "UNSAMPLED" || filtered.Items[0].Usage.Used != nil {
		t.Fatal("uncollected shown as zero")
	}
	for _, p := range []identity.Principal{owner, administrator} {
		for _, id := range []string{foreignAsset, crossOrg} {
			if _, e = service.StorageAsset(ctx, p, id); e == nil {
				t.Fatal("foreign asset exposed")
			}
			if _, e = service.StorageBackups(ctx, p, id, 1, 10); e == nil {
				t.Fatal("foreign backups exposed")
			}
		}
	}
	for _, suffix := range []string{"", "/backups"} {
		w := httptest.NewRecorder()
		handler.ServeAuthenticated(w, httptest.NewRequest("GET", "/v1/storage-assets/"+foreignAsset+suffix, nil), administrator)
		if w.Code != 404 {
			t.Fatal("foreign asset did not return 404", w.Code)
		}
	}
	noRead := webapi.ID()
	must(`INSERT INTO roles(id,org_id,code,name,builtin) VALUES($1,$2,'storage_none','No storage',false)`, noRead, org)
	must(`UPDATE memberships SET role='storage_none' WHERE org_id=$1 AND user_id=$2`, org, other.UserID)
	w := httptest.NewRecorder()
	handler.ServeAuthenticated(w, httptest.NewRequest("GET", "/v1/storage-assets", nil), other)
	if w.Code != 403 {
		t.Fatal("removed permission not enforced", w.Code)
	}
	adminPage, e := service.StorageAssets(ctx, administrator, StorageListInput{Page: 1, Limit: 10})
	if e != nil || adminPage.Total != 0 {
		t.Fatal("admin inherited owner access", e)
	}
	must(`UPDATE storage_assets SET usage_used_bytes=0,usage_available_bytes=9000000000,usage_sampled_at=clock_timestamp(),usage_attempted_at=clock_timestamp() WHERE id=$1`, stopped)
	v, e := service.StorageAsset(ctx, owner, stopped)
	if e != nil || v.Usage.Status != "CURRENT" || v.Usage.Used == nil || *v.Usage.Used != 0 {
		t.Fatal("real zero use not preserved", v, e)
	}
	must(`UPDATE storage_assets SET usage_sampled_at=clock_timestamp()-interval '6 minutes' WHERE id=$1`, stopped)
	v, e = service.StorageAsset(ctx, owner, stopped)
	if e != nil || v.Usage.Status != "STALE" || v.Usage.SampledAt == nil {
		t.Fatal("stale observation not marked", e)
	}
	must(`UPDATE storage_assets SET usage_error=true WHERE id=$1`, stopped)
	v, e = service.StorageAsset(ctx, owner, stopped)
	if e != nil || v.Usage.Status != "UNAVAILABLE" {
		t.Fatal("failed probe not marked", e)
	}
	good, failed := webapi.ID(), webapi.ID()
	must(`INSERT INTO storage_backups(id,org_id,owner_id,instance_id,asset_id,capacity_bytes,template_version,image_digest,status,completed_at,verified_at,backup_sha256,archive_path,created_at) SELECT $2,a.org_id,a.owner_id,a.instance_id,a.id,a.capacity_bytes,$3,'sha256:fixture','SUCCEEDED',clock_timestamp(),clock_timestamp(),repeat('a',64),'/private/must-never-leak',clock_timestamp()-interval '1 minute' FROM storage_assets a WHERE a.id=$1`, retained, good, webapi.ID())
	must(`INSERT INTO storage_backups(id,org_id,owner_id,instance_id,asset_id,capacity_bytes,template_version,image_digest,status,archive_path) SELECT $2,a.org_id,a.owner_id,a.instance_id,a.id,a.capacity_bytes,$3,'sha256:fixture','FAILED','/private/must-never-leak' FROM storage_assets a WHERE a.id=$1`, retained, failed, webapi.ID())
	v, e = service.StorageAsset(ctx, owner, retained)
	if e != nil || v.LatestBackup == nil || v.LatestBackup.ID != failed || v.LatestVerifiedBackup == nil || v.LatestVerifiedBackup.ID != good {
		t.Fatal("failure replaced last verified backup", e)
	}
	request := httptest.NewRequest("GET", "/v1/storage-assets/"+retained+"/backups?page=1&limit=1", nil)
	record := httptest.NewRecorder()
	handler.ServeAuthenticated(record, request, owner)
	if record.Code != 200 || strings.Contains(record.Body.String(), "private") || strings.Contains(record.Body.String(), "sha256") || strings.Contains(record.Body.String(), "archive_path") {
		t.Fatal("private backup data leaked", record.Code, record.Body.String())
	}
	var history StorageBackupPage
	if json.Unmarshal(record.Body.Bytes(), &history) != nil || history.Total != 2 || len(history.Items) != 1 {
		t.Fatal("backup pagination invalid")
	}
	if _, e = apiDB.Exec(ctx, `SELECT archive_path FROM storage_backups LIMIT 1`); e == nil {
		t.Fatal("API role can read private backup path")
	}
	if _, e = apiDB.Exec(ctx, `UPDATE storage_assets SET usage_error=false WHERE id=$1`, retained); e == nil {
		t.Fatal("API role can publish physical usage")
	}
	for _, path := range []string{"/v1/storage-assets?status=unknown", "/v1/storage-assets?page=0", "/v1/storage-assets?page=1&page=2", "/v1/storage-assets/" + retained + "?q=x"} {
		w := httptest.NewRecorder()
		handler.ServeAuthenticated(w, httptest.NewRequest("GET", path, nil), owner)
		if w.Code != 422 {
			t.Fatal("invalid query accepted", path, w.Code)
		}
	}
	// Owner-confirmed release can waive retention, but cannot waive ownership,
	// permission, legal hold, active work, confirmation or conservative accounting.
	commands := NewCommandService(apiDB, nil, false)
	input := StorageReleaseInput{ConfirmedName: "数据_保留", ConfirmedAssetID: retained}
	must(`INSERT INTO quota_policies(org_id) VALUES($1)`, org)
	if _, err := commands.ReleaseStorage(ctx, administrator, retained, input, "test"); err == nil {
		t.Fatal("administrator released another owner's data")
	}
	if _, err := commands.ReleaseStorage(ctx, other, retained, input, "test"); err == nil {
		t.Fatal("missing release permission accepted")
	}
	if _, err := commands.ReleaseStorage(ctx, owner, retained, StorageReleaseInput{ConfirmedName: "wrong", ConfirmedAssetID: retained}, "test"); err == nil {
		t.Fatal("wrong confirmation accepted")
	}
	var retainedInstance string
	if err := adminDB.QueryRow(ctx, `SELECT instance_id::text FROM storage_assets WHERE id=$1`, retained).Scan(&retainedInstance); err != nil {
		t.Fatal(err)
	}
	op := webapi.ID()
	must(`INSERT INTO operations(id,instance_id,actor_id,type,status,stage,intent,trace_id) VALUES($1,$2,$3,'DELETE','SUCCEEDED','COMPLETE','{}','release-fixture')`, op, retainedInstance, owner.UserID)
	must(`INSERT INTO quota_reservations(id,org_id,owner_id,instance_id,operation_id,resource_type,amount,status) VALUES($1,$2,$3,$4,$5,'STORAGE',10737418240,'USED')`, webapi.ID(), org, owner.UserID, retainedInstance, op)
	must(`UPDATE storage_assets SET legal_hold=true WHERE id=$1`, retained)
	if _, err := commands.ReleaseStorage(ctx, owner, retained, input, "test"); err == nil {
		t.Fatal("held data released")
	}
	must(`UPDATE storage_assets SET legal_hold=false,retention_until=clock_timestamp()+interval '7 days' WHERE id=$1`, retained)
	must(`UPDATE instances SET deleted_at=clock_timestamp() WHERE id=$1`, retainedInstance)
	must(`UPDATE operations SET status='UNCERTAIN' WHERE id=$1`, op)
	if _, err := commands.ReleaseStorage(ctx, owner, retained, input, "test"); err == nil {
		t.Fatal("active work ignored")
	}
	must(`UPDATE operations SET status='SUCCEEDED' WHERE id=$1`, op)
	must(`CREATE OR REPLACE FUNCTION reject_release_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='STORAGE_OWNER_RELEASE' THEN RAISE EXCEPTION 'test audit failure'; END IF; RETURN NEW; END $$`)
	must(`CREATE TRIGGER reject_release_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_release_audit()`)
	if _, err := commands.ReleaseStorage(ctx, owner, retained, input, "test"); err == nil {
		t.Fatal("acceptance succeeded without audit")
	}
	var requests int
	if err := adminDB.QueryRow(ctx, `SELECT count(*) FROM storage_release_requests WHERE asset_id=$1`, retained).Scan(&requests); err != nil || requests != 0 {
		t.Fatal("failed audit retained release request", err)
	}
	must(`DROP TRIGGER reject_release_audit ON audit_events`)
	accepted, err := commands.ReleaseStorage(ctx, owner, retained, input, "test")
	if err != nil || accepted.Status != "ACCEPTED" {
		t.Fatal("explicit early release rejected", err)
	}
	replay, err := commands.ReleaseStorage(ctx, owner, retained, input, "test")
	if err != nil || replay != accepted {
		t.Fatal("release replay created another request", err)
	}
	var retainedQuota int64
	if err := adminDB.QueryRow(ctx, `SELECT sum(amount) FROM quota_reservations WHERE instance_id=$1 AND resource_type='STORAGE' AND status='USED'`, retainedInstance).Scan(&retainedQuota); err != nil || retainedQuota != 10<<30 {
		t.Fatal("acceptance released quota before physical proof", err)
	}
	browseRequest := webapi.ID()
	must(`INSERT INTO storage_browse_requests(id,org_id,owner_id,instance_id,asset_id,action,root,path,after_name,result_public_key,status,expires_at)
		SELECT $2,org_id,owner_id,instance_id,id,'LIST','workspace','','',NULL,'QUEUED',clock_timestamp()+interval '2 minutes' FROM storage_assets WHERE id=$1`, stopped, browseRequest)
	cancelled, cancelBrowse := context.WithCancel(ctx)
	time.AfterFunc(20*time.Millisecond, cancelBrowse)
	if _, _, _, err := service.waitStorageFileRequest(cancelled, owner, browseRequest, "LIST", nil); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled browse request did not preserve cancellation", err)
	}
	cancelBrowse()
	var remainingBrowse int
	if err := adminDB.QueryRow(ctx, `SELECT count(*) FROM storage_browse_requests WHERE id=$1`, browseRequest).Scan(&remainingBrowse); err != nil || remainingBrowse != 0 {
		t.Fatal("cancelled browse retained its active lock", err)
	}
	successfulBrowse := webapi.ID()
	must(`INSERT INTO storage_browse_requests(id,org_id,owner_id,instance_id,asset_id,action,root,path,after_name,result_public_key,status,listing,expires_at)
		SELECT $2,org_id,owner_id,instance_id,id,'LIST','workspace','','',NULL,'SUCCEEDED','{"root":"workspace","path":"","entries":[]}'::jsonb,clock_timestamp()+interval '2 minutes' FROM storage_assets WHERE id=$1`, stopped, successfulBrowse)
	listing, _, _, err := service.waitStorageFileRequest(ctx, owner, successfulBrowse, "LIST", nil)
	var browsedDirectory StorageDirectory
	if err != nil || json.Unmarshal(listing, &browsedDirectory) != nil || browsedDirectory.Root != "workspace" || browsedDirectory.Path != "" {
		t.Fatal("successful browse with null error code was not returned", err)
	}
	if err := adminDB.QueryRow(ctx, `SELECT count(*) FROM storage_browse_requests WHERE id=$1`, successfulBrowse).Scan(&remainingBrowse); err != nil || remainingBrowse != 0 {
		t.Fatal("successful browse result was not cleared", err)
	}
	// Group all processing stages before pagination; preserve owner and prefix scope.
	{
		stages := []string{"PREPARING", "RESTORING", "RECLAIM_PENDING"}
		want := map[string]string{}
		for _, stage := range stages {
			want[asset(owner, "处理中_"+stage, stage)] = stage
			_ = asset(administrator, "处理中_其他"+stage, stage)
		}
		_ = asset(owner, "其他前缀", "PREPARING")
		seen := map[string]bool{}
		for page := 1; page <= 4; page++ {
			count := 0
			if page <= 3 {
				count = 1
			}
			result, err := service.StorageAssets(ctx, owner, StorageListInput{Page: page, Limit: 1, Status: "PROCESSING", Query: "处理中_"})
			if err != nil || result.Total != 3 || len(result.Items) != count {
				t.Fatalf("processing page %d: %+v %v", page, result, err)
			}
			for _, item := range result.Items {
				if want[item.ID] != item.Status || seen[item.ID] {
					t.Fatalf("foreign, duplicate or rewritten status: %+v", item)
				}
				seen[item.ID] = true
			}
		}
		for _, stage := range stages {
			result, err := service.StorageAssets(ctx, owner, StorageListInput{Page: 1, Limit: 10, Status: stage, Query: "处理中_"})
			if err != nil || result.Total != 1 || len(result.Items) != 1 || result.Items[0].Status != stage {
				t.Fatalf("exact status compatibility %s: %+v %v", stage, result, err)
			}
		}
		w := httptest.NewRecorder()
		handler.ServeAuthenticated(w, httptest.NewRequest("GET", "/v1/storage-assets?status=PROCESSING&limit=1", nil), owner)
		if w.Code != 200 {
			t.Fatal("processing filter rejected by HTTP handler", w.Code)
		}
	}
	must(`UPDATE memberships SET status='DISABLED' WHERE org_id=$1 AND user_id=$2`, owner.OrganizationID, owner.UserID)
	if _, e = service.StorageAsset(ctx, owner, retained); e == nil {
		t.Fatal("disabled owner could read retained asset")
	}
}
