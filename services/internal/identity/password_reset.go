package identity

import (
	"context"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	"golang.org/x/crypto/bcrypt"
)

func (s *ManagementService) resetAAD(org, user, operation string) security.Context {
	return security.Context{Environment: s.environment, OrganizationID: org, RecordID: user + "/" + operation, Purpose: "identity-password-hash"}
}

func (s *ManagementService) resetChange(ctx context.Context, p Principal, user string, version int64, operation, action string, token int64, envelope security.Envelope, trace string) (int64, error) {
	var result int64
	err := s.pool.QueryRow(ctx, `SELECT public.hc_iam_password_reset($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, p.OrganizationID, p.UserID, p.SessionID, p.Generation, action, user, version, operation, token, envelope.KeyID, envelope.Nonce, envelope.Ciphertext, trace).Scan(&result)
	return result, managementError(err)
}

func (s *ManagementService) resetPassword(ctx context.Context, p Principal, user ManagedUser, password, trace string) (ManagedUser, error) {
	if user.PasswordReset != nil && user.PasswordReset.Status == "PENDING" {
		return user, webapi.Fail(409, "PASSWORD_RESET_PENDING", "已有密码重置待确认，请重试原操作")
	}
	if s.cipher == nil {
		return user, ErrUnavailable
	}
	plain := []byte(password)
	hash, err := bcrypt.GenerateFromPassword(plain, 12)
	clear(plain)
	if err != nil {
		return user, ErrUnavailable
	}
	operation := newID()
	envelope, err := s.cipher.Encrypt(hash, s.resetAAD(p.OrganizationID, user.ID, operation))
	clear(hash)
	if err != nil {
		return user, err
	}
	token, err := s.resetChange(ctx, p, user.ID, user.Version, operation, "BEGIN", 0, envelope, trace)
	if err != nil {
		return user, err
	}
	return s.executePasswordReset(ctx, p, user.ID, operation, token, trace)
}

func (s *ManagementService) retryPasswordReset(ctx context.Context, p Principal, id string, version int64, trace string) (ManagedUser, error) {
	user, err := s.GetUser(ctx, p, id)
	if err != nil {
		return user, err
	}
	if s.local == nil || s.cipher == nil {
		return user, webapi.Fail(409, "LOCAL_IDENTITY_UNAVAILABLE", "本地身份供应服务尚未配置")
	}
	if user.PasswordReset == nil || user.PasswordReset.Status != "PENDING" {
		return user, webapi.Fail(409, "STATE_CONFLICT", "没有待确认的密码重置")
	}
	token, err := s.resetChange(ctx, p, id, version, user.PasswordReset.ID, "RETRY", 0, security.Envelope{}, trace)
	if err != nil {
		return user, err
	}
	return s.executePasswordReset(ctx, p, id, user.PasswordReset.ID, token, trace)
}

func (s *ManagementService) executePasswordReset(ctx context.Context, p Principal, user, operation string, token int64, trace string) (ManagedUser, error) {
	var envelope security.Envelope
	var account, email string
	err := s.pool.QueryRow(ctx, `SELECT r.key_id,r.nonce,r.ciphertext,p.provider_account_id,p.login_email FROM identity_password_resets r JOIN identity_provisioning_operations p ON p.org_id=r.org_id AND p.user_id=r.user_id WHERE r.org_id=$1 AND r.user_id=$2 AND r.id=$3 AND r.lease_token=$4 AND r.status='PENDING' AND r.lease_expires_at>clock_timestamp() AND p.status='SUCCEEDED'`, p.OrganizationID, user, operation, token).Scan(&envelope.KeyID, &envelope.Nonce, &envelope.Ciphertext, &account, &email)
	if err != nil {
		return ManagedUser{}, managementError(err)
	}
	hash, err := s.cipher.Decrypt(envelope, s.resetAAD(p.OrganizationID, user, operation))
	if err != nil {
		return ManagedUser{}, err
	}
	// No platform transaction remains open during provider I/O. Replays send
	// the same hash, and require the provider's update acknowledgement.
	provider, cancel := context.WithTimeout(ctx, 5*time.Second)
	err = s.local.ResetHash(provider, account, email, hash)
	cancel()
	clear(hash)
	action := "CONFIRM"
	if err != nil {
		action = "UNCERTAIN"
	}
	if _, err = s.resetChange(ctx, p, user, 0, operation, action, token, security.Envelope{}, trace); err != nil {
		return ManagedUser{}, err
	}
	return s.GetUser(ctx, p, user)
}
