package settings

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type probeLease struct {
	model      Model
	credential string
	token      int64
	started    time.Time
	envelope   security.Envelope
	evidence   []byte
}

func (s *Service) acquire(ctx context.Context, p identity.Principal, version int64) (probeLease, error) {
	var lease probeLease
	tx, err := s.begin(ctx, p, "model:validate", true)
	if err != nil {
		return lease, err
	}
	defer tx.Rollback(context.Background())
	if version < 1 {
		return lease, invalid()
	}
	if err := tx.QueryRow(ctx, `SELECT id::text FROM model_configuration_heads WHERE org_id=$1 FOR UPDATE`, p.OrganizationID).Scan(&lease.model.ID); errors.Is(err, pgx.ErrNoRows) {
		return lease, notFound()
	} else if err != nil {
		return lease, err
	}
	lease.model, err = readModel(ctx, tx, p.OrganizationID, version)
	if err != nil {
		return lease, err
	}
	if !lease.model.CredentialConfigured || lease.model.Status == "REVOKED" || !s.policy.AllowsModel(lease.model) {
		return lease, invalid()
	}
	err = tx.QueryRow(ctx, `SELECT c.credential_ref::text,s.key_id,s.nonce,s.ciphertext FROM model_configurations c JOIN secrets s ON s.id=c.credential_ref WHERE c.id=$1 AND c.version=$2 AND s.revoked_at IS NULL`, lease.model.ID, version).Scan(&lease.credential, &lease.envelope.KeyID, &lease.envelope.Nonce, &lease.envelope.Ciphertext)
	if err != nil {
		return lease, err
	}
	err = tx.QueryRow(ctx, `UPDATE model_validations SET status='CHECKING',lease_token=lease_token+1,lease_expires_at=clock_timestamp()+interval '45 seconds',attempted_at=clock_timestamp()
 WHERE config_id=$1 AND config_version=$2 AND (lease_expires_at IS NULL OR lease_expires_at<=clock_timestamp()) RETURNING lease_token,attempted_at`, lease.model.ID, version).Scan(&lease.token, &lease.started)
	if errors.Is(err, pgx.ErrNoRows) {
		return lease, webapi.Fail(409, "STATE_CONFLICT", "该版本正在校验，请稍后查询结果")
	}
	if err != nil {
		return lease, err
	}
	return lease, tx.Commit(ctx)
}
func (s *Service) reserveProbe(ctx context.Context, org string, lease probeLease) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var id *string
	var day string
	err = tx.QueryRow(ctx, `SELECT public.hc_worker_lock_model($1)::text`, org).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return webapi.Fail(429, "PROBE_BUDGET_EXCEEDED", "模型探测预算已用尽或本轮校验已失效")
	}
	if err != nil {
		return err
	}
	if (id == nil || *id != lease.model.ID) && !s.personalApproved(ctx, tx, org, lease.model) {
		return webapi.Fail(429, "PROBE_BUDGET_EXCEEDED", "模型探测预算已用尽或本轮校验已失效")
	}
	err = tx.QueryRow(ctx, `SELECT config_id::text FROM model_validations WHERE config_id=$1 AND config_version=$2 AND status='CHECKING' AND lease_token=$3 FOR UPDATE`, lease.model.ID, lease.model.Version, lease.token).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return webapi.Fail(429, "PROBE_BUDGET_EXCEEDED", "模型探测预算已用尽或本轮校验已失效")
	}
	if err != nil {
		return err
	}
	if err = tx.QueryRow(ctx, `SELECT (clock_timestamp() AT TIME ZONE 'UTC')::date::text`).Scan(&day); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO probe_budgets(org_id,day,attempts) VALUES($1,$2::date,0) ON CONFLICT(org_id,day) DO NOTHING`, org, day); err != nil {
		return err
	}
	var attempts int
	if err = tx.QueryRow(ctx, `SELECT attempts FROM probe_budgets WHERE org_id=$1 AND day=$2::date FOR UPDATE`, org, day).Scan(&attempts); err != nil {
		return err
	}
	// Evaluate the database deadline after every potentially contended lock.
	// A request is charged only when the same live lease still owns this version.
	err = tx.QueryRow(ctx, `UPDATE probe_budgets SET attempts=attempts+1 WHERE org_id=$1 AND day=$2::date AND day=(clock_timestamp() AT TIME ZONE 'UTC')::date AND attempts<1000
 AND EXISTS(SELECT 1 FROM model_validations v JOIN model_configurations c ON c.id=v.config_id AND c.version=v.config_version JOIN secrets s ON s.id=c.credential_ref JOIN organizations o ON o.id=c.org_id WHERE c.org_id=$1 AND o.status='ACTIVE' AND v.config_id=$3 AND v.config_version=$4 AND v.status='CHECKING' AND v.lease_token=$5 AND v.lease_expires_at>clock_timestamp() AND c.status<>'REVOKED' AND c.revoked_at IS NULL AND s.revoked_at IS NULL) RETURNING attempts`, org, day, lease.model.ID, lease.model.Version, lease.token).Scan(&attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return webapi.Fail(429, "PROBE_BUDGET_EXCEEDED", "模型探测预算已用尽或本轮校验已失效")
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Service) runProbe(ctx context.Context, org string, lease *probeLease) (string, error, error) {
	key, err := s.cipher.Decrypt(lease.envelope, s.aad(org, lease.credential))
	if err != nil {
		return "", nil, err
	}
	defer clear(key)
	lease.evidence = []byte("{}")
	if lease.model.Provider == "platform-models" {
		gateway, ok := s.policy.Gateway(lease.model.Endpoint)
		if !ok {
			return "MODEL_NOT_APPROVED", nil, nil
		}
		var budgetErr error
		reason, models := gatewayProbe(ctx, s.gatewayClient(gateway), lease.model, string(key), func() error { budgetErr = s.reserveProbe(ctx, org, *lease); return budgetErr })
		if reason == "" {
			lease.evidence, _ = json.Marshal(map[string]any{"response_models": models, "streaming": true, "tool_use": true, "manifest_digest": lease.model.Manifest.Digest})
		}
		return reason, budgetErr, nil
	}
	reason := "MODEL_TRANSPORT_UNAVAILABLE"
	var budgetErr error
	for attempt := 0; attempt < 2; attempt++ {
		if err := s.reserveProbe(ctx, org, *lease); err != nil {
			budgetErr = err
			reason = "PROBE_BUDGET_EXCEEDED"
			break
		}
		var retry bool
		reason, retry = s.probe.Check(ctx, lease.model, string(key))
		if !retry || ctx.Err() != nil {
			break
		}
	}
	return reason, budgetErr, nil
}

func (s *Service) publishProbe(ctx context.Context, tx pgx.Tx, org, id string, lease probeLease, reason string) error {
	status, health := "INVALID", "UNHEALTHY"
	if reason == "" {
		status, health = "VALID", "HEALTHY"
	}
	tag, err := tx.Exec(ctx, `UPDATE model_validations v SET status=$5,checked_at=$6,expires_at=$6::timestamptz+interval '5 minutes',reason_code=$7,lease_expires_at=NULL,evidence=$9
 WHERE config_id=$1 AND config_version=$2 AND status='CHECKING' AND lease_token=$3 AND lease_expires_at>clock_timestamp()
 AND EXISTS(SELECT 1 FROM model_configurations c JOIN secrets sec ON sec.id=c.credential_ref JOIN organizations o ON o.id=c.org_id WHERE c.id=v.config_id AND c.version=v.config_version AND c.org_id=$8 AND c.status<>'REVOKED' AND c.revoked_at IS NULL AND sec.revoked_at IS NULL AND o.status='ACTIVE')
 AND $4::uuid=$1`, lease.model.ID, lease.model.Version, lease.token, id, health, lease.started, reason, org, lease.evidence)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return webapi.Fail(409, "STATE_CONFLICT", "校验目标已变化，请重新查询")
	}
	_, err = tx.Exec(ctx, `UPDATE model_configurations SET status=$3 WHERE id=$1 AND version=$2 AND revoked_at IS NULL`, id, lease.model.Version, status)
	return err
}

func (s *Service) Validate(ctx context.Context, p identity.Principal, version int64, trace string) (Model, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	lease, err := s.acquire(ctx, p, version)
	if err != nil {
		return Model{}, err
	}
	reason, budgetErr, err := s.runProbe(ctx, p.OrganizationID, &lease)
	if err != nil {
		return Model{}, err
	}
	// Finalization gets its own short budget so a timed-out upstream cannot leave
	// a successful status behind. Lease and current authorization are rechecked.
	finish, cancelFinish := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancelFinish()
	tx, err := s.begin(finish, p, "model:validate", false)
	if err != nil {
		return Model{}, err
	}
	defer tx.Rollback(context.Background())
	var id string
	if err := tx.QueryRow(finish, `SELECT id::text FROM model_configuration_heads WHERE org_id=$1 FOR UPDATE`, p.OrganizationID).Scan(&id); err != nil {
		return Model{}, err
	}
	if err := s.publishProbe(finish, tx, p.OrganizationID, id, lease, reason); err != nil {
		return Model{}, err
	}
	if err := s.audit(finish, tx, p, id, "MODEL_VALIDATE", trace); err != nil {
		return Model{}, err
	}
	m, err := readModel(finish, tx, p.OrganizationID, version)
	if err != nil {
		return Model{}, err
	}
	if err := tx.Commit(finish); err != nil {
		return Model{}, err
	}
	if budgetErr != nil {
		return Model{}, budgetErr
	}
	return m, nil
}
