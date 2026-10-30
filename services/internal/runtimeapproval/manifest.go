// Package runtimeapproval holds the single supported runtime contract and the
// operator-only approval boundary. Runtime consumers only parse the frozen data.
package runtimeapproval

import (
	"errors"
	"regexp"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	workspaceengine "github.com/jonluo94/deepseek-harness-cloud/services/internal/workspace"
)

const (
	// The reviewed source commit is recorded separately from the npm artifact:
	// it is not asserted to be the build origin of the published package.
	ReviewCommit           = "d743267388641bc76f17c45ce8b4c231aed1d32c"
	PackageVersion         = "0.2.1-alpha.2"
	PackageIntegrity       = "sha512-CkRE0uAOg2ldL2VpfF4cNRsl178CH5nM+8YklPrlMZl5ln0KhVfd42dJuRlgOXZmkpizCRvrVS8LAL+GKfgk0w=="
	WorkspaceSourceVersion = workspaceengine.Version
	TerraformVersion       = "1.15.5"
	ConfigFormat           = "hc-runtime-config-v1"
	BootstrapAdapter       = "harness-cloud-bootstrap-v1"
	NativeAdapter          = "dsh-root-token-cookie-v1"
	ExternalModelAdapter   = "platform-messages-v1"
	StorageAdapter         = "bounded-ext4-v1"
	NetworkAdapter         = "public-web-v1"
)

var digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var uuidPattern = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`)
var ErrManifest = errors.New("complete supported runtime manifest is required")

type Manifest struct {
	SchemaVersion          int      `json:"schema_version"`
	UpstreamCommit         string   `json:"upstream_commit"`
	Image                  string   `json:"image_digest"`
	StorageImage           string   `json:"storage_image_digest"`
	NetworkImage           string   `json:"network_image_digest"`
	WorkspaceSourceVersion string   `json:"workspace_source_version"`
	TerraformVersion       string   `json:"terraform_version"`
	ProviderLock           string   `json:"terraform_provider_lock"`
	TemplateVersion        string   `json:"template_version"`
	ProfileDigest          string   `json:"profile_digest"`
	ConfigFormat           string   `json:"config_format"`
	Bootstrap              string   `json:"workspace_bootstrap_adapter"`
	NativeAuth             string   `json:"native_auth_adapter"`
	Paths                  []string `json:"persistent_paths"`
	ToolDigest             string   `json:"tool_policy_digest"`
	PackageVersion         string   `json:"runtime_package_version"`
	PackageIntegrity       string   `json:"runtime_package_integrity"`
	AdapterDigest          string   `json:"runtime_adapter_digest"`
	ExternalModels         string   `json:"external_model_adapter,omitempty"`
	StorageAdapter         string   `json:"storage_adapter,omitempty"`
	NetworkAdapter         string   `json:"network_adapter,omitempty"`
}

func (m Manifest) Valid() bool {
	if m.NetworkAdapter != "" && m.NetworkAdapter != NetworkAdapter {
		return false
	}
	if m.StorageAdapter != "" && m.StorageAdapter != StorageAdapter {
		return false
	}
	if m.ExternalModels != "" && m.ExternalModels != ExternalModelAdapter {
		return false
	}
	if !digestPattern.MatchString(m.StorageImage) || !digestPattern.MatchString(m.NetworkImage) {
		return false
	}
	if m.SchemaVersion != 1 || m.UpstreamCommit != ReviewCommit || !digestPattern.MatchString(m.Image) || m.WorkspaceSourceVersion != WorkspaceSourceVersion || m.TerraformVersion != TerraformVersion || !digestPattern.MatchString(m.ProviderLock) || !uuidPattern.MatchString(m.TemplateVersion) || !digestPattern.MatchString(m.ProfileDigest) || !digestPattern.MatchString(m.ToolDigest) || m.ConfigFormat != ConfigFormat || m.Bootstrap != BootstrapAdapter || m.NativeAuth != NativeAdapter || m.PackageVersion != PackageVersion || m.PackageIntegrity != PackageIntegrity || !digestPattern.MatchString(m.AdapterDigest) || len(m.Paths) != 2 {
		return false
	}
	return (m.Paths[0] == "/data/workspace" && m.Paths[1] == "/data/dsh") || (m.Paths[0] == "/data/dsh" && m.Paths[1] == "/data/workspace")
}
func (m Manifest) Matches(image, version string) bool {
	return m.Valid() && m.Image == image && m.TemplateVersion == version
}
func Parse(raw []byte) (Manifest, error) {
	var m Manifest
	if webapi.StrictUnmarshal(raw, &m) != nil || !m.Valid() {
		return m, ErrManifest
	}
	return m, nil
}
