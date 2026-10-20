package settings

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// Connection serialization protects simultaneous UPSERT/REVOKE claims even
// when different processes observed both pending rows in an older snapshot.
func (a *ModelSync) claim(ctx context.Context) (modelSyncLease, bool, error) {
	var l modelSyncLease
	tx, err := a.service.pool.Begin(ctx)
	if err != nil {
		return l, false, err
	}
	defer tx.Rollback(context.Background())
	var id, connection string
	err = tx.QueryRow(ctx, `SELECT id::text,connection_id::text FROM model_sync_operations candidate WHERE status IN ('PENDING','RUNNING','UNCERTAIN') AND retry_at<=clock_timestamp() AND (lease_expires_at IS NULL OR lease_expires_at<=clock_timestamp()) AND (action='REVOKE' OR EXISTS(SELECT 1 FROM user_model_connections h WHERE h.id=candidate.connection_id AND h.enabled AND h.archived_at IS NULL AND h.current_version=candidate.connection_version)) AND NOT EXISTS(SELECT 1 FROM model_sync_operations other WHERE other.connection_id=candidate.connection_id AND other.id<>candidate.id AND other.status='RUNNING' AND other.lease_expires_at>clock_timestamp()) ORDER BY created_at,id LIMIT 1`).Scan(&id, &connection)
	if errors.Is(err, pgx.ErrNoRows) {
		return l, false, nil
	}
	if err != nil {
		return l, false, err
	}
	var locked string
	if err = tx.QueryRow(ctx, `SELECT id::text FROM user_model_connections WHERE id=$1 FOR UPDATE SKIP LOCKED`, connection).Scan(&locked); errors.Is(err, pgx.ErrNoRows) {
		return l, false, nil
	} else if err != nil {
		return l, false, err
	}
	err = tx.QueryRow(ctx, `UPDATE model_sync_operations o SET status='RUNNING',lease_owner='model-sync',lease_token=lease_token+1,lease_expires_at=clock_timestamp()+interval '60 seconds',attempts=LEAST(attempts+1,100),updated_at=clock_timestamp()
 WHERE id=$1 AND status IN ('PENDING','RUNNING','UNCERTAIN') AND retry_at<=clock_timestamp() AND (lease_expires_at IS NULL OR lease_expires_at<=clock_timestamp())
 AND (action='REVOKE' OR EXISTS(SELECT 1 FROM user_model_connections h WHERE h.id=o.connection_id AND h.enabled AND h.archived_at IS NULL AND h.current_version=o.connection_version))
 AND NOT EXISTS(SELECT 1 FROM model_sync_operations other WHERE other.connection_id=o.connection_id AND other.id<>o.id AND other.status='RUNNING' AND other.lease_expires_at>clock_timestamp())
 RETURNING id::text,org_id::text,owner_id::text,connection_id::text,connection_version,action,remote_model_id,lease_token`, id).Scan(&l.ID, &l.Org, &l.Owner, &l.Connection, &l.Version, &l.Action, &l.RemoteID, &l.Token)
	if errors.Is(err, pgx.ErrNoRows) {
		return l, false, nil
	}
	if err != nil {
		return l, false, err
	}
	if l.Action == "UPSERT" {
		if _, err = tx.Exec(ctx, `UPDATE user_model_connection_versions SET sync_status='SYNCING' WHERE connection_id=$1 AND version=$2 AND sync_status IN ('PENDING_SYNC','SYNCING','UNCERTAIN')`, l.Connection, l.Version); err != nil {
			return l, false, err
		}
	}
	return l, true, tx.Commit(ctx)
}
