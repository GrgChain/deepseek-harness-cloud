package runtimeapproval

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/devlocal"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func TestApprovalAtomicIdempotentAndOperationsOnly(t *testing.T) {
	path := os.Getenv("HC_TEST_DB_DSN_FILE")
	if path == "" {
		t.Skip("isolated PostgreSQL approval database required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("private database configuration missing")
	}
	u, err := url.Parse(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal("invalid database configuration")
	}
	u.Path = "/hc_test_approval"
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
	if _, err = pool.Exec(ctx, `TRUNCATE organizations,approved_templates,approved_specs CASCADE`); err != nil {
		t.Fatal(err)
	}
	org, template := webapi.ID(), webapi.ID()
	m := fixtureManifest()
	m.StorageAdapter = StorageAdapter
	if _, err = pool.Exec(ctx, `INSERT INTO organizations(id,name,status) VALUES($1,'Approval test','ACTIVE')`, org); err != nil {
		t.Fatal(err)
	}
	if err = Approve(ctx, pool, org, template, "integration-test", m, 1); err != nil {
		t.Fatal(err)
	}
	if err = Approve(ctx, pool, org, template, "integration-replay", m, 5); err != nil {
		t.Fatal(err)
	}
	var audit, slots, cpu, memory, specs int64
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM audit_events WHERE action='RUNTIME_APPROVE'),max_concurrent_creates,cpu_millicores,memory_bytes,(SELECT count(*) FROM approved_specs WHERE enabled AND cpu_millicores=2000 AND memory_bytes=4294967296 AND storage_bytes=10737418240) FROM quota_policies WHERE org_id=$1`, org).Scan(&audit, &slots, &cpu, &memory, &specs); err != nil || audit != 1 || slots != 1 || cpu != 40000 || memory != 80<<30 || specs != 1 {
		t.Fatalf("approval replay changed limits or audit: %d %d %d %d %d %v", audit, slots, cpu, memory, specs, err)
	}
	var catalogCount int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM approved_specs WHERE enabled`).Scan(&catalogCount); err != nil || catalogCount != 3000 {
		t.Fatal("bounded storage catalog missing or duplicated", catalogCount, err)
	}
	changed := m
	changed.AdapterDigest = "sha256:" + strings.Repeat("b", 64)
	if err = Approve(ctx, pool, org, template, "test", changed, 1); err == nil {
		t.Fatal("existing frozen manifest overwritten")
	}
	tx, err := pool.Begin(ctx)
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
		t.Fatal("private API identity missing")
	}
	appURL, _ := url.Parse(strings.TrimSpace(string(appRaw)))
	appURL.Path = u.Path
	app, err := pgxpool.New(ctx, appURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if err = Approve(ctx, app, org, template, "member", m, 1); err == nil {
		t.Fatal("runtime API role approved template")
	}
	if _, err = pool.Exec(ctx, `TRUNCATE approved_templates,approved_specs,quota_policies CASCADE`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO quota_policies(org_id,max_instances,cpu_millicores,memory_bytes,storage_bytes) VALUES($1,30,60000,128849018880,322122547200)`, org); err != nil {
		t.Fatal(err)
	}
	if err = Approve(ctx, pool, org, template, "test-existing-quota", m, 1); err != nil {
		t.Fatal(err)
	}
	var instances, storage int64
	if err = pool.QueryRow(ctx, `SELECT max_instances,cpu_millicores,memory_bytes,storage_bytes,max_concurrent_creates FROM quota_policies WHERE org_id=$1`, org).Scan(&instances, &cpu, &memory, &storage, &slots); err != nil || instances != 30 || cpu != 60000 || memory != 120<<30 || storage != 300<<30 || slots != 1 {
		t.Fatal("approval changed saved resource limits", instances, cpu, memory, storage, slots, err)
	}
	if _, err = pool.Exec(ctx, `TRUNCATE approved_templates,approved_specs,quota_policies CASCADE;
 CREATE OR REPLACE FUNCTION hc_test_reject_approval() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='RUNTIME_APPROVE' THEN RAISE EXCEPTION 'test audit failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER hc_test_reject_approval BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION hc_test_reject_approval()`); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(context.Background(), `DROP TRIGGER hc_test_reject_approval ON audit_events; DROP FUNCTION hc_test_reject_approval()`)
	if err = Approve(ctx, pool, org, template, "test", m, 1); err == nil {
		t.Fatal("audit failure accepted approval")
	}
	var rows int
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM approved_templates)+(SELECT count(*) FROM approved_specs)+(SELECT count(*) FROM quota_policies)`).Scan(&rows); err != nil || rows != 0 {
		t.Fatal("late failure left partial template/spec/quota", rows, err)
	}
}
