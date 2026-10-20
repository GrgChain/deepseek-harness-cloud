package instances

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	"strings"
)

// Eligibility is a current database observation, never authority to bypass the
// retry transaction's locks, quotas or live runtime admission.
func retryPreview(ctx context.Context, tx pgx.Tx, p identity.Principal, id string) (bool, error) {
	source, err := readRetrySource(ctx, tx, p, id)
	if err != nil {
		return false, err
	}
	var permitted bool
	if err = tx.QueryRow(ctx, `SELECT public.hc_has_permission($1,$2,'instances:retry') AND public.hc_has_permission($1,$2,$3) AND ($4<>'CREATE' OR public.hc_has_permission($1,$2,'instances:start'))`, p.OrganizationID, p.UserID, "instances:"+strings.ToLower(source.Action), source.Action).Scan(&permitted); err != nil {
		return false, err
	}
	if !permitted {
		return false, nil
	}
	var original retryIntent
	if source.Status != "FAILED" || source.Actor != "USER" || source.Reconciled == nil || !retryDiagnostic(source.Code) || webapi.StrictUnmarshal(source.Intent, &original) != nil || original.Schema != 1 || original.Reason != "USER_REQUEST" || original.Expected < 1 || original.Generation < 1 {
		return false, nil
	}
	var state Lifecycle
	var latest, name string
	var ready, maintenance bool
	err = tx.QueryRow(ctx, `SELECT i.observed_state,i.desired_state,i.version,i.access_generation,i.name,EXISTS(SELECT 1 FROM operations o WHERE o.instance_id=i.id AND o.status IN ('ACCEPTED','RUNNING','UNCERTAIN')),(SELECT id::text FROM operations WHERE instance_id=i.id ORDER BY created_at DESC,id DESC LIMIT 1),EXISTS(SELECT 1 FROM storage_assets a WHERE a.id=i.storage_asset_id AND a.status='READY' AND a.init_version>=1 AND a.filesystem_id IS NOT NULL AND a.capacity_bytes=(i.config_snapshot->'spec'->>'storage_bytes')::bigint),EXISTS(SELECT 1 FROM storage_assets a WHERE a.id=i.storage_asset_id AND a.status IN ('RESTORING','RECLAIM_PENDING')) FROM instances i WHERE i.id=$1`, source.Instance).Scan(&state.Observed, &state.Desired, &state.Version, &state.AccessGeneration, &name, &state.ActiveOperation, &latest, &ready, &maintenance)
	if err != nil {
		return false, err
	}
	if maintenance && source.Action != "STOP" {
		return false, nil
	}
	facts := RetryFacts{Latest: latest == id, Failed: true, User: true, Reconciled: true, Diagnostic: true, OriginalAssetReady: ready}
	if source.Action == "CREATE" || source.Action == "START" || source.Action == "CONFIGURE" {
		target, e := DecodeSnapshot(original.Target)
		if e != nil || original.Name != nil {
			return false, nil
		}
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM model_configurations c JOIN secrets sec ON sec.id=c.credential_ref AND sec.org_id=c.org_id JOIN model_validations v ON v.config_id=c.id AND v.config_version=c.version WHERE c.org_id=$1 AND c.id=$2 AND c.version=$3 AND public.hc_model_available(c.org_id,$4,c.id,c.version) AND c.status='VALID' AND c.revoked_at IS NULL AND sec.revoked_at IS NULL AND v.status='HEALTHY' AND v.checked_at<=clock_timestamp() AND v.expires_at>clock_timestamp())`, p.OrganizationID, target.ModelID, target.ModelVersion, p.UserID).Scan(&facts.ModelUsable)
		if err != nil {
			return false, err
		}
	} else if len(original.Target) > 0 || (source.Action == "DELETE" && (original.Name == nil || *original.Name != name)) || (source.Action == "STOP" && original.Name != nil) {
		return false, nil
	}
	_, err = state.PlanRetry(source.Action, original.Desired, facts)
	return err == nil, nil
}
