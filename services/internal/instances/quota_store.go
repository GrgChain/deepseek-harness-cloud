package instances

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/userquota"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func lockedQuota(ctx context.Context, tx pgx.Tx, org, owner string) (QuotaPolicy, QuotaUsage, error) {
	var policy QuotaPolicy
	err := tx.QueryRow(ctx, `SELECT max_instances,max_user_instances,cpu_millicores,memory_bytes,storage_bytes,max_concurrent_creates FROM quota_policies WHERE org_id=$1 FOR UPDATE`, org).Scan(&policy.Instances, &policy.UserInstances, &policy.CPU, &policy.Memory, &policy.Storage, &policy.Concurrent)
	if errors.Is(err, pgx.ErrNoRows) {
		return policy, QuotaUsage{}, webapi.Fail(503, "DEPENDENCY_UNAVAILABLE", "组织配额尚未初始化")
	}
	if err != nil {
		return policy, QuotaUsage{}, err
	}
	var usage QuotaUsage
	err = tx.QueryRow(ctx, `SELECT COALESCE(sum(amount) FILTER(WHERE resource_type='INSTANCE'),0),COALESCE(sum(amount) FILTER(WHERE resource_type='INSTANCE' AND owner_id=$2),0),COALESCE(sum(amount) FILTER(WHERE resource_type='RUNNING'),0),COALESCE(sum(amount) FILTER(WHERE resource_type='CPU'),0),COALESCE(sum(amount) FILTER(WHERE resource_type='MEMORY'),0),COALESCE(sum(amount) FILTER(WHERE resource_type='STORAGE'),0),COALESCE(sum(amount) FILTER(WHERE resource_type='CONCURRENT'),0) FROM quota_commitments WHERE org_id=$1`, org, owner).Scan(&usage.Instances, &usage.UserInstances, &usage.Running, &usage.CPU, &usage.Memory, &usage.Storage, &usage.Concurrent)
	if err != nil {
		return policy, QuotaUsage{}, err
	}
	personal, err := userquota.Read(ctx, tx, org, owner)
	if err != nil {
		return policy, usage, err
	}
	policy.UserInstances = personal.Limits.Instances
	return policy, usage, nil
}

func checkUserQuota(ctx context.Context, tx pgx.Tx, org, owner string, demands map[string]int64) error {
	personal, err := userquota.Read(ctx, tx, org, owner)
	if err != nil {
		return err
	}
	return personal.Check(demands)
}
