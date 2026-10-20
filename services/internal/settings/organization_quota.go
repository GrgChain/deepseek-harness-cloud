package settings

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/userquota"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type OrganizationQuota struct {
	Version              int64            `json:"version"`
	Limits               userquota.Limits `json:"limits"`
	Usage                userquota.Limits `json:"usage"`
	DefaultUserInstances int64            `json:"default_user_instances"`
}

type OrganizationQuotaInput struct {
	ExpectedVersion *int64 `json:"expected_version"`
	Limits          *struct {
		Instances *int64 `json:"max_instances"`
		CPU       *int64 `json:"cpu_millicores"`
		Memory    *int64 `json:"memory_bytes"`
		Storage   *int64 `json:"storage_bytes"`
	} `json:"limits"`
}

func readOrganizationQuota(ctx context.Context, tx pgx.Tx, org string) (OrganizationQuota, error) {
	var quota OrganizationQuota
	err := tx.QueryRow(ctx, `SELECT version,max_instances,max_user_instances,cpu_millicores,memory_bytes,storage_bytes
      FROM quota_policies WHERE org_id=$1`, org).Scan(&quota.Version, &quota.Limits.Instances, &quota.DefaultUserInstances, &quota.Limits.CPU, &quota.Limits.Memory, &quota.Limits.Storage)
	if errors.Is(err, pgx.ErrNoRows) {
		return quota, webapi.Fail(503, "QUOTA_NOT_CONFIGURED", "组织配额尚未初始化")
	}
	if err != nil {
		return quota, err
	}
	err = tx.QueryRow(ctx, `SELECT
      COALESCE(sum(amount) FILTER(WHERE resource_type='INSTANCE'),0),
      COALESCE(sum(amount) FILTER(WHERE resource_type='CPU'),0),
      COALESCE(sum(amount) FILTER(WHERE resource_type='MEMORY'),0),
      COALESCE(sum(amount) FILTER(WHERE resource_type='STORAGE'),0)
      FROM quota_commitments WHERE org_id=$1`, org).Scan(&quota.Usage.Instances, &quota.Usage.CPU, &quota.Usage.Memory, &quota.Usage.Storage)
	return quota, err
}

func (s *Service) GetOrganizationQuota(ctx context.Context, p identity.Principal) (OrganizationQuota, error) {
	tx, err := s.begin(ctx, p, "configuration:read", false)
	if err != nil {
		return OrganizationQuota{}, err
	}
	defer tx.Rollback(context.Background())
	quota, err := readOrganizationQuota(ctx, tx, p.OrganizationID)
	if err != nil {
		return quota, err
	}
	return quota, tx.Commit(ctx)
}

func (s *Service) SaveOrganizationQuota(ctx context.Context, p identity.Principal, in OrganizationQuotaInput, trace string) (OrganizationQuota, error) {
	if in.ExpectedVersion == nil || *in.ExpectedVersion < 0 || in.Limits == nil {
		return OrganizationQuota{}, webapi.Fail(422, "ORGANIZATION_QUOTA_INVALID", "请提交完整的组织资源上限")
	}
	values := []*int64{in.Limits.Instances, in.Limits.CPU, in.Limits.Memory, in.Limits.Storage}
	for _, value := range values {
		if value == nil || *value < 1 || *value > 9007199254740991 {
			return OrganizationQuota{}, webapi.Fail(422, "ORGANIZATION_QUOTA_INVALID", "组织资源上限须为正数安全整数")
		}
	}
	if *in.Limits.Instances > 2147483647 {
		return OrganizationQuota{}, webapi.Fail(422, "ORGANIZATION_QUOTA_INVALID", "实例总上限超出允许范围")
	}
	next := userquota.Limits{Instances: *in.Limits.Instances, CPU: *in.Limits.CPU, Memory: *in.Limits.Memory, Storage: *in.Limits.Storage}
	tx, err := s.begin(ctx, p, "defaults:write", true)
	if err != nil {
		return OrganizationQuota{}, err
	}
	defer tx.Rollback(context.Background())
	var current OrganizationQuota
	err = tx.QueryRow(ctx, `SELECT version,max_instances,max_user_instances,cpu_millicores,memory_bytes,storage_bytes
      FROM quota_policies WHERE org_id=$1 FOR UPDATE`, p.OrganizationID).Scan(&current.Version, &current.Limits.Instances, &current.DefaultUserInstances, &current.Limits.CPU, &current.Limits.Memory, &current.Limits.Storage)
	if errors.Is(err, pgx.ErrNoRows) {
		return OrganizationQuota{}, webapi.Fail(503, "QUOTA_NOT_CONFIGURED", "组织配额尚未初始化")
	}
	if err != nil {
		return OrganizationQuota{}, err
	}
	if current.Version != *in.ExpectedVersion {
		return OrganizationQuota{}, versionConflict()
	}
	current, err = readOrganizationQuota(ctx, tx, p.OrganizationID)
	if err != nil {
		return OrganizationQuota{}, err
	}
	for resource, value := range next.Resources() {
		if value < current.Usage.Resources()[resource] {
			return OrganizationQuota{}, webapi.Fail(409, "QUOTA_BELOW_USAGE", "组织资源上限不能低于当前已预留或已占用资源")
		}
	}
	if next.Instances < current.DefaultUserInstances {
		return OrganizationQuota{}, webapi.Fail(409, "QUOTA_BELOW_DEFAULT_USER_LIMIT", "实例总上限不能低于默认单用户实例上限")
	}
	var exceedsUserLimit bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_quota_policies q JOIN users u ON u.id=q.owner_id
      WHERE q.org_id=$1 AND u.status<>'ARCHIVED' AND
      (q.max_instances>$2 OR q.cpu_millicores>$3 OR q.memory_bytes>$4 OR q.storage_bytes>$5))`,
		p.OrganizationID, next.Instances, next.CPU, next.Memory, next.Storage).Scan(&exceedsUserLimit); err != nil {
		return OrganizationQuota{}, err
	}
	if exceedsUserLimit {
		return OrganizationQuota{}, webapi.Fail(409, "QUOTA_BELOW_USER_LIMIT", "组织上限不能低于已分配的用户额度，请先调整用户配额")
	}
	if current.Limits == next {
		return current, tx.Commit(ctx)
	}
	_, err = tx.Exec(ctx, `UPDATE quota_policies SET max_instances=$2,cpu_millicores=$3,memory_bytes=$4,storage_bytes=$5,version=version+1 WHERE org_id=$1`, p.OrganizationID, next.Instances, next.CPU, next.Memory, next.Storage)
	if err != nil {
		return OrganizationQuota{}, err
	}
	if trace == "" {
		trace = webapi.ID()
	}
	details, err := json.Marshal(map[string]any{"before": current.Limits, "after": next, "previous_version": current.Version, "version": current.Version + 1})
	if err != nil {
		return OrganizationQuota{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events(id,org_id,actor_id,owner_id,resource_id,action,outcome,trace_id,details)
      VALUES($1,$2,$3,$3,$2,'ORGANIZATION_QUOTA_UPDATE','SUCCEEDED',$4,$5)`, webapi.ID(), p.OrganizationID, p.UserID, trace, details)
	if err != nil {
		return OrganizationQuota{}, err
	}
	quota, err := readOrganizationQuota(ctx, tx, p.OrganizationID)
	if err != nil {
		return quota, err
	}
	return quota, tx.Commit(ctx)
}
