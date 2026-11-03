package identity

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

const selectUser = `SELECT u.id::text,u.display_name,u.email,COALESCE(op.login_email,''),u.identity_source,u.identity_issuer,u.identity_subject,u.workspace_owner_id::text,
 u.status,r.id::text,r.code,r.name,u.version,u.created_at,u.updated_at,u.last_login_at,
 op.id::text,op.status,CASE WHEN op.status='PENDING' AND EXISTS(
   SELECT 1 FROM identity_accounts a WHERE a.org_id=m.org_id AND a.login_email=lower(op.login_email) AND a.user_id IS DISTINCT FROM u.id
 ) THEN 'LOGIN_EMAIL_IN_USE' ELSE op.reason_code END,reset.id::text,reset.status,reset.reason_code FROM users u JOIN memberships m ON m.user_id=u.id
 JOIN roles r ON r.org_id=m.org_id AND r.code=m.role LEFT JOIN identity_provisioning_operations op ON op.user_id=u.id AND op.org_id=m.org_id
 LEFT JOIN LATERAL (SELECT id,status,reason_code FROM identity_password_resets WHERE org_id=m.org_id AND user_id=u.id ORDER BY created_at DESC,id DESC LIMIT 1) reset ON true`

func scanManagedUser(row pgx.Row) (ManagedUser, error) {
	var u ManagedUser
	var op, status, reason *string
	var reset, resetStatus, resetReason *string
	err := row.Scan(&u.ID, &u.DisplayName, &u.Email, &u.LoginEmail, &u.IdentitySource, &u.Issuer, &u.Subject, &u.WorkspaceOwnerID, &u.Status, &u.RoleID, &u.RoleCode, &u.RoleName, &u.Version, &u.CreatedAt, &u.UpdatedAt, &u.LastLoginAt, &op, &status, &reason, &reset, &resetStatus, &resetReason)
	if op != nil && status != nil && reason != nil {
		u.Provisioning = &Provisioning{*op, *status, *reason}
	}
	if reset != nil && resetStatus != nil && resetReason != nil {
		u.PasswordReset = &Provisioning{*reset, *resetStatus, *resetReason}
	}
	return u, managementError(err)
}

func (s *ManagementService) GetUser(ctx context.Context, p Principal, id string) (ManagedUser, error) {
	if !uuidPattern.MatchString(id) {
		return ManagedUser{}, managementInvalid("用户标识不合法")
	}
	tx, err := s.begin(ctx, p, "users:read")
	if err != nil {
		return ManagedUser{}, err
	}
	defer tx.Rollback(context.Background())
	return scanManagedUser(tx.QueryRow(ctx, selectUser+` WHERE m.org_id=$1 AND u.id=$2`, p.OrganizationID, id))
}

func (s *ManagementService) CreateUser(ctx context.Context, p Principal, key string, in UserCreateInput, trace string) (ManagedUser, error) {
	if err := validateUserProfile(in.DisplayName, in.Email); err != nil {
		return ManagedUser{}, err
	}
	if !managementKeyPattern.MatchString(key) || !uuidPattern.MatchString(in.RoleID) || (in.IdentitySource != "oidc" && in.IdentitySource != "local") {
		return ManagedUser{}, managementInvalid("身份来源、角色或请求标识不合法")
	}
	if s.local != nil && in.IdentitySource != "local" {
		return ManagedUser{}, managementInvalid("内置身份服务使用平台登录账号，请提供初始密码")
	}
	id := newID()
	status := "ACTIVE"
	subject := in.Subject
	if in.IdentitySource == "local" {
		if s.local == nil {
			return ManagedUser{}, webapi.Fail(409, "LOCAL_IDENTITY_UNAVAILABLE", "未配置本地账号供应服务，请检查平台身份配置")
		}
		if in.Subject != "" {
			return ManagedUser{}, managementInvalid("本地账号不接受指定OIDC主体")
		}
		if err := validateLocalPassword(in.InitialPassword); err != nil {
			return ManagedUser{}, err
		}
		status, subject = "PENDING_IDENTITY", s.local.Subject(id)
	} else if !validLabel(subject, 1024) || in.InitialPassword != "" {
		return ManagedUser{}, managementInvalid("绑定已有账号需要可信subject，不能提交密码")
	}
	// Passwords are neither persisted nor included in request/audit hashes.
	safe := in
	safe.InitialPassword = ""
	data, _ := json.Marshal(safe)
	payload := map[string]any{"display_name": in.DisplayName, "email": in.Email, "identity_source": in.IdentitySource, "issuer": s.issuer, "subject": subject, "role_id": in.RoleID, "workspace_owner_id": newID(), "status": status, "operation_id": newID(), "provider_account_id": id}
	result, err := s.change(ctx, p, "USER_CREATE", id, 0, payload, key, digest(string(data)), trace)
	if err != nil {
		return ManagedUser{}, err
	}
	u, err := s.GetUser(ctx, p, result)
	if err != nil || u.Status != "PENDING_IDENTITY" {
		return u, err
	}
	return s.supply(ctx, p, u, in.InitialPassword, trace)
}

func (s *ManagementService) supply(ctx context.Context, p Principal, u ManagedUser, password, trace string) (ManagedUser, error) {
	if s.local == nil {
		return u, webapi.Fail(409, "LOCAL_IDENTITY_UNAVAILABLE", "本地身份供应服务尚未配置")
	}
	var accountID, loginEmail string
	// Short authorization transaction is closed before the credential transaction.
	tx, err := s.begin(ctx, p, "users:manage")
	if err != nil {
		return u, err
	}
	err = tx.QueryRow(ctx, `SELECT provider_account_id,login_email FROM identity_provisioning_operations WHERE org_id=$1 AND user_id=$2 AND status='PENDING'`, p.OrganizationID, u.ID).Scan(&accountID, &loginEmail)
	_ = tx.Rollback(context.Background())
	if err != nil {
		return u, managementError(err)
	}
	supplyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err = s.local.Ensure(supplyCtx, accountID, loginEmail, u.DisplayName, password); err != nil {
		if _, recordErr := s.change(ctx, p, "USER_IDENTITY_FAILURE", u.ID, u.Version, map[string]any{}, "", nil, trace); recordErr != nil {
			return u, recordErr
		}
		return s.GetUser(ctx, p, u.ID)
	}
	if _, err = s.change(ctx, p, "USER_IDENTITY_CONFIRM", u.ID, u.Version, map[string]any{}, "", nil, trace); err != nil {
		return u, err
	}
	return s.GetUser(ctx, p, u.ID)
}

func (s *ManagementService) EditUser(ctx context.Context, p Principal, id string, in UserEditInput, trace string) (ManagedUser, error) {
	if err := validateUserProfile(in.DisplayName, in.Email); err != nil {
		return ManagedUser{}, err
	}
	if !uuidPattern.MatchString(id) || in.ExpectedVersion < 1 {
		return ManagedUser{}, managementInvalid("用户标识或版本不合法")
	}
	if _, err := s.change(ctx, p, "USER_UPDATE", id, in.ExpectedVersion, in, "", nil, trace); err != nil {
		return ManagedUser{}, err
	}
	return s.GetUser(ctx, p, id)
}

// DeleteUser retains the immutable identity and audit history. The existing
// archive transaction checks assets, administrators and pending identity work,
// revokes sessions, and commits the status change together with its audit.
func (s *ManagementService) DeleteUser(ctx context.Context, p Principal, id string, in UserDeleteInput, trace string) error {
	if !uuidPattern.MatchString(id) || in.ExpectedVersion < 1 {
		return managementInvalid("用户标识或版本不合法")
	}
	u, err := s.GetUser(ctx, p, id)
	if err != nil {
		return err
	}
	if u.Version != in.ExpectedVersion {
		return webapi.Fail(409, "VERSION_CONFLICT", "用户已变化，请重新读取并确认")
	}
	name := u.DisplayName
	if name == "" {
		name = u.ID
	}
	if in.ConfirmedName != name {
		return managementInvalid("请输入完整用户名以确认删除")
	}
	_, err = s.change(ctx, p, "USER_ARCHIVE", id, in.ExpectedVersion, map[string]any{}, "", nil, trace)
	return err
}

func (s *ManagementService) UserAction(ctx context.Context, p Principal, id, action string, in UserActionInput, trace string) (ManagedUser, error) {
	if !uuidPattern.MatchString(id) || in.ExpectedVersion < 1 {
		return ManagedUser{}, managementInvalid("用户标识或版本不合法")
	}
	if action == "USER_ROLE" {
		if !uuidPattern.MatchString(in.RoleID) || in.Password != "" {
			return ManagedUser{}, managementInvalid("角色分配参数不合法")
		}
	} else if in.RoleID != "" {
		return ManagedUser{}, managementInvalid("此动作不接受角色字段")
	}
	if action == "USER_PASSWORD_RESET_RETRY" {
		if in.Password != "" {
			return ManagedUser{}, managementInvalid("重试原密码重置不接受新密码")
		}
		return s.retryPasswordReset(ctx, p, id, in.ExpectedVersion, trace)
	}
	if action == "USER_IDENTITY_RETRY" || action == "USER_PASSWORD_RESET" {
		if err := validateLocalPassword(in.Password); err != nil {
			return ManagedUser{}, err
		}
		u, err := s.GetUser(ctx, p, id)
		if err != nil {
			return u, err
		}
		if u.Version != in.ExpectedVersion {
			return u, webapi.Fail(409, "VERSION_CONFLICT", "用户已变化，请重新读取")
		}
		if u.IdentitySource != "local" || s.local == nil {
			return u, webapi.Fail(409, "LOCAL_IDENTITY_UNAVAILABLE", "此账号的密码由组织身份提供方维护")
		}
		if action == "USER_IDENTITY_RETRY" {
			if u.Status != "PENDING_IDENTITY" {
				return u, webapi.Fail(409, "STATE_CONFLICT", "账号无需重试身份供应")
			}
			return s.supply(ctx, p, u, in.Password, trace)
		}
		if u.Status != "ACTIVE" && u.Status != "DISABLED" {
			return u, webapi.Fail(409, "STATE_CONFLICT", "当前状态不能重置密码")
		}
		return s.resetPassword(ctx, p, u, in.Password, trace)
	} else if in.Password != "" {
		return ManagedUser{}, managementInvalid("此动作不接受密码字段")
	}
	payload := map[string]any{}
	if action == "USER_ROLE" {
		payload["role_id"] = in.RoleID
	}
	if _, err := s.change(ctx, p, action, id, in.ExpectedVersion, payload, "", nil, trace); err != nil {
		return ManagedUser{}, err
	}
	return s.GetUser(ctx, p, id)
}
