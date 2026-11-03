package access

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func (s *Store) Challenge(ctx context.Context, host string) (Challenge, string, error) {
	return s.ReplaceChallenge(ctx, host, "")
}

func (s *Store) ReplaceChallenge(ctx context.Context, host, previous string) (Challenge, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	id, ok := s.rules.Resolve(s.rules.authority(host))
	if !ok {
		return Challenge{}, "", notFound()
	}
	secret := newSecret()
	result := Challenge{ID: webapi.ID()}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Challenge{}, "", err
	}
	defer tx.Rollback(context.Background())
	// Keep the instance before challenge order even for anonymous replacement.
	// The later challenge INSERT takes an instance FK lock and must not invert
	// the order used by ticket consumption.
	var locked bool
	if err = tx.QueryRow(ctx, `SELECT public.hc_access_lock_instance(i.org_id,i.owner_id,i.id) FROM instances i WHERE i.id=$1 AND i.org_id=$2`, id, s.organization).Scan(&locked); err != nil || !locked {
		if errors.Is(err, pgx.ErrNoRows) {
			err = notFound()
		}
		if err == nil {
			err = notFound()
		}
		return Challenge{}, "", err
	}
	if opaque.MatchString(previous) {
		if _, err = tx.Exec(ctx, `UPDATE access_challenges SET consumed_at=clock_timestamp() WHERE instance_id=$1 AND host=$2 AND secret_sha256=$3 AND consumed_at IS NULL`, id, host, hash(previous)); err != nil {
			return Challenge{}, "", err
		}
	}
	err = tx.QueryRow(ctx, `INSERT INTO access_challenges(id,instance_id,host,secret_sha256,expires_at)
 SELECT $1,i.id,r.host,$4,clock_timestamp()+interval '60 seconds' FROM instances i JOIN access_routes r ON r.instance_id=i.id JOIN organizations o ON o.id=i.org_id WHERE i.id=$2 AND r.host=$3 AND r.enabled AND i.deleted_at IS NULL AND i.observed_state='RUNNING' AND i.desired_state='RUNNING' AND o.status='ACTIVE' AND i.org_id=$5 RETURNING expires_at`, result.ID, id, host, hash(secret), s.organization).Scan(&result.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Challenge{}, "", notFound()
	}
	result.ExpiresAt = result.ExpiresAt.UTC()
	if err != nil {
		return Challenge{}, "", err
	}
	return result, secret, tx.Commit(ctx)
}
