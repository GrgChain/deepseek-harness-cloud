package access

import (
	"context"
	"crypto/hmac"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func (s *Store) Issue(ctx context.Context, p identity.Principal, expected Target, challengeID, trace string) (Handoff, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if !uuid.MatchString(challengeID) {
		return Handoff{}, denied()
	}
	tx, err := s.begin(ctx, p, expected.InstanceID)
	if err != nil {
		return Handoff{}, err
	}
	defer tx.Rollback(context.Background())
	var ignored string
	err = tx.QueryRow(ctx, `SELECT id::text FROM access_challenges WHERE id=$1 AND instance_id=$2 AND host=$3 FOR UPDATE`, challengeID, expected.InstanceID, expected.Host).Scan(&ignored)
	if errors.Is(err, pgx.ErrNoRows) {
		return Handoff{}, denied()
	}
	if err != nil {
		return Handoff{}, err
	}
	p, _, err = authority(ctx, tx, p)
	if err != nil {
		return Handoff{}, err
	}
	current, err := s.target(ctx, tx, p, expected.InstanceID)
	if err != nil {
		return Handoff{}, err
	}
	if !current.same(expected) {
		return Handoff{}, denied()
	}
	result := Handoff{URL: s.rules.URL(current.Host) + "/__dsh/handoff", Ticket: newSecret(), ChallengeID: challengeID}
	err = tx.QueryRow(ctx, `INSERT INTO access_tickets(id,ticket_sha256,challenge_id,portal_session_id,instance_id,host,auth_generation,access_generation,expires_at)
 SELECT $1,$2,c.id,$4,c.instance_id,c.host,$5,$6,LEAST(clock_timestamp()+interval '45 seconds',c.expires_at,$7::timestamptz) FROM access_challenges c WHERE c.id=$3 AND c.instance_id=$8 AND c.host=$9 AND c.consumed_at IS NULL AND c.expires_at>=clock_timestamp()+interval '5 seconds' AND $7::timestamptz>clock_timestamp()
 ON CONFLICT(challenge_id) DO NOTHING RETURNING expires_at`, webapi.ID(), hash(result.Ticket), challengeID, p.SessionID, p.Generation, current.AccessGeneration, p.ExpiresAt, current.InstanceID, current.Host).Scan(&result.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Handoff{}, denied()
	}
	if err != nil {
		return Handoff{}, err
	}
	if err = s.audit(ctx, tx, p, current.InstanceID, "INSTANCE_ACCESS_TICKET", "SUCCEEDED", trace); err != nil {
		return Handoff{}, err
	}
	result.ExpiresAt = result.ExpiresAt.UTC()
	return result, tx.Commit(ctx)
}

func (s *Store) ticketPrincipal(ctx context.Context, host, ticket, challengeID string) (identity.Principal, string, error) {
	var p identity.Principal
	var id string
	if !opaque.MatchString(ticket) || !uuid.MatchString(challengeID) {
		return p, id, denied()
	}
	err := s.pool.QueryRow(ctx, `SELECT ps.org_id::text,ps.user_id::text,ps.id::text,t.auth_generation,t.instance_id::text FROM access_tickets t JOIN portal_sessions ps ON ps.id=t.portal_session_id WHERE t.ticket_sha256=$1 AND t.host=$2 AND t.challenge_id=$3`, hash(ticket), host, challengeID).Scan(&p.OrganizationID, &p.UserID, &p.SessionID, &p.Generation, &id)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, id, denied()
	}
	return p, id, err
}

func (s *Store) PrepareHandoff(ctx context.Context, host, ticket, challengeID, browser string) (Target, error) {
	if !opaque.MatchString(browser) {
		return Target{}, denied()
	}
	p, id, err := s.ticketPrincipal(ctx, host, ticket, challengeID)
	if err != nil {
		return Target{}, err
	}
	target, err := s.Prepare(ctx, p, id)
	if err != nil {
		return Target{}, err
	}
	var cookieHash []byte
	err = s.pool.QueryRow(ctx, `SELECT c.secret_sha256 FROM access_challenges c JOIN access_tickets t ON t.challenge_id=c.id WHERE c.id=$1 AND c.instance_id=$2 AND c.host=$3 AND t.host=$3 AND t.instance_id=c.instance_id AND t.ticket_sha256=$4 AND t.portal_session_id=$5 AND t.auth_generation=$6 AND t.access_generation=$7 AND c.consumed_at IS NULL AND t.consumed_at IS NULL AND c.expires_at>clock_timestamp() AND t.expires_at>clock_timestamp()`, challengeID, id, host, hash(ticket), p.SessionID, p.Generation, target.AccessGeneration).Scan(&cookieHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return Target{}, denied()
	}
	if err != nil {
		return Target{}, err
	}
	if target.Host != host || !hmac.Equal(cookieHash, hash(browser)) {
		return Target{}, denied()
	}
	return target, nil
}

func (s *Store) Consume(ctx context.Context, host, ticket, challengeID, browser string, expected Target, trace string) (string, Authorization, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var out Authorization
	if !opaque.MatchString(browser) {
		return "", out, denied()
	}
	p, id, err := s.ticketPrincipal(ctx, host, ticket, challengeID)
	if err != nil {
		return "", out, err
	}
	if id != expected.InstanceID || host != expected.Host {
		return "", out, denied()
	}
	tx, err := s.begin(ctx, p, id)
	if err != nil {
		return "", out, err
	}
	defer tx.Rollback(context.Background())
	var browserHash []byte
	var challengeExpiry time.Time
	var challengeConsumed *time.Time
	err = tx.QueryRow(ctx, `SELECT secret_sha256,expires_at,consumed_at FROM access_challenges WHERE id=$1 AND instance_id=$2 AND host=$3 FOR UPDATE`, challengeID, id, host).Scan(&browserHash, &challengeExpiry, &challengeConsumed)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", out, denied()
	}
	if err != nil {
		return "", out, err
	}
	var ticketID, portalID string
	var authGeneration, accessGeneration int64
	var ticketExpiry time.Time
	var ticketConsumed *time.Time
	err = tx.QueryRow(ctx, `SELECT id::text,portal_session_id::text,auth_generation,access_generation,expires_at,consumed_at FROM access_tickets WHERE ticket_sha256=$1 AND challenge_id=$2 AND instance_id=$3 AND host=$4 FOR UPDATE`, hash(ticket), challengeID, id, host).Scan(&ticketID, &portalID, &authGeneration, &accessGeneration, &ticketExpiry, &ticketConsumed)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", out, denied()
	}
	if err != nil {
		return "", out, err
	}
	p, now, err := authority(ctx, tx, p)
	if err != nil {
		return "", out, err
	}
	current, err := s.target(ctx, tx, p, id)
	if err != nil {
		return "", out, err
	}
	if !current.same(expected) || !hmac.Equal(browserHash, hash(browser)) || challengeConsumed != nil || ticketConsumed != nil || !challengeExpiry.After(now) || !ticketExpiry.After(now) || portalID != p.SessionID || authGeneration != p.Generation || accessGeneration != current.AccessGeneration {
		return "", out, denied()
	}
	// Conditions are evaluated after every potentially contended lock.
	for _, q := range []struct {
		sql string
		id  string
	}{
		{`UPDATE access_challenges SET consumed_at=clock_timestamp() WHERE id=$1 AND consumed_at IS NULL AND expires_at>clock_timestamp()`, challengeID},
		{`UPDATE access_tickets SET consumed_at=clock_timestamp() WHERE id=$1 AND consumed_at IS NULL AND expires_at>clock_timestamp()`, ticketID},
	} {
		tag, err := tx.Exec(ctx, q.sql, q.id)
		if err != nil {
			return "", out, err
		}
		if tag.RowsAffected() != 1 {
			return "", out, denied()
		}
	}
	secret := newSecret()
	out = Authorization{Target: current, Principal: p, SessionID: webapi.ID(), ExpiresAt: now.Add(30 * time.Minute)}
	if p.ExpiresAt.Before(out.ExpiresAt) {
		out.ExpiresAt = p.ExpiresAt
	}
	if _, err = tx.Exec(ctx, `INSERT INTO access_sessions(id,secret_sha256,portal_session_id,instance_id,host,auth_generation,access_generation,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, out.SessionID, hash(secret), p.SessionID, id, host, p.Generation, current.AccessGeneration, out.ExpiresAt); err != nil {
		return "", Authorization{}, err
	}
	if err = s.audit(ctx, tx, p, id, "INSTANCE_ACCESS", "ACCEPTED", trace); err != nil {
		return "", Authorization{}, err
	}
	return secret, out, tx.Commit(ctx)
}
