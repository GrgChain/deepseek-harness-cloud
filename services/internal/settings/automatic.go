package settings

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

// Referenced versions remain eligible, including failed versions used at runtime.
const usedModelVersion = `(EXISTS(SELECT 1 FROM organization_defaults d WHERE d.org_id=c.org_id AND d.model_config_id=c.id AND d.model_config_version=c.version)
 OR EXISTS(SELECT 1 FROM user_model_defaults d WHERE d.org_id=c.org_id AND d.connection_id=c.id AND d.connection_version=c.version)
 OR EXISTS(SELECT 1 FROM instances i WHERE i.org_id=c.org_id AND i.model_config_id=c.id AND i.model_config_version=c.version AND i.deleted_at IS NULL AND i.desired_state<>'DELETED'))`

// Active personal models get their initial check and successful versions renew
// before the evidence expires, even without a default or workspace reference.
// Rotation/removal fence older versions; unused failures require explicit retry.
const automaticValidationTarget = `((c.status IN ('VALID','INVALID') AND ` + usedModelVersion + `)
 OR (c.status IN ('UNVALIDATED','VALID') AND EXISTS(SELECT 1 FROM user_model_connections h JOIN user_model_connection_versions p ON p.connection_id=h.id AND p.version=h.current_version
 WHERE h.org_id=c.org_id AND h.id=c.id AND h.current_version=c.version AND h.enabled AND h.archived_at IS NULL AND NOT h.provider_retired AND p.sync_status='SYNCED')))`

type AutoValidator struct{ service *Service }

func NewAutoValidator(service *Service) *AutoValidator { return &AutoValidator{service} }

func (a *AutoValidator) acquire(ctx context.Context) (probeLease, string, bool, error) {
	s := a.service
	var lease probeLease
	var org string
	err := s.pool.QueryRow(ctx, `SELECT c.org_id::text,c.id::text,c.version FROM model_configurations c JOIN model_validations v ON v.config_id=c.id AND v.config_version=c.version JOIN secrets sec ON sec.id=c.credential_ref JOIN organizations o ON o.id=c.org_id
 WHERE o.status='ACTIVE' AND c.revoked_at IS NULL AND sec.revoked_at IS NULL AND (v.attempted_at IS NULL OR v.attempted_at<=clock_timestamp()-interval '4 minutes') AND (v.lease_expires_at IS NULL OR v.lease_expires_at<=clock_timestamp()) AND `+automaticValidationTarget+`
 ORDER BY v.attempted_at NULLS FIRST,c.org_id,c.version LIMIT 1`).Scan(&org, &lease.model.ID, &lease.model.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return lease, "", false, nil
	}
	if err != nil {
		return lease, "", false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return lease, "", false, err
	}
	defer tx.Rollback(context.Background())
	var head *string
	if err = tx.QueryRow(ctx, `SELECT public.hc_worker_lock_model($1)::text`, org).Scan(&head); err != nil {
		return lease, org, false, err
	}
	lease.model, err = readModelVersion(ctx, tx, org, lease.model.ID, lease.model.Version)
	if err != nil {
		return lease, org, false, err
	}
	id := lease.model.ID
	allowed := s.policy.AllowsModel(lease.model) || s.personalApproved(ctx, tx, org, lease.model)
	var available bool
	if err = tx.QueryRow(ctx, `SELECT public.hc_model_available($1,r.owner_id,$2,$3) FROM model_connection_registry r WHERE r.org_id=$1 AND r.id=$2`, org, id, lease.model.Version).Scan(&available); err != nil {
		return lease, org, false, err
	}
	allowed = allowed && available
	err = tx.QueryRow(ctx, `UPDATE model_validations v SET status=CASE WHEN $3 THEN 'CHECKING' ELSE 'UNKNOWN' END,reason_code=CASE WHEN $3 THEN '' ELSE 'VALIDATION_REQUIRED' END,lease_token=lease_token+1,attempted_at=clock_timestamp(),lease_expires_at=CASE WHEN $3 THEN clock_timestamp()+interval '45 seconds' ELSE NULL END
 WHERE config_id=$1 AND config_version=$2 AND (attempted_at IS NULL OR attempted_at<=clock_timestamp()-interval '4 minutes') AND (lease_expires_at IS NULL OR lease_expires_at<=clock_timestamp())
 AND EXISTS(SELECT 1 FROM model_configurations c JOIN secrets sec ON sec.id=c.credential_ref JOIN organizations o ON o.id=c.org_id WHERE c.id=v.config_id AND c.version=v.config_version AND c.revoked_at IS NULL AND sec.revoked_at IS NULL AND o.status='ACTIVE' AND `+automaticValidationTarget+`)
 RETURNING lease_token,attempted_at`, id, lease.model.Version, allowed).Scan(&lease.token, &lease.started)
	if errors.Is(err, pgx.ErrNoRows) {
		return lease, org, false, nil
	}
	if err != nil {
		return lease, org, false, err
	}
	if allowed {
		err = tx.QueryRow(ctx, `SELECT c.credential_ref::text,sec.key_id,sec.nonce,sec.ciphertext FROM model_configurations c JOIN secrets sec ON sec.id=c.credential_ref WHERE c.id=$1 AND c.version=$2 AND c.revoked_at IS NULL AND sec.revoked_at IS NULL`, id, lease.model.Version).Scan(&lease.credential, &lease.envelope.KeyID, &lease.envelope.Nonce, &lease.envelope.Ciphertext)
		if err != nil {
			return lease, org, false, err
		}
	}
	return lease, org, allowed, tx.Commit(ctx)
}

// RunOnce is bounded and serial in Run. Cross-process duplication is prevented
// by the persisted attempt time, configuration-head lock and version lease.
func (a *AutoValidator) RunOnce(ctx context.Context) (bool, error) {
	if a.service.maintenance {
		return false, nil
	}
	acquire, cancel := context.WithTimeout(ctx, 5*time.Second)
	lease, org, claimed, err := a.acquire(acquire)
	cancel()
	if err != nil || !claimed {
		return false, err
	}
	probe, cancel := context.WithTimeout(ctx, 30*time.Second)
	reason, budgetErr, err := a.service.runProbe(probe, org, &lease)
	cancel()
	if ctx.Err() != nil {
		return true, ctx.Err()
	}
	if err != nil {
		return true, err
	}
	finish, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := a.service.pool.Begin(finish)
	if err != nil {
		return true, err
	}
	defer tx.Rollback(context.Background())
	var head *string
	if err = tx.QueryRow(finish, `SELECT public.hc_worker_lock_model($1)::text`, org).Scan(&head); err != nil {
		return true, err
	}
	id := lease.model.ID
	var used bool
	if err = tx.QueryRow(finish, `SELECT EXISTS(SELECT 1 FROM model_configurations c WHERE c.id=$1 AND c.version=$2 AND `+automaticValidationTarget+`)`, id, lease.model.Version).Scan(&used); err != nil {
		return true, err
	}
	var available bool
	if err = tx.QueryRow(finish, `SELECT public.hc_model_available($1,r.owner_id,$2,$3) FROM model_connection_registry r WHERE r.org_id=$1 AND r.id=$2`, org, id, lease.model.Version).Scan(&available); err != nil {
		return true, err
	}
	if !used || !available || !(a.service.policy.AllowsModel(lease.model) || a.service.personalApproved(finish, tx, org, lease.model)) {
		return true, webapi.Fail(409, "STATE_CONFLICT", "校验目标已变化，请重新查询")
	}
	if err = a.service.publishProbe(finish, tx, org, id, lease, reason); err != nil {
		return true, err
	}
	if _, err = tx.Exec(finish, `INSERT INTO audit_events(id,org_id,resource_id,action,outcome,trace_id,details) VALUES($1,$2,$3,'MODEL_PROBE',$4,$5,jsonb_build_object('schema_version',1,'configuration_version',$6::bigint,'reason_code',$7::text))`, webapi.ID(), org, id, map[bool]string{true: "SUCCEEDED", false: "FAILED"}[reason == ""], webapi.ID(), lease.model.Version, reason); err != nil {
		return true, err
	}
	if err = tx.Commit(finish); err != nil {
		return true, err
	}
	return true, budgetErr
}

func (a *AutoValidator) Run(ctx context.Context) error {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		for range 10 {
			ran, err := a.RunOnce(ctx)
			if err != nil && ctx.Err() == nil {
				slog.Warn("Automatic model validation temporarily unavailable")
			}
			if err != nil || !ran || ctx.Err() != nil {
				break
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
