package worker

import (
	"encoding/json"
	workspaceengine "github.com/jonluo94/deepseek-harness-cloud/services/internal/workspace"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkerOptionsRequirePrivateCompleteConfiguration(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "worker.json")
	valid := Options{SchemaVersion: 1, WorkspaceEngine: "embedded", Terraform: workspaceengine.TerraformOptions{Binary: filepath.Join(root, "terraform"), BinarySHA256: strings.Repeat("a", 64), Root: root, CLIConfig: filepath.Join(root, "terraform.rc")}, WorkspaceOrganizationID: "00000000-0000-4000-8000-000000000001", ControlUserID: "00000000-0000-4000-8000-000000000002", Environment: "local", DataKeyFile: filepath.Join(root, "key"), DataKeyID: "local-v1", ModelAllowlistFile: filepath.Join(root, "model-allowlist.json"), Host: HostOptions{RuntimeRoot: root, AssetRoot: root, RuntimeImage: "sha256:" + strings.Repeat("a", 64), StorageImage: "sha256:" + strings.Repeat("b", 64), NetworkImage: "sha256:" + strings.Repeat("c", 64), Domain: "harness.test", StorageBudget: 100 << 30}}
	save := func(value Options) {
		t.Helper()
		raw, _ := json.Marshal(value)
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	save(valid)
	got, err := LoadOptions(path)
	if err != nil || got.ControlUserID != valid.ControlUserID {
		t.Fatalf("private configuration rejected: %v", err)
	}
	for _, change := range []func(*Options){func(o *Options) { o.SchemaVersion = 2 }, func(o *Options) { o.ControlUserID = "me" }, func(o *Options) { o.WorkspaceOrganizationID = "" }, func(o *Options) { o.DataKeyFile = "relative" }, func(o *Options) { o.DataKeyID = "" }, func(o *Options) { o.WorkspaceEngine = "http://192.168.1.1:17080" }, func(o *Options) { o.WorkspaceEngine = "https://workspaceengine.example/?token=private" }, func(o *Options) { o.Environment = "bad env" }} {
		value := valid
		change(&value)
		save(value)
		if _, err := LoadOptions(path); err == nil {
			t.Fatal("incomplete or unsafe configuration accepted")
		}
	}
	for _, modelPath := range []string{"", "relative/model-policy.json"} {
		value := valid
		value.ModelAllowlistFile = modelPath
		save(value)
		if _, err := LoadOptions(path); err == nil {
			t.Fatal("Worker accepted missing or relative model allowlist")
		}
	}
	save(valid)
	raw, _ := os.ReadFile(path)
	for _, invalid := range []string{string(raw) + " {}", string(raw[:len(raw)-1]) + `,"unknown":"private"}`} {
		os.WriteFile(path, []byte(invalid), 0600)
		if _, err := LoadOptions(path); err == nil {
			t.Fatal("unknown or trailing configuration accepted")
		}
	}

	for _, legacy := range []string{"coder_endpoint", "coder_token_file", "coder_organization_id", "allow_local_coder_http"} {
		raw, _ := json.Marshal(valid)
		raw = append(raw[:len(raw)-1], []byte(`,"`+legacy+`":null}`)...)
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadOptions(path); err == nil {
			t.Fatal("legacy configuration alias accepted", legacy)
		}
	}
	save(valid)
	os.Chmod(path, 0644)
	if _, err := LoadOptions(path); err == nil {
		t.Fatal("public Worker configuration accepted")
	}
	if _, err := LoadOptions(""); err == nil {
		t.Fatal("unconfigured Worker accepted")
	}
}
