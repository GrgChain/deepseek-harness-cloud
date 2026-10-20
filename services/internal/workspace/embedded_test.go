package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type provisionerFunc func(context.Context, ProvisionTask) ([]byte, error)

func (f provisionerFunc) Apply(ctx context.Context, task ProvisionTask) ([]byte, error) {
	return f(ctx, task)
}

func TestEmbeddedInterruptedBuildResumesOnceWithPartialState(t *testing.T) {
	c, pool, owner := embeddedFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	template, version, file := webapi.ID(), webapi.ID(), webapi.ID()
	source := map[string][]byte{"main.tf": []byte("terraform {}"), ".terraform.lock.hcl": []byte("# lock"), "variables.json": []byte(`{"runtime_image":"fixture","runtime_root":"fixture"}`)}
	if err := c.PublishTemplate(ctx, template, version, file, "v-interrupted", source); err != nil {
		t.Fatal(err)
	}
	instance, asset := webapi.ID(), webapi.ID()
	name, _ := WorkspaceName(instance)
	w, err := c.CreateWorkspace(ctx, owner, CreateWorkspaceRequest{Name: name, TemplateVersionID: version, RichParameterValues: []Parameter{{"instance_id", instance}, {"asset_id", asset}}})
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	done := make(chan error, 1)
	interrupted, stop := context.WithCancel(ctx)
	defer stop()
	partial := []byte(`{"partial":"original-resource"}`)
	go func() {
		done <- c.RunPendingBuilds(interrupted, provisionerFunc(func(run context.Context, task ProvisionTask) ([]byte, error) {
			close(started)
			<-run.Done()
			return partial, run.Err()
		}))
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("first runner did not start")
	}
	// Another Worker must not execute the same build while its owner holds the lock.
	var competingCalls atomic.Int32
	err = c.RunPendingBuilds(ctx, provisionerFunc(func(context.Context, ProvisionTask) ([]byte, error) {
		competingCalls.Add(1)
		return nil, nil
	}))
	stop()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("interrupted runner did not exit")
	}
	if err != nil || competingCalls.Load() != 0 {
		t.Fatal("competing Worker executed the in-flight build", err)
	}
	var state []byte
	var status string
	if err = pool.QueryRow(ctx, `SELECT job_status,provisioner_state FROM workspace_builds WHERE id=$1`, w.LatestBuild.ID).Scan(&status, &state); err != nil || status != "running" || !bytes.Equal(state, partial) {
		t.Fatal("interruption lost partial state or published a terminal failure", err)
	}
	var resumed ProvisionTask
	err = c.RunPendingBuilds(ctx, provisionerFunc(func(_ context.Context, task ProvisionTask) ([]byte, error) {
		resumed = task
		return []byte(`{"resumed":true}`), nil
	}))
	if err != nil || resumed.Build.ID != w.LatestBuild.ID || !bytes.Equal(resumed.PreviousState, partial) {
		t.Fatal("restart did not reconcile the original build and state", err)
	}
	b, err := c.Build(ctx, w.LatestBuild.ID)
	var count int
	if err != nil || b.Job.Status != "succeeded" {
		t.Fatal("resumed build did not complete", err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM workspace_builds WHERE workspace_id=$1`, w.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("restart allocated another build", err)
	}
}

// This fixture uses only hc_test_embedded; it cannot mutate the user's database.
func embeddedFixture(t *testing.T) (*Client, *pgxpool.Pool, string) {
	t.Helper()
	path := os.Getenv("HC_TEST_DB_DSN_FILE")
	if path == "" {
		t.Skip("isolated PostgreSQL required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("test database reference unavailable")
	}
	u, err := url.Parse(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal("test database reference invalid")
	}
	u.Path = "/hc_test_embedded"
	ctx := context.Background()
	if err := db.Apply(ctx, u.String()); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal("test database connection failed")
	}
	t.Cleanup(pool.Close)
	if _, err = pool.Exec(ctx, `TRUNCATE workspace_builds,workspace_workspaces,workspace_template_versions,workspace_templates,workspace_authority CASCADE`); err != nil {
		t.Fatal(err)
	}
	org, control, owner, user := webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID()
	// The repository supports one platform organization, reused across reruns.
	if err := pool.QueryRow(ctx, `INSERT INTO organizations(id,name,status) VALUES($1,'Embedded fixture','ACTIVE') ON CONFLICT DO NOTHING RETURNING id::text`, org).Scan(&org); err != nil {
		if err = pool.QueryRow(ctx, `SELECT id::text FROM organizations LIMIT 1`).Scan(&org); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = pool.Exec(ctx, `INSERT INTO users(id,identity_issuer,identity_subject,workspace_owner_id,status) VALUES($1::uuid,'https://embedded.test',$1::text,$2,'ACTIVE');`, user, owner); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO memberships(org_id,user_id,role,status) VALUES($1,$2,'MEMBER','ACTIVE')`, org, user); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO workspace_authority(singleton,organization_id,control_user_id) VALUES(true,$1,$2) ON CONFLICT(singleton) DO UPDATE SET organization_id=excluded.organization_id,control_user_id=excluded.control_user_id`, org, control); err != nil {
		t.Fatal(err)
	}
	c, err := NewEmbedded(pool, org, control)
	if err != nil {
		t.Fatal(err)
	}
	return c, pool, owner
}

func TestEmbeddedLifecyclePinsVersionsAndSerializesBuilds(t *testing.T) {
	c, pool, owner := embeddedFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	template, version, file := webapi.ID(), webapi.ID(), webapi.ID()
	source := map[string][]byte{"main.tf": []byte("terraform {}"), ".terraform.lock.hcl": []byte("# lock"), "variables.json": []byte(`{"runtime_image":"sha256:` + strings.Repeat("a", 64) + `","runtime_root":"/private/runtime"}`)}
	if err := c.PublishTemplate(ctx, template, version, file, "v-fixture", source); err != nil {
		t.Fatal(err)
	}
	published, err := c.Template(ctx, template)
	if err != nil || published.ActiveVersion != version {
		t.Fatal("published template identity was lost", err)
	}
	v, err := c.TemplateVersion(ctx, version)
	if err != nil || v.TemplateID != template || v.Job.FileID != file {
		t.Fatal("template version no longer references its immutable source", err)
	}
	stored, err := c.TemplateSource(ctx, file)
	if err != nil || !bytes.Equal(stored["main.tf"], source["main.tf"]) || !bytes.Equal(stored["variables.json"], source["variables.json"]) {
		t.Fatal("template source did not round trip", err)
	}
	variables, err := c.TemplateVariables(ctx, version)
	if err != nil || len(variables) != 2 || variables[0].Value != "sha256:"+strings.Repeat("a", 64) || variables[1].Value != "/private/runtime" {
		t.Fatal("template variables did not match the pinned source", err)
	}
	// Published versions are immutable even when the caller reuses their ID.
	changed := map[string][]byte{}
	for k, v := range source {
		changed[k] = v
	}
	changed["main.tf"] = []byte("changed")
	if err := c.PublishTemplate(ctx, template, version, file, "v-fixture", changed); !errors.Is(err, ErrConflict) {
		t.Fatal("published template was mutated", err)
	}
	instance, asset := webapi.ID(), webapi.ID()
	name, _ := WorkspaceName(instance)
	w, err := c.CreateWorkspace(ctx, owner, CreateWorkspaceRequest{Name: name, TemplateVersionID: version, RichParameterValues: []Parameter{{"instance_id", instance}, {"asset_id", asset}}})
	if err != nil {
		t.Fatal(err)
	}
	if w.LatestBuild.BuildNumber != 1 || w.LatestBuild.TemplateVersionID != version || w.AutomaticUpdates != "never" || w.LatestBuild.Job.Status != "pending" {
		t.Fatal("initial build contract changed")
	}
	if _, err = c.CreateWorkspace(ctx, owner, CreateWorkspaceRequest{Name: w.Name, TemplateVersionID: version, RichParameterValues: []Parameter{{"instance_id", instance}, {"asset_id", asset}}}); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate workspace accepted", err)
	}
	items, err := c.WorkspaceInventory(ctx, published.OrganizationID)
	if err != nil || len(items) != 1 || items[0].ID != w.ID {
		t.Fatal("inventory lost the managed workspace", err)
	}
	if _, err = c.WorkspaceInventory(ctx, webapi.ID()); !errors.Is(err, ErrUnavailable) {
		t.Fatal("inventory accepted a different organization", err)
	}
	if _, err = c.CreateBuild(ctx, w.ID, "stop", version, nil); !errors.Is(err, ErrConflict) {
		t.Fatal("concurrent build accepted", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE workspace_builds SET job_status='succeeded',provisioner_state='{}' WHERE id=$1`, w.LatestBuild.ID); err != nil {
		t.Fatal(err)
	}
	b, err := c.CreateBuild(ctx, w.ID, "stop", version, nil)
	if err != nil {
		t.Fatal(err)
	}
	if b.BuildNumber != 2 || b.Status != "pending" {
		t.Fatal("build numbering changed")
	}
	params, err := c.BuildParameters(ctx, b.ID)
	if err != nil || len(params) != 2 {
		t.Fatal("immutable parameters were lost", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE workspace_builds SET job_status='succeeded',provisioner_state='{}' WHERE id=$1`, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = c.CreateBuild(ctx, w.ID, "start", version, []Parameter{{"instance_id", instance}, {"asset_id", webapi.ID()}}); !errors.Is(err, ErrConflict) {
		t.Fatal("asset mutation accepted", err)
	}
	// All ownership checks are local: disabling membership must immediately deny.
	if _, err = pool.Exec(ctx, `UPDATE memberships SET status='DISABLED' WHERE user_id=(SELECT id FROM users WHERE workspace_owner_id=$1)`, owner); err != nil {
		t.Fatal(err)
	}
	if _, err = c.CreateBuild(ctx, w.ID, "start", version, nil); err == nil {
		t.Fatal("disabled owner started workspace")
	}
}

func TestEmbeddedRunsRealTerraformWithoutCoderServer(t *testing.T) {
	path := os.Getenv("HC_TEST_TERRAFORM_OPTIONS_FILE")
	if path == "" {
		t.Skip("pinned local Terraform required")
	}
	var options TerraformOptions
	raw, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(raw, &options) != nil {
		t.Fatal("Terraform test configuration unavailable")
	}
	options.Root = t.TempDir()
	if err = os.Chmod(options.Root, 0700); err != nil {
		t.Fatal(err)
	}
	runner, err := NewTerraform(options)
	if err != nil {
		t.Fatal(err)
	}
	c, pool, owner := embeddedFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	template, version, file := webapi.ID(), webapi.ID(), webapi.ID()
	source := map[string][]byte{"main.tf": []byte(`terraform { required_version = "1.15.5" }
variable "runtime_image" { type = string }
variable "runtime_root" { type = string }
resource "terraform_data" "fixture" { input = "no-coder-server" }
`), ".terraform.lock.hcl": []byte("# builtin provider\n"), "variables.json": []byte(`{"runtime_image":"fixture","runtime_root":"fixture"}`)}
	if err = c.PublishTemplate(ctx, template, version, file, "v-terraform", source); err != nil {
		t.Fatal(err)
	}
	instance, asset := webapi.ID(), webapi.ID()
	name, _ := WorkspaceName(instance)
	w, err := c.CreateWorkspace(ctx, owner, CreateWorkspaceRequest{Name: name, TemplateVersionID: version, RichParameterValues: []Parameter{{"instance_id", instance}, {"asset_id", asset}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = c.RunPendingBuilds(ctx, runner); err != nil {
		t.Fatal(err)
	}
	b, err := c.Build(ctx, w.LatestBuild.ID)
	if err != nil || b.Job.Status != "succeeded" || b.Status != "running" {
		t.Fatal("native Terraform start failed", b.Job.Status, err)
	}
	var state []byte
	if err = pool.QueryRow(ctx, `SELECT provisioner_state FROM workspace_builds WHERE id=$1`, b.ID).Scan(&state); err != nil || !bytes.Contains(state, []byte("no-coder-server")) {
		t.Fatal("Terraform state was not persisted", err)
	}
	next, err := c.CreateBuild(ctx, w.ID, "delete", version, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.RunPendingBuilds(ctx, runner); err != nil {
		t.Fatal(err)
	}
	b, err = c.Build(ctx, next.ID)
	if err != nil || b.Status != "deleted" {
		t.Fatal("native Terraform delete failed", b.Job.Status, err)
	}
	if _, err = c.Workspace(ctx, w.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted workspace remained active", err)
	}
	if _, err = c.WorkspaceByName(ctx, owner, name); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted workspace remained visible by name", err)
	}
	retained, err := c.WorkspaceByNameIncludingDeleted(ctx, owner, name)
	if err != nil || retained.ID != w.ID || retained.LatestBuild.ID != next.ID {
		t.Fatal("reconciliation lost the original deleted workspace", err)
	}
	if _, err = c.WorkspaceByNameIncludingDeleted(ctx, webapi.ID(), name); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted workspace was visible to another owner", err)
	}
	// Build records and pinned template bytes survive deletion.
	if _, err = c.Build(ctx, w.LatestBuild.ID); err != nil {
		t.Fatal("build history was lost", err)
	}
}
