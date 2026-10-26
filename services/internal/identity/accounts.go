package identity

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

var errLocalProvider = errors.New("platform identity account unavailable or account mismatch")

// Accounts stores credentials in the platform database. Creation is insert-only:
// retrying an accepted provisioning operation cannot replace its first password.
type Accounts struct {
	pool         *pgxpool.Pool
	organization string
}

func NewAccounts(pool *pgxpool.Pool, organization string) *Accounts {
	return &Accounts{pool: pool, organization: organization}
}
func (p *Accounts) Subject(id string) string { return id }
func (p *Accounts) Ensure(ctx context.Context, id, email, name, password string) error {
	if !uuidPattern.MatchString(id) || validateUserProfile(name, email) != nil || validateLocalPassword(password) != nil {
		return errLocalProvider
	}
	// A completed provisioning replay must not re-enter the pending-only lock or
	// reset a password. Match the original account, organization and login email.
	var complete bool
	if err := p.pool.QueryRow(ctx, `SELECT EXISTS(
 SELECT 1 FROM identity_accounts a JOIN identity_provisioning_operations op
 ON op.org_id=a.org_id AND op.user_id=a.user_id JOIN users u ON u.id=a.user_id
 WHERE a.id=$1 AND a.org_id=$2 AND lower(a.login_email)=lower($3)
 AND op.provider_account_id=$1::text AND lower(op.login_email)=lower($3)
 AND op.status='SUCCEEDED' AND u.identity_source='local'
 AND u.identity_subject=$1::text AND u.status IN ('ACTIVE','DISABLED'))`, id, p.organization, email).Scan(&complete); err != nil {
		return errors.Join(errLocalProvider, err)
	}
	if complete {
		return nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return errLocalProvider
	}
	defer clear(hash)
	email = strings.ToLower(email)
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return errors.Join(errLocalProvider, err)
	}
	defer tx.Rollback(context.Background())
	var providerID, loginEmail string
	err = tx.QueryRow(ctx, `SELECT provider_account_id,login_email
 FROM public.hc_identity_lock_provisioning($1::uuid,$2::uuid)`, p.organization, id).Scan(&providerID, &loginEmail)
	if err != nil || providerID != id || !strings.EqualFold(loginEmail, email) {
		return errors.Join(errLocalProvider, err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO identity_accounts(id,org_id,user_id,login_email,display_name,password_hash)
 SELECT $1::uuid,$2::uuid,u.id,$3::text,$4::text,$5::bytea FROM users u JOIN identity_provisioning_operations op ON op.user_id=u.id AND op.org_id=$2
 WHERE op.provider_account_id=$1::text AND lower(op.login_email)=$3 AND op.status='PENDING'
 AND u.identity_source='local' AND u.identity_subject=$1::text AND u.status='PENDING_IDENTITY'
	ON CONFLICT(id) DO NOTHING`, id, p.organization, email, name, hash)
	if err != nil {
		return errors.Join(errLocalProvider, err)
	}
	var matches bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM identity_accounts a JOIN identity_provisioning_operations op ON op.user_id=a.user_id AND op.org_id=a.org_id
 WHERE a.id=$1 AND a.org_id=$2 AND a.login_email=$3 AND op.provider_account_id=$1::text AND lower(op.login_email)=$3)`, id, p.organization, email).Scan(&matches)
	if err != nil || !matches {
		return errors.Join(errLocalProvider, err)
	}
	if err = tx.Commit(ctx); err != nil {
		return errors.Join(errLocalProvider, err)
	}
	return nil
}
func (p *Accounts) ResetHash(ctx context.Context, id, email string, hash []byte) error {
	cost, err := bcrypt.Cost(hash)
	if !uuidPattern.MatchString(id) || err != nil || cost != 12 || len(hash) != 60 {
		return errLocalProvider
	}
	result, err := p.pool.Exec(ctx, `UPDATE identity_accounts a SET password_hash=$4,
 credential_version=credential_version+CASE WHEN password_hash=$4 THEN 0 ELSE 1 END,updated_at=clock_timestamp()
 FROM identity_provisioning_operations op JOIN users u ON u.id=op.user_id
 WHERE a.id=$1 AND a.org_id=$2 AND a.login_email=$3 AND a.user_id=op.user_id AND op.org_id=a.org_id
 AND op.provider_account_id=$1::text AND lower(op.login_email)=$3 AND op.status='SUCCEEDED' AND u.identity_source='local'
 AND u.status IN ('ACTIVE','DISABLED') AND EXISTS(SELECT 1 FROM identity_password_resets r
 WHERE r.org_id=a.org_id AND r.user_id=a.user_id AND r.status='PENDING' AND r.lease_expires_at>clock_timestamp())`, id, p.organization, strings.ToLower(email), hash)
	if err != nil || result.RowsAffected() != 1 {
		return errLocalProvider
	}
	return nil
}
