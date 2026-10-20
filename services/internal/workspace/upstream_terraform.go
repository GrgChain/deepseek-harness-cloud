// Copyright (C) Coder Technologies, Inc.
// SPDX-License-Identifier: AGPL-3.0-only
// Adapted from provisioner/terraform/{safeenv,provision,executor}.go in Coder
// v2.37.4 on 2026-10-13. The platform supplies metadata without a Coder server.
package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

type TerraformOptions struct {
	Binary       string `json:"binary"`
	BinarySHA256 string `json:"binary_sha256"`
	Root         string `json:"root"`
	CLIConfig    string `json:"cli_config"`
}
type Terraform struct{ options TerraformOptions }

func NewTerraform(o TerraformOptions) (*Terraform, error) {
	for _, p := range []string{o.Binary, o.Root, o.CLIConfig} {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return nil, ErrUnavailable
		}
	}
	if len(o.BinarySHA256) != 64 {
		return nil, ErrUnavailable
	}
	info, err := os.Lstat(o.Root)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrUnavailable
	}
	for _, p := range []string{o.Binary, o.CLIConfig} {
		info, err := os.Lstat(p)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
			return nil, ErrUnavailable
		}
	}
	binary, err := os.ReadFile(o.Binary)
	if err != nil {
		return nil, ErrUnavailable
	}
	sum := sha256.Sum256(binary)
	if hex.EncodeToString(sum[:]) != o.BinarySHA256 {
		return nil, ErrUnavailable
	}
	return &Terraform{o}, nil
}
func (t *Terraform) Check(ctx context.Context) error {
	command := exec.CommandContext(ctx, t.options.Binary, "version", "-json")
	command.Env = t.environment(ProvisionTask{})
	command.Stderr = io.Discard
	raw, err := command.Output()
	var v struct {
		Version string `json:"terraform_version"`
	}
	if err != nil || json.Unmarshal(raw, &v) != nil || v.Version != "1.15.5" {
		return ErrUnavailable
	}
	return nil
}

// Coder's safeEnviron strips CODER_ variables. The embedded executor further
// allowlists host values so HC_ database/OIDC/model secrets cannot reach providers.
func (t *Terraform) environment(task ProvisionTask) []string {
	env := []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=" + t.options.Root, "LANG=C.UTF-8", "TF_IN_AUTOMATION=true", "TF_INPUT=false", "TF_CLI_CONFIG_FILE=" + t.options.CLIConfig, "CHECKPOINT_DISABLE=1"}
	if task.Build.ID == "" {
		return env
	}
	env = append(env, "CODER_AGENT_URL=http://127.0.0.1:18080", "CODER_WORKSPACE_TRANSITION="+task.Build.Transition, "CODER_WORKSPACE_ID="+task.Workspace.ID, "CODER_WORKSPACE_NAME="+task.Workspace.Name, "CODER_WORKSPACE_OWNER_ID="+task.Workspace.OwnerID, "CODER_WORKSPACE_OWNER=hc-"+task.Workspace.OwnerID, "CODER_WORKSPACE_OWNER_NAME=hc-"+task.Workspace.OwnerID, "CODER_WORKSPACE_OWNER_EMAIL=workspace@harness.local", "CODER_WORKSPACE_OWNER_LOGIN_TYPE=platform", "CODER_WORKSPACE_OWNER_GROUPS=[]", "CODER_WORKSPACE_OWNER_RBAC_ROLES=[]", "CODER_WORKSPACE_TEMPLATE_ID="+task.Workspace.TemplateID, "CODER_WORKSPACE_TEMPLATE_NAME=hc-dsh-initial", "CODER_WORKSPACE_TEMPLATE_VERSION="+task.Build.TemplateVersionID, "CODER_WORKSPACE_BUILD_ID="+task.Build.ID)
	for _, p := range task.Parameters {
		sum := sha256.Sum256([]byte(p.Name))
		suffix := hex.EncodeToString(sum[:])
		env = append(env, "CODER_PARAMETER_"+suffix+"="+p.Value, "CODER_PARAMETER_PREVIOUS_"+suffix+"="+p.Value)
	}
	return env
}
func writeProvisionFile(path string, raw []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".provision-")
	if err != nil {
		return ErrUnavailable
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(raw)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return ErrUnavailable
	}
	if os.Rename(f.Name(), path) != nil {
		return ErrUnavailable
	}
	return nil
}
func (t *Terraform) Apply(ctx context.Context, task ProvisionTask) ([]byte, error) {
	if !instanceUUID.MatchString(task.Workspace.ID) || !instanceUUID.MatchString(task.Build.ID) || task.Build.WorkspaceID != task.Workspace.ID || !validParameters(task.Parameters) {
		return nil, ErrConflict
	}
	dir := filepath.Join(t.options.Root, task.Workspace.ID)
	if err := os.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
		return nil, ErrUnavailable
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return nil, ErrUnavailable
	}
	lock, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer lock.Close()
	for {
		if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	for _, name := range []string{"main.tf", ".terraform.lock.hcl"} {
		if len(task.Source[name]) == 0 {
			return nil, ErrConflict
		}
		if err = writeProvisionFile(filepath.Join(dir, name), task.Source[name]); err != nil {
			return nil, err
		}
	}
	// Template variables are supplied by the approved immutable version.
	if err = writeProvisionFile(filepath.Join(dir, "variables.auto.tfvars.json"), task.Source["variables.json"]); err != nil {
		return nil, err
	}
	statePath := filepath.Join(dir, "terraform.tfstate")
	if info, inspectErr := os.Lstat(statePath); inspectErr == nil && (!info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0) {
		return nil, ErrUnavailable
	}
	if _, err = os.Lstat(statePath); os.IsNotExist(err) && len(task.PreviousState) > 0 {
		if err = writeProvisionFile(statePath, task.PreviousState); err != nil {
			return nil, err
		}
	} else if err != nil && !os.IsNotExist(err) {
		return nil, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	run := func(args ...string) error {
		command := exec.CommandContext(ctx, t.options.Binary, args...)
		command.Dir = dir
		command.Env = t.environment(task)
		// Retain only generic stage/status in the build record. Provider output can
		// include state and secrets and must not enter application logs or errors.
		command.Stdout = io.Discard
		command.Stderr = io.Discard
		command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		command.Cancel = func() error { return syscall.Kill(-command.Process.Pid, syscall.SIGINT) }
		command.WaitDelay = 5 * time.Second
		if err := command.Run(); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return ErrUnavailable
		}
		return nil
	}
	if err = t.Check(ctx); err == nil {
		err = run("init", "-no-color", "-input=false", "-lockfile=readonly")
	}
	plan := []string{"plan", "-no-color", "-input=false", "-out=provision.tfplan"}
	if task.Build.Transition == "delete" {
		plan = append(plan, "-destroy")
	}
	if err == nil {
		err = run(plan...)
	}
	if err == nil {
		err = run("apply", "-no-color", "-auto-approve", "-input=false", "-json", "provision.tfplan")
	}
	state, readErr := os.ReadFile(statePath)
	if readErr != nil && err == nil {
		err = ErrUnavailable
	}
	if len(state) > 16<<20 {
		return nil, ErrUnavailable
	}
	// All state files and plan files remain in the private engine directory.
	for _, name := range []string{"terraform.tfstate", "terraform.tfstate.backup", "provision.tfplan"} {
		_ = os.Chmod(filepath.Join(dir, name), 0600)
	}
	if err != nil && ctx.Err() != nil {
		return state, ctx.Err()
	}
	return state, err
}
