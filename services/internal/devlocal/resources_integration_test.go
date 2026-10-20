package devlocal

import (
	"context"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func TestResourceInitializationOnIsolatedPostgres(t *testing.T) {
	path := os.Getenv("HC_TEST_DB_DSN_FILE")
	if path == "" {
		t.Skip("isolated PostgreSQL required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("private database configuration missing")
	}
	u, err := url.Parse(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal("invalid database configuration")
	}
	u.Path = "/hc_test_bootstrap"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = db.Apply(ctx, u.String()); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	must := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	must(`TRUNCATE organizations,users,approved_templates,approved_specs CASCADE`)
	org, user, custom, template, version, spec := webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID()
	must(`INSERT INTO organizations(id,name,status) VALUES($1,'Bootstrap test','ACTIVE')`, org)
	must(`INSERT INTO users(id,identity_issuer,identity_subject,workspace_owner_id,status) VALUES($1,'https://test.local/oidc','member',$2,'ACTIVE'),($3,'https://test.local/oidc','custom',$4,'ACTIVE')`, user, webapi.ID(), custom, webapi.ID())
	must(`INSERT INTO memberships(org_id,user_id,role,status) VALUES($1,$2,'MEMBER','ACTIVE'),($1,$3,'MEMBER','ACTIVE')`, org, user, custom)
	if err = InitResources(ctx, pool, org, "test", true); err == nil {
		t.Fatal("missing approved resources accepted")
	}
	var rows int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM quota_policies`).Scan(&rows); err != nil || rows != 0 {
		t.Fatal("failed initialization left a quota", rows, err)
	}
	// Login-only startup still needs an organization quota, and can later gain
	// runtime defaults without changing any saved policy.
	if err = InitResources(ctx, pool, org, "test", false); err != nil {
		t.Fatal(err)
	}
	var systemInstances, defaultUserInstances, systemCPU, systemMemory, systemStorage int64
	if err = pool.QueryRow(ctx, `SELECT max_instances,max_user_instances,cpu_millicores,memory_bytes,storage_bytes FROM quota_policies WHERE org_id=$1`, org).Scan(&systemInstances, &defaultUserInstances, &systemCPU, &systemMemory, &systemStorage); err != nil || systemInstances != 20 || defaultUserInstances != 2 || systemCPU != 40000 || systemMemory != 80<<30 || systemStorage != 200<<30 {
		t.Fatal("incorrect initial system limits", systemInstances, defaultUserInstances, systemCPU, systemMemory, systemStorage, err)
	}
	must(`INSERT INTO user_quota_policies(org_id,owner_id,max_instances,cpu_millicores,memory_bytes,storage_bytes) VALUES($1,$2,0,0,0,0)`, org, custom)
	must(`INSERT INTO approved_templates(id,version,name,image_digest,runtime_manifest,enabled) VALUES($1,$2,'test',$3,'{}',true)`, template, version, "sha256:"+strings.Repeat("a", 64))
	must(`INSERT INTO approved_specs(id,cpu_millicores,memory_bytes,storage_bytes,enabled) VALUES($1,1000,2147483648,10737418240,true)`, spec)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- InitResources(ctx, pool, org, "test", true)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var instances, cpu, memory, storage, retention, count int64
	if err = pool.QueryRow(ctx, `SELECT u.max_instances,u.cpu_millicores,u.memory_bytes,u.storage_bytes,d.retention_days,(SELECT count(*) FROM user_quota_policies) FROM user_quota_policies u JOIN organization_defaults d USING(org_id) WHERE u.owner_id=$1`, user).Scan(&instances, &cpu, &memory, &storage, &retention, &count); err != nil || instances != 2 || cpu != 2000 || memory != 4<<30 || storage != 20<<30 || retention != 7 || count != 2 {
		t.Fatal("default user resources missing or incorrect", instances, cpu, memory, storage, retention, count, err)
	}
	if err = pool.QueryRow(ctx, `SELECT max_instances FROM user_quota_policies WHERE owner_id=$1`, custom).Scan(&instances); err != nil || instances != 0 {
		t.Fatal("custom quota overwritten", err)
	}
	must(`UPDATE quota_policies SET max_user_instances=3,cpu_millicores=7000,version=7; UPDATE organization_defaults SET retention_days=30,version=4; UPDATE user_quota_policies SET max_instances=1,version=8`)
	fingerprint := func() string {
		t.Helper()
		var value string
		if err := pool.QueryRow(ctx, `SELECT jsonb_build_array((SELECT jsonb_agg(q ORDER BY org_id) FROM quota_policies q),(SELECT jsonb_agg(d ORDER BY org_id) FROM organization_defaults d),(SELECT jsonb_agg(u ORDER BY owner_id) FROM user_quota_policies u),(SELECT count(*) FROM audit_events))::text`).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := fingerprint()
	if err = InitResources(ctx, pool, org, "replay", true); err != nil || fingerprint() != before {
		t.Fatal("startup changed saved policies, versions or audit", err)
	}
	must(`TRUNCATE quota_policies,organization_defaults CASCADE;
 CREATE FUNCTION hc_test_reject_bootstrap() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'audit failure'; END $$;
 CREATE TRIGGER hc_test_reject_bootstrap BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION hc_test_reject_bootstrap()`)
	defer pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS hc_test_reject_bootstrap ON audit_events; DROP FUNCTION IF EXISTS hc_test_reject_bootstrap()`)
	if err = InitResources(ctx, pool, org, "test", true); err == nil {
		t.Fatal("audit failure accepted")
	}
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM quota_policies)+(SELECT count(*) FROM organization_defaults)+(SELECT count(*) FROM user_quota_policies)`).Scan(&rows); err != nil || rows != 0 {
		t.Fatal("audit failure left partial resource policies", rows, err)
	}
}
