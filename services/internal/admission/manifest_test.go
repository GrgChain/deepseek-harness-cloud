package admission

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/runtimeapproval"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	workspaceengine "github.com/jonluo94/deepseek-harness-cloud/services/internal/workspace"
)

func TestManifestApprovalBindsVersionImageAndSupportedAdapters(t *testing.T) {
	image, version := "sha256:"+strings.Repeat("a", 64), webapi.ID()
	manifest := map[string]any{"schema_version": 1, "terraform_version": runtimeapproval.TerraformVersion, "runtime_package_version": runtimeapproval.PackageVersion, "runtime_package_integrity": runtimeapproval.PackageIntegrity, "runtime_adapter_digest": image, "upstream_commit": runtimeapproval.ReviewCommit, "image_digest": image, "storage_image_digest": image, "network_image_digest": image, "workspace_source_version": workspaceengine.Version, "terraform_provider_lock": image, "template_version": version, "profile_digest": image, "config_format": "hc-runtime-config-v1", "workspace_bootstrap_adapter": "harness-cloud-bootstrap-v1", "native_auth_adapter": "dsh-root-token-cookie-v1", "persistent_paths": []string{"/data/workspace", "/data/dsh"}, "tool_policy_digest": image}
	raw, _ := json.Marshal(manifest)
	if !manifestApproved(raw, image, version) {
		t.Fatal("complete pinned manifest rejected")
	}
	for _, field := range []string{"schema_version", "upstream_commit", "image_digest", "workspace_source_version", "terraform_provider_lock", "template_version", "profile_digest", "config_format", "workspace_bootstrap_adapter", "native_auth_adapter", "persistent_paths", "tool_policy_digest"} {
		saved := manifest[field]
		delete(manifest, field)
		raw, _ := json.Marshal(manifest)
		if manifestApproved(raw, image, version) {
			t.Fatalf("missing %s opened runtime", field)
		}
		manifest[field] = saved
	}
	for key, value := range map[string]any{"extra": true, "template_version": webapi.ID(), "workspace_source_version": "latest", "native_auth_adapter": "unknown", "persistent_paths": []string{"/data/workspace", "/data/workspace"}, "profile_digest": "sha256:short"} {
		saved, existed := manifest[key]
		manifest[key] = value
		raw, _ := json.Marshal(manifest)
		if manifestApproved(raw, image, version) {
			t.Fatalf("unsupported %s opened runtime", key)
		}
		if existed {
			manifest[key] = saved
		} else {
			delete(manifest, key)
		}
	}
}
