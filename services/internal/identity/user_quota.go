package identity

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/userquota"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

// Pointers distinguish a missing field from an explicit zero allowance.
type UserQuotaInput struct {
	ExpectedVersion *int64 `json:"expected_version"`
	Limits          *struct {
		Instances *int64 `json:"max_instances"`
		CPU       *int64 `json:"cpu_millicores"`
		Memory    *int64 `json:"memory_bytes"`
		Storage   *int64 `json:"storage_bytes"`
	} `json:"limits"`
}

func quotaTarget(ctx context.Context, tx pgx.Tx, org, id string) (string, error) {
	var status string
	err := tx.QueryRow(ctx, `SELECT u.status FROM users u JOIN memberships m ON m.user_id=u.id WHERE m.org_id=$1 AND u.id=$2`, org, id).Scan(&status)
	return status, managementError(err)
}

func (s *ManagementService) GetUserQuota(ctx context.Context, p Principal, id string) (userquota.View, error) {
	if !uuidPattern.MatchString(id) {
		return userquota.View{}, managementInvalid("用户标识不合法")
	}
	tx, err := s.begin(ctx, p, "users:read")
	if err != nil {
		return userquota.View{}, err
	}
	defer tx.Rollback(context.Background())
	if _, err = quotaTarget(ctx, tx, p.OrganizationID, id); err != nil {
		return userquota.View{}, err
	}
	return userquota.Read(ctx, tx, p.OrganizationID, id)
}

func (s *ManagementService) SaveUserQuota(ctx context.Context, p Principal, id string, in UserQuotaInput, trace string) (userquota.View, error) {
	if !uuidPattern.MatchString(id) || in.ExpectedVersion == nil || *in.ExpectedVersion < 0 || in.Limits == nil {
		return userquota.View{}, managementInvalid("请提交用户、配额预期版本和完整四项额度")
	}
	for _, value := range []*int64{in.Limits.Instances, in.Limits.CPU, in.Limits.Memory, in.Limits.Storage} {
		if value == nil || *value < 0 || *value > 9007199254740991 {
			return userquota.View{}, managementInvalid("额度须为非负安全整数，四项均须提交")
		}
	}
	next := userquota.Limits{Instances: *in.Limits.Instances, CPU: *in.Limits.CPU, Memory: *in.Limits.Memory, Storage: *in.Limits.Storage}
	tx, err := s.begin(ctx, p, "users:manage")
	if err != nil {
		return userquota.View{}, err
	}
	defer tx.Rollback(context.Background())
	status, err := quotaTarget(ctx, tx, p.OrganizationID, id)
	if err != nil {
		return userquota.View{}, err
	}
	if status == "ARCHIVED" {
		return userquota.View{}, webapi.Fail(409, "STATE_CONFLICT", "已归档用户不能调整配额")
	}
	// Serialize with creation/start/retry before reading usage or the version.
	var org string
	err = tx.QueryRow(ctx, `SELECT org_id::text FROM quota_policies WHERE org_id=$1 FOR UPDATE`, p.OrganizationID).Scan(&org)
	if err == pgx.ErrNoRows {
		return userquota.View{}, webapi.Fail(503, "DEPENDENCY_UNAVAILABLE", "组织配额尚未初始化")
	}
	if err != nil {
		return userquota.View{}, err
	}
	if err = RequirePermission(ctx, tx, p, "users:manage"); err != nil {
		return userquota.View{}, err
	}
	old, err := userquota.Read(ctx, tx, p.OrganizationID, id)
	if err != nil {
		return userquota.View{}, err
	}
	if old.Version != *in.ExpectedVersion {
		return userquota.View{}, webapi.Fail(409, "VERSION_CONFLICT", "用户配额已变化，请重新读取后确认")
	}
	usage, orgLimits := old.Usage.Resources(), old.OrganizationLimits.Resources()
	for resource, value := range next.Resources() {
		if value < usage[resource] {
			return userquota.View{}, webapi.Fail(409, "QUOTA_BELOW_USAGE", "额度不能低于当前已预留或已占用资源，请先释放资源")
		}
		if value > orgLimits[resource] {
			return userquota.View{}, managementInvalid("用户额度不能高于系统总上限")
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO user_quota_policies(org_id,owner_id,version,max_instances,cpu_millicores,memory_bytes,storage_bytes)
      VALUES($1,$2,1,$3,$4,$5,$6) ON CONFLICT(org_id,owner_id) DO UPDATE
      SET version=user_quota_policies.version+1,max_instances=EXCLUDED.max_instances,
      cpu_millicores=EXCLUDED.cpu_millicores,memory_bytes=EXCLUDED.memory_bytes,storage_bytes=EXCLUDED.storage_bytes,updated_at=clock_timestamp()`,
		p.OrganizationID, id, next.Instances, next.CPU, next.Memory, next.Storage)
	if err != nil {
		return userquota.View{}, err
	}
	if trace == "" {
		trace = webapi.ID()
	}
	details, err := json.Marshal(map[string]any{"before": old.Limits, "after": next, "previous_version": old.Version, "version": old.Version + 1, "previously_configured": old.Configured})
	if err != nil {
		return userquota.View{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events(id,org_id,actor_id,owner_id,resource_id,action,outcome,trace_id,details)
      VALUES($1,$2,$3,$4,$4,'USER_QUOTA_UPDATE','SUCCEEDED',$5,$6)`, webapi.ID(), p.OrganizationID, p.UserID, id, trace, details)
	if err != nil {
		return userquota.View{}, err
	}
	old.Configured, old.Version, old.Limits = true, old.Version+1, next
	return old, tx.Commit(ctx)
}
