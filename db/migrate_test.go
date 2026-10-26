package db

import (
	"context"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// This suite owns its database. Never point HC_TEST_DB_DSN at an application database.
func TestMigrationsOnIsolatedPostgres(t *testing.T) {
	dsn := os.Getenv("HC_TEST_DB_DSN")
	if path := os.Getenv("HC_TEST_DB_DSN_FILE"); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal("test DSN file is unreadable")
		}
		parsed, err := url.Parse(strings.TrimSpace(string(data)))
		if err != nil {
			t.Fatal("test DSN is invalid")
		}
		parsed.Path = "/hc_test_d1"
		dsn = parsed.String()
	}
	if dsn == "" {
		t.Skip("set HC_TEST_DB_DSN to an isolated PostgreSQL 17 database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var database string
	if err := conn.QueryRow(ctx, "SELECT current_database()").Scan(&database); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(database, "hc_test_") {
		t.Fatal("test requires database name starting hc_test_")
	}
	var version int
	if err := conn.QueryRow(ctx, "SELECT current_setting('server_version_num')::int").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version < 170000 || version >= 180000 {
		t.Fatalf("expected PostgreSQL 17, got %d", version)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- Apply(ctx, dsn) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := Check(ctx, conn); err != nil {
		t.Fatal(err)
	}
	t.Run("expanded catalog preserves existing specs and is idempotent", func(t *testing.T) {
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(context.Background())
		if _, err = tx.Exec(ctx, `INSERT INTO approved_specs(id,cpu_millicores,memory_bytes,storage_bytes,enabled) VALUES(gen_random_uuid(),2000,4294967296,18253611008,true),(gen_random_uuid(),2000,4294967296,19327352832,false)`); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if _, err = tx.Exec(ctx, expandedComputeSpecs); err != nil {
				t.Fatal(err)
			}
			var count, disabled int
			if err = tx.QueryRow(ctx, `SELECT count(*) FILTER(WHERE enabled AND storage_bytes=18253611008),count(*) FILTER(WHERE enabled AND storage_bytes=19327352832) FROM approved_specs`).Scan(&count, &disabled); err != nil || count != 30 || disabled != 0 {
				t.Fatalf("expanded or disabled catalog differs: %d %d %v", count, disabled, err)
			}
		}
	})
	t.Run("workbench permission upgrade is idempotent and preserves custom roles", func(t *testing.T) {
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(context.Background())
		var org, custom string
		if err = tx.QueryRow(ctx, `SELECT id::text FROM organizations LIMIT 1`).Scan(&org); err == pgx.ErrNoRows {
			err = tx.QueryRow(ctx, `INSERT INTO organizations(id,name,status) VALUES(gen_random_uuid(),'context permission migration','ACTIVE') RETURNING id::text`).Scan(&org)
		}
		if err != nil {
			t.Fatal(err)
		}
		var previousVersions, previousAudits int
		if err = tx.QueryRow(ctx, `SELECT sum(version) FROM roles WHERE org_id=$1 AND builtin`, org).Scan(&previousVersions); err != nil {
			t.Fatal(err)
		}
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE org_id=$1 AND action='CONTEXT_DEFAULT_PERMISSIONS_GRANTED'`, org).Scan(&previousAudits); err != nil {
			t.Fatal(err)
		}
		if err = tx.QueryRow(ctx, `INSERT INTO roles(id,org_id,code,name,builtin) VALUES(gen_random_uuid(),$1,'context_custom','Custom',false) RETURNING id::text`, org).Scan(&custom); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `DELETE FROM role_permissions WHERE role_id IN (SELECT id FROM roles WHERE org_id=$1) AND permission_id IN ('memory:read','memory:manage','knowledge:read','knowledge:manage')`, org); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if _, err = tx.Exec(ctx, workbenchContextPermissions); err != nil {
				t.Fatal(err)
			}
		}
		var granted, changed, audited, customCount int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM role_permissions rp JOIN roles r ON r.id=rp.role_id WHERE r.org_id=$1 AND r.builtin AND rp.permission_id IN ('memory:read','memory:manage','knowledge:read','knowledge:manage')`, org).Scan(&granted); err != nil || granted != 8 {
			t.Fatal("built-in grants incomplete", granted, err)
		}
		if err = tx.QueryRow(ctx, `SELECT sum(version) FROM roles WHERE org_id=$1 AND builtin`, org).Scan(&changed); err != nil || changed != previousVersions+2 {
			t.Fatal("migration is not idempotent", changed, err)
		}
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE org_id=$1 AND action='CONTEXT_DEFAULT_PERMISSIONS_GRANTED'`, org).Scan(&audited); err != nil || audited != previousAudits+2 {
			t.Fatal("grant audit duplicated or missing", audited, err)
		}
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM role_permissions WHERE role_id=$1`, custom).Scan(&customCount); err != nil || customCount != 0 {
			t.Fatal("custom role changed", customCount, err)
		}

	})
	for _, table := range []string{"model_providers", "model_catalog_entries", "user_model_connections", "user_model_connection_versions", "model_sync_operations", "model_gateway_checks", "user_model_defaults", "external_provider_secrets", "model_connection_registry"} {
		var present bool
		if err := conn.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, "public."+table).Scan(&present); err != nil || !present {
			t.Fatalf("private model management table missing: %s", table)
		}
	}
	for _, column := range []string{"users.workspace_owner_id", "instances.workspace_id", "instances.workspace_name"} {
		parts := strings.Split(column, ".")
		var present bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name=$1 AND column_name=$2)`, parts[0], parts[1]).Scan(&present); err != nil || !present {
			t.Fatalf("Workspace column missing: %s", column)
		}
	}
	var legacyColumns int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name IN ('quota_policies','user_quota_policies') AND column_name='max_running'`).Scan(&legacyColumns); err != nil || legacyColumns != 0 {
		t.Fatal("independent running quota columns remain")
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND column_name LIKE 'coder\_%' ESCAPE '\'`).Scan(&legacyColumns); err != nil || legacyColumns != 0 {
		t.Fatal("legacy business columns remain")
	}
	// Applications reject an old schema; only the migrator may advance it.
	pending, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pending.Exec(ctx, "DELETE FROM hc_schema_migrations WHERE version=$1", migrations()[len(migrations())-1].version); err != nil {
		t.Fatal(err)
	}
	if err := Check(ctx, pending); err == nil {
		t.Fatal("readiness accepted a pending migration")
	}
	if err := CheckBackup(ctx, pending); err != nil {
		t.Fatal("known prior schema cannot be backed up before upgrading", err)
	}
	if err := pending.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM information_schema.tables WHERE table_schema='public'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 73 {
		t.Fatalf("expected 73 public tables/views including migration ledger, got %d", count)
	}
	if _, err := conn.Exec(ctx, "INSERT INTO organizations(id,name,status) VALUES ('00000000-0000-4000-8000-000000000001','Test','ACTIVE') ON CONFLICT DO NOTHING"); err != nil {
		t.Fatal(err)
	}
	t.Run("organization model head retains idempotent insertion", func(t *testing.T) {
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(context.Background())
		var id string
		if err := tx.QueryRow(ctx, `INSERT INTO model_configuration_heads(id,org_id) VALUES(gen_random_uuid(),'00000000-0000-4000-8000-000000000001') ON CONFLICT(org_id) DO UPDATE SET current_version=model_configuration_heads.current_version RETURNING id::text`).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO model_configuration_heads(id,org_id) VALUES($1,'00000000-0000-4000-8000-000000000001') ON CONFLICT(id) DO NOTHING`, id); err != nil {
			t.Fatal("idempotent head registration failed", err)
		}
	})
	if _, err := conn.Exec(ctx, "INSERT INTO organizations(id,name,status) VALUES ('00000000-0000-4000-8000-000000000002','Other','ACTIVE')"); err == nil {
		t.Fatal("single-organization constraint missing")
	}
	if _, err := conn.Exec(ctx, "UPDATE hc_schema_migrations SET checksum='tampered' WHERE version=1"); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, dsn); err == nil {
		t.Fatal("modified migration checksum accepted")
	}
	if err := Check(ctx, conn); err == nil {
		t.Fatal("readiness accepted modified schema ledger")
	}
	if _, err := conn.Exec(ctx, "UPDATE hc_schema_migrations SET checksum=$1 WHERE version=1", checksumOf(baseline)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "INSERT INTO hc_schema_migrations(version,checksum) VALUES (0,'unexpected')"); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, dsn); err == nil {
		t.Fatal("unexpected ledger entry accepted")
	}
	if _, err := conn.Exec(ctx, "DELETE FROM hc_schema_migrations WHERE version=0"); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"hc_api", "hc_worker", "hc_gateway"} {
		var canCreate, canWrite bool
		if err := conn.QueryRow(ctx, "SELECT has_schema_privilege($1,'public','CREATE'), has_table_privilege($1,'organizations','INSERT')", role).Scan(&canCreate, &canWrite); err != nil {
			t.Fatal(err)
		}
		if canCreate || canWrite {
			t.Fatalf("bootstrap role %s has unauthorized privileges", role)
		}
	}
}
