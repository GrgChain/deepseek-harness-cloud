package db

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
	t.Run("personal plugin upgrade grants only built-in roles", func(t *testing.T) {
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(context.Background())
		var org, custom string
		if err = tx.QueryRow(ctx, `SELECT id::text FROM organizations LIMIT 1`).Scan(&org); err == pgx.ErrNoRows {
			err = tx.QueryRow(ctx, `INSERT INTO organizations(id,name,status) VALUES(gen_random_uuid(),'plugin permission migration','ACTIVE') RETURNING id::text`).Scan(&org)
		}
		if err != nil {
			t.Fatal(err)
		}
		if err = tx.QueryRow(ctx, `INSERT INTO roles(id,org_id,code,name,builtin) VALUES(gen_random_uuid(),$1,'plugin_custom','Custom',false) RETURNING id::text`, org).Scan(&custom); err != nil {
			t.Fatal(err)
		}
		// Restore the relevant pre-58 state inside this rolled-back transaction.
		if _, err = tx.Exec(ctx, `DELETE FROM role_permissions WHERE permission_id='plugins:manage'; DELETE FROM permissions WHERE id='plugins:manage'; DROP TABLE personal_plugins`); err != nil {
			t.Fatal(err)
		}
		var previousVersions, previousAudits int
		if err = tx.QueryRow(ctx, `SELECT sum(version) FROM roles WHERE org_id=$1 AND builtin`, org).Scan(&previousVersions); err != nil {
			t.Fatal(err)
		}
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE org_id=$1 AND action='PLUGIN_DEFAULT_PERMISSION_GRANTED'`, org).Scan(&previousAudits); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, personalPlugins); err != nil {
			t.Fatal(err)
		}
		var granted, changed, audited, customCount int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM role_permissions rp JOIN roles r ON r.id=rp.role_id WHERE r.org_id=$1 AND r.builtin AND rp.permission_id='plugins:manage'`, org).Scan(&granted); err != nil || granted != 2 {
			t.Fatal("built-in plugin grants incomplete", granted, err)
		}
		if err = tx.QueryRow(ctx, `SELECT sum(version) FROM roles WHERE org_id=$1 AND builtin`, org).Scan(&changed); err != nil || changed != previousVersions+2 {
			t.Fatal("plugin grant did not invalidate existing roles", changed, err)
		}
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE org_id=$1 AND action='PLUGIN_DEFAULT_PERMISSION_GRANTED'`, org).Scan(&audited); err != nil || audited != previousAudits+2 {
			t.Fatal("plugin grant audit missing", audited, err)
		}
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM role_permissions WHERE role_id=$1`, custom).Scan(&customCount); err != nil || customCount != 0 {
			t.Fatal("custom role changed", customCount, err)
		}
		var dependencies []string
		if err = tx.QueryRow(ctx, `SELECT dependencies FROM permissions WHERE id='plugins:manage'`).Scan(&dependencies); err != nil || len(dependencies) != 1 || dependencies[0] != "instances:read" {
			t.Fatal("plugin permission dependency differs", dependencies, err)
		}
	})
	t.Run("offline plugin upgrade preserves online entries and enforces complete envelopes", func(t *testing.T) {
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(context.Background())
		if _, err = tx.Exec(ctx, `ALTER TABLE personal_plugins DROP CONSTRAINT personal_plugins_archive,
 DROP COLUMN addition_method,DROP COLUMN archive_filename,DROP COLUMN archive_sha256,
 DROP COLUMN archive_key_id,DROP COLUMN archive_nonce,DROP COLUMN archive_ciphertext`); err != nil {
			t.Fatal(err)
		}
		var org, owner, id string
		if err = tx.QueryRow(ctx, `SELECT id::text FROM organizations LIMIT 1`).Scan(&org); err == pgx.ErrNoRows {
			err = tx.QueryRow(ctx, `INSERT INTO organizations(id,name,status) VALUES(gen_random_uuid(),'offline plugin migration','ACTIVE') RETURNING id::text`).Scan(&org)
		}
		if err != nil {
			t.Fatal(err)
		}
		if err = tx.QueryRow(ctx, `INSERT INTO users(id,identity_issuer,identity_subject,workspace_owner_id,status) VALUES(gen_random_uuid(),'migration-test',gen_random_uuid()::text,gen_random_uuid(),'ACTIVE') RETURNING id::text`).Scan(&owner); err != nil {
			t.Fatal(err)
		}
		if err = tx.QueryRow(ctx, `INSERT INTO personal_plugins(id,org_id,owner_id,name,package_name,package_version,author,category,summary,description,compatibility,checked_at)
 VALUES(gen_random_uuid(),$1,$2,'Existing','migration-plugin','1.2.3','author','开发工具','summary','description','DSH','2026-11-03') RETURNING id::text`, org, owner).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, offlinePlugins); err != nil {
			t.Fatal(err)
		}
		var preserved bool
		if err = tx.QueryRow(ctx, `SELECT package_version='1.2.3' AND addition_method='online' AND archive_filename='' AND archive_sha256='' AND archive_key_id IS NULL AND archive_nonce IS NULL AND archive_ciphertext IS NULL FROM personal_plugins WHERE id=$1`, id).Scan(&preserved); err != nil || !preserved {
			t.Fatal("existing online metadata changed", preserved, err)
		}
		for _, update := range []string{
			`addition_method='offline'`,
			`archive_filename='unbound.tgz'`,
			`addition_method='offline',archive_filename='archive.tgz',archive_sha256=repeat('a',64),archive_key_id='key',archive_nonce=decode(repeat('00',12),'hex'),archive_ciphertext=decode(repeat('00',16),'hex')`,
			`addition_method='offline',archive_filename='archive.tgz',archive_sha256='invalid',archive_key_id='key',archive_nonce=decode(repeat('00',12),'hex'),archive_ciphertext=decode(repeat('00',17),'hex')`,
			`addition_method='offline',archive_filename='archive.tgz',archive_sha256=repeat('a',64),archive_key_id=NULL,archive_nonce=decode(repeat('00',12),'hex'),archive_ciphertext=decode(repeat('00',17),'hex')`,
		} {
			sp, err := tx.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			_, err = sp.Exec(ctx, `UPDATE personal_plugins SET `+update+` WHERE id=$1`, id)
			if err == nil {
				t.Fatal("inconsistent archive envelope was accepted", update)
			}
			if err = sp.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
		}
		if _, err = tx.Exec(ctx, `UPDATE personal_plugins SET addition_method='offline',archive_filename='archive.tgz',archive_sha256=repeat('a',64),archive_key_id='key',archive_nonce=decode(repeat('00',12),'hex'),archive_ciphertext=decode(repeat('00',17),'hex') WHERE id=$1`, id); err != nil {
			t.Fatal("complete encrypted archive was rejected", err)
		}
	})
	t.Run("editable plugin catalog preserves personal entries and isolates overrides", func(t *testing.T) {
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(context.Background())
		// Restore pre-60 state without changing the older migration definitions.
		if _, err = tx.Exec(ctx, `ALTER TABLE personal_plugins DROP COLUMN source_note,DROP COLUMN revision,DROP COLUMN default_id; DROP TABLE plugin_catalog_defaults`); err != nil {
			t.Fatal(err)
		}
		var org, owner, other, online, offline string
		if err = tx.QueryRow(ctx, `SELECT id::text FROM organizations LIMIT 1`).Scan(&org); err == pgx.ErrNoRows {
			err = tx.QueryRow(ctx, `INSERT INTO organizations(id,name,status) VALUES(gen_random_uuid(),'editable plugin migration','ACTIVE') RETURNING id::text`).Scan(&org)
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range []*string{&owner, &other} {
			if err = tx.QueryRow(ctx, `INSERT INTO users(id,identity_issuer,identity_subject,workspace_owner_id,status) VALUES(gen_random_uuid(),'editable-plugin-migration',gen_random_uuid()::text,gen_random_uuid(),'ACTIVE') RETURNING id::text`).Scan(id); err != nil {
				t.Fatal(err)
			}
		}
		if err = tx.QueryRow(ctx, `INSERT INTO personal_plugins(id,org_id,owner_id,name,package_name,package_version,author,category,summary,description,compatibility,checked_at)
 VALUES(gen_random_uuid(),$1,$2,'Existing online','editable-online','1.2.3','author','开发工具','summary','description','DSH','2026-11-03') RETURNING id::text`, org, owner).Scan(&online); err != nil {
			t.Fatal(err)
		}
		if err = tx.QueryRow(ctx, `INSERT INTO personal_plugins(id,org_id,owner_id,name,package_name,package_version,author,category,summary,description,compatibility,checked_at,addition_method,archive_filename,archive_sha256,archive_key_id,archive_nonce,archive_ciphertext)
 VALUES(gen_random_uuid(),$1,$2,'Existing offline','editable-offline','4.5.6','author','开发工具','summary','description','DSH','2026-11-03','offline','original.tgz',repeat('a',64),'original-key',decode(repeat('01',12),'hex'),decode(repeat('02',17),'hex')) RETURNING id::text`, org, owner).Scan(&offline); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, editablePluginCatalog); err != nil {
			t.Fatal(err)
		}
		var preserved bool
		if err = tx.QueryRow(ctx, `SELECT name='Existing online' AND package_version='1.2.3' AND addition_method='online' AND archive_key_id IS NULL AND source_note='' AND revision=1 AND default_id IS NULL FROM personal_plugins WHERE id=$1`, online).Scan(&preserved); err != nil || !preserved {
			t.Fatal("existing online entry changed", preserved, err)
		}
		if err = tx.QueryRow(ctx, `SELECT name='Existing offline' AND package_version='4.5.6' AND addition_method='offline' AND archive_filename='original.tgz' AND archive_sha256=repeat('a',64) AND archive_key_id='original-key' AND archive_nonce=decode(repeat('01',12),'hex') AND archive_ciphertext=decode(repeat('02',17),'hex') AND source_note='' AND revision=1 AND default_id IS NULL FROM personal_plugins WHERE id=$1`, offline).Scan(&preserved); err != nil || !preserved {
			t.Fatal("existing offline archive changed", preserved, err)
		}
		type catalogEntry struct {
			id, packageName, name, version, author, category, summary, description string
			keywords                                                               []string
			repository, documentation, compatibility                               string
		}
		expected := []catalogEntry{
			{"6e2bccdc-1ae1-4f04-8ddf-6bab763d2631", "@openviking/dsh-memory-plugin", "OpenViking 记忆", "0.5.29", "OpenViking / volcengine", "会话与记忆", "将 OpenViking 的记忆和上下文能力接入 DSH。", "由 OpenViking 项目提供的 DSH 组合包。使用前需按项目文档准备 OpenViking 服务及相关配置；具体连接能力和授权范围以该版本文档为准。", []string{"记忆", "上下文", "memory", "OpenViking"}, "https://github.com/volcengine/OpenViking", "https://github.com/volcengine/OpenViking/tree/main/examples/dsh-memory-plugin", "未声明 DSH 版本范围；Node.js 要求 ^22.19.0 或 >=24。"},
			{"6e2bccdc-1ae1-4f04-8ddf-6bab763d2632", "@linxin666/dsh-ssh", "DSH SSH", "0.4.11", "zhu1090093659 / linxin666", "开发工具", "在 DSH Web 中使用 SSH 终端、SFTP 和远程主机工具。", "为 DSH Web 提供远程 SSH 操作，包括主机配置、持久连接、终端、文件传输和端口转发。使用时需要自行配置主机与认证，并满足运行环境的网络策略。", []string{"SSH", "SFTP", "远程", "终端"}, "https://github.com/zhu1090093659/dsh-web", "https://github.com/zhu1090093659/dsh-web#readme", "包声明 DSH >=0.2.1-rc.3；Node.js ^22.19.0 或 >=24.0.0。"},
			{"6e2bccdc-1ae1-4f04-8ddf-6bab763d2633", "dshmarket", "DSH Market", "1.71.1", "dsh-market", "集成扩展", "社区维护的 DSH 插件发现与浏览工具。", "该第三方组合包在 DSH 中提供社区目录的浏览和搜索。它由独立社区维护，目录收录规则及所需的 DSH 版本请查看项目文档。", []string{"市场", "目录", "marketplace", "发现"}, "https://github.com/dsh-market/dsh-market", "https://github.com/dsh-market/dsh-market#readme", "未声明 DSH 版本范围，需由 DSH 检查依赖兼容性。"},
		}
		rows, err := tx.Query(ctx, `SELECT id::text,package_name,name,package_version,author,category,summary,description,keywords,repository,documentation,compatibility,checked_at::text,source_note,revision,sort_order FROM plugin_catalog_defaults ORDER BY sort_order,id`)
		if err != nil {
			t.Fatal(err)
		}
		index := 0
		for rows.Next() {
			var actual catalogEntry
			var checkedAt, sourceNote string
			var revision int64
			var sortOrder int
			if err = rows.Scan(&actual.id, &actual.packageName, &actual.name, &actual.version, &actual.author, &actual.category, &actual.summary, &actual.description, &actual.keywords, &actual.repository, &actual.documentation, &actual.compatibility, &checkedAt, &sourceNote, &revision, &sortOrder); err != nil {
				t.Fatal(err)
			}
			if index >= len(expected) {
				t.Fatal("unexpected default plugin entry")
			}
			want := expected[index]
			if actual.id != want.id || actual.packageName != want.packageName || actual.name != want.name || actual.version != want.version || actual.author != want.author || actual.category != want.category || actual.summary != want.summary || actual.description != want.description || strings.Join(actual.keywords, "\x00") != strings.Join(want.keywords, "\x00") || actual.repository != want.repository || actual.documentation != want.documentation || actual.compatibility != want.compatibility || checkedAt != "2026-10-27" || sourceNote != "包信息及 DSH bundle 声明" || revision != 1 || sortOrder != index {
				t.Fatalf("default plugin metadata changed: %s", actual.packageName)
			}
			index++
		}
		if err = rows.Err(); err != nil || index != len(expected) {
			t.Fatal("default plugins incomplete", index, err)
		}
		rows.Close()
		reject := func(code, constraint, sql string, args ...any) {
			t.Helper()
			sp, err := tx.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			_, err = sp.Exec(ctx, sql, args...)
			var problem *pgconn.PgError
			if !errors.As(err, &problem) || problem.Code != code || constraint != "" && problem.ConstraintName != constraint {
				t.Fatalf("expected %s/%s, got %v", code, constraint, err)
			}
			if err = sp.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
		}
		reject("23514", "", `UPDATE personal_plugins SET revision=0 WHERE id=$1`, online)
		reject("23514", "", `UPDATE plugin_catalog_defaults SET revision=0 WHERE id=$1`, expected[0].id)
		reject("23503", "", `UPDATE personal_plugins SET default_id=gen_random_uuid() WHERE id=$1`, online)
		reject("23505", "", `UPDATE plugin_catalog_defaults SET package_name=$1 WHERE id=$2`, expected[1].packageName, expected[0].id)
		const copyDefault = `INSERT INTO personal_plugins(id,org_id,owner_id,name,package_name,package_version,author,category,summary,description,keywords,repository,documentation,compatibility,checked_at,source_note,revision,default_id)
 SELECT gen_random_uuid(),$1,$2,name,package_name||$4,package_version,author,category,summary,description,keywords,repository,documentation,compatibility,checked_at,source_note,revision+1,id FROM plugin_catalog_defaults WHERE id=$3 RETURNING id::text`
		var override string
		if err = tx.QueryRow(ctx, copyDefault, org, owner, expected[0].id, "").Scan(&override); err != nil {
			t.Fatal("first default override rejected", err)
		}
		reject("23505", "personal_plugins_default", copyDefault, org, owner, expected[0].id, "-duplicate")
		reject("23505", "personal_plugins_org_id_owner_id_package_name_key", `UPDATE personal_plugins SET package_name='editable-online' WHERE id=$1`, override)
		// A separate owner may independently override the same default.
		var otherOverride string
		if err = tx.QueryRow(ctx, copyDefault, org, other, expected[0].id, "").Scan(&otherOverride); err != nil || otherOverride == override {
			t.Fatal("another owner's default override was rejected", err)
		}
		reject("23503", "", `DELETE FROM plugin_catalog_defaults WHERE id=$1`, expected[0].id)
		changed, err := tx.Exec(ctx, `UPDATE personal_plugins SET name='Personal name',source_note='Personal source note',revision=revision+1 WHERE id=$1 AND org_id=$2 AND owner_id=$3 AND revision=2`, override, org, owner)
		if err != nil || changed.RowsAffected() != 1 {
			t.Fatal("current revision did not update", err)
		}
		for _, args := range [][]any{{override, org, owner, 2}, {override, org, other, 3}} {
			changed, err = tx.Exec(ctx, `UPDATE personal_plugins SET name='Unexpected overwrite',revision=revision+1 WHERE id=$1 AND org_id=$2 AND owner_id=$3 AND revision=$4`, args...)
			if err != nil || changed.RowsAffected() != 0 {
				t.Fatal("stale revision or another owner changed the override", err)
			}
		}
		if err = tx.QueryRow(ctx, `SELECT name='Personal name' AND source_note='Personal source note' AND revision=3 FROM personal_plugins WHERE id=$1`, override).Scan(&preserved); err != nil || !preserved {
			t.Fatal("personal override did not persist", err)
		}
		if err = tx.QueryRow(ctx, `SELECT name='OpenViking 记忆' AND revision=1 FROM plugin_catalog_defaults WHERE id=$1`, expected[0].id).Scan(&preserved); err != nil || !preserved {
			t.Fatal("personal edit modified the shared default", err)
		}
		var publicPrivileges int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM pg_class c CROSS JOIN LATERAL aclexplode(coalesce(c.relacl,acldefault('r',c.relowner))) a WHERE c.oid='plugin_catalog_defaults'::regclass AND a.grantee=0`).Scan(&publicPrivileges); err != nil || publicPrivileges != 0 {
			t.Fatal("default catalog grants PUBLIC access", publicPrivileges, err)
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
	if count != 78 {
		t.Fatalf("expected 78 public tables/views including migration ledger, got %d", count)
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
