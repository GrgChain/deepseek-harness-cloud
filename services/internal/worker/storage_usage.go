package worker

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/instances"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

// Only statfs counters are collected. Never walk user files or start stopped compute.
const storageStatProgram = `const s=require('node:fs').statfsSync('/data',{bigint:true});console.log(JSON.stringify({used_bytes:Number((s.blocks-s.bfree)*s.bsize),available_bytes:Number(s.bavail*s.bsize)}))`
const StorageUsageInterval = time.Minute
const StorageUsageTimeout = 5 * time.Second

type StorageUsage struct {
	Used      int64 `json:"used_bytes"`
	Available int64 `json:"available_bytes"`
}

func decodeStorageUsage(raw []byte, capacity int64) (StorageUsage, error) {
	var counters struct {
		Used      *int64 `json:"used_bytes"`
		Available *int64 `json:"available_bytes"`
	}
	if webapi.StrictUnmarshal(raw, &counters) != nil || counters.Used == nil || counters.Available == nil {
		return StorageUsage{}, ErrInvalidEvidence
	}
	result := StorageUsage{*counters.Used, *counters.Available}
	if result.Used < 0 || result.Available < 0 || result.Used > capacity || result.Available > capacity-result.Used || result.Used+result.Available == 0 {
		return result, ErrInvalidEvidence
	}
	return result, nil
}
func (h *LocalHost) StorageUsage(ctx context.Context, p ExecutionPlan) (StorageUsage, error) {
	var result StorageUsage
	if !h.supportsRuntimeImage(p.Snapshot.ImageDigest) {
		return result, ErrHostIdentity
	}
	c, err := h.container(ctx, p.InstanceID)
	if err != nil {
		return result, err
	}
	if c == nil || !c.State.Running {
		return result, ErrStorageUnavailable
	}
	if err = validateContainer(*c, p, h.options.RuntimeRoot, false); err != nil {
		return result, err
	}
	if err = h.InspectAsset(ctx, p); err != nil {
		return result, err
	}
	raw, err := dockerCommand(ctx, nil, "exec", "--user", "1000:1000", "hc-"+p.InstanceID, "/usr/local/bin/node", "-e", storageStatProgram)
	if err != nil {
		return result, err
	}
	return decodeStorageUsage(raw, p.Capacity)
}

type storageUsageHost interface {
	Acquire(context.Context, string) (func(), error)
	StorageUsage(context.Context, ExecutionPlan) (StorageUsage, error)
}

type StorageUsageCollector struct {
	pool *pgxpool.Pool
	host storageUsageHost
}

func NewStorageUsageCollector(pool *pgxpool.Pool, host storageUsageHost) *StorageUsageCollector {
	return &StorageUsageCollector{pool, host}
}
func (c *StorageUsageCollector) collect(ctx context.Context, id string) {
	probe, cancel := context.WithTimeout(ctx, StorageUsageTimeout)
	defer cancel()
	unlock, err := c.host.Acquire(probe, id)
	if err != nil {
		return
	}
	defer unlock()
	var p ExecutionPlan
	var raw []byte
	err = c.pool.QueryRow(probe, `SELECT i.id::text,i.org_id::text,i.owner_id::text,i.version,i.workspace_id::text,i.config_snapshot,a.id::text,a.resource_name,a.capacity_bytes,a.filesystem_id,u.workspace_owner_id::text FROM instances i JOIN storage_assets a ON a.id=i.storage_asset_id AND a.instance_id=i.id JOIN users u ON u.id=i.owner_id JOIN memberships m ON m.org_id=i.org_id AND m.user_id=i.owner_id JOIN organizations org ON org.id=i.org_id WHERE i.id=$1 AND i.observed_state='RUNNING' AND i.desired_state='RUNNING' AND i.deleted_at IS NULL AND a.status='READY' AND a.init_version>=1 AND a.filesystem_id=a.id::text AND org.status='ACTIVE' AND u.status='ACTIVE' AND m.status='ACTIVE' AND NOT EXISTS(SELECT 1 FROM operations o WHERE o.instance_id=i.id AND o.status IN ('ACCEPTED','RUNNING','UNCERTAIN'))`, id).Scan(&p.InstanceID, &p.OrganizationID, &p.OwnerID, &p.Version, &p.WorkspaceID, &raw, &p.AssetID, &p.ResourceName, &p.Capacity, &p.FilesystemID, &p.WorkspaceOwner)
	if err != nil {
		return
	}
	p.Snapshot, err = instances.DecodeSnapshot(raw)
	if err != nil {
		return
	}
	result, probeErr := c.host.StorageUsage(probe, p)
	finish, stop := context.WithTimeout(ctx, 2*time.Second)
	defer stop()
	// A queued lifecycle command supersedes this observation, even while its Worker waits on the host guard.
	_, _ = c.pool.Exec(finish, `UPDATE storage_assets a SET usage_attempted_at=clock_timestamp(),usage_error=$4,
 usage_used_bytes=CASE WHEN $4 THEN usage_used_bytes ELSE $5 END,
 usage_available_bytes=CASE WHEN $4 THEN usage_available_bytes ELSE $6 END,
 usage_sampled_at=CASE WHEN $4 THEN usage_sampled_at ELSE clock_timestamp() END
 FROM instances i WHERE a.id=$1 AND a.instance_id=i.id AND i.id=$2 AND i.version=$3 AND i.observed_state='RUNNING' AND i.desired_state='RUNNING' AND a.status='READY' AND NOT EXISTS(SELECT 1 FROM operations o WHERE o.instance_id=i.id AND o.status IN ('ACCEPTED','RUNNING','UNCERTAIN'))`, p.AssetID, p.InstanceID, p.Version, probeErr != nil, result.Used, result.Available)
}
func (c *StorageUsageCollector) tick(ctx context.Context) {
	query, cancel := context.WithTimeout(ctx, 2*time.Second)
	rows, err := c.pool.Query(query, `WITH due AS (SELECT a.id FROM storage_assets a JOIN instances i ON a.id=i.storage_asset_id JOIN users u ON u.id=i.owner_id JOIN memberships m ON m.org_id=i.org_id AND m.user_id=i.owner_id JOIN organizations org ON org.id=i.org_id
 WHERE i.observed_state='RUNNING' AND i.desired_state='RUNNING' AND a.status='READY' AND u.status='ACTIVE' AND m.status='ACTIVE' AND org.status='ACTIVE' AND NOT EXISTS(SELECT 1 FROM operations o WHERE o.instance_id=i.id AND o.status IN ('ACCEPTED','RUNNING','UNCERTAIN'))
 AND (a.usage_attempted_at IS NULL OR a.usage_attempted_at<clock_timestamp()-interval '60 seconds') ORDER BY a.usage_attempted_at NULLS FIRST,i.id LIMIT 2 FOR UPDATE OF a SKIP LOCKED)
 UPDATE storage_assets a SET usage_attempted_at=clock_timestamp() FROM due WHERE a.id=due.id RETURNING a.instance_id::text`)
	if err != nil {
		cancel()
		return
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) != nil {
			break
		}
		ids = append(ids, id)
	}
	rows.Close()
	cancel()
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		c.collect(ctx, id)
	}
}
func (c *StorageUsageCollector) Run(ctx context.Context) error {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		c.tick(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
