package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/config"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/instances"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	workspaceengine "github.com/jonluo94/deepseek-harness-cloud/services/internal/workspace"
)

type pinnedTestContract struct {
	TemplateID      string `json:"template_id"`
	TemplateVersion string `json:"template_version"`
	ImageDigest     string `json:"image_digest"`
}

func realHostContract(t *testing.T) (HostOptions, pinnedTestContract, *workspaceengine.Client, string) {
	t.Helper()
	path := os.Getenv("HC_WORKER_HOST_CONTRACT_FILE")
	var options HostOptions
	var raw []byte
	var err error
	if path != "" {
		raw, err = config.ReadPrivateFile(path)
		if err != nil || json.Unmarshal(raw, &options) != nil {
			t.Fatal("private host contract unavailable or invalid")
		}
	} else {
		path = os.Getenv("HC_WORKER_CONFIG_FILE")
		if path == "" {
			t.Skip("explicit local Docker/Workspace Worker configuration required")
		}
		configured, err := LoadOptions(path)
		if err != nil {
			t.Fatal(err)
		}
		options = configured.Host
	}
	host, err := NewLocalHost(options)
	if err != nil {
		t.Fatal(err)
	}
	if err = host.Capacity(); err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(path)
	workspaceRoot := filepath.Join(root, "workspace")
	raw, err = config.ReadPrivateFile(filepath.Join(workspaceRoot, "template-contract.json"))
	if err != nil {
		t.Fatal("private Workspace contract unavailable")
	}
	var contract pinnedTestContract
	if json.Unmarshal(raw, &contract) != nil || contract.ImageDigest != options.RuntimeImage {
		t.Fatal("Workspace image contract mismatched")
	}
	raw, err = config.ReadPrivateFile(filepath.Join(workspaceRoot, "control.json"))
	var authority struct {
		Organization string `json:"organization_id"`
		Control      string `json:"control_user_id"`
	}
	if err != nil || json.Unmarshal(raw, &authority) != nil {
		t.Fatal("embedded authority unavailable")
	}
	dsnRaw, err := config.ReadPrivateFile(os.Getenv("HC_TEST_DB_DSN_FILE"))
	if err != nil {
		t.Fatal("isolated database required")
	}
	u, err := url.Parse(strings.TrimSpace(string(dsnRaw)))
	if err != nil {
		t.Fatal("invalid test database reference")
	}
	u.Path = "/hc_test_worker"
	ctx := context.Background()
	if err = db.Apply(ctx, u.String()); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal("test database unavailable")
	}
	t.Cleanup(pool.Close)
	if _, err = pool.Exec(ctx, `INSERT INTO workspace_authority(singleton,organization_id,control_user_id) VALUES(true,$1,$2) ON CONFLICT(singleton) DO UPDATE SET organization_id=excluded.organization_id,control_user_id=excluded.control_user_id`, authority.Organization, authority.Control); err != nil {
		t.Fatal(err)
	}
	client, err := workspaceengine.NewEmbedded(pool, authority.Organization, authority.Control)
	if err != nil {
		t.Fatal(err)
	}
	source := map[string][]byte{}
	for _, n := range []string{"main.tf", ".terraform.lock.hcl"} {
		source[n], err = os.ReadFile(filepath.Join(root, "..", "infrastructure/terraform", n))
		if err != nil {
			t.Fatal(err)
		}
	}
	source["variables.json"], err = config.ReadPrivateFile(filepath.Join(workspaceRoot, "template-variables.json"))
	if err != nil {
		t.Fatal(err)
	}
	var versionName string
	var inputVars map[string]string
	_ = json.Unmarshal(source["variables.json"], &inputVars)
	// Keep canonical import bytes consistent with operator approval.
	source["variables.json"], _ = json.Marshal(inputVars)
	versionName = "v-host-fixture-" + contract.TemplateVersion
	var file string
	_ = pool.QueryRow(ctx, `SELECT file_id::text FROM workspace_template_versions WHERE id=$1`, contract.TemplateVersion).Scan(&file)
	if file == "" {
		file = webapi.ID()
		if err = client.PublishTemplate(ctx, contract.TemplateID, contract.TemplateVersion, file, versionName, source); err != nil {
			t.Fatal(err)
		}
	}
	optionsRaw, err := config.ReadPrivateFile(os.Getenv("HC_TEST_TERRAFORM_OPTIONS_FILE"))
	var terraformOptions workspaceengine.TerraformOptions
	if err != nil || json.Unmarshal(optionsRaw, &terraformOptions) != nil {
		t.Fatal("pinned Terraform test configuration required")
	}
	terraformOptions.Root = filepath.Join(terraformOptions.Root, "test-worker")
	if err = os.Mkdir(terraformOptions.Root, 0700); err != nil && !os.IsExist(err) {
		t.Fatal(err)
	}
	provisioner, err := workspaceengine.NewTerraform(terraformOptions)
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- client.RunBuilds(runCtx, provisioner) }()
	t.Cleanup(func() { cancel(); <-done })
	raw, err = config.ReadPrivateFile(filepath.Join(root, "auth", "identity-mapping.json"))
	if err != nil {
		t.Fatal("owner mapping unavailable")
	}
	var mapping identity.Mapping
	if json.Unmarshal(raw, &mapping) != nil {
		t.Fatal("invalid owner mapping")
	}
	var owner string
	for _, member := range mapping.Members {
		if member.Role == "MEMBER" {
			owner = member.WorkspaceOwnerID
		}
	}
	if !uuidPattern.MatchString(owner) {
		t.Fatal("real member owner is required")
	}
	var org string
	_ = pool.QueryRow(ctx, `SELECT id::text FROM organizations LIMIT 1`).Scan(&org)
	if org == "" {
		org = "00000000-0000-4000-8000-000000000061"
		if _, err = pool.Exec(ctx, `INSERT INTO organizations(id,name,status) VALUES($1,'Worker fixture','ACTIVE')`, org); err != nil {
			t.Fatal(err)
		}
	}
	var user string
	if err = pool.QueryRow(ctx, `INSERT INTO users(id,identity_issuer,identity_subject,workspace_owner_id,status) VALUES($1,'https://embedded-worker.test',$2::text,$2::uuid,'ACTIVE') ON CONFLICT(workspace_owner_id) DO UPDATE SET status='ACTIVE' RETURNING id::text`, webapi.ID(), owner).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO memberships(org_id,user_id,role,status) VALUES($1,$2,'MEMBER','ACTIVE') ON CONFLICT(org_id,user_id) DO UPDATE SET status='ACTIVE'`, org, user); err != nil {
		t.Fatal(err)
	}
	return options, contract, client, owner
}

func TestLocalHostUsesEmbeddedWorkspaceAndOriginalHardCapacityAsset(t *testing.T) {
	options, contract, client, owner := realHostContract(t)
	host, err := NewLocalHost(options)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	instance, asset := webapi.ID(), webapi.ID()
	name, _ := workspaceengine.WorkspaceName(instance)
	p := ExecutionPlan{InstanceID: instance, OrganizationID: webapi.ID(), OwnerID: webapi.ID(), WorkspaceOwner: owner, WorkspaceName: name, Action: "CREATE", Desired: "RUNNING", AssetID: asset, AssetStatus: "PREPARING", ResourceName: "hc-" + asset, Capacity: 20 << 30, Version: 1, RuntimeGeneration: webapi.ID(), Endpoint: "https://api.deepseek.com/anthropic", modelKey: []byte("synthetic-worker-contract-no-inference")}
	p.Snapshot = instances.Snapshot{DefaultsVersion: 1, TemplateID: contract.TemplateID, TemplateVersion: contract.TemplateVersion, ImageDigest: contract.ImageDigest, Spec: instances.ResourceSpec{ID: webapi.ID(), CPU: 1000, Memory: 2 << 30, Storage: 20 << 30}, ModelID: webapi.ID(), ModelVersion: 1, Provider: "deepseek-official", Protocol: "deepseek-messages", Model: "deepseek-flash"}
	release, err := host.Acquire(ctx, instance)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	waitBuild := func(ctx context.Context, id string) workspaceengine.Build {
		t.Helper()
		for attempt := 0; attempt < 520; attempt++ {
			build, err := client.Build(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if build.Job.Status == "succeeded" {
				return build
			}
			if build.Job.Status == "failed" || build.Job.Status == "canceled" {
				t.Fatal("controlled Workspace build failed")
			}
			select {
			case <-ctx.Done():
				t.Fatal("Workspace contract deadline reached")
			case <-time.After(250 * time.Millisecond):
			}
		}
		t.Fatal("Workspace build did not complete")
		return workspaceengine.Build{}
	}
	var workspace workspaceengine.Workspace
	deleted := false
	prepared := false
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		if workspace.ID != "" && !deleted {
			build, err := client.CreateBuild(cleanup, workspace.ID, "delete", contract.TemplateVersion, nil)
			if err != nil {
				t.Error("owned Workspace test workspace cleanup rejected")
				return
			}
			waitBuild(cleanup, build.ID)
		}
		if _, err := host.ConfirmAbsent(cleanup, p, true); err != nil {
			t.Error("owned test compute is not absent; retaining its storage")
			return
		}
		if prepared {
			if _, err := dockerCommand(cleanup, nil, "volume", "rm", p.ResourceName); err != nil {
				t.Error("owned test volume is still in use; retaining its asset")
				return
			}
			script := "import os,subprocess,shutil; p='/host-dev/hc-cloud/'+os.environ['ASSET']\nif os.path.islink(p):\n d=os.readlink(p); subprocess.run(['losetup','-d',d],check=True); os.unlink(p)\na='/assets/'+os.environ['ASSET']\nif os.path.isdir(a): shutil.rmtree(a)"
			if _, err := dockerCommand(cleanup, nil, "run", "--rm", "--network", "none", "--privileged", "--env", "ASSET="+asset, "--mount", "type=bind,src="+options.AssetRoot+",dst=/assets", "--mount", "type=bind,src=/dev,dst=/host-dev", "--entrypoint", "python3", options.StorageImage, "-c", script); err != nil {
				t.Error("owned test filesystem cleanup failed")
			}
		}
		os.RemoveAll(filepath.Join(options.RuntimeRoot, instance))
		os.Remove(filepath.Join(options.RuntimeRoot, ".locks", instance))
	})
	assetProof, err := host.Prepare(ctx, p)
	if err != nil {
		t.Fatalf("prepare original storage: %v", err)
	}
	prepared = true
	if assetProof.FilesystemID != asset || assetProof.Capacity != 20<<30 {
		t.Fatal("original hard-capacity proof mismatched")
	}
	if err = host.Configure(ctx, p); err != nil {
		t.Fatalf("configure runtime: %v", err)
	}
	workspace, err = client.CreateWorkspace(ctx, owner, workspaceengine.CreateWorkspaceRequest{Name: name, TemplateVersionID: contract.TemplateVersion, RichParameterValues: []workspaceengine.Parameter{{Name: "instance_id", Value: instance}, {Name: "asset_id", Value: asset}}})
	if err != nil {
		t.Fatal(err)
	}
	if workspace.OwnerID != owner || workspace.TemplateID != contract.TemplateID {
		t.Fatal("Workspace ownership or template mismatched")
	}
	p.WorkspaceID = &workspace.ID
	waitBuild(ctx, workspace.LatestBuild.ID)
	started, err := host.Start(ctx, p, workspace.LatestBuild.ID)
	if err != nil {
		t.Fatalf("real runtime start: %v", err)
	}
	usageCtx, usageCancel := context.WithTimeout(ctx, StorageUsageTimeout)
	usage, usageErr := host.StorageUsage(usageCtx, p)
	usageCancel()
	if usageErr != nil || usage.Used < 0 || usage.Available <= 0 || usage.Used+usage.Available > p.Capacity {
		t.Fatal("real statfs usage probe failed", usage, usageErr)
	}
	observed, err := host.container(ctx, instance)
	if err != nil || observed == nil || observed.HostConfig.NanoCpus != 1000000000 || observed.HostConfig.Memory != 2<<30 || observed.HostConfig.MemorySwap != 2<<30 {
		t.Fatal("selected CPU/memory limits were not applied to the real container", err)
	}

	if !started.AssetReady || started.Running == nil || started.Running.Generation != p.RuntimeGeneration || started.Running.Host != host.host(instance) {
		t.Fatal("runtime readiness is not generation-bound")
	}
	if err = host.Configure(ctx, p); err != nil {
		t.Fatalf("matching running configuration was not recovered: %v", err)
	}
	nativeHealth, cancelNativeHealth := context.WithTimeout(ctx, 2*time.Second)
	appHealthy, accessHealthy := checkNativeHealth(nativeHealth, "http://"+started.Running.IP+":3080", publicAuthority(started.Running.Host, options.PublicPort), started.Running.NativeCookie)
	cancelNativeHealth()
	if !appHealthy || !accessHealthy {
		t.Fatal("real native HTTP/WS health or Origin denial failed within its bounded budget")
	}
	proof := "retained-go-worker-" + webapi.ID()
	sum := sha256.Sum256([]byte(proof))
	expected := hex.EncodeToString(sum[:])
	if _, err = dockerCommand(ctx, nil, "exec", "hc-"+instance, "node", "-e", "require('fs').writeFileSync('/data/workspace/retained-proof.txt',process.argv[1],{mode:0o600})", proof); err != nil {
		t.Fatal(err)
	}
	p.Observed, p.Desired, p.AssetStatus, p.FilesystemID = "RUNNING", "RUNNING", "READY", asset
	browseCtx, cancelBrowse := context.WithTimeout(ctx, StorageBrowseTimeout)
	_, preview, previewErr := host.BrowseStorage(browseCtx, p, instances.StorageBrowseInput{Action: "PREVIEW", Root: "workspace", Path: "retained-proof.txt"})
	cancelBrowse()
	if previewErr != nil || string(preview.Content) != proof {
		t.Fatalf("running workspace data was not readable through the read-only browser: %v", previewErr)
	}
	readonlyCheck := "import errno,os\np='/data/workspace/should-not-write'\ntry:\n fd=os.open(p,os.O_CREAT|os.O_EXCL|os.O_WRONLY,0o600)\nexcept OSError as error:\n assert error.errno==errno.EROFS\nelse:\n os.close(fd); os.unlink(p); raise RuntimeError('read-only mount accepted a write')"
	if _, err = dockerCommand(ctx, nil, "run", "--rm", "--network", "none", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "--user", "1000:1000", "--mount", "type=volume,src="+p.ResourceName+",dst=/data,readonly,volume-nocopy", "--entrypoint", "python3", options.StorageImage, "-B", "-c", readonlyCheck); err != nil {
		t.Fatalf("live sidecar did not expose the data volume as read-only: %v", err)
	}
	stillRunning, inspectErr := host.container(ctx, instance)
	if inspectErr != nil || stillRunning == nil || !stillRunning.State.Running {
		t.Fatal("live data browsing changed the workspace runtime state", inspectErr)
	}
	stop, err := client.CreateBuild(ctx, workspace.ID, "stop", contract.TemplateVersion, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitBuild(ctx, stop.ID)
	stopped, err := host.ConfirmAbsent(ctx, p, false)
	if err != nil || !stopped.ComputeAbsent || !stopped.AssetReady || stopped.WorkspaceAbsent {
		t.Fatalf("confirmed stop lost the original asset: %v", err)
	}
	p.Action = "START"
	p.AssetStatus = "READY"
	p.FilesystemID = asset
	p.RuntimeGeneration = webapi.ID()
	if _, err = host.Prepare(ctx, p); err != nil {
		t.Fatalf("original asset was not reusable: %v", err)
	}
	if err = host.Configure(ctx, p); err != nil {
		t.Fatal(err)
	}
	restart, err := client.CreateBuild(ctx, workspace.ID, "start", contract.TemplateVersion, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitBuild(ctx, restart.ID)
	resumed, err := host.Start(ctx, p, restart.ID)
	if err != nil || resumed.Running == nil || resumed.Running.Generation == started.Running.Generation {
		t.Fatalf("real restart reused an old generation: %v", err)
	}
	checksum, err := dockerCommand(ctx, nil, "exec", "hc-"+instance, "node", "-e", "console.log(require('crypto').createHash('sha256').update(require('fs').readFileSync('/data/workspace/retained-proof.txt')).digest('hex'))")
	if err != nil || strings.TrimSpace(string(checksum)) != expected {
		t.Fatal("original workspace file changed across stop/start")
	}
	remove, err := client.CreateBuild(ctx, workspace.ID, "delete", contract.TemplateVersion, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitBuild(ctx, remove.ID)
	deleted = true
	if _, err = client.Workspace(ctx, workspace.ID); !errors.Is(err, workspaceengine.ErrNotFound) {
		t.Fatal("Workspace workspace deletion was not confirmed")
	}
	retained, err := host.ConfirmAbsent(ctx, p, true)
	if err != nil || !retained.ComputeAbsent || !retained.WorkspaceAbsent || !retained.AssetReady || retained.FilesystemID != asset {
		t.Fatalf("delete lost retained storage: %v", err)
	}
	logs, err := dockerCommand(ctx, nil, "run", "--rm", "--network", "none", "--read-only", "--cap-drop", "ALL", "--mount", "type=volume,src="+p.ResourceName+",dst=/data,readonly,volume-nocopy", "--entrypoint", "node", options.RuntimeImage, "-e", "console.log(require('crypto').createHash('sha256').update(require('fs').readFileSync('/data/workspace/retained-proof.txt')).digest('hex'))")
	if err != nil || strings.TrimSpace(string(logs)) != expected || bytes.Contains(logs, p.ModelKey()) {
		t.Fatal("retained original storage checksum failed")
	}
}
