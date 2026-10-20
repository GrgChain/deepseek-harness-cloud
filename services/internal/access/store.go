package access

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"net"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/instances"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	workspaceengine "github.com/jonluo94/deepseek-harness-cloud/services/internal/workspace"
)

type Store struct {
	pool         *pgxpool.Pool
	cipher       *security.SecretCipher
	environment  string
	organization string
	rules        Rules
}

func NewStore(pool *pgxpool.Pool, cipher *security.SecretCipher, environment, organization string, rules Rules) *Store {
	return &Store{pool: pool, cipher: cipher, environment: environment, organization: organization, rules: rules}
}

func (s *Store) begin(ctx context.Context, p identity.Principal, id string) (pgx.Tx, error) {
	if p.OrganizationID != s.organization || !uuid.MatchString(p.OrganizationID) || !uuid.MatchString(p.UserID) || !uuid.MatchString(p.SessionID) || !uuid.MatchString(id) || p.Generation < 1 {
		return nil, denied()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (pgx.Tx, error) { _ = tx.Rollback(context.Background()); return nil, err }
	var role *string
	if err = tx.QueryRow(ctx, `SELECT public.hc_access_lock_principal($1,$2,$3,$4)`, p.OrganizationID, p.UserID, p.SessionID, p.Generation).Scan(&role); err != nil {
		return fail(err)
	}
	if role == nil {
		return fail(denied())
	}
	var allowed bool
	if err = tx.QueryRow(ctx, `SELECT public.hc_has_permission($1,$2,'instances:access')`, p.OrganizationID, p.UserID).Scan(&allowed); err != nil {
		return fail(err)
	}
	if !allowed {
		return fail(denied())
	}
	var model *string
	// Keep the organization model lock order when a head exists. Personal
	// models do not need an organization default; target checks the instance's
	// actual frozen model and its live availability below.
	if err = tx.QueryRow(ctx, `SELECT public.hc_worker_lock_model($1)::text`, p.OrganizationID).Scan(&model); err != nil {
		return fail(err)
	}
	var owned bool
	if err = tx.QueryRow(ctx, `SELECT public.hc_access_lock_instance($1,$2,$3)`, p.OrganizationID, p.UserID, id).Scan(&owned); err != nil {
		return fail(err)
	}
	if !owned {
		return fail(notFound())
	}
	return tx, nil
}

// Call this again after every later lock: an earlier successful check cannot
// extend authorization across a wait on an instance, challenge or session.
func authority(ctx context.Context, tx pgx.Tx, p identity.Principal) (identity.Principal, time.Time, error) {
	var now time.Time
	err := tx.QueryRow(ctx, `SELECT m.role,ps.expires_at,clock_timestamp() FROM memberships m JOIN users u ON u.id=m.user_id JOIN organizations o ON o.id=m.org_id JOIN portal_sessions ps ON ps.org_id=m.org_id AND ps.user_id=m.user_id
 WHERE m.org_id=$1 AND m.user_id=$2 AND ps.id=$3 AND m.status='ACTIVE' AND u.status='ACTIVE' AND o.status='ACTIVE' AND m.auth_generation=$4 AND ps.auth_generation=m.auth_generation AND ps.revoked_at IS NULL AND ps.expires_at>clock_timestamp() AND ps.last_seen_at>clock_timestamp()-interval '30 minutes' AND public.hc_has_permission(m.org_id,m.user_id,'instances:access')`, p.OrganizationID, p.UserID, p.SessionID, p.Generation).Scan(&p.Role, &p.ExpiresAt, &now)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, now, denied()
	}
	p.ExpiresAt = p.ExpiresAt.UTC()
	return p, now.UTC(), err
}
func (s *Store) target(ctx context.Context, tx pgx.Tx, p identity.Principal, id string) (Target, error) {
	var t Target
	var raw []byte
	var model, state, desired string
	var modelVersion, capacity int64
	var route, asset, credential, healthy bool
	err := tx.QueryRow(ctx, `SELECT i.id::text,i.org_id::text,i.owner_id::text,u.workspace_owner_id::text,COALESCE(i.workspace_id::text,''),i.workspace_name,COALESCE(i.storage_asset_id::text,''),COALESCE(r.host,''),COALESCE(host(r.upstream_ip),''),COALESCE(r.runtime_generation::text,''),i.version,i.access_generation,i.config_snapshot,i.model_config_id::text,i.model_config_version,i.observed_state,i.desired_state,COALESCE(r.enabled,false),COALESCE(a.status='READY' AND a.init_version>=1 AND a.filesystem_id IS NOT NULL AND a.filesystem_id<>'',false),COALESCE(a.capacity_bytes,0),c.status<>'REVOKED' AND c.revoked_at IS NULL AND sec.revoked_at IS NULL AND public.hc_model_available(i.org_id,i.owner_id,c.id,c.version),
 (SELECT count(*)=4 FROM instance_health h WHERE h.instance_id=i.id AND h.dimension IN ('application','storage','configuration','access') AND h.status='HEALTHY' AND h.checked_at<=clock_timestamp() AND h.expires_at>clock_timestamp() AND h.expires_at>h.checked_at)
 FROM instances i JOIN users u ON u.id=i.owner_id JOIN model_configurations c ON c.org_id=i.org_id AND c.id=i.model_config_id AND c.version=i.model_config_version JOIN secrets sec ON sec.id=c.credential_ref AND sec.org_id=c.org_id LEFT JOIN access_routes r ON r.instance_id=i.id LEFT JOIN storage_assets a ON a.id=i.storage_asset_id AND a.org_id=i.org_id AND a.owner_id=i.owner_id AND a.instance_id=i.id
 WHERE i.id=$1 AND i.org_id=$2 AND i.owner_id=$3 AND i.deleted_at IS NULL`, id, p.OrganizationID, p.UserID).Scan(&t.InstanceID, &t.OrganizationID, &t.OwnerID, &t.WorkspaceOwner, &t.WorkspaceID, &t.WorkspaceName, &t.AssetID, &t.Host, &t.IP, &t.Generation, &t.Version, &t.AccessGeneration, &raw, &model, &modelVersion, &state, &desired, &route, &asset, &capacity, &credential, &healthy)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, notFound()
	}
	if err != nil {
		return t, err
	}
	if !credential {
		return t, denied()
	}
	if state != "RUNNING" || desired != "RUNNING" || !route || !asset || !healthy {
		return t, webapi.Fail(409, "STATE_CONFLICT", "工作空间状态或必需健康检查暂不允许进入")
	}
	t.Snapshot, err = instances.DecodeSnapshot(raw)
	name, _ := workspaceengine.WorkspaceName(id)
	ip := net.ParseIP(t.IP)
	if err != nil || t.Snapshot.ModelID != model || t.Snapshot.ModelVersion != modelVersion || t.Snapshot.Spec.Storage != capacity || t.Host != s.rules.Host(id) || !uuid.MatchString(t.Generation) || !uuid.MatchString(t.WorkspaceID) || !uuid.MatchString(t.AssetID) || t.WorkspaceName != name || ip == nil || ip.To4() == nil || !ip.IsPrivate() {
		return t, unavailable()
	}
	err = tx.QueryRow(ctx, `SELECT build_id::text FROM operations WHERE id=$1 AND instance_id=$2 AND status='SUCCEEDED' AND type IN ('CREATE','START','CONFIGURE')`, t.Generation, id).Scan(&t.BuildID)
	if err != nil || !uuid.MatchString(t.BuildID) {
		return t, unavailable()
	}
	return t, nil
}
func (s *Store) Prepare(ctx context.Context, p identity.Principal, id string) (Target, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	tx, err := s.begin(ctx, p, id)
	if err != nil {
		return Target{}, err
	}
	defer tx.Rollback(context.Background())
	if _, _, err = authority(ctx, tx, p); err != nil {
		return Target{}, err
	}
	t, err := s.target(ctx, tx, p, id)
	if err != nil {
		return t, err
	}
	return t, tx.Commit(ctx)
}

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

func (s *Store) Authorize(ctx context.Context, host, secret string, expected *Target, touch bool) (Authorization, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var out Authorization
	if !opaque.MatchString(secret) {
		return out, denied()
	}
	var id string
	err := s.pool.QueryRow(ctx, `SELECT a.id::text,a.instance_id::text,ps.org_id::text,ps.user_id::text,ps.id::text,a.auth_generation FROM access_sessions a JOIN portal_sessions ps ON ps.id=a.portal_session_id WHERE a.secret_sha256=$1 AND a.host=$2`, hash(secret), host).Scan(&out.SessionID, &id, &out.Principal.OrganizationID, &out.Principal.UserID, &out.Principal.SessionID, &out.Principal.Generation)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, denied()
	}
	if err != nil {
		return out, err
	}
	tx, err := s.begin(ctx, out.Principal, id)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(context.Background())
	var revoked *time.Time
	var accessGeneration, authGeneration int64
	err = tx.QueryRow(ctx, `SELECT expires_at,revoked_at,access_generation,auth_generation FROM access_sessions WHERE id=$1 AND secret_sha256=$2 AND host=$3 AND portal_session_id=$4 AND instance_id=$5 FOR UPDATE`, out.SessionID, hash(secret), host, out.Principal.SessionID, id).Scan(&out.ExpiresAt, &revoked, &accessGeneration, &authGeneration)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, denied()
	}
	if err != nil {
		return out, err
	}
	var now time.Time
	out.Principal, now, err = authority(ctx, tx, out.Principal)
	if err != nil {
		return out, err
	}
	out.Target, err = s.target(ctx, tx, out.Principal, id)
	if err != nil {
		return out, err
	}
	if revoked != nil || !out.ExpiresAt.After(now) || authGeneration != out.Principal.Generation || accessGeneration != out.Target.AccessGeneration || host != out.Target.Host || (expected != nil && !out.Target.same(*expected)) {
		return out, denied()
	}
	var envelope security.Envelope
	var generation string
	var expiry time.Time
	err = tx.QueryRow(ctx, `SELECT runtime_generation::text,key_id,nonce,cookie_ciphertext,expires_at FROM native_sessions WHERE instance_id=$1`, id).Scan(&generation, &envelope.KeyID, &envelope.Nonce, &envelope.Ciphertext, &expiry)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	if errors.Is(err, pgx.ErrNoRows) || generation != out.Target.Generation || !expiry.After(now) {
		return out, webapi.Fail(502, "NATIVE_AUTH_FAILED", "工作空间原生会话暂不可用，请稍后重新进入")
	}
	out.Cookie, err = s.cipher.Decrypt(envelope, security.Context{Environment: s.environment, OrganizationID: out.Principal.OrganizationID, RecordID: id + ":" + generation, Purpose: "native-cookie"})
	if err != nil || !validNativeHeader(s.rules.authority(host), out.Cookie) {
		out.Destroy()
		return out, webapi.Fail(502, "NATIVE_AUTH_FAILED", "工作空间原生会话暂不可用，请稍后重新进入")
	}
	if touch {
		if _, err = tx.Exec(ctx, `UPDATE portal_sessions SET last_seen_at=clock_timestamp() WHERE id=$1 AND revoked_at IS NULL AND expires_at>clock_timestamp() AND last_seen_at<=clock_timestamp()-interval '60 seconds'`, out.Principal.SessionID); err != nil {
			out.Destroy()
			return out, err
		}
	}
	out.ExpiresAt = out.ExpiresAt.UTC()
	if out.Principal.ExpiresAt.Before(out.ExpiresAt) {
		out.ExpiresAt = out.Principal.ExpiresAt
	}
	if err = tx.Commit(ctx); err != nil {
		out.Destroy()
		return out, err
	}
	return out, nil
}

func (s *Store) audit(ctx context.Context, tx pgx.Tx, p identity.Principal, id, action, outcome, trace string) error {
	if trace == "" {
		trace = webapi.ID()
	}
	_, err := tx.Exec(ctx, `INSERT INTO audit_events(id,org_id,actor_id,owner_id,resource_id,action,outcome,trace_id,details) VALUES($1,$2,$3,$3,$4,$5,$6,$7,$8)`, webapi.ID(), p.OrganizationID, p.UserID, id, action, outcome, trace, json.RawMessage(`{"schema_version":1}`))
	return err
}

func (s *Store) Revoke(ctx context.Context, authorization Authorization, trace string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	tag, err := tx.Exec(ctx, `UPDATE access_sessions SET revoked_at=clock_timestamp() WHERE id=$1 AND instance_id=$2 AND portal_session_id=$3 AND revoked_at IS NULL`, authorization.SessionID, authorization.Target.InstanceID, authorization.Principal.SessionID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		if err = s.audit(ctx, tx, authorization.Principal, authorization.Target.InstanceID, "INSTANCE_ACCESS", "DENIED", trace); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) Entered(ctx context.Context, authorization Authorization, trace string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err = s.audit(ctx, tx, authorization.Principal, authorization.Target.InstanceID, "INSTANCE_ACCESS", "SUCCEEDED", trace); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
