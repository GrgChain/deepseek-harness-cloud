package identity

import (
	"context"
	"crypto/subtle"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
)

type Service struct {
	pool                              *pgxpool.Pool
	provider                          Provider
	cipher                            *security.SecretCipher
	environment, organization, origin string
}

func NewService(pool *pgxpool.Pool, provider Provider, cipher *security.SecretCipher, environment, organization, origin string) *Service {
	return &Service{pool: pool, provider: provider, cipher: cipher, environment: environment, organization: organization, origin: origin}
}
func (s *Service) aad(id, purpose string) security.Context {
	return security.Context{Environment: s.environment, OrganizationID: s.organization, RecordID: id, Purpose: purpose}
}

func (s *Service) Login(ctx context.Context, oldBrowser, path string) (LoginResult, error) {
	path, err := ReturnPath(path)
	if err != nil {
		return LoginResult{}, err
	}
	id, state, nonce, verifier, browser := newID(), randomSecret(), randomSecret(), randomSecret(), randomSecret()
	encrypted, err := s.cipher.Encrypt([]byte(verifier), s.aad(id, "oidc-pkce"))
	if err != nil {
		return LoginResult{}, ErrUnavailable
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return LoginResult{}, ErrUnavailable
	}
	defer tx.Rollback(context.Background())
	if oldBrowser != "" {
		if _, err := tx.Exec(ctx, "UPDATE oidc_transactions SET consumed_at=clock_timestamp() WHERE browser_sha256=$1 AND consumed_at IS NULL", digest(oldBrowser)); err != nil {
			return LoginResult{}, ErrUnavailable
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO oidc_transactions(id,state_sha256,browser_sha256,nonce_sha256,pkce_cipher,pkce_nonce,key_id,return_path,expires_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,clock_timestamp()+interval '5 minutes')`, id, digest(state), digest(browser), digest(nonce), encrypted.Ciphertext, encrypted.Nonce, encrypted.KeyID, path); err != nil {
		return LoginResult{}, ErrUnavailable
	}
	if err := tx.Commit(ctx); err != nil {
		return LoginResult{}, ErrUnavailable
	}
	return LoginResult{URL: s.provider.Authorize(state, nonce, verifier), BrowserSecret: browser}, nil
}

func (s *Service) Callback(ctx context.Context, state, browser, code, oldSession string) (CallbackResult, error) {
	if len(state) < 32 || len(state) > 256 || len(browser) != 43 {
		return CallbackResult{}, ErrUnauthenticated
	}
	var id, path string
	var nonceHash []byte
	var envelope security.Envelope
	// Consumption commits before network I/O. A failed exchange requires a new login.
	err := s.pool.QueryRow(ctx, `UPDATE oidc_transactions SET consumed_at=clock_timestamp()
 WHERE state_sha256=$1 AND browser_sha256=$2 AND consumed_at IS NULL AND expires_at>clock_timestamp()
 RETURNING id::text,nonce_sha256,pkce_cipher,pkce_nonce,key_id,return_path`, digest(state), digest(browser)).Scan(&id, &nonceHash, &envelope.Ciphertext, &envelope.Nonce, &envelope.KeyID, &path)
	if errors.Is(err, pgx.ErrNoRows) {
		return CallbackResult{}, ErrUnauthenticated
	}
	if err != nil {
		return CallbackResult{}, ErrUnavailable
	}
	if code == "" {
		return CallbackResult{}, ErrUnauthenticated
	}
	verifier, err := s.cipher.Decrypt(envelope, s.aad(id, "oidc-pkce"))
	if err != nil {
		return CallbackResult{}, ErrUnauthenticated
	}
	verified, err := s.provider.Exchange(ctx, code, string(verifier))
	if err != nil || verified.Subject == "" || subtle.ConstantTimeCompare(nonceHash, digest(verified.Nonce)) != 1 {
		return CallbackResult{}, ErrUnauthenticated
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CallbackResult{}, ErrUnavailable
	}
	defer tx.Rollback(context.Background())
	var userID, role string
	var generation int64
	err = tx.QueryRow(ctx, `SELECT u.id::text,m.role,m.auth_generation FROM users u JOIN memberships m ON m.user_id=u.id
 JOIN organizations o ON o.id=m.org_id WHERE u.identity_issuer=$1 AND u.identity_subject=$2 AND m.org_id=$3
 AND u.status='ACTIVE' AND m.status='ACTIVE' AND o.status='ACTIVE' FOR UPDATE OF m`, verified.Issuer, verified.Subject, s.organization).Scan(&userID, &role, &generation)
	if errors.Is(err, pgx.ErrNoRows) {
		return CallbackResult{}, ErrForbidden
	}
	if err != nil {
		return CallbackResult{}, ErrUnavailable
	}
	if verified.Generation != 0 {
		var valid bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM identity_accounts a WHERE a.user_id=$1 AND a.org_id=$2 AND a.enabled AND a.credential_version=$3 AND NOT EXISTS(SELECT 1 FROM identity_password_resets r WHERE r.org_id=a.org_id AND r.user_id=a.user_id AND r.status='PENDING'))`, userID, s.organization, verified.CredentialVersion).Scan(&valid); err != nil {
			return CallbackResult{}, ErrUnavailable
		}
		if !valid || generation != verified.Generation {
			return CallbackResult{}, ErrUnauthenticated
		}
	}
	if oldSession != "" {
		if _, err := tx.Exec(ctx, `UPDATE portal_sessions SET revoked_at=clock_timestamp() WHERE secret_sha256=$1 AND revoked_at IS NULL`, digest(oldSession)); err != nil {
			return CallbackResult{}, ErrUnavailable
		}
		if _, err := tx.Exec(ctx, `UPDATE access_sessions SET revoked_at=clock_timestamp() WHERE portal_session_id IN (SELECT id FROM portal_sessions WHERE secret_sha256=$1) AND revoked_at IS NULL`, digest(oldSession)); err != nil {
			return CallbackResult{}, ErrUnavailable
		}
	}
	sessionID, secret, csrf := newID(), randomSecret(), randomSecret()
	encrypted, err := s.cipher.Encrypt([]byte(csrf), s.aad(sessionID, "portal-csrf"))
	if err != nil {
		return CallbackResult{}, ErrUnavailable
	}
	var expires time.Time
	err = tx.QueryRow(ctx, `INSERT INTO portal_sessions(id,secret_sha256,org_id,user_id,auth_generation,csrf_sha256,csrf_cipher,csrf_key_id,csrf_nonce,expires_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,clock_timestamp()+interval '8 hours') RETURNING expires_at`, sessionID, digest(secret), s.organization, userID, generation, digest(csrf), encrypted.Ciphertext, encrypted.KeyID, encrypted.Nonce).Scan(&expires)
	if err != nil {
		return CallbackResult{}, ErrUnavailable
	}
	if err := audit(ctx, tx, s.organization, userID, sessionID, "LOGIN"); err != nil {
		return CallbackResult{}, ErrUnavailable
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET last_login_at=clock_timestamp() WHERE id=$1`, userID); err != nil {
		return CallbackResult{}, ErrUnavailable
	}
	if err := tx.Commit(ctx); err != nil {
		return CallbackResult{}, ErrUnavailable
	}
	return CallbackResult{ReturnPath: path, SessionSecret: secret, ExpiresAt: expires}, nil
}

func (s *Service) Authenticate(ctx context.Context, secret string) (Principal, error) {
	if len(secret) != 43 {
		return Principal{}, ErrUnauthenticated
	}
	var p Principal
	var encrypted security.Envelope
	err := s.pool.QueryRow(ctx, `SELECT s.id::text,s.org_id::text,s.user_id::text,m.role,s.auth_generation,s.expires_at,s.csrf_sha256,s.csrf_cipher,s.csrf_nonce,s.csrf_key_id,
 r.id::text,r.name,public.hc_effective_permissions(s.org_id,s.user_id)
 FROM portal_sessions s JOIN users u ON u.id=s.user_id JOIN memberships m ON m.org_id=s.org_id AND m.user_id=s.user_id
 JOIN roles r ON r.org_id=m.org_id AND r.code=m.role
 JOIN organizations o ON o.id=m.org_id WHERE s.secret_sha256=$1 AND s.org_id=$2 AND s.revoked_at IS NULL
 AND s.expires_at>clock_timestamp() AND s.last_seen_at>clock_timestamp()-interval '30 minutes'
 AND u.status='ACTIVE' AND m.status='ACTIVE' AND o.status='ACTIVE' AND m.auth_generation=s.auth_generation`, digest(secret), s.organization).Scan(&p.SessionID, &p.OrganizationID, &p.UserID, &p.Role, &p.Generation, &p.ExpiresAt, &p.CSRFHash, &encrypted.Ciphertext, &encrypted.Nonce, &encrypted.KeyID, &p.RoleID, &p.RoleName, &p.Permissions)
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, ErrUnauthenticated
	}
	if err != nil {
		return Principal{}, ErrUnavailable
	}
	csrf, err := s.cipher.Decrypt(encrypted, s.aad(p.SessionID, "portal-csrf"))
	if err != nil || subtle.ConstantTimeCompare(p.CSRFHash, digest(string(csrf))) != 1 {
		return Principal{}, ErrUnavailable
	}
	p.CSRFToken = string(csrf)
	if _, err := s.pool.Exec(ctx, `UPDATE portal_sessions SET last_seen_at=clock_timestamp() WHERE id=$1 AND last_seen_at<clock_timestamp()-interval '60 seconds'
 AND last_seen_at>clock_timestamp()-interval '30 minutes' AND expires_at>clock_timestamp() AND revoked_at IS NULL`, p.SessionID); err != nil {
		return Principal{}, ErrUnavailable
	}
	return p, nil
}

func (s *Service) Logout(ctx context.Context, p Principal, csrf, origin string) error {
	if err := ValidateWrite(p, csrf, origin, s.origin); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ErrUnavailable
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(ctx, `UPDATE portal_sessions SET revoked_at=clock_timestamp() WHERE id=$1 AND revoked_at IS NULL`, p.SessionID); err != nil {
		return ErrUnavailable
	}
	if _, err := tx.Exec(ctx, `UPDATE access_sessions SET revoked_at=clock_timestamp() WHERE portal_session_id=$1 AND revoked_at IS NULL`, p.SessionID); err != nil {
		return ErrUnavailable
	}
	if err := audit(ctx, tx, s.organization, p.UserID, p.SessionID, "LOGOUT"); err != nil {
		return ErrUnavailable
	}
	if err := tx.Commit(ctx); err != nil {
		return ErrUnavailable
	}
	return nil
}

func audit(ctx context.Context, tx pgx.Tx, org, user, resource, action string) error {
	_, err := tx.Exec(ctx, `INSERT INTO audit_events(id,org_id,actor_id,owner_id,resource_id,action,outcome,trace_id) VALUES($1,$2,$3,$3,$4,$5,'SUCCEEDED',$6)`, newID(), org, user, resource, action, newID())
	return err
}
