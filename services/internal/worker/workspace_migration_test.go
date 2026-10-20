package worker

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/devlocal"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/instances"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/runtimeapproval"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/workspace"
)

func TestPriorStoppedBuildBridgesOnlyApprovedLabelMigration(t *testing.T) {
	path := os.Getenv("HC_TEST_DB_DSN_FILE")
	if path == "" {
		t.Skip("isolated PostgreSQL required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("private test connection missing")
	}
	u, err := url.Parse(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal("invalid private test connection")
	}
	u.Path = "/hc_test_workspace_bridge"
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
	if _, err = pool.Exec(ctx, `TRUNCATE workspace_templates,approved_templates CASCADE`); err != nil {
		t.Fatal(err)
	}
	org, control, template, oldVersion, nextVersion := webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID()
	client, err := workspace.NewEmbedded(pool, org, control)
	if err != nil {
		t.Fatal(err)
	}
	old := map[string][]byte{"main.tf": []byte(`locals { labels = ["hc.coder_owner_id", "hc.coder_workspace_id"] }`), ".terraform.lock.hcl": []byte("fixed lock"), "variables.json": []byte(`{"runtime_image":"fixed","runtime_root":"/private"}`)}
	next := map[string][]byte{"main.tf": []byte(`locals { labels = ["hc.workspace_owner_id", "hc.workspace_id"] }`), ".terraform.lock.hcl": old[".terraform.lock.hcl"], "variables.json": old["variables.json"]}
	if err = client.PublishTemplate(ctx, template, oldVersion, webapi.ID(), "old", old); err != nil {
		t.Fatal(err)
	}
	if err = client.PublishTemplate(ctx, template, nextVersion, webapi.ID(), "next", next); err != nil {
		t.Fatal(err)
	}
	d := "sha256:" + strings.Repeat("a", 64)
	m := runtimeapproval.Manifest{SchemaVersion: 1, UpstreamCommit: runtimeapproval.ReviewCommit, Image: d, StorageImage: d, NetworkImage: d, WorkspaceSourceVersion: runtimeapproval.WorkspaceSourceVersion, TerraformVersion: runtimeapproval.TerraformVersion, ProviderLock: d, ProfileDigest: d, ConfigFormat: runtimeapproval.ConfigFormat, Bootstrap: runtimeapproval.BootstrapAdapter, NativeAuth: runtimeapproval.NativeAdapter, Paths: []string{"/data/workspace", "/data/dsh"}, ToolDigest: d, PackageVersion: runtimeapproval.PackageVersion, PackageIntegrity: runtimeapproval.PackageIntegrity, AdapterDigest: d}
	for _, v := range []string{oldVersion, nextVersion} {
		m.TemplateVersion = v
		raw, _ = json.Marshal(m)
		if _, err = pool.Exec(ctx, `INSERT INTO approved_templates(id,version,name,image_digest,runtime_manifest,enabled) VALUES($1,$2,'test',$3,$4,false)`, template, v, d, raw); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = pool.Exec(ctx, `UPDATE workspace_template_versions SET archived=true WHERE id=$1`, oldVersion); err != nil {
		t.Fatal(err)
	}
	buildID := webapi.ID()
	b := workspace.Build{ID: buildID, TemplateVersionID: oldVersion, Transition: "stop", Job: workspace.Job{Status: "succeeded"}}
	p := ExecutionPlan{OrganizationID: webapi.ID(), LastBuildID: &buildID, Snapshot: instances.Snapshot{TemplateID: template, TemplateVersion: nextVersion}}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = devlocal.GrantWorker(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	workerRaw, err := os.ReadFile(filepath.Join(filepath.Dir(path), "worker-dsn"))
	if err != nil {
		t.Fatal("private Worker identity missing")
	}
	workerURL, err := url.Parse(strings.TrimSpace(string(workerRaw)))
	if err != nil {
		t.Fatal("invalid private Worker identity")
	}
	workerURL.Path = u.Path
	app, err := pgxpool.New(ctx, workerURL.String())
	if err != nil {
		t.Fatal("restricted Worker connection unavailable")
	}
	defer app.Close()
	if _, err = app.Exec(ctx, `UPDATE approved_templates SET enabled=false WHERE false`); err == nil {
		t.Fatal("Worker can write template approval")
	}
	e := Executor{store: NewExecutionStore(app, nil, "test"), organization: org}
	if err = e.verifyMigratedPriorBuild(ctx, p, b); err != nil {
		t.Fatal("approved stopped predecessor rejected", err)
	}
	for _, change := range []func(*ExecutionPlan, *workspace.Build){func(p *ExecutionPlan, b *workspace.Build) { p.LastBuildID = nil }, func(p *ExecutionPlan, b *workspace.Build) { b.Transition = "start" }, func(p *ExecutionPlan, b *workspace.Build) { b.Job.Status = "running" }, func(p *ExecutionPlan, b *workspace.Build) { p.Snapshot.TemplateVersion = webapi.ID() }, func(p *ExecutionPlan, b *workspace.Build) { p.Snapshot.TemplateID = webapi.ID() }} {
		badP, badB := p, b
		change(&badP, &badB)
		if e.verifyMigratedPriorBuild(ctx, badP, badB) == nil {
			t.Fatal("foreign or unsettled predecessor accepted")
		}
	}
	t.Run("explicit compatible runtime application preserves storage and rejects drift", func(t *testing.T) {
		oldRuntime := m
		oldRuntime.TemplateVersion = oldVersion
		oldRuntime.ExternalModels = runtimeapproval.ExternalModelAdapter
		nextRuntime := oldRuntime
		nextRuntime.TemplateVersion = nextVersion
		nextRuntime.Image = "sha256:" + strings.Repeat("b", 64)
		nextRuntime.AdapterDigest = nextRuntime.Image
		oldFiles := map[string][]byte{"main.tf": next["main.tf"], ".terraform.lock.hcl": next[".terraform.lock.hcl"], "variables.json": []byte(`{"runtime_image":"` + oldRuntime.Image + `","runtime_root":"/private"}`)}
		nextFiles := map[string][]byte{"main.tf": oldFiles["main.tf"], ".terraform.lock.hcl": oldFiles[".terraform.lock.hcl"], "variables.json": []byte(`{"runtime_image":"` + nextRuntime.Image + `","runtime_root":"/private"}`)}
		oldRaw, _ := json.Marshal(oldFiles)
		nextRaw, _ := json.Marshal(nextFiles)
		if !runtimeTemplateChange(oldRaw, nextRaw, oldRuntime, nextRuntime) {
			t.Fatal("compatible adapter source rejected")
		}
		for _, key := range []string{"main.tf", ".terraform.lock.hcl", "variables.json"} {
			bad := map[string][]byte{}
			for k, v := range nextFiles {
				bad[k] = v
			}
			bad[key] = []byte(`{"runtime_image":"` + nextRuntime.Image + `","runtime_root":"/other"}`)
			raw, _ := json.Marshal(bad)
			if runtimeTemplateChange(oldRaw, raw, oldRuntime, nextRuntime) {
				t.Fatal("source drift accepted", key)
			}
		}
		tx, e1 := app.Begin(ctx)
		if e1 != nil {
			t.Fatal(e1)
		}
		defer tx.Rollback(ctx)
		// Approval remains operator-only; fixture mutation uses the isolated root.
		for _, v := range []struct {
			id       string
			manifest runtimeapproval.Manifest
			files    []byte
			enabled  bool
		}{{oldVersion, oldRuntime, oldRaw, false}, {nextVersion, nextRuntime, nextRaw, true}} {
			mr, _ := json.Marshal(v.manifest)
			if _, e1 = pool.Exec(ctx, `UPDATE approved_templates SET runtime_manifest=$2,image_digest=$3,enabled=$4 WHERE version=$1`, v.id, mr, v.manifest.Image, v.enabled); e1 != nil {
				t.Fatal(e1)
			}
			if _, e1 = pool.Exec(ctx, `UPDATE workspace_template_versions SET source=$2,archived=false WHERE id=$1`, v.id, v.files); e1 != nil {
				t.Fatal(e1)
			}
		}
		source := instances.Snapshot{TemplateID: template, TemplateVersion: oldVersion, ImageDigest: oldRuntime.Image, ModelID: webapi.ID(), ModelVersion: 1, Provider: "platform-models", Protocol: "anthropic-messages", Model: "hc-old-v1", ApprovalDigest: d}
		target := source
		target.TemplateVersion = nextVersion
		target.ImageDigest = nextRuntime.Image
		target.ModelID = webapi.ID()
		target.Model = "hc-new-v1"
		target.ApprovalDigest = nextRuntime.Image
		if e1 = e.store.validateTarget(ctx, tx, "CONFIGURE", source, target); e1 != nil {
			t.Fatal("approved target rejected", e1)
		}
		if e1 = e.store.validateTarget(ctx, tx, "START", source, target); e1 == nil {
			t.Fatal("start upgraded runtime")
		}
		bad := target
		bad.DefaultsVersion++
		if e1 = e.store.validateTarget(ctx, tx, "CONFIGURE", source, bad); e1 == nil {
			t.Fatal("default drift accepted")
		}
		if e1 = e.verifyMigratedPriorBuild(ctx, p, b); e1 != nil {
			t.Fatal("approved runtime predecessor rejected", e1)
		}
		// Restore the label fixture for the final provider-lock rejection.
		for _, v := range []struct {
			id    string
			files map[string][]byte
		}{{oldVersion, old}, {nextVersion, next}} {
			raw, _ := json.Marshal(v.files)
			m.TemplateVersion = v.id
			mr, _ := json.Marshal(m)
			if _, e1 = pool.Exec(ctx, `UPDATE workspace_template_versions SET source=$2,archived=($1::uuid=$3::uuid) WHERE id=$1`, v.id, raw, oldVersion); e1 != nil {
				t.Fatal(e1)
			}
			if _, e1 = pool.Exec(ctx, `UPDATE approved_templates SET runtime_manifest=$2,image_digest=$3,enabled=false WHERE version=$1`, v.id, mr, d); e1 != nil {
				t.Fatal(e1)
			}
		}
	})
	next[".terraform.lock.hcl"] = []byte("changed provider")
	raw, _ = json.Marshal(next)
	if _, err = pool.Exec(ctx, `UPDATE workspace_template_versions SET source=$2 WHERE id=$1`, nextVersion, raw); err != nil {
		t.Fatal(err)
	}
	if e.verifyMigratedPriorBuild(ctx, p, b) == nil {
		t.Fatal("provider change accepted as label-only migration")
	}
}
