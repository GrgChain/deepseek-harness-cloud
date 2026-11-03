package worker

import (
	"context"
	"encoding/json"

	"github.com/jonluo94/deepseek-harness-cloud/infrastructure/plugins"
	"github.com/jonluo94/deepseek-harness-cloud/infrastructure/runtime/workspace-assets"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/workspaceextensions"
)

var installExtensionsProgram = workspaceassets.InstallExtensions
var cloudExtensionsProgram = workspaceassets.CloudExtensions

func (h *LocalHost) installExtensions(ctx context.Context, p ExecutionPlan) error {
	if p.Action != "CREATE" || len(p.extensions.Items) == 0 {
		return nil
	}
	identity, err := storageIdentity(p)
	if err != nil {
		return err
	}
	container, err := h.container(ctx, p.InstanceID)
	if err != nil {
		return err
	}
	if container != nil {
		return validateContainer(*container, p, h.options.RuntimeRoot, true)
	}
	var sessionPlugin []byte
	for _, item := range p.extensions.Items {
		if item.SessionMemory {
			sessionPlugin = pluginassets.OpenVikingPlugin
			break
		}
	}
	input, err := json.Marshal(struct {
		storageRequest
		Extensions     workspaceextensions.Snapshot `json:"extensions"`
		Program        string                       `json:"program"`
		SessionProgram string                       `json:"session_program"`
		SessionPlugin  []byte                       `json:"session_plugin,omitempty"`
	}{identity, p.extensions, cloudExtensionsProgram, workspaceassets.OpenVikingSession, sessionPlugin})
	if err != nil {
		return err
	}
	defer clear(input)
	raw, err := dockerCommand(ctx, input, "run", "--rm", "-i", "--network", "none", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "--user", "1000:1000", "--memory", "256m", "--cpus", "1", "--pids-limit", "64", "--mount", "type=volume,src="+p.ResourceName+",dst=/data,volume-nocopy", "--entrypoint", "/usr/local/bin/node", p.Snapshot.ImageDigest, "--input-type=module", "-e", installExtensionsProgram, "--", "--install")
	if err != nil {
		return err
	}
	var proof struct {
		Installed bool `json:"installed"`
	}
	if webapi.StrictUnmarshal(raw, &proof) != nil || !proof.Installed {
		return ErrInvalidEvidence
	}
	return nil
}
