package worker

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

// SafetyStore compensates revocation from authoritative PostgreSQL facts. It
// never releases quota or infers absence of compute from its observed state.
type SafetyStore struct{ pool *pgxpool.Pool }

func NewSafetyStore(pool *pgxpool.Pool) *SafetyStore { return &SafetyStore{pool} }
func (s *SafetyStore) Scan(ctx context.Context, cursor string, limit int) (string, error) {
	if cursor != "" && !uuidPattern.MatchString(cursor) || limit < 1 || limit > 10 {
		return cursor, ErrInvalidEvidence
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text FROM instances WHERE deleted_at IS NULL AND observed_state NOT IN ('STOPPED','DELETED') AND id>COALESCE(NULLIF($1,'')::uuid,'00000000-0000-0000-0000-000000000000'::uuid) ORDER BY id LIMIT $2`, cursor, limit)
	if err != nil {
		return cursor, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return cursor, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return cursor, err
	}
	var scanErr error
	for _, id := range ids {
		if ctx.Err() != nil {
			return cursor, ctx.Err()
		}
		if err = s.reconcile(ctx, id); err != nil && scanErr == nil {
			scanErr = err
		}
		// Per-instance failure must not starve every later instance. The failed
		// instance is revisited when the bounded stable cursor wraps.
		cursor = id
	}
	if len(ids) < limit {
		return "", scanErr
	}
	return cursor, scanErr
}
func (s *SafetyStore) reconcile(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var org, owner string
	err = tx.QueryRow(ctx, `SELECT org_id::text,owner_id::text FROM instances WHERE id=$1 AND deleted_at IS NULL AND observed_state NOT IN ('STOPPED','DELETED')`, id).Scan(&org, &owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var authorized bool
	if err = tx.QueryRow(ctx, `SELECT public.hc_worker_lock_authority($1,$2)`, org, owner).Scan(&authorized); err != nil {
		return err
	}
	if authorized {
		if err = tx.QueryRow(ctx, `SELECT public.hc_has_permission($1,$2,'instances:start')`, org, owner).Scan(&authorized); err != nil {
			return err
		}
	}
	var revoked bool
	if err = tx.QueryRow(ctx, `SELECT c.revoked_at IS NOT NULL OR c.status='REVOKED' OR sec.revoked_at IS NOT NULL FROM instances i JOIN model_configurations c ON c.org_id=i.org_id AND c.id=i.model_config_id AND c.version=i.model_config_version JOIN secrets sec ON sec.org_id=c.org_id AND sec.id=c.credential_ref WHERE i.id=$1`, id).Scan(&revoked); err != nil {
		return err
	}
	reason := "AUTH_REVOKED"
	if authorized {
		if !revoked {
			return nil
		}
		reason = "CREDENTIAL_REVOKED"
	}
	var ignored string
	if err = tx.QueryRow(ctx, `SELECT org_id::text FROM quota_policies WHERE org_id=$1 FOR UPDATE`, org).Scan(&ignored); err != nil {
		return err
	}
	var desired, state string
	var version int64
	var enabled bool
	err = tx.QueryRow(ctx, `SELECT i.desired_state,i.observed_state,i.version,COALESCE(r.enabled,false) FROM instances i LEFT JOIN access_routes r ON r.instance_id=i.id WHERE i.id=$1 AND i.deleted_at IS NULL FOR UPDATE OF i`, id).Scan(&desired, &state, &version, &enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if state == "STOPPED" || state == "DELETED" {
		return nil
	}
	var active, action string
	err = tx.QueryRow(ctx, `SELECT id::text,type FROM operations WHERE instance_id=$1 AND status IN ('ACCEPTED','RUNNING','UNCERTAIN') FOR UPDATE`, id).Scan(&active, &action)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	changed := enabled || desired == "RUNNING" || active == ""
	if changed {
		if version == math.MaxInt64 {
			return ErrInvalidEvidence
		}
		version++
		if _, err = tx.Exec(ctx, `UPDATE instances SET desired_state=CASE WHEN desired_state='DELETED' THEN 'DELETED' ELSE 'STOPPED' END,version=$2,access_generation=access_generation+1,updated_at=clock_timestamp() WHERE id=$1`, id, version); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE access_routes SET enabled=false WHERE instance_id=$1`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE access_sessions SET revoked_at=clock_timestamp() WHERE instance_id=$1 AND revoked_at IS NULL`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM native_sessions WHERE instance_id=$1`, id); err != nil {
		return err
	}
	for _, dimension := range []string{"configuration", "access"} {
		if _, err = tx.Exec(ctx, `INSERT INTO instance_health(instance_id,dimension,status,checked_at,expires_at,reason_code) VALUES($1,$2,'UNHEALTHY',clock_timestamp(),clock_timestamp()+interval '60 seconds',$3) ON CONFLICT(instance_id,dimension) DO UPDATE SET status=EXCLUDED.status,checked_at=EXCLUDED.checked_at,expires_at=EXCLUDED.expires_at,reason_code=EXCLUDED.reason_code`, id, dimension, reason); err != nil {
			return err
		}
	}
	trace := "safety-" + webapi.ID()
	auditAction, outcome := "INSTANCE_ACCESS_REVOKE", "SUCCEEDED"
	if active == "" {
		active = webapi.ID()
		auditAction, outcome = "INSTANCE_STOP", "ACCEPTED"
		intent, _ := json.Marshal(Intent{SchemaVersion: 1, ExpectedVersion: version, Desired: "STOPPED", Reason: reason, RequestID: trace})
		if _, err = tx.Exec(ctx, `INSERT INTO operations(id,instance_id,actor_kind,type,status,stage,intent,trace_id) VALUES($1,$2,'SYSTEM','STOP','ACCEPTED','QUEUED',$3,$4)`, active, id, intent, trace); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(id,operation_id,status) VALUES($1,$2,'PENDING')`, webapi.ID(), active); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE instances SET observed_state='STOPPING' WHERE id=$1`, id); err != nil {
			return err
		}
	} else if action == "CREATE" || action == "START" || action == "CONFIGURE" {
		if _, err = tx.Exec(ctx, `UPDATE operations SET error_code=$2,stage='REVOKING_ACCESS',updated_at=clock_timestamp() WHERE id=$1 AND error_code IS DISTINCT FROM $2`, active, reason); err != nil {
			return err
		}
	}
	if changed {
		if _, err = tx.Exec(ctx, `INSERT INTO audit_events(id,org_id,owner_id,resource_id,action,outcome,trace_id,details) VALUES($1,$2,$3,$4,$5,$6,$7,jsonb_build_object('schema_version',1,'operation_id',$8::text,'reason_code',$9::text))`, webapi.ID(), org, owner, id, auditAction, outcome, trace, active, reason); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
