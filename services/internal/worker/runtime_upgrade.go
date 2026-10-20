package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/instances"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/runtimeapproval"
)

// Retained storage may use a compatible, approved adapter only through explicit
// CONFIGURE. Ordinary starts retain their accepted image, template and resources.
func (s *ExecutionStore) validateTarget(ctx context.Context, tx pgx.Tx, action string, source, target instances.Snapshot) error {
	fixed := target
	fixed.ModelID, fixed.ModelVersion = source.ModelID, source.ModelVersion
	fixed.Provider, fixed.Protocol, fixed.Model, fixed.ApprovalDigest = source.Provider, source.Protocol, source.Model, source.ApprovalDigest
	if target.TemplateVersion != source.TemplateVersion || target.ImageDigest != source.ImageDigest {
		if action != "CONFIGURE" || target.TemplateID != source.TemplateID {
			return ErrInvalidEvidence
		}
		var oldSource, newSource, oldRaw, newRaw []byte
		err := tx.QueryRow(ctx, `SELECT old.source,new.source,a.runtime_manifest,n.runtime_manifest FROM approved_templates a
 JOIN approved_templates n ON n.id=a.id
 JOIN workspace_template_versions old ON old.id=a.version AND old.template_id=a.id
 JOIN workspace_template_versions new ON new.id=n.version AND new.template_id=n.id AND new.organization_id=old.organization_id
 WHERE a.id=$1 AND a.version=$2 AND n.version=$3 AND a.runtime_manifest->>'image_digest'=$4 AND n.runtime_manifest->>'image_digest'=$5 AND NOT a.enabled AND n.enabled AND NOT new.archived`, source.TemplateID, source.TemplateVersion, target.TemplateVersion, source.ImageDigest, target.ImageDigest).Scan(&oldSource, &newSource, &oldRaw, &newRaw)
		old, e1 := runtimeapproval.Parse(oldRaw)
		next, e2 := runtimeapproval.Parse(newRaw)
		if err != nil || e1 != nil || e2 != nil || !old.Matches(source.ImageDigest, source.TemplateVersion) || !next.Matches(target.ImageDigest, target.TemplateVersion) || !runtimeTemplateChange(oldSource, newSource, old, next) {
			return ErrInvalidEvidence
		}
		fixed.TemplateVersion, fixed.ImageDigest = source.TemplateVersion, source.ImageDigest
	}
	if fixed != source {
		return ErrInvalidEvidence
	}
	return nil
}

func runtimeTemplateChange(oldRaw, newRaw []byte, old, next runtimeapproval.Manifest) bool {
	if !runtimeapproval.CompatibleUpgrade(old, next) {
		return false
	}
	var a, b map[string][]byte
	if json.Unmarshal(oldRaw, &a) != nil || json.Unmarshal(newRaw, &b) != nil || len(a) != 3 || len(b) != 3 || len(a["main.tf"]) == 0 || !bytes.Equal(a["main.tf"], b["main.tf"]) || len(a[".terraform.lock.hcl"]) == 0 || !bytes.Equal(a[".terraform.lock.hcl"], b[".terraform.lock.hcl"]) {
		return false
	}
	var av, bv map[string]any
	if json.Unmarshal(a["variables.json"], &av) != nil || json.Unmarshal(b["variables.json"], &bv) != nil || av == nil || bv == nil || av["runtime_image"] != old.Image || bv["runtime_image"] != next.Image {
		return false
	}
	av["runtime_image"] = bv["runtime_image"]
	return reflect.DeepEqual(av, bv)
}
