package operations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/worker"
)

// PostgresDailySource backs up only a stable stopped environment. It does not
// stop instances, edit quota or weaken maintenance/ownership requirements.
type PostgresDailySource struct {
	pool               *pgxpool.Pool
	dsn, org, operator string
	storage            *StorageService
}

func NewPostgresDailySource(pool *pgxpool.Pool, dsn string, host *worker.LocalHost, org, operator string) *PostgresDailySource {
	return &PostgresDailySource{pool: pool, dsn: dsn, org: org, operator: operator, storage: NewStorageService(pool, host, org, operator)}
}
func (s *PostgresDailySource) Snapshot(ctx context.Context) (DailySnapshot, error) {
	p := DailySnapshot{OrganizationID: s.org}
	if s.pool == nil || !uuid.MatchString(s.org) || !operatorName.MatchString(s.operator) {
		return p, ErrInput
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return p, ErrDailyBackup
	}
	defer tx.Rollback(context.Background())
	if db.Check(ctx, tx) != nil {
		return p, ErrDailyBackup
	}
	var safe bool
	var state []byte
	err = tx.QueryRow(ctx, `SELECT current_database(),
 (SELECT count(*)=1 AND bool_and(id=$1::uuid AND status='ACTIVE') FROM organizations)
 AND NOT EXISTS(SELECT 1 FROM instances WHERE org_id=$1 AND NOT ((desired_state='STOPPED' AND observed_state='STOPPED') OR (desired_state='DELETED' AND observed_state='DELETED')))
 AND NOT EXISTS(SELECT 1 FROM operations o JOIN instances i ON i.id=o.instance_id WHERE i.org_id=$1 AND o.status IN ('ACCEPTED','RUNNING','UNCERTAIN'))
 AND NOT EXISTS(SELECT 1 FROM storage_assets WHERE org_id=$1 AND (status NOT IN ('READY','RETAINED','DELETED') OR maintenance_intent IS NOT NULL))
 AND NOT EXISTS(SELECT 1 FROM quota_reservations WHERE org_id=$1 AND status IN ('HELD','USED') AND resource_type NOT IN ('INSTANCE','STORAGE'))
 AND NOT EXISTS(SELECT 1 FROM recovery_quota_holds WHERE org_id=$1)
 AND NOT EXISTS(SELECT 1 FROM access_routes r JOIN instances i ON i.id=r.instance_id WHERE i.org_id=$1 AND r.enabled),
 EXISTS(SELECT 1 FROM audit_retention_holds WHERE org_id=$1 AND active) OR EXISTS(SELECT 1 FROM storage_assets WHERE org_id=$1 AND legal_hold),
 jsonb_build_object('instances',(SELECT coalesce(jsonb_agg(jsonb_build_array(id,version,desired_state,observed_state,config_snapshot,storage_asset_id,workspace_id) ORDER BY id),'[]') FROM instances WHERE org_id=$1),
 'assets',(SELECT coalesce(jsonb_agg(jsonb_build_array(id,instance_id,owner_id,resource_name,status,filesystem_id,capacity_bytes,legal_hold,retention_until,maintenance_intent) ORDER BY id),'[]') FROM storage_assets WHERE org_id=$1),
 'quota',(SELECT coalesce(jsonb_agg(jsonb_build_array(id,instance_id,resource_type,amount,status) ORDER BY id),'[]') FROM quota_reservations WHERE org_id=$1),
 'user_quotas',(SELECT coalesce(jsonb_agg(to_jsonb(q) ORDER BY owner_id),'[]') FROM user_quota_policies q WHERE org_id=$1),
 'provider_accounts',(SELECT coalesce(jsonb_agg(to_jsonb(a) ORDER BY id),'[]') FROM personal_provider_accounts a WHERE org_id=$1),
 'models',(SELECT coalesce(jsonb_agg(jsonb_build_array(id,version,provider,endpoint,protocol,model,credential_ref,status,revoked_at) ORDER BY id,version),'[]') FROM model_configurations WHERE org_id=$1),
 'defaults',(SELECT coalesce(jsonb_agg(to_jsonb(d)),'[]') FROM organization_defaults d WHERE org_id=$1),
 'authorization',(SELECT coalesce(jsonb_agg(jsonb_build_array(m.user_id,m.role,m.status,m.auth_generation,u.workspace_owner_id,u.status) ORDER BY m.user_id),'[]') FROM memberships m JOIN users u ON u.id=m.user_id WHERE m.org_id=$1))`, s.org).Scan(&p.Database, &safe, &p.Held, &state)
	if err != nil || !safe {
		return p, ErrDailyBackup
	}
	rows, err := tx.Query(ctx, `SELECT id::text FROM storage_assets WHERE org_id=$1 AND status<>'DELETED' ORDER BY id LIMIT 1001`, s.org)
	if err != nil {
		return p, ErrDailyBackup
	}
	for rows.Next() {
		var id string
		if rows.Scan(&id) != nil {
			rows.Close()
			return p, ErrDailyBackup
		}
		p.Assets = append(p.Assets, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(p.Assets) > 1000 {
		return p, ErrDailyBackup
	}
	sum := sha256.Sum256(state)
	p.StateSHA256 = hex.EncodeToString(sum[:])
	if !validDailySnapshot(p) {
		return p, ErrDailyBackup
	}
	return p, nil
}
func (s *PostgresDailySource) Database(ctx context.Context, path string, key []byte) (ArchiveProof, error) {
	return BackupDatabase(ctx, s.pool, s.dsn, path, s.operator, key)
}
func (s *PostgresDailySource) Asset(ctx context.Context, id, path string, key []byte) (ArchiveProof, error) {
	setID := strings.TrimPrefix(filepath.Base(filepath.Dir(path)), "pending-")
	if !uuid.MatchString(setID) {
		return ArchiveProof{}, ErrDailyBackup
	}
	return s.storage.backup(ctx, id, path, key, setID)
}

// Hold changes use the organization row and protected tables in this same
// order. Keep them locked until this one physical prune has finished, so a
// concurrent legal hold cannot be accepted midway through deleting a set.
func (s *PostgresDailySource) RetentionGuard(ctx context.Context, action func() error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ErrDailyBackup
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SET LOCAL lock_timeout='1s'; SET LOCAL statement_timeout='4s'; SET LOCAL idle_in_transaction_session_timeout='10s'`); err != nil {
		return ErrDailyBackup
	}
	var org string
	if err = tx.QueryRow(ctx, `SELECT id::text FROM organizations WHERE id=$1 FOR NO KEY UPDATE`, s.org).Scan(&org); err != nil {
		return ErrDailyBackup
	}
	if _, err = tx.Exec(ctx, `LOCK TABLE audit_retention_holds,storage_assets IN SHARE MODE`); err != nil {
		return ErrDailyBackup
	}
	var held bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM audit_retention_holds WHERE org_id=$1 AND active) OR EXISTS(SELECT 1 FROM storage_assets WHERE org_id=$1 AND legal_hold)`, s.org).Scan(&held); err != nil || held {
		return ErrDailyBackup
	}
	if err = action(); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *PostgresDailySource) Audit(ctx context.Context, id, action string, details any) error {
	if !uuid.MatchString(id) || (action != "BACKUP_SET_CREATE" && action != "BACKUP_SET_PRUNE_ACCEPTED" && action != "BACKUP_SET_PRUNE") {
		return ErrInput
	}
	raw, err := json.Marshal(details)
	if err != nil {
		return ErrDailyBackup
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return ErrDailyBackup
	}
	operator, _ := json.Marshal(s.operator)
	object["operator_ref"] = operator
	raw, err = json.Marshal(object)
	if err != nil {
		return ErrDailyBackup
	}
	outcome := "SUCCEEDED"
	if action == "BACKUP_SET_PRUNE_ACCEPTED" {
		outcome = "ACCEPTED"
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ErrDailyBackup
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SET LOCAL lock_timeout='1s'; SET LOCAL statement_timeout='4s'`); err != nil {
		return ErrDailyBackup
	}
	if action != "BACKUP_SET_CREATE" {
		if _, err = tx.Exec(ctx, `LOCK TABLE audit_retention_holds,storage_assets IN SHARE MODE`); err != nil {
			return ErrDailyBackup
		}
		var held bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM audit_retention_holds WHERE org_id=$1 AND active) OR EXISTS(SELECT 1 FROM storage_assets WHERE org_id=$1 AND legal_hold)`, s.org).Scan(&held); err != nil || held {
			return ErrDailyBackup
		}
	}
	if action == "BACKUP_SET_PRUNE_ACCEPTED" || action == "BACKUP_SET_PRUNE" {
		state := "PRUNE_PENDING"
		if action == "BACKUP_SET_PRUNE" {
			state = "PRUNED"
		}
		if _, err = tx.Exec(ctx, `UPDATE storage_backups SET status=$3 WHERE org_id=$1 AND set_id=$2 AND status IN ('SUCCEEDED','PRUNE_PENDING')`, s.org, id, state); err != nil {
			return ErrDailyBackup
		}
	}
	if action == "BACKUP_SET_CREATE" {
		var meta struct {
			Day string `json:"day"`
		}
		if json.Unmarshal(raw, &meta) == nil && len(meta.Day) == 10 {
			if _, err = tx.Exec(ctx, `UPDATE storage_backups SET archive_path=replace(archive_path,'/pending-'||$2::text||'/','/day-'||$3::text||'-'||$2::text||'/') WHERE org_id=$1 AND set_id=$2::uuid`, s.org, id, meta.Day); err != nil {
				return ErrDailyBackup
			}
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(id,org_id,action,outcome,trace_id,details) VALUES($1::uuid,$2::uuid,$3,$4,$5,$6)`, webapi.ID(), s.org, action, outcome, id, raw); err != nil {
		return ErrDailyBackup
	}
	return tx.Commit(ctx)
}
