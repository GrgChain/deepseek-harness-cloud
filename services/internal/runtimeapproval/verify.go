package runtimeapproval

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	workspaceengine "github.com/jonluo94/deepseek-harness-cloud/services/internal/workspace"
)

type Contract struct {
	TemplateID               string `json:"template_id"`
	TemplateVersion          string `json:"template_version"`
	OrganizationID           string `json:"organization_id"`
	WorkspaceSourceVersion   string `json:"workspace_source_version"`
	TerraformVersion         string `json:"terraform_version"`
	WorkspaceProviderVersion string `json:"workspace_provider_version"`
	DockerProvider           string `json:"docker_provider"`
	SourceSHA                string `json:"source_sha256"`
	Image                    string `json:"image_digest"`
}
type ImageFacts struct {
	StorageImage     string            `json:"storage_image_digest,omitempty"`
	NetworkImage     string            `json:"network_image_digest,omitempty"`
	Files            map[string]string `json:"files"`
	PackageVersion   string            `json:"package_version"`
	PackageIntegrity string            `json:"package_integrity"`
}
type TemplateReader interface {
	BuildInfo(context.Context) (workspaceengine.BuildInfo, error)
	User(context.Context, string) (workspaceengine.User, error)
	Template(context.Context, string) (workspaceengine.Template, error)
	TemplateVersion(context.Context, string) (workspaceengine.TemplateVersion, error)
	TemplateVariables(context.Context, string) ([]workspaceengine.Variable, error)
	TemplateSource(context.Context, string) (map[string][]byte, error)
}

func SHA(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }

// Verify binds the immutable workspace template source and packaged image files to the local
// reviewed sources. The caller obtains ImageFacts from the immutable image ID,
// never from a tag or a browser-supplied declaration.
func Verify(ctx context.Context, client TemplateReader, c Contract, controlUser, runtimeRoot string, sources map[string][]byte, image ImageFacts) (Manifest, error) {
	if err := verifyTemplate(ctx, client, c, controlUser, runtimeRoot, c.TemplateVersion, sources); err != nil {
		return Manifest{}, err
	}
	names := []string{"package.json", "package-lock.json", "contract.mjs", "private-file.mjs", "bootstrap.mjs", "start.mjs"}
	if len(image.Files) == 10 {
		names = append(names, "external-model.mjs", "messages-wire.mjs")
	}
	for _, name := range names {
		if len(sources[name]) == 0 || image.Files[name] != SHA(sources[name]) {
			return Manifest{}, ErrManifest
		}
	}
	return manifestFromFacts(c, sources, image)
}

// VerifyRetained checks the actual immutable image against the historical
// approval, rather than assuming today's runtime sources describe that image.
// Both template versions may remain available in the workspace module; builds pin a version.
func VerifyRetained(ctx context.Context, client TemplateReader, c Contract, controlUser, runtimeRoot, currentVersion string, sources map[string][]byte, image ImageFacts, approved Manifest) error {
	if !approved.Matches(c.Image, c.TemplateVersion) || !uuidPattern.MatchString(currentVersion) {
		return ErrManifest
	}
	if err := verifyTemplate(ctx, client, c, controlUser, runtimeRoot, currentVersion, sources); err != nil {
		return err
	}
	observed, err := manifestFromFacts(c, sources, image)
	if err != nil || !reflect.DeepEqual(observed, approved) {
		return ErrManifest
	}
	return nil
}

func verifyTemplate(ctx context.Context, client TemplateReader, c Contract, controlUser, runtimeRoot, allowedActive string, sources map[string][]byte) error {
	invalid := func() error { return ErrManifest }
	if !uuidPattern.MatchString(c.TemplateID) || !uuidPattern.MatchString(c.TemplateVersion) || !uuidPattern.MatchString(c.OrganizationID) || !uuidPattern.MatchString(controlUser) || c.WorkspaceSourceVersion != WorkspaceSourceVersion || c.TerraformVersion != TerraformVersion || c.WorkspaceProviderVersion != "2.19.0" || c.DockerProvider != "4.6.0" || !digestPattern.MatchString(c.Image) {
		return invalid()
	}
	info, err := client.BuildInfo(ctx)
	if err != nil || info.Version != WorkspaceSourceVersion || info.Telemetry {
		return invalid()
	}
	u, err := client.User(ctx, "me")
	if err != nil || u.ID != controlUser || !workspaceengine.OwnerStatusUsable(u.Status) || len(u.OrganizationIDs) != 1 || u.OrganizationIDs[0] != c.OrganizationID {
		return invalid()
	}
	owner := false
	for _, role := range u.Roles {
		owner = owner || role.Name == "owner"
	}
	if !owner {
		return invalid()
	}
	t, err := client.Template(ctx, c.TemplateID)
	if err != nil || t.ID != c.TemplateID || t.OrganizationID != c.OrganizationID || (t.ActiveVersion != c.TemplateVersion && t.ActiveVersion != allowedActive) || t.Deleted || t.Name != "hc-dsh-initial" {
		return invalid()
	}
	v, err := client.TemplateVersion(ctx, c.TemplateVersion)
	if err != nil || v.ID != c.TemplateVersion || v.TemplateID != c.TemplateID || v.OrganizationID != c.OrganizationID || v.Archived || v.ExternalAgent || v.Job.Status != "succeeded" || v.Job.OrganizationID != c.OrganizationID || v.Job.InitiatorID != controlUser {
		return invalid()
	}
	remote, err := client.TemplateSource(ctx, v.Job.FileID)
	if err != nil || len(remote) != 3 || len(sources["main.tf"]) == 0 || len(sources[".terraform.lock.hcl"]) == 0 || !bytes.Equal(remote["main.tf"], sources["main.tf"]) || !bytes.Equal(remote[".terraform.lock.hcl"], sources[".terraform.lock.hcl"]) {
		return invalid()
	}
	var variables struct {
		Image string `json:"runtime_image"`
		Root  string `json:"runtime_root"`
	}
	if webapi.StrictUnmarshal(remote["variables.json"], &variables) != nil || variables.Image != c.Image || variables.Root != runtimeRoot {
		return invalid()
	}
	// This serialization matches the pinned importer, including its spaces.
	ib, _ := json.Marshal(variables.Image)
	rb, _ := json.Marshal(variables.Root)
	canonical := []byte(fmt.Sprintf(`{"runtime_image": %s, "runtime_root": %s}`, ib, rb))
	content := append(append(append([]byte{}, sources["main.tf"]...), sources[".terraform.lock.hcl"]...), canonical...)
	if SHA(content) != c.SourceSHA || v.Name != "v0-"+c.SourceSHA[:20] {
		return invalid()
	}
	values, err := client.TemplateVariables(ctx, c.TemplateVersion)
	if err != nil || len(values) != 2 {
		return invalid()
	}
	seen := map[string]bool{}
	for _, value := range values {
		if value.Sensitive || seen[value.Name] || (value.Name != "runtime_image" && value.Name != "runtime_root") || (value.Name == "runtime_image" && value.Value != c.Image) || (value.Name == "runtime_root" && value.Value != runtimeRoot) {
			return invalid()
		}
		seen[value.Name] = true
	}
	return nil
}

func manifestFromFacts(c Contract, sources map[string][]byte, image ImageFacts) (Manifest, error) {
	invalid := func() (Manifest, error) { return Manifest{}, ErrManifest }
	if image.PackageVersion != PackageVersion || image.PackageIntegrity != PackageIntegrity || (len(image.Files) != 8 && len(image.Files) != 10) || !digestPattern.MatchString(image.StorageImage) || !digestPattern.MatchString(image.NetworkImage) {
		return invalid()
	}
	for _, name := range []string{"storage-helper.py", "network-helper.py"} {
		if len(sources[name]) == 0 || image.Files[name] != SHA(sources[name]) {
			return invalid()
		}
	}
	names := []string{"package.json", "package-lock.json", "contract.mjs", "private-file.mjs", "bootstrap.mjs", "start.mjs"}
	if len(image.Files) == 10 {
		names = append(names, "external-model.mjs", "messages-wire.mjs")
	}
	sort.Strings(names)
	var adapters bytes.Buffer
	for _, name := range names {
		if !digestPattern.MatchString("sha256:" + image.Files[name]) {
			return invalid()
		}
		fmt.Fprintf(&adapters, "%s\x00%s\n", name, image.Files[name])
	}
	m := Manifest{SchemaVersion: 1, UpstreamCommit: ReviewCommit, Image: c.Image, StorageImage: image.StorageImage, NetworkImage: image.NetworkImage, WorkspaceSourceVersion: WorkspaceSourceVersion, TerraformVersion: TerraformVersion, ProviderLock: "sha256:" + SHA(sources[".terraform.lock.hcl"]), TemplateVersion: c.TemplateVersion, ProfileDigest: "sha256:" + image.Files["contract.mjs"], ConfigFormat: ConfigFormat, Bootstrap: BootstrapAdapter, NativeAuth: NativeAdapter, Paths: []string{"/data/workspace", "/data/dsh"}, ToolDigest: "sha256:" + image.Files["contract.mjs"], PackageVersion: PackageVersion, PackageIntegrity: PackageIntegrity, AdapterDigest: "sha256:" + SHA(adapters.Bytes())}
	if len(image.Files) == 10 {
		m.ExternalModels = ExternalModelAdapter
	}
	if bytes.Contains(sources["storage-helper.py"], []byte("MAX_CAPACITY = 100 * GiB")) {
		m.StorageAdapter = StorageAdapter
	}
	if !m.Valid() {
		return invalid()
	}
	return m, nil
}
