package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/runtimeapproval"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/workspace"
)

// Historical builds remain immutable. Only the exact last successful stopped
// build may bridge a reviewed label or compatible runtime change; new builds stay strict.
func (e *Executor) verifyMigratedPriorBuild(ctx context.Context, p ExecutionPlan, b workspace.Build) error {
	if e.store == nil || p.LastBuildID == nil || *p.LastBuildID != b.ID || b.Transition != "stop" || b.Job.Status != "succeeded" {
		return ErrHostIdentity
	}
	var oldSource, newSource, oldManifest, newManifest []byte
	var oldArchived, nextEnabled bool
	err := e.store.pool.QueryRow(ctx, `SELECT old.source,new.source,a.runtime_manifest,n.runtime_manifest,old.archived,n.enabled
 FROM workspace_template_versions old
 JOIN workspace_template_versions new ON new.template_id=old.template_id AND new.organization_id=old.organization_id
 JOIN approved_templates a ON a.id=old.template_id AND a.version=old.id
 JOIN approved_templates n ON n.id=new.template_id AND n.version=new.id
 WHERE old.id=$1 AND new.id=$2 AND NOT new.archived
 AND old.template_id=$3 AND old.organization_id=$4 AND NOT a.enabled`, b.TemplateVersionID, p.Snapshot.TemplateVersion, p.Snapshot.TemplateID, e.organization).Scan(&oldSource, &newSource, &oldManifest, &newManifest, &oldArchived, &nextEnabled)
	if err != nil {
		return ErrHostIdentity
	}
	old, err := runtimeapproval.Parse(oldManifest)
	if err != nil || old.TemplateVersion != b.TemplateVersionID {
		return ErrHostIdentity
	}
	next, err := runtimeapproval.Parse(newManifest)
	if err != nil || next.TemplateVersion != p.Snapshot.TemplateVersion {
		return ErrHostIdentity
	}
	if nextEnabled && runtimeTemplateChange(oldSource, newSource, old, next) {
		return nil
	}
	old.TemplateVersion = next.TemplateVersion
	if !oldArchived || !labelOnlyMigration(oldSource, newSource) || !reflect.DeepEqual(old, next) {
		return ErrHostIdentity
	}
	return nil
}

func labelOnlyMigration(oldRaw, newRaw []byte) bool {
	var old, next map[string][]byte
	if json.Unmarshal(oldRaw, &old) != nil || json.Unmarshal(newRaw, &next) != nil || len(old) != 3 || len(next) != 3 {
		return false
	}
	main := old["main.tf"]
	if bytes.Count(main, []byte("hc.coder_owner_id")) != 1 || bytes.Count(main, []byte("hc.coder_workspace_id")) != 1 {
		return false
	}
	main = bytes.ReplaceAll(bytes.ReplaceAll(main, []byte("hc.coder_owner_id"), []byte("hc.workspace_owner_id")), []byte("hc.coder_workspace_id"), []byte("hc.workspace_id"))
	return !bytes.Contains(main, []byte("hc.coder_")) && bytes.Equal(main, next["main.tf"]) && len(old[".terraform.lock.hcl"]) > 0 && bytes.Equal(old[".terraform.lock.hcl"], next[".terraform.lock.hcl"]) && len(old["variables.json"]) > 0 && bytes.Equal(old["variables.json"], next["variables.json"])
}
