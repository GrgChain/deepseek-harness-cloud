package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/instances"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/operations"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/runtimeapproval"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/worker"
	workspaceengine "github.com/jonluo94/deepseek-harness-cloud/services/internal/workspace"
)

// Recovery reuses the approval verifier against immutable workspace template source and actual
// immutable image files. An imported DB declaration alone cannot approve code.
func recoveredRuntimeVerifier(o worker.Options, client *workspaceengine.Client, host *worker.LocalHost) operations.RecoveryRuntimeVerifier {
	return func(ctx context.Context, snapshot instances.Snapshot, approved runtimeapproval.Manifest) error {
		supported := o.Host.RuntimeImage == snapshot.ImageDigest
		for _, image := range o.Host.PreviousRuntimeImages {
			supported = supported || image == snapshot.ImageDigest
		}
		if !supported || host.Probe(ctx) != nil || approved.StorageImage != o.Host.StorageImage || approved.NetworkImage != o.Host.NetworkImage {
			return operations.ErrRecoveryAdoption
		}
		sources := map[string][]byte{}
		for _, name := range []string{"main.tf", ".terraform.lock.hcl"} {
			data, err := os.ReadFile(filepath.Join("infrastructure/terraform", name))
			if err != nil || len(data) == 0 || len(data) > 2<<20 {
				return operations.ErrRecoveryAdoption
			}
			sources[name] = data
		}
		image, err := inspectRuntimeImage(ctx, snapshot.ImageDigest)
		if err != nil {
			return operations.ErrRecoveryAdoption
		}
		image.StorageImage, image.NetworkImage = o.Host.StorageImage, o.Host.NetworkImage
		for _, helper := range []struct{ image, path, source, name string }{
			{o.Host.StorageImage, "/opt/harness-storage/helper.py", "infrastructure/storage/helper.py", "storage-helper.py"},
			{o.Host.NetworkImage, "/opt/harness-network/helper.py", "infrastructure/network/helper.py", "network-helper.py"},
		} {
			observed, err := inspectImage(ctx, helper.image, `process.stdout.write(require('node:crypto').createHash('sha256').update(require('node:fs').readFileSync('`+helper.path+`')).digest('hex'))`)
			local, localErr := os.ReadFile(helper.source)
			if err != nil || localErr != nil || string(observed) != runtimeapproval.SHA(local) {
				return operations.ErrRecoveryAdoption
			}
			sources[helper.name], image.Files[helper.name] = local, string(observed)
		}
		ib, _ := json.Marshal(snapshot.ImageDigest)
		rb, _ := json.Marshal(o.Host.RuntimeRoot)
		canonical := []byte(fmt.Sprintf(`{"runtime_image": %s, "runtime_root": %s}`, ib, rb))
		content := append(append(append([]byte{}, sources["main.tf"]...), sources[".terraform.lock.hcl"]...), canonical...)
		contract := runtimeapproval.Contract{TemplateID: snapshot.TemplateID, TemplateVersion: snapshot.TemplateVersion, OrganizationID: o.WorkspaceOrganizationID, WorkspaceSourceVersion: runtimeapproval.WorkspaceSourceVersion, TerraformVersion: runtimeapproval.TerraformVersion, WorkspaceProviderVersion: "2.19.0", DockerProvider: "4.6.0", SourceSHA: runtimeapproval.SHA(content), Image: snapshot.ImageDigest}
		template, err := client.Template(ctx, snapshot.TemplateID)
		if err != nil {
			return operations.ErrRecoveryAdoption
		}
		return runtimeapproval.VerifyRetained(ctx, client, contract, o.ControlUserID, o.Host.RuntimeRoot, template.ActiveVersion, sources, image, approved)
	}
}
