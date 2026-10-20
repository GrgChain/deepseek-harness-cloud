package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/access"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/config"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/hostcapacity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/runtimeapproval"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/worker"
	workspaceengine "github.com/jonluo94/deepseek-harness-cloud/services/internal/workspace"
)

var errLocalApproval = errors.New("local runtime approval requires matching reviewed sources, immutable images, successful runtime proofs and controlled embedded workspace configuration")

// The inspection container has no credentials, mounts or network. Its only
// output is public file hashes and npm artifact metadata from the pinned image.
func inspectRuntimeImage(ctx context.Context, image string) (runtimeapproval.ImageFacts, error) {
	script := `const fs=require('node:fs'),crypto=require('node:crypto');const files={};for(const n of ['package.json','package-lock.json','contract.mjs','private-file.mjs','bootstrap.mjs','start.mjs','external-model.mjs','messages-wire.mjs'])if(fs.existsSync('/opt/harness-runtime/'+n))files[n]=crypto.createHash('sha256').update(fs.readFileSync('/opt/harness-runtime/'+n)).digest('hex');const p=JSON.parse(fs.readFileSync('/opt/harness-runtime/package-lock.json')).packages['node_modules/@deepseek-ai/dsh'];process.stdout.write(JSON.stringify({files,package_version:p.version,package_integrity:p.integrity}));`
	raw, err := inspectImage(ctx, image, script)
	var facts runtimeapproval.ImageFacts
	if err != nil || webapi.StrictUnmarshal(raw, &facts) != nil {
		return facts, errLocalApproval
	}
	return facts, nil
}
func inspectImage(ctx context.Context, image, script string) ([]byte, error) {
	name := "hc-approval-" + webapi.ID()
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	command := exec.CommandContext(bounded, "docker", "--host", "unix:///var/run/docker.sock", "run", "--rm", "--name", name, "--network", "none", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--memory", "128m", "--pids-limit", "32", "--entrypoint", "node", image, "-e", script)
	command.Env = []string{"PATH=" + os.Getenv("PATH")}
	command.Stderr = io.Discard
	raw, err := command.Output()
	if err != nil {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = exec.CommandContext(cleanup, "docker", "--host", "unix:///var/run/docker.sock", "rm", "-f", name).Run()
		return nil, errLocalApproval
	}
	if len(raw) > 8192 {
		return nil, errLocalApproval
	}
	return raw, nil
}
func writeManifest(path string, m runtimeapproval.Manifest) error {
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if old, err := config.ReadPrivateFile(path); err == nil {
		if bytes.Equal(old, raw) {
			return nil
		}
		return runtimeapproval.ErrConflict
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errLocalApproval
	}
	if _, err = file.Write(raw); err == nil {
		err = file.Sync()
	}
	closed := file.Close()
	if err != nil {
		return errLocalApproval
	}
	if closed != nil {
		return errLocalApproval
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return errLocalApproval
	}
	defer directory.Close()
	return directory.Sync()
}
func approveLocalRuntime(ctx context.Context) error {
	return verifyAndApproveLocalRuntime(ctx, "", true)
}

func verifyAndApproveLocalRuntime(ctx context.Context, previous string, apply bool) error {
	o, err := worker.LoadOptions(".local/worker-config.json")
	if err != nil || o.Environment != "local" || o.WorkspaceEngine != "embedded" || o.Host.StorageBudget != 100<<30 {
		return errLocalApproval
	}
	raw, err := config.ReadPrivateFile(".local/workspace/template-contract.json")
	if err != nil {
		return errLocalApproval
	}
	var c runtimeapproval.Contract
	if webapi.StrictUnmarshal(raw, &c) != nil || c.OrganizationID != o.WorkspaceOrganizationID || c.Image != o.Host.RuntimeImage {
		return errLocalApproval
	}
	org, err := config.ReadSecret(".local/auth/organization-id")
	if err != nil {
		return errLocalApproval
	}
	accessOptions, err := access.LoadOptions(".local/access-config.json")
	if err != nil || accessOptions.OrganizationID != org || accessOptions.WorkspaceOrganizationID != c.OrganizationID || accessOptions.ControlUserID != o.ControlUserID {
		return errLocalApproval
	}
	dsn, err := config.ReadSecret(os.Getenv("HC_DB_DSN_FILE"))
	if err != nil {
		return errLocalApproval
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return errLocalApproval
	}
	defer pool.Close()
	client, err := workspaceengine.NewEmbedded(pool, o.WorkspaceOrganizationID, o.ControlUserID)
	if err != nil {
		return errLocalApproval
	}
	host, err := worker.NewLocalHost(o.Host)
	if err != nil {
		return err
	}
	if err = host.Capacity(); err != nil {
		return err
	}
	if err = host.Probe(ctx); err != nil {
		return err
	}
	terraform, err := workspaceengine.NewTerraform(o.Terraform)
	if err != nil || terraform.Check(ctx) != nil {
		return errLocalApproval
	}
	facts, err := hostcapacity.Read()
	if err != nil || facts.Slots() < 1 {
		return hostcapacity.ErrUnavailable
	}
	var proof struct {
		Images  map[string]string `json:"images"`
		Sources map[string]string `json:"source_sha256"`
		Passed  []string          `json:"passed"`
	}
	raw, err = config.ReadPrivateFile(".local/runtime-validation-evidence.json")
	if err != nil || json.Unmarshal(raw, &proof) != nil || proof.Images["hc-dsh-runtime"] != o.Host.RuntimeImage || proof.Images["hc-storage-helper"] != o.Host.StorageImage || proof.Images["hc-network-helper"] != o.Host.NetworkImage {
		return errLocalApproval
	}
	// Resource approval does not depend on user model credentials.
	// Real model acceptance is checked separately for each selected account.
	for _, required := range []string{"make runtime-test", "make storage-test", "make network-test", "make workspace-test"} {
		found := false
		for _, passed := range proof.Passed {
			found = found || passed == required || strings.HasPrefix(passed, required+" (")
		}
		if !found {
			return errLocalApproval
		}
	}
	var gatewayProof struct {
		Outcome string `json:"outcome"`
	}
	raw, err = config.ReadPrivateFile(".local/gateway-validation-evidence.json")
	if err != nil || json.Unmarshal(raw, &gatewayProof) != nil || gatewayProof.Outcome != "PASS" {
		return errLocalApproval
	}
	for path, expected := range proof.Sources {
		if !strings.HasPrefix(path, "infrastructure/") || filepath.Clean(path) != path {
			continue
		}
		data, e := os.ReadFile(path)
		if e != nil || runtimeapproval.SHA(data) != expected {
			return errLocalApproval
		}
	}
	sources := map[string][]byte{}
	for _, name := range []string{"main.tf", ".terraform.lock.hcl", "package.json", "package-lock.json", "contract.mjs", "private-file.mjs", "bootstrap.mjs", "start.mjs", "external-model.mjs", "messages-wire.mjs"} {
		root := "infrastructure/runtime"
		if name == "main.tf" || name == ".terraform.lock.hcl" {
			root = "infrastructure/terraform"
		}
		data, e := os.ReadFile(filepath.Join(root, name))
		if e != nil || len(data) == 0 || len(data) > 2<<20 {
			return errLocalApproval
		}
		sources[name] = data
	}
	image, err := inspectRuntimeImage(ctx, o.Host.RuntimeImage)
	if err != nil {
		return err
	}
	image.StorageImage, image.NetworkImage = o.Host.StorageImage, o.Host.NetworkImage
	for _, helper := range []struct{ image, path, local, name string }{{o.Host.StorageImage, "/opt/harness-storage/helper.py", "infrastructure/storage/helper.py", "storage-helper.py"}, {o.Host.NetworkImage, "/opt/harness-network/helper.py", "infrastructure/network/helper.py", "network-helper.py"}} {
		script := `process.stdout.write(require('node:crypto').createHash('sha256').update(require('node:fs').readFileSync('` + helper.path + `')).digest('hex'))`
		observed, e := inspectImage(ctx, helper.image, script)
		local, e2 := os.ReadFile(helper.local)
		if e != nil || e2 != nil || string(observed) != runtimeapproval.SHA(local) {
			return errLocalApproval
		}
		sources[helper.name] = local
		image.Files[helper.name] = string(observed)
	}
	m, err := runtimeapproval.Verify(ctx, client, c, o.ControlUserID, o.Host.RuntimeRoot, sources, image)
	if err != nil {
		return err
	}
	path := os.Getenv("HC_RUNTIME_MANIFEST_FILE")
	if path == "" {
		path = ".local/runtime-manifest.json"
	}
	if err = writeManifest(path, m); err != nil {
		return err
	}
	var database string
	if err = pool.QueryRow(ctx, `SELECT current_database()`).Scan(&database); err != nil || database != "hc_dev" {
		return errors.New("dev-runtime-approve only supports the isolated hc_dev database")
	}
	if previous != "" {
		var previousImage string
		if err = pool.QueryRow(ctx, `SELECT image_digest FROM approved_templates WHERE id=$1 AND version=$2`, c.TemplateID, previous).Scan(&previousImage); err != nil {
			return errLocalApproval
		}
		// A storage adapter release can keep the same runtime image while
		// publishing a new template and storage helper.
		retained := previousImage == o.Host.RuntimeImage
		for _, image := range o.Host.PreviousRuntimeImages {
			retained = retained || image == previousImage
		}
		if !retained {
			return errLocalApproval
		}
		return runtimeapproval.Upgrade(ctx, pool, org, c.TemplateID, os.Getenv("HC_OPERATOR_REF"), previous, m, apply)
	}
	return runtimeapproval.Approve(ctx, pool, org, c.TemplateID, os.Getenv("HC_OPERATOR_REF"), m, facts.Slots())
}
