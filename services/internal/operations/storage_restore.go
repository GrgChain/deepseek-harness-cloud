package operations

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func (s *StorageService) Restore(ctx context.Context, input, targetID, preimage, confirmed string, key []byte, apply bool) (StorageReport, error) {
	report := StorageReport{AssetID: targetID}
	if !s.valid() || !uuid.MatchString(targetID) || len(key) != 32 || !filepath.IsAbs(input) || privateDirectory(filepath.Dir(input)) != nil || (apply && confirmed != targetID) {
		return report, ErrInput
	}
	info, err := os.Lstat(input)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return report, ErrInput
	}
	file, err := os.Open(input)
	if err != nil {
		return report, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return report, ErrInput
	}
	stage, err := os.CreateTemp(filepath.Dir(input), ".hc-asset-recovery-*")
	if err != nil {
		return report, err
	}
	defer stage.Close()
	defer os.Remove(stage.Name())
	m, proof, err := OpenArchive(stage, file, key)
	if err != nil {
		return report, err
	}
	if m.Kind != "storage" || m.OrganizationID != s.organization {
		return report, ErrStoragePolicy
	}
	report.SourceAssetID = m.AssetID
	report.Proof = proof
	source, err := s.read(ctx, s.pool, m.AssetID)
	if err != nil {
		return report, err
	}
	target, err := s.read(ctx, s.pool, targetID)
	if err != nil {
		return report, err
	}
	if m.InstanceID != source.InstanceID || m.OwnerID != source.OwnerID || m.Capacity != source.Capacity {
		return report, ErrStoragePolicy
	}
	if err = CheckStorageRestore(source, target); err != nil {
		return report, err
	}
	unlock, err := s.guards(ctx, source, target)
	if err != nil {
		return report, err
	}
	defer unlock()
	tx, states, err := s.lock(ctx, source.ID, target.ID)
	if err != nil {
		return report, err
	}
	defer tx.Rollback(context.Background())
	source, target = states[source.ID], states[target.ID]
	if err = CheckStorageRestore(source, target); err != nil {
		return report, err
	}
	if !apply {
		return report, nil
	}
	intent := assetIntent{SchemaVersion: 1, Action: "RESTORE", SourceAssetID: source.ID, BackupSHA: proof.SHA256}
	if target.Status == "RESTORING" {
		var existing assetIntent
		if webapi.StrictUnmarshal(target.Intent, &existing) != nil || existing.SchemaVersion != 1 || existing.Action != "RESTORE" || existing.SourceAssetID != source.ID || existing.BackupSHA != proof.SHA256 {
			return report, ErrStoragePolicy
		}
		p, err := os.Open(preimage)
		if err != nil {
			return report, ErrStoragePolicy
		}
		defer p.Close()
		preInfo, err := p.Stat()
		if err != nil || !preInfo.Mode().IsRegular() || preInfo.Mode().Perm()&0077 != 0 || privateDirectory(filepath.Dir(preimage)) != nil {
			return report, ErrStoragePolicy
		}
		pm, pp, err := OpenArchive(io.Discard, p, key)
		if err != nil || pm.AssetID != target.ID || pm.InstanceID != target.InstanceID || pm.OwnerID != target.OwnerID || pp.SHA256 != existing.PreimageSHA {
			return report, ErrStoragePolicy
		}
		intent = existing
		report.Preimage = pp
	} else {
		if err = s.host.InspectAsset(ctx, source.Plan); err != nil {
			return report, err
		}
		if err = s.host.InspectAsset(ctx, target.Plan); err != nil {
			return report, err
		}
		pp, err := s.writeBackup(ctx, target, preimage, key)
		if err != nil {
			return report, err
		}
		report.Preimage = pp
		intent.PreimageSHA = pp.SHA256
		raw, _ := json.Marshal(intent)
		if _, err = tx.Exec(ctx, `UPDATE storage_assets SET status='RESTORING',maintenance_intent=$2 WHERE id=$1`, target.ID, raw); err != nil {
			return report, err
		}
		if _, err = tx.Exec(ctx, `UPDATE instances SET version=version+1,access_generation=access_generation+1,updated_at=clock_timestamp() WHERE id=$1`, target.InstanceID); err != nil {
			return report, err
		}
		if _, err = tx.Exec(ctx, `UPDATE access_routes SET enabled=false WHERE instance_id=$1`, target.InstanceID); err != nil {
			return report, err
		}
		if _, err = tx.Exec(ctx, `UPDATE access_sessions SET revoked_at=coalesce(revoked_at,clock_timestamp()) WHERE instance_id=$1`, target.InstanceID); err != nil {
			return report, err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM native_sessions WHERE instance_id=$1`, target.InstanceID); err != nil {
			return report, err
		}
		if err = s.audit(ctx, tx, target, "STORAGE_RESTORE_BEGIN", map[string]any{"source_asset_id": source.ID, "target_asset_id": target.ID, "backup_sha256": proof.SHA256, "preimage_sha256": pp.SHA256}); err != nil {
			return report, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return report, err
	}
	if _, err = stage.Seek(0, io.SeekStart); err != nil {
		return report, err
	}
	if err = s.host.RestoreAsset(ctx, target.Plan, source.Plan, proof.SHA256, stage); err != nil {
		return report, err
	}
	if err = s.host.InspectAsset(ctx, target.Plan); err != nil {
		return report, err
	}
	final, states, err := s.lock(ctx, target.ID)
	if err != nil {
		return report, err
	}
	defer final.Rollback(context.Background())
	current := states[target.ID]
	var saved assetIntent
	if current.ActiveOperation || current.Observed != "STOPPED" || current.Desired != "STOPPED" || current.Status != "RESTORING" || webapi.StrictUnmarshal(current.Intent, &saved) != nil || saved != intent {
		return report, ErrStoragePolicy
	}
	if _, err = final.Exec(ctx, `UPDATE storage_assets SET status='READY',maintenance_intent=NULL WHERE id=$1`, target.ID); err != nil {
		return report, err
	}
	if _, err = final.Exec(ctx, `UPDATE instances SET version=version+1,updated_at=clock_timestamp() WHERE id=$1`, target.InstanceID); err != nil {
		return report, err
	}
	if err = s.audit(ctx, final, target, "STORAGE_RESTORE", map[string]any{"source_asset_id": source.ID, "target_asset_id": target.ID, "backup_sha256": proof.SHA256, "preimage_sha256": intent.PreimageSHA}); err != nil {
		return report, err
	}
	err = final.Commit(ctx)
	report.Applied = err == nil
	return report, err
}
