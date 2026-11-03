package operations

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func (s *StorageService) Reclaim(ctx context.Context, id, confirmed, reason string, apply bool) (StorageReport, error) {
	report := StorageReport{AssetID: id}
	if !s.valid() || !uuid.MatchString(id) || !keyName.MatchString(reason) || (apply && confirmed != id) {
		return report, ErrInput
	}
	a, err := s.read(ctx, s.pool, id)
	if err != nil {
		return report, err
	}
	unlock, err := s.guards(ctx, a)
	if err != nil {
		return report, err
	}
	defer unlock()
	tx, states, err := s.lock(ctx, id)
	if err != nil {
		return report, err
	}
	defer tx.Rollback(context.Background())
	a = states[id]
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return report, err
	}
	if err = CheckReclaim(a, now); err != nil {
		return report, err
	}
	if !apply {
		return report, nil
	}
	intent := assetIntent{SchemaVersion: 1, Action: "RECLAIM", Reason: reason}
	if a.Status == "RECLAIM_PENDING" {
		var existing assetIntent
		if webapi.StrictUnmarshal(a.Intent, &existing) != nil || existing != intent {
			return report, ErrStoragePolicy
		}
	} else {
		raw, _ := json.Marshal(intent)
		if _, err = tx.Exec(ctx, `UPDATE storage_assets SET status='RECLAIM_PENDING',maintenance_intent=$2 WHERE id=$1`, id, raw); err != nil {
			return report, err
		}
		if err = s.audit(ctx, tx, a, "STORAGE_RECLAIM_BEGIN", map[string]any{"source_asset_id": a.ID, "reason_code": reason}); err != nil {
			return report, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return report, err
	}
	if err = s.host.ReclaimAsset(ctx, a.Plan); err != nil {
		return report, err
	}
	final, states, err := s.lock(ctx, id)
	if err != nil {
		return report, err
	}
	defer final.Rollback(context.Background())
	current := states[id]
	if err = CheckReclaim(current, now); err != nil {
		return report, err
	}
	var saved assetIntent
	if current.Status != "RECLAIM_PENDING" || webapi.StrictUnmarshal(current.Intent, &saved) != nil || saved != intent {
		return report, ErrStoragePolicy
	}
	if _, err = final.Exec(ctx, `UPDATE storage_assets SET status='DELETED',maintenance_intent=NULL WHERE id=$1`, id); err != nil {
		return report, err
	}
	if _, err = final.Exec(ctx, `UPDATE quota_reservations SET status='RELEASED',updated_at=clock_timestamp() WHERE instance_id=$1 AND resource_type='STORAGE' AND status IN ('HELD','USED')`, a.InstanceID); err != nil {
		return report, err
	}
	if _, err = final.Exec(ctx, `UPDATE quota_policies SET version=version+1 WHERE org_id=$1`, a.OrganizationID); err != nil {
		return report, err
	}
	if err = s.audit(ctx, final, a, "STORAGE_RECLAIM", map[string]any{"source_asset_id": a.ID, "reason_code": reason}); err != nil {
		return report, err
	}
	err = final.Commit(ctx)
	report.Applied = err == nil
	return report, err
}
