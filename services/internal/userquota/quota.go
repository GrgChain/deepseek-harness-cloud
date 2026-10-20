// Package userquota defines per-owner limits shared by IAM, command admission
// and read-only preflight. Callers must hold the organization quota row lock
// before making admission decisions or changing limits.
package userquota

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type Limits struct {
	Instances int64 `json:"max_instances"`
	CPU       int64 `json:"cpu_millicores"`
	Memory    int64 `json:"memory_bytes"`
	Storage   int64 `json:"storage_bytes"`
}

type View struct {
	OwnerID            string `json:"owner_id"`
	Version            int64  `json:"version"`
	Configured         bool   `json:"configured"`
	Limits             Limits `json:"limits"`
	Usage              Limits `json:"usage"`
	OrganizationLimits Limits `json:"organization_limits"`
}

// Read includes outstanding reservations, retained storage and recovery holds.
func Read(ctx context.Context, tx pgx.Tx, org, owner string) (View, error) {
	v := View{OwnerID: owner}
	err := tx.QueryRow(ctx, `SELECT COALESCE(q.version,0),q.owner_id IS NOT NULL,
      COALESCE(q.max_instances,o.max_user_instances),
      COALESCE(q.cpu_millicores,o.cpu_millicores),COALESCE(q.memory_bytes,o.memory_bytes),COALESCE(q.storage_bytes,o.storage_bytes),
      o.max_instances,o.cpu_millicores,o.memory_bytes,o.storage_bytes
      FROM quota_policies o LEFT JOIN user_quota_policies q ON q.org_id=o.org_id AND q.owner_id=$2 WHERE o.org_id=$1`, org, owner).Scan(
		&v.Version, &v.Configured, &v.Limits.Instances, &v.Limits.CPU, &v.Limits.Memory, &v.Limits.Storage,
		&v.OrganizationLimits.Instances, &v.OrganizationLimits.CPU, &v.OrganizationLimits.Memory, &v.OrganizationLimits.Storage)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, webapi.Fail(503, "DEPENDENCY_UNAVAILABLE", "组织配额尚未初始化")
	}
	if err != nil {
		return v, err
	}
	err = tx.QueryRow(ctx, `SELECT
      COALESCE(sum(amount) FILTER(WHERE resource_type='INSTANCE'),0),
      COALESCE(sum(amount) FILTER(WHERE resource_type='CPU'),0),
      COALESCE(sum(amount) FILTER(WHERE resource_type='MEMORY'),0),
      COALESCE(sum(amount) FILTER(WHERE resource_type='STORAGE'),0)
      FROM quota_commitments WHERE org_id=$1 AND owner_id=$2`, org, owner).Scan(&v.Usage.Instances, &v.Usage.CPU, &v.Usage.Memory, &v.Usage.Storage)
	return v, err
}

func (l Limits) Resources() map[string]int64 {
	return map[string]int64{"INSTANCE": l.Instances, "CPU": l.CPU, "MEMORY": l.Memory, "STORAGE": l.Storage}
}

// Check uses subtraction so an overflowing addition cannot grant credit.
func (v View) Check(demands map[string]int64) error {
	used := v.Usage.Resources()
	for resource, limit := range v.Limits.Resources() {
		amount := demands[resource]
		if amount < 0 || used[resource] < 0 || limit < 0 || used[resource] > limit || amount > limit-used[resource] {
			return webapi.Fail(429, "QUOTA_EXCEEDED", "用户资源配额不足（"+resource+"），请释放资源或联系管理员调整")
		}
	}
	return nil
}
