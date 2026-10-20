package main

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
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

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
