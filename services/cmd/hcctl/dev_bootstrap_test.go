package main

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func TestBootstrapQuotaSurvivesRuntimeInitializationFailure(t *testing.T) {
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	u.Path = "/postgres"
	admin, err := pgx.Connect(ctx, u.String())
	if err != nil {
		t.Fatal("database administrator unavailable")
	}
	defer admin.Close(context.Background())
	database := "hc_test_bootstrap_quota_" + strings.ReplaceAll(webapi.ID(), "-", "")
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{database}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{database}.Sanitize()); err != nil {
			t.Error("cannot remove isolated database", err)
		}
	}()
	u.Path = "/" + database
	if err = db.Apply(ctx, u.String()); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	org := webapi.ID()
	if _, err = pool.Exec(ctx, `INSERT INTO organizations(id,name,status) VALUES($1,'Bootstrap quota test','ACTIVE')`, org); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	if err = os.Mkdir(".local", 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(".local/worker-config.json", []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HC_OPERATOR_REF", "test")
	t.Setenv("HC_DB_DSN_FILE", "missing-runtime-dsn")
	if err = bootstrapLocalResources(ctx, pool, org); err == nil {
		t.Fatal("missing runtime configuration accepted")
	}
	var instances, cpu, memory, storage int64
	if err = pool.QueryRow(ctx, `SELECT max_instances,cpu_millicores,memory_bytes,storage_bytes FROM quota_policies WHERE org_id=$1`, org).Scan(&instances, &cpu, &memory, &storage); err != nil || instances != 20 || cpu != 40000 || memory != 80<<30 || storage != 200<<30 {
		t.Fatal("runtime failure left organization quotas unavailable", instances, cpu, memory, storage, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE quota_policies SET max_instances=12,version=7 WHERE org_id=$1`, org); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(".local/worker-config.json"); err != nil {
		t.Fatal(err)
	}
	if err = bootstrapLocalResources(ctx, pool, org); err != nil {
		t.Fatal(err)
	}
	var version int64
	if err = pool.QueryRow(ctx, `SELECT version,max_instances FROM quota_policies WHERE org_id=$1`, org).Scan(&version, &instances); err != nil || version != 7 || instances != 12 {
		t.Fatal("quota replay changed saved settings", version, instances, err)
	}
}

func TestWorkspaceInitializationRestoresIdentifiersAfterReset(t *testing.T) {
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
	u.Path = "/hc_test_workspace_bootstrap"
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
	reset := func() {
		t.Helper()
		if _, err := pool.Exec(ctx, `TRUNCATE workspace_authority,workspace_templates CASCADE`); err != nil {
			t.Fatal(err)
		}
	}
	reset()
	t.Chdir(t.TempDir())
	for _, dir := range []string{".local/auth", ".local/workspace", "infrastructure/terraform"} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	org := webapi.ID()
	for name, value := range map[string]string{
		".local/migrator-dsn": u.String(), ".local/auth/organization-id": org,
		".local/workspace/template-variables.json":     `{"runtime_image":"sha256:` + strings.Repeat("a", 64) + `","runtime_root":"/private/runtime"}`,
		"infrastructure/terraform/main.tf":             "locals { value = 1 }",
		"infrastructure/terraform/.terraform.lock.hcl": "test lock",
	} {
		if err := os.WriteFile(name, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	dsnPath, err := filepath.Abs(".local/migrator-dsn")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HC_DB_DSN_FILE", dsnPath)
	t.Setenv("HC_OPERATOR_REF", "test")
	initialize := func() {
		t.Helper()
		for _, command := range []string{"workspace-init", "workspace-template"} {
			if err := runWorkspace(command); err != nil {
				t.Fatal(command, err)
			}
		}
	}
	read := func(path string) string {
		t.Helper()
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	initialize()
	control, contract := read(".local/workspace/control.json"), read(".local/workspace/template-contract.json")
	// Recreate exactly the state left by wiping PostgreSQL but keeping .local.
	reset()
	initialize()
	initialize()
	if read(".local/workspace/control.json") != control || read(".local/workspace/template-contract.json") != contract {
		t.Fatal("reset or replay changed private workspace identity or frozen template identifiers")
	}
	var templates, versions int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM workspace_templates),(SELECT count(*) FROM workspace_template_versions)`).Scan(&templates, &versions); err != nil || templates != 1 || versions != 1 {
		t.Fatal("replay duplicated workspace templates", templates, versions, err)
	}
}
