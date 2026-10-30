package runtimeapproval

import (
	"context"
	"fmt"
	"testing"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	workspaceengine "github.com/jonluo94/deepseek-harness-cloud/services/internal/workspace"
)

type sourceFixture struct {
	user      workspaceengine.User
	template  workspaceengine.Template
	version   workspaceengine.TemplateVersion
	variables []workspaceengine.Variable
	files     map[string][]byte
}

func (f *sourceFixture) BuildInfo(context.Context) (workspaceengine.BuildInfo, error) {
	return workspaceengine.BuildInfo{Version: WorkspaceSourceVersion}, nil
}
func (f *sourceFixture) User(context.Context, string) (workspaceengine.User, error) {
	return f.user, nil
}
func (f *sourceFixture) Template(context.Context, string) (workspaceengine.Template, error) {
	return f.template, nil
}
func (f *sourceFixture) TemplateVersion(context.Context, string) (workspaceengine.TemplateVersion, error) {
	return f.version, nil
}
func (f *sourceFixture) TemplateVariables(context.Context, string) ([]workspaceengine.Variable, error) {
	return f.variables, nil
}
func (f *sourceFixture) TemplateSource(context.Context, string) (map[string][]byte, error) {
	return f.files, nil
}
func verificationFixture() (*sourceFixture, Contract, map[string][]byte, ImageFacts) {
	c := Contract{TemplateID: webapi.ID(), TemplateVersion: webapi.ID(), OrganizationID: webapi.ID(), WorkspaceSourceVersion: WorkspaceSourceVersion, TerraformVersion: TerraformVersion, WorkspaceProviderVersion: "2.19.0", DockerProvider: "4.6.0", Image: fixtureManifest().Image}
	sources := map[string][]byte{}
	for _, name := range []string{"main.tf", ".terraform.lock.hcl", "package.json", "package-lock.json", "contract.mjs", "private-file.mjs", "bootstrap.mjs", "start.mjs", "storage-helper.py", "network-helper.py"} {
		sources[name] = []byte("reviewed " + name)
	}
	vars := []byte(fmt.Sprintf(`{"runtime_image": "%s", "runtime_root": "/private/runtime"}`, c.Image))
	c.SourceSHA = SHA(append(append(append([]byte{}, sources["main.tf"]...), sources[".terraform.lock.hcl"]...), vars...))
	f := &sourceFixture{user: workspaceengine.User{ID: webapi.ID(), Status: "active", OrganizationIDs: []string{c.OrganizationID}, Roles: []workspaceengine.Role{{Name: "owner"}}}, template: workspaceengine.Template{ID: c.TemplateID, OrganizationID: c.OrganizationID, Name: "hc-dsh-initial", ActiveVersion: c.TemplateVersion}, variables: []workspaceengine.Variable{{Name: "runtime_image", Value: c.Image}, {Name: "runtime_root", Value: "/private/runtime"}}, files: map[string][]byte{"main.tf": sources["main.tf"], ".terraform.lock.hcl": sources[".terraform.lock.hcl"], "variables.json": vars}}
	f.version.ID, f.version.TemplateID, f.version.OrganizationID, f.version.Name = c.TemplateVersion, c.TemplateID, c.OrganizationID, "v0-"+c.SourceSHA[:20]
	f.version.Job.Status, f.version.Job.InitiatorID, f.version.Job.OrganizationID, f.version.Job.FileID = "succeeded", f.user.ID, c.OrganizationID, webapi.ID()
	image := ImageFacts{StorageImage: c.Image, NetworkImage: c.Image, Files: map[string]string{}, PackageVersion: PackageVersion, PackageIntegrity: PackageIntegrity}
	for name, data := range sources {
		if name != "main.tf" && name != ".terraform.lock.hcl" {
			image.Files[name] = SHA(data)
		}
	}
	return f, c, sources, image
}
func TestApprovalInspectsActualArchiveImageAndControlOwner(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*sourceFixture, *Contract, map[string][]byte, *ImageFacts)
	}{
		{"valid", func(*sourceFixture, *Contract, map[string][]byte, *ImageFacts) {}},
		{"archive replaced", func(f *sourceFixture, _ *Contract, _ map[string][]byte, _ *ImageFacts) {
			f.files["main.tf"] = []byte("agent/app bypass")
		}},
		{"extra variable", func(f *sourceFixture, _ *Contract, _ map[string][]byte, _ *ImageFacts) {
			f.variables = append(f.variables, workspaceengine.Variable{Name: "credential", Value: "unsafe"})
		}},
		{"different image files", func(_ *sourceFixture, _ *Contract, _ map[string][]byte, i *ImageFacts) {
			i.Files["start.mjs"] = SHA([]byte("unsafe"))
		}},
		{"different artifact", func(_ *sourceFixture, _ *Contract, _ map[string][]byte, i *ImageFacts) {
			i.PackageIntegrity = "unknown"
		}},
		{"owner disabled", func(f *sourceFixture, _ *Contract, _ map[string][]byte, _ *ImageFacts) { f.user.Status = "suspended" }},
		{"version changed", func(f *sourceFixture, _ *Contract, _ map[string][]byte, _ *ImageFacts) {
			f.template.ActiveVersion = webapi.ID()
		}},
		{"agent present", func(f *sourceFixture, _ *Contract, _ map[string][]byte, _ *ImageFacts) {
			f.version.ExternalAgent = true
		}},
		{"wrong importer", func(f *sourceFixture, _ *Contract, _ map[string][]byte, _ *ImageFacts) {
			f.version.Job.InitiatorID = webapi.ID()
		}},
		{"source changed", func(_ *sourceFixture, _ *Contract, s map[string][]byte, _ *ImageFacts) {
			s["contract.mjs"] = []byte("changed policy")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, c, s, i := verificationFixture()
			tc.mutate(f, &c, s, &i)
			m, err := Verify(context.Background(), f, c, f.user.ID, "/private/runtime", s, i)
			if (err == nil) != (tc.name == "valid") {
				t.Fatalf("accepted=%v", err == nil)
			}
			if err == nil && !m.Matches(c.Image, c.TemplateVersion) {
				t.Fatal("manifest lost frozen identity")
			}
		})
	}
}

func TestApprovalBindsPublicWebPolicyToInspectedNetworkHelper(t *testing.T) {
	f, c, sources, image := verificationFixture()
	sources["network-helper.py"] = []byte("PUBLIC_WEB_PORTS = \"80,443\"\n")
	if _, err := Verify(context.Background(), f, c, f.user.ID, "/private/runtime", sources, image); err == nil {
		t.Fatal("uninspected network helper policy accepted")
	}
	image.Files["network-helper.py"] = SHA(sources["network-helper.py"])
	m, err := Verify(context.Background(), f, c, f.user.ID, "/private/runtime", sources, image)
	if err != nil || m.NetworkAdapter != NetworkAdapter {
		t.Fatal("verified public web policy missing from approval", err)
	}
}
