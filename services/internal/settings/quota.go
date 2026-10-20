package settings

import (
	"context"
	"errors"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/userquota"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/instances"
)

type ResourceQuota struct {
	Available                  userquota.Limits `json:"available"`
	Required                   userquota.Limits `json:"required"`
	ConcurrentCreatesAvailable int64            `json:"concurrent_creates_available"`
}

// A read-only preview. Command acceptance still locks the quota policy and
// reserves the same resources atomically; this observation never grants credit.
func (s *Service) quotaPreview(ctx context.Context, p identity.Principal, spec Spec) (*ResourceQuota, string, error) {
	tx, err := s.begin(ctx, p, "configuration:read", false)
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback(context.Background())
	var policy instances.QuotaPolicy
	err = tx.QueryRow(ctx, `SELECT max_instances,max_user_instances,cpu_millicores,memory_bytes,storage_bytes,max_concurrent_creates FROM quota_policies WHERE org_id=$1`, p.OrganizationID).Scan(&policy.Instances, &policy.UserInstances, &policy.CPU, &policy.Memory, &policy.Storage, &policy.Concurrent)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "QUOTA_NOT_CONFIGURED", nil
	}
	if err != nil {
		return nil, "", err
	}
	var usage instances.QuotaUsage
	err = tx.QueryRow(ctx, `SELECT COALESCE(sum(amount) FILTER(WHERE resource_type='INSTANCE'),0),COALESCE(sum(amount) FILTER(WHERE resource_type='INSTANCE' AND owner_id=$2),0),COALESCE(sum(amount) FILTER(WHERE resource_type='RUNNING'),0),COALESCE(sum(amount) FILTER(WHERE resource_type='CPU'),0),COALESCE(sum(amount) FILTER(WHERE resource_type='MEMORY'),0),COALESCE(sum(amount) FILTER(WHERE resource_type='STORAGE'),0),COALESCE(sum(amount) FILTER(WHERE resource_type='CONCURRENT'),0) FROM quota_commitments WHERE org_id=$1`, p.OrganizationID, p.UserID).Scan(&usage.Instances, &usage.UserInstances, &usage.Running, &usage.CPU, &usage.Memory, &usage.Storage, &usage.Concurrent)
	if err != nil {
		return nil, "", err
	}
	personal, err := userquota.Read(ctx, tx, p.OrganizationID, p.UserID)
	if err != nil {
		return nil, "", err
	}
	policy.UserInstances = personal.Limits.Instances
	demands, err := policy.PlanReservation(true, usage, instances.Resources{CPU: spec.CPU, Memory: spec.Memory, Storage: spec.Storage})
	reason := ""
	if err != nil || personal.Check(demands) != nil {
		reason = "QUOTA_EXCEEDED"
	}
	remaining := func(limit, used int64) int64 { return max(0, limit-used) }
	preview := &ResourceQuota{
		Available: userquota.Limits{
			Instances: min(remaining(policy.Instances, usage.Instances), remaining(personal.Limits.Instances, personal.Usage.Instances)),
			CPU:       min(remaining(policy.CPU, usage.CPU), remaining(personal.Limits.CPU, personal.Usage.CPU)),
			Memory:    min(remaining(policy.Memory, usage.Memory), remaining(personal.Limits.Memory, personal.Usage.Memory)),
			Storage:   min(remaining(policy.Storage, usage.Storage), remaining(personal.Limits.Storage, personal.Usage.Storage)),
		},
		Required:                   userquota.Limits{Instances: 1, CPU: spec.CPU, Memory: spec.Memory, Storage: spec.Storage},
		ConcurrentCreatesAvailable: remaining(policy.Concurrent, usage.Concurrent),
	}
	return preview, reason, tx.Commit(ctx)
}
