package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/runtimeapproval"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/workspace"
)

func TestWorkspaceNamesMigrationIsAtomicAndReplayable(t *testing.T) {
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
		t.Fatal("invalid private database configuration")
	}
	u.Path = "/hc_test_workspace_names"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = db.Apply(ctx, u.String()); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal("isolated database unavailable")
	}
	defer pool.Close()
	if _, err = pool.Exec(ctx, `TRUNCATE organizations,workspace_authority,workspace_templates,approved_templates CASCADE`); err != nil {
		t.Fatal(err)
	}
	org, control, template, version := webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID()
	if _, err = pool.Exec(ctx, `INSERT INTO organizations(id,name,status) VALUES($1,'Names migration test','ACTIVE');`, org); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO workspace_authority(singleton,organization_id,control_user_id) VALUES(true,$1,$2)`, org, control); err != nil {
		t.Fatal(err)
	}
	client, err := workspace.NewEmbedded(pool, org, control)
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	source := map[string][]byte{"main.tf": []byte(`locals { labels = ["hc.coder_owner_id", "hc.coder_workspace_id"] }`), ".terraform.lock.hcl": []byte("test lock"), "variables.json": []byte(`{"runtime_image":"` + digest + `","runtime_root":"/private/runtime"}`)}
	if err = client.PublishTemplate(ctx, template, version, webapi.ID(), "old-labels", source); err != nil {
		t.Fatal(err)
	}
	m := runtimeapproval.Manifest{SchemaVersion: 1, UpstreamCommit: runtimeapproval.ReviewCommit, Image: digest, StorageImage: digest, NetworkImage: digest, WorkspaceSourceVersion: runtimeapproval.WorkspaceSourceVersion, TerraformVersion: runtimeapproval.TerraformVersion, ProviderLock: digest, TemplateVersion: version, ProfileDigest: digest, ConfigFormat: runtimeapproval.ConfigFormat, Bootstrap: runtimeapproval.BootstrapAdapter, NativeAuth: runtimeapproval.NativeAdapter, Paths: []string{"/data/workspace", "/data/dsh"}, ToolDigest: digest, PackageVersion: runtimeapproval.PackageVersion, PackageIntegrity: runtimeapproval.PackageIntegrity, AdapterDigest: digest}
	raw, _ = json.Marshal(m)
	if _, err = pool.Exec(ctx, `INSERT INTO approved_templates(id,version,name,image_digest,runtime_manifest,enabled) VALUES($1,$2,'test',$3,$4,true)`, template, version, digest, raw); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HC_MAINTENANCE", "false")
	if migrateWorkspaceNames(ctx, pool, "test-operator") == nil {
		t.Fatal("migration accepted without maintenance")
	}
	t.Setenv("HC_MAINTENANCE", "true")
	if _, err = pool.Exec(ctx, `CREATE FUNCTION hc_test_reject_names() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='WORKSPACE_NAMES_MIGRATE' THEN RAISE EXCEPTION 'test audit failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER hc_test_reject_names BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION hc_test_reject_names()`); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS hc_test_reject_names ON audit_events; DROP FUNCTION IF EXISTS hc_test_reject_names()`)
	if migrateWorkspaceNames(ctx, pool, "test-operator") == nil {
		t.Fatal("late audit failure accepted")
	}
	var versions, approvals, audits int
	var archived, enabled bool
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM workspace_template_versions),(SELECT count(*) FROM approved_templates),v.archived,a.enabled FROM workspace_template_versions v JOIN approved_templates a ON a.version=v.id WHERE v.id=$1`, version).Scan(&versions, &approvals, &archived, &enabled); err != nil || versions != 1 || approvals != 1 || archived || !enabled {
		t.Fatal("failed migration left partial changes", err)
	}
	if _, err = pool.Exec(ctx, `DROP TRIGGER hc_test_reject_names ON audit_events; DROP FUNCTION hc_test_reject_names()`); err != nil {
		t.Fatal(err)
	}
	if err = migrateWorkspaceNames(ctx, pool, "test-operator"); err != nil {
		t.Fatal(err)
	}
	if err = migrateWorkspaceNames(ctx, pool, "test-replay"); err != nil {
		t.Fatal("migration could not replay", err)
	}
	var active string
	var oldSource, newSource []byte
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM workspace_template_versions),(SELECT count(*) FROM approved_templates),(SELECT count(*) FROM audit_events WHERE action='WORKSPACE_NAMES_MIGRATE'),t.active_version_id::text,old.source,new.source FROM workspace_templates t JOIN workspace_template_versions old ON old.id=$1 JOIN workspace_template_versions new ON new.id=t.active_version_id WHERE t.id=$2`, version, template).Scan(&versions, &approvals, &audits, &active, &oldSource, &newSource); err != nil || versions != 2 || approvals != 2 || audits != 1 || active == version {
		t.Fatal("replay changed immutable versions or audit", err)
	}
	var oldFiles, newFiles map[string][]byte
	json.Unmarshal(oldSource, &oldFiles)
	json.Unmarshal(newSource, &newFiles)
	if !bytes.Equal(oldFiles["main.tf"], source["main.tf"]) || bytes.Contains(newFiles["main.tf"], []byte("hc.coder_")) || !bytes.Contains(newFiles["main.tf"], []byte("hc.workspace_id")) {
		t.Fatal("template bytes or new labels incorrect")
	}
}
