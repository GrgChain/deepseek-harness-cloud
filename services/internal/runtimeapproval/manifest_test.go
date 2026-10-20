package runtimeapproval

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func fixtureManifest() Manifest {
	d := "sha256:" + strings.Repeat("a", 64)
	return Manifest{SchemaVersion: 1, UpstreamCommit: ReviewCommit, Image: d, StorageImage: d, NetworkImage: d, WorkspaceSourceVersion: WorkspaceSourceVersion, TerraformVersion: TerraformVersion, ProviderLock: d, TemplateVersion: webapi.ID(), ProfileDigest: d, ConfigFormat: ConfigFormat, Bootstrap: BootstrapAdapter, NativeAuth: NativeAdapter, Paths: []string{"/data/workspace", "/data/dsh"}, ToolDigest: d, PackageVersion: PackageVersion, PackageIntegrity: PackageIntegrity, AdapterDigest: d}
}
func TestManifestBindsCompletePackageAndSupportedRuntime(t *testing.T) {
	m := fixtureManifest()
	raw, _ := json.Marshal(m)
	parsed, err := Parse(raw)
	if err != nil || !parsed.Matches(m.Image, m.TemplateVersion) {
		t.Fatal("valid frozen manifest rejected")
	}
	for _, change := range []func(*Manifest){
		func(m *Manifest) { m.UpstreamCommit = strings.Repeat("b", 40) }, func(m *Manifest) { m.PackageVersion = "latest" }, func(m *Manifest) { m.PackageIntegrity = "sha512:unknown" }, func(m *Manifest) { m.AdapterDigest = "" }, func(m *Manifest) { m.TerraformVersion = "latest" }, func(m *Manifest) { m.ConfigFormat = "unknown" }, func(m *Manifest) { m.Paths = []string{"/data/workspace", "/data/workspace"} },
	} {
		bad := m
		change(&bad)
		raw, _ := json.Marshal(bad)
		if _, err := Parse(raw); err == nil {
			t.Fatal("unsupported runtime opened approval")
		}
	}
	if m.Matches("sha256:"+strings.Repeat("b", 64), m.TemplateVersion) || m.Matches(m.Image, webapi.ID()) {
		t.Fatal("manifest accepted a different image/version")
	}
	for _, raw := range []string{`{}`, `{"schema_version":1,"schema_version":1}`, `null`} {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Fatal("incomplete or ambiguous manifest accepted")
		}
	}
}

func TestBoundedStorageUpgradePreservesLegacyAssetsAndRejectsDowngrade(t *testing.T) {
	old := fixtureManifest()
	next := old
	next.TemplateVersion = webapi.ID()
	next.StorageImage = "sha256:" + strings.Repeat("b", 64)
	next.StorageAdapter = StorageAdapter
	if !CompatibleUpgrade(old, next) {
		t.Fatal("reviewed additive storage upgrade rejected")
	}
	if CompatibleUpgrade(next, old) {
		t.Fatal("downgrade could strand configurable assets")
	}
	changed := next
	changed.ConfigFormat = "unsupported"
	if CompatibleUpgrade(old, changed) {
		t.Fatal("storage upgrade bypassed runtime format boundary")
	}
}
