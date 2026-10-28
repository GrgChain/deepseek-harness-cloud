package worker

import (
	"context"
	_ "embed"
	"encoding/json"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/skills"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

//go:embed install_skills.mjs
var installSkillsProgram string

func (h *LocalHost) installSkills(ctx context.Context, p ExecutionPlan) error {
	if p.Action != "CREATE" || len(p.skills) == 0 {
		return nil
	}
	if err := skills.ValidateWorkspaceSkills(p.skills); err != nil {
		return ErrInvalidEvidence
	}
	identity, err := storageIdentity(p)
	if err != nil {
		return err
	}
	// Creation recovery may inspect an already running container, but must never
	// mutate its files. Installation has completed before that container exists.
	container, err := h.container(ctx, p.InstanceID)
	if err != nil {
		return err
	}
	if container != nil {
		return validateContainer(*container, p, h.options.RuntimeRoot, true)
	}
	input, err := json.Marshal(struct {
		storageRequest
		Skills []skills.WorkspaceSkill `json:"skills"`
	}{identity, p.skills})
	if err != nil {
		return err
	}
	defer clear(input)
	raw, err := dockerCommand(ctx, input, "run", "--rm", "-i", "--network", "none", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "--user", "1000:1000", "--memory", "512m", "--cpus", "1", "--pids-limit", "64", "--mount", "type=volume,src="+p.ResourceName+",dst=/data,volume-nocopy", "--entrypoint", "/usr/local/bin/node", p.Snapshot.ImageDigest, "--input-type=module", "-e", installSkillsProgram, "--", "--install")
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
