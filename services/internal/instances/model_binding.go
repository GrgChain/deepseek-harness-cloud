package instances

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

// Organization authority is already locked. Lock a private connection before
// quota/instance rows so revoke and accepted commands use the same order.
func bindModel(ctx context.Context, tx pgx.Tx, p identity.Principal, snapshot *Snapshot, id string, version int64) error {
	if !keyPattern.MatchString(id) {
		return webapi.Fail(422, "CONFIGURATION_INVALID", "请选择可用模型")
	}
	var owner *string
	err := tx.QueryRow(ctx, `SELECT owner_id::text FROM model_connection_registry WHERE org_id=$1 AND id=$2`, p.OrganizationID, id).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && owner != nil && *owner != p.UserID {
		return webapi.Fail(404, "NOT_FOUND", "模型连接不存在")
	}
	if err != nil {
		return err
	}
	if owner != nil {
		var current int64
		err = tx.QueryRow(ctx, `SELECT current_version FROM user_model_connections WHERE org_id=$1 AND owner_id=$2 AND id=$3 AND enabled AND archived_at IS NULL FOR SHARE`, p.OrganizationID, p.UserID, id).Scan(&current)
		if errors.Is(err, pgx.ErrNoRows) {
			return webapi.Fail(422, "CONFIGURATION_INVALID", "个人模型不可用")
		}
		if err != nil {
			return err
		}
		if version == 0 {
			version = current
		}
	}
	if version < 1 {
		return webapi.Fail(422, "CONFIGURATION_INVALID", "请选择模型版本")
	}
	err = tx.QueryRow(ctx, `SELECT c.provider,c.protocol,c.model,COALESCE(c.manifest->>'digest','') FROM model_configurations c JOIN secrets s ON s.org_id=c.org_id AND s.id=c.credential_ref JOIN model_validations v ON v.config_id=c.id AND v.config_version=c.version WHERE c.org_id=$1 AND c.id=$2 AND c.version=$3 AND public.hc_model_available($1,$4,c.id,c.version) AND c.status='VALID' AND c.revoked_at IS NULL AND s.revoked_at IS NULL AND v.status='HEALTHY' AND v.checked_at<=clock_timestamp() AND v.expires_at>clock_timestamp()`, p.OrganizationID, id, version, p.UserID).Scan(&snapshot.Provider, &snapshot.Protocol, &snapshot.Model, &snapshot.ApprovalDigest)
	if errors.Is(err, pgx.ErrNoRows) {
		return webapi.Fail(422, "CONFIGURATION_INVALID", "模型尚未同步、校验已过期或已撤销")
	}
	if err != nil {
		return err
	}
	snapshot.ModelID = id
	snapshot.ModelVersion = version
	return nil
}
