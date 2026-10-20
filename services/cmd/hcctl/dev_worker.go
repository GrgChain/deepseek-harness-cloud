package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/config"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/settings"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/worker"
	workspaceengine "github.com/jonluo94/deepseek-harness-cloud/services/internal/workspace"
)

// initLocalWorker freezes local image IDs and references existing private
// credentials. It never approves a template, changes quota or overwrites config.
func initLocalWorker(ctx context.Context, assetRoot, domain string) error {
	invalid := errors.New("local Worker initialization requires private workspace/auth files, approved local images and HC_STORAGE_ROOT/HC_INSTANCE_DOMAIN")
	root, err := filepath.Abs(".local")
	if err != nil {
		return invalid
	}
	path := filepath.Join(root, "worker-config.json")
	if _, err = os.Lstat(path); !os.IsNotExist(err) {
		return errors.New("Worker configuration already exists or cannot be inspected; refusing to overwrite")
	}
	if !filepath.IsAbs(assetRoot) || filepath.Clean(assetRoot) != assetRoot || domain == "" {
		return invalid
	}
	raw, err := config.ReadPrivateFile(filepath.Join(root, "workspace/template-variables.json"))
	if err != nil {
		return invalid
	}
	var variables struct {
		RuntimeImage string `json:"runtime_image"`
		RuntimeRoot  string `json:"runtime_root"`
	}
	if json.Unmarshal(raw, &variables) != nil {
		return invalid
	}
	raw, err = config.ReadPrivateFile(filepath.Join(root, "workspace/template-contract.json"))
	if err != nil {
		return invalid
	}
	var contract struct {
		Image        string `json:"image_digest"`
		Organization string `json:"organization_id"`
		Version      string `json:"workspace_source_version"`
	}
	if json.Unmarshal(raw, &contract) != nil || contract.Image != variables.RuntimeImage || contract.Version != workspaceengine.Version {
		return invalid
	}
	raw, err = config.ReadPrivateFile(filepath.Join(root, "workspace/control.json"))
	var authority workspaceControl
	if err != nil || json.Unmarshal(raw, &authority) != nil || authority.Organization != contract.Organization {
		return invalid
	}
	raw, err = config.ReadPrivateFile(filepath.Join(root, "workspace-engine/options.json"))
	var terraform workspaceengine.TerraformOptions
	if err != nil || json.Unmarshal(raw, &terraform) != nil {
		return invalid
	}
	runner, err := workspaceengine.NewTerraform(terraform)
	if err != nil || runner.Check(ctx) != nil {
		return invalid
	}
	image := func(tag string) (string, error) {
		command := exec.CommandContext(ctx, "docker", "--host", "unix:///var/run/docker.sock", "image", "inspect", "--format", "{{.Id}}", tag)
		command.Env = []string{"PATH=" + os.Getenv("PATH")}
		command.Stderr = io.Discard
		result, err := command.Output()
		if err != nil || len(result) > 128 {
			return "", invalid
		}
		return strings.TrimSpace(string(result)), nil
	}
	storage, err := image("hc-storage-helper:0.1.0")
	if err != nil {
		return invalid
	}
	network, err := image("hc-network-helper:0.1.0")
	if err != nil {
		return invalid
	}
	o := worker.Options{SchemaVersion: 1, WorkspaceEngine: "embedded", Terraform: terraform, WorkspaceOrganizationID: contract.Organization, ControlUserID: authority.Control, Environment: "local", DataKeyFile: filepath.Join(root, "auth/data-key"), DataKeyID: "local-v1", Host: worker.HostOptions{RuntimeRoot: variables.RuntimeRoot, AssetRoot: assetRoot, RuntimeImage: variables.RuntimeImage, StorageImage: storage, NetworkImage: network, Domain: domain, StorageBudget: 100 << 30}}
	o.ModelAllowlistFile = filepath.Join(root, "auth/model-allowlist.json")
	policyRaw, err := config.ReadPrivateFile(o.ModelAllowlistFile)
	if err != nil {
		return invalid
	}
	if _, err = settings.ParsePolicy(policyRaw); err != nil {
		return invalid
	}
	key, err := config.ReadPrivateFile(o.DataKeyFile)
	if err != nil || len(key) != 32 {
		return invalid
	}
	clear(key)
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
	raw, err = json.MarshalIndent(o, "", "  ")
	if err != nil {
		return invalid
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("private Worker configuration could not be created without overwriting")
	}
	if _, err = file.Write(raw); err != nil {
		file.Close()
		return invalid
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return invalid
	}
	if err = file.Close(); err != nil {
		return invalid
	}
	directory, err := os.Open(root)
	if err != nil {
		return invalid
	}
	defer directory.Close()
	return directory.Sync()
}
