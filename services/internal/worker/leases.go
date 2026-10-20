// Package worker owns durable execution leases. External effects must be
// reconciled after every claim, including a claim recovered from UNCERTAIN.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrLostLease = errors.New("operation execution lease was lost")
var workerName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

type Lease struct {
	ID, InstanceID, Owner, Action, ActorKind string
	ActorID                                  *string
	Intent                                   json.RawMessage
	Token                                    int64
	ExpiresAt                                time.Time
}
type LeaseStore struct {
	pool        *pgxpool.Pool
	maintenance bool
}

func NewLeaseStore(pool *pgxpool.Pool) *LeaseStore             { return &LeaseStore{pool: pool} }
func (s *LeaseStore) WithMaintenance(enabled bool) *LeaseStore { s.maintenance = enabled; return s }
func (s *LeaseStore) Claim(ctx context.Context, owner string, slots int, systemOnly bool) ([]Lease, error) {
	if !workerName.MatchString(owner) || slots < 1 || slots > 10 {
		return nil, errors.New("invalid bounded lease claim")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	// Lock only operation rows first; terminal writers use the same operation ->
	// outbox order. Never wait on instance/quota locks while holding these rows.
	rows, err := tx.Query(ctx, `SELECT o.id::text FROM operations o JOIN outbox_events e ON e.operation_id=o.id WHERE e.status='PENDING' AND e.retry_at<=clock_timestamp() AND o.status IN ('ACCEPTED','RUNNING','UNCERTAIN') AND (o.lease_expires_at IS NULL OR o.lease_expires_at<=clock_timestamp()) AND (NOT $3 OR o.type IN ('STOP','DELETE') OR o.status IN ('RUNNING','UNCERTAIN')) AND (($2 AND o.actor_kind='SYSTEM' AND o.type='STOP') OR (NOT $2 AND o.actor_kind='USER')) ORDER BY e.retry_at,o.id LIMIT $1 FOR UPDATE OF o SKIP LOCKED`, slots, systemOnly, s.maintenance)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	leases := []Lease{}
	for _, id := range ids {
		var pending bool
		err = tx.QueryRow(ctx, `SELECT status='PENDING' AND retry_at<=clock_timestamp() FROM outbox_events WHERE operation_id=$1 FOR UPDATE`, id).Scan(&pending)
		if err != nil {
			return nil, err
		}
		if !pending {
			continue
		}
		var lease Lease
		err = tx.QueryRow(ctx, `UPDATE operations SET lease_owner=$2,lease_token=lease_token+1,lease_expires_at=clock_timestamp()+interval '60 seconds',status=CASE WHEN status='ACCEPTED' THEN 'RUNNING' ELSE status END,updated_at=clock_timestamp() WHERE id=$1 AND status IN ('ACCEPTED','RUNNING','UNCERTAIN') AND (lease_expires_at IS NULL OR lease_expires_at<=clock_timestamp()) RETURNING id::text,instance_id::text,type,actor_kind,actor_id::text,intent,lease_token,lease_expires_at`, id, owner).Scan(&lease.ID, &lease.InstanceID, &lease.Action, &lease.ActorKind, &lease.ActorID, &lease.Intent, &lease.Token, &lease.ExpiresAt)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		lease.Owner = owner
		leases = append(leases, lease)
	}
	return leases, tx.Commit(ctx)
}
func (s *LeaseStore) Renew(ctx context.Context, lease Lease) error {
	tag, err := s.pool.Exec(ctx, `UPDATE operations SET lease_expires_at=clock_timestamp()+interval '60 seconds',updated_at=clock_timestamp() WHERE id=$1 AND lease_owner=$2 AND lease_token=$3 AND lease_expires_at>clock_timestamp() AND status IN ('RUNNING','UNCERTAIN')`, lease.ID, lease.Owner, lease.Token)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrLostLease
	}
	return nil
}
func validStage(stage string) bool {
	switch stage {
	case "PREPARING_STORAGE", "CREATING_WORKSPACE", "BUILDING", "CONFIGURING", "CHECKING", "REVOKING_ACCESS", "STOPPING", "DELETING", "RECONCILING":
		return true
	}
	return false
}

// Stage changes only operation progress. Resource/terminal publication requires
// the complete quota -> instance -> operation -> outbox transaction elsewhere.
func (s *LeaseStore) Stage(ctx context.Context, lease Lease, stage string) error {
	if !validStage(stage) {
		return errors.New("invalid nonterminal operation stage")
	}
	tag, err := s.pool.Exec(ctx, `UPDATE operations SET stage=$4,updated_at=clock_timestamp() WHERE id=$1 AND lease_owner=$2 AND lease_token=$3 AND lease_expires_at>clock_timestamp() AND status IN ('RUNNING','UNCERTAIN')`, lease.ID, lease.Owner, lease.Token, stage)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrLostLease
	}
	return nil
}
func (s *LeaseStore) Defer(ctx context.Context, lease Lease, uncertain bool) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var id string
	err = tx.QueryRow(ctx, `SELECT id::text FROM operations WHERE id=$1 AND lease_owner=$2 AND lease_token=$3 AND lease_expires_at>clock_timestamp() AND status IN ('RUNNING','UNCERTAIN') FOR UPDATE`, lease.ID, lease.Owner, lease.Token).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLostLease
	}
	if err != nil {
		return err
	}
	var outbox string
	err = tx.QueryRow(ctx, `SELECT id::text FROM outbox_events WHERE operation_id=$1 AND status='PENDING' FOR UPDATE`, id).Scan(&outbox)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLostLease
	}
	if err != nil {
		return err
	}
	// Time may have passed while the outbox lock was unavailable.
	tag, err := tx.Exec(ctx, `UPDATE operations SET status=CASE WHEN $4 THEN 'UNCERTAIN' ELSE status END,stage='RECONCILING',lease_owner=NULL,lease_expires_at=NULL,updated_at=clock_timestamp() WHERE id=$1 AND lease_owner=$2 AND lease_token=$3 AND lease_expires_at>clock_timestamp()`, id, lease.Owner, lease.Token, uncertain)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrLostLease
	}
	tag, err = tx.Exec(ctx, `UPDATE outbox_events SET attempts=attempts+1,retry_at=clock_timestamp()+interval '10 seconds' WHERE id=$1 AND status='PENDING'`, outbox)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrLostLease
	}
	return tx.Commit(ctx)
}
