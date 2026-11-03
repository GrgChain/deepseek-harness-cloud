package identity

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/pagination"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type ManagementService struct {
	pool        *pgxpool.Pool
	cursor      *pagination.Codec
	issuer      string
	local       LocalAccountProvider
	cipher      *security.SecretCipher
	environment string
}

func NewManagementService(pool *pgxpool.Pool, cursor *pagination.Codec, issuer string, local LocalAccountProvider, cipher *security.SecretCipher, environment string) *ManagementService {
	return &ManagementService{pool: pool, cursor: cursor, issuer: issuer, local: local, cipher: cipher, environment: environment}
}
func managementError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return webapi.Fail(404, "NOT_FOUND", "用户或角色不存在")
	}
	var database *pgconn.PgError
	if !errors.As(err, &database) {
		return err
	}
	switch database.Message {
	case "IAM_UNAUTHENTICATED":
		return ErrUnauthenticated
	case "IAM_FORBIDDEN":
		return ErrForbidden
	case "IAM_NOT_FOUND":
		return webapi.Fail(404, "NOT_FOUND", "用户或角色不存在")
	case "IAM_VERSION_CONFLICT":
		return webapi.Fail(409, "VERSION_CONFLICT", "用户或角色已变化，请重新读取后确认")
	case "IAM_IDEMPOTENCY_CONFLICT":
		return webapi.Fail(409, "IDEMPOTENCY_CONFLICT", "相同请求标识不能用于不同参数")
	case "IAM_BUILTIN_ROLE":
		return webapi.Fail(409, "BUILTIN_ROLE", "内置角色不能修改或删除")
	case "IAM_ROLE_IN_USE":
		return webapi.Fail(409, "ROLE_IN_USE", "角色仍有分配用户，请先改派角色")
	case "IAM_USER_HAS_ASSETS":
		return webapi.Fail(409, "USER_HAS_ASSETS", "用户仍有实例、保留数据或恢复占用，请先禁用并处理资产")
	case "IAM_LAST_ADMIN":
		return webapi.Fail(409, "LAST_ADMIN_REQUIRED", "必须保留至少一名有效管理员")
	case "IAM_SELF_CHANGE":
		return webapi.Fail(409, "SELF_CHANGE_FORBIDDEN", "不能修改自己的角色、禁用或归档自己")
	case "IAM_STATE_CONFLICT":
		return webapi.Fail(409, "STATE_CONFLICT", "当前用户状态不允许此操作")
	case "IAM_IDENTITY_ALREADY_SUPPLIED":
		return webapi.Fail(409, "IDENTITY_ALREADY_SUPPLIED", "登录账号已创建，不能再修改登录邮箱；请重试身份确认")
	case "IAM_IDENTITY_NOT_PENDING":
		return webapi.Fail(409, "STATE_CONFLICT", "身份账号状态已变化，请重新读取用户记录")
	case "IAM_PASSWORD_RESET_PENDING":
		return webapi.Fail(409, "PASSWORD_RESET_PENDING", "已有密码重置待确认，请查询并重试原操作")
	case "IAM_INVALID_PERMISSIONS":
		return managementInvalid("所选权限与数据库权限目录不一致，或权限依赖不完整")
	case "IAM_INVALID", "IAM_INVALID_ROLE", "IAM_IMMUTABLE_ROLE_CODE":
		return managementInvalid("角色、权限、身份状态或不可变字段不合法")
	}
	if database.Code == "23505" {
		return webapi.Fail(409, "IDENTITY_CONFLICT", "身份、角色编码或请求记录已存在")
	}
	if database.Code == "23503" || database.Code == "23514" || database.Code == "23502" {
		return managementInvalid("身份或角色数据不符合约束")
	}
	return err
}
func (s *ManagementService) begin(ctx context.Context, p Principal, permission string) (pgx.Tx, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if err = RequirePermission(ctx, tx, p, permission); err != nil {
		_ = tx.Rollback(context.Background())
		return nil, err
	}
	return tx, nil
}
func (s *ManagementService) change(ctx context.Context, p Principal, action, id string, version int64, payload any, key string, fingerprint []byte, trace string) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	var keyHash []byte
	if key != "" {
		keyHash = digest(key)
	}
	var result string
	err = s.pool.QueryRow(ctx, `SELECT public.hc_iam_change($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)::text`, p.OrganizationID, p.UserID, p.SessionID, p.Generation, action, id, version, data, keyHash, fingerprint, trace).Scan(&result)
	return result, managementError(err)
}
