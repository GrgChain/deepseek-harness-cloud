package operations

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/instances"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/worker"
)

type StorageService struct {
	pool                   *pgxpool.Pool
	host                   *worker.LocalHost
	organization, operator string
}

func NewStorageService(pool *pgxpool.Pool, host *worker.LocalHost, org, operator string) *StorageService {
	return &StorageService{pool, host, org, operator}
}

func (s *StorageService) valid() bool {
	return s.pool != nil && s.host != nil && uuid.MatchString(s.organization) && operatorName.MatchString(s.operator)
}

func (s *StorageService) read(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id string) (AssetState, error) {
	var a AssetState
	var snapshot []byte
	var retention, deleted *time.Time
	err := q.QueryRow(ctx, `SELECT a.id::text,a.instance_id::text,a.org_id::text,a.owner_id::text,coalesce(a.filesystem_id,''),a.capacity_bytes,a.status,i.observed_state,i.desired_state,a.legal_hold,a.retention_until,i.deleted_at,a.maintenance_intent,
 EXISTS(SELECT 1 FROM operations o WHERE o.instance_id=i.id AND o.status IN ('ACCEPTED','RUNNING','UNCERTAIN')) OR EXISTS(SELECT 1 FROM storage_release_requests r WHERE r.asset_id=a.id AND r.status<>'SUCCEEDED'),
 (SELECT coalesce(sum(r.amount),0)=a.capacity_bytes FROM quota_reservations r WHERE r.instance_id=i.id AND r.resource_type='STORAGE' AND r.status IN ('HELD','USED')),
 EXISTS(SELECT 1 FROM model_configurations c JOIN secrets sec ON sec.id=c.credential_ref WHERE c.id=i.model_config_id AND c.version=i.model_config_version AND c.status='VALID' AND c.revoked_at IS NULL AND sec.revoked_at IS NULL AND EXISTS(SELECT 1 FROM organizations org JOIN memberships m ON m.org_id=org.id JOIN users owner ON owner.id=m.user_id WHERE org.id=i.org_id AND m.user_id=i.owner_id AND org.status='ACTIVE' AND m.status='ACTIVE' AND owner.status='ACTIVE')),
 i.config_snapshot,i.workspace_id::text,i.workspace_name,u.workspace_owner_id::text
 FROM storage_assets a JOIN instances i ON i.id=a.instance_id AND i.storage_asset_id=a.id JOIN users u ON u.id=i.owner_id WHERE a.id=$1 AND a.org_id=$2`, id, s.organization).Scan(&a.ID, &a.InstanceID, &a.OrganizationID, &a.OwnerID, &a.FilesystemID, &a.Capacity, &a.Status, &a.Observed, &a.Desired, &a.LegalHold, &retention, &deleted, &a.Intent, &a.ActiveOperation, &a.StorageReserved, &a.ModelUsable, &snapshot, &a.Plan.WorkspaceID, &a.Plan.WorkspaceName, &a.Plan.WorkspaceOwner)
	if err != nil {
		return a, err
	}
	if retention != nil {
		a.RetentionUntil = *retention
	}
	if deleted != nil {
		a.DeletedAt = *deleted
	}
	a.Plan.Snapshot, err = instances.DecodeSnapshot(snapshot)
	if err != nil {
		return a, err
	}
	a.Plan.InstanceID = a.InstanceID
	a.Plan.AssetID = a.ID
	a.Plan.OrganizationID = a.OrganizationID
	a.Plan.OwnerID = a.OwnerID
	a.Plan.Capacity = a.Capacity
	a.Plan.FilesystemID = a.FilesystemID
	a.Plan.AssetStatus = a.Status
	a.Plan.ResourceName = "hc-" + a.ID
	return a, nil
}

func (s *StorageService) lock(ctx context.Context, ids ...string) (pgx.Tx, map[string]AssetState, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	fail := func(err error) (pgx.Tx, map[string]AssetState, error) {
		_ = tx.Rollback(context.Background())
		return nil, nil, err
	}
	owners := map[string]bool{}
	for _, id := range ids {
		a, err := s.read(ctx, tx, id)
		if err != nil {
			return fail(err)
		}
		owners[a.OwnerID] = true
	}
	orderedOwners := []string{}
	for id := range owners {
		orderedOwners = append(orderedOwners, id)
	}
	sort.Strings(orderedOwners)
	for _, owner := range orderedOwners {
		var id string
		if err = tx.QueryRow(ctx, `SELECT user_id::text FROM memberships WHERE org_id=$1 AND user_id=$2 FOR UPDATE`, s.organization, owner).Scan(&id); err != nil {
			return fail(err)
		}
	}
	// Model head precedes quota/instance locks, matching ordinary commands.
	var model string
	if err = tx.QueryRow(ctx, `SELECT id::text FROM model_configuration_heads WHERE org_id=$1 FOR UPDATE`, s.organization).Scan(&model); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fail(err)
	}
	var version int64
	if err = tx.QueryRow(ctx, `SELECT version FROM quota_policies WHERE org_id=$1 FOR UPDATE`, s.organization).Scan(&version); err != nil {
		return fail(err)
	}
	ordered := []AssetState{}
	for _, id := range ids {
		a, err := s.read(ctx, tx, id)
		if err != nil {
			return fail(err)
		}
		ordered = append(ordered, a)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].InstanceID < ordered[j].InstanceID })
	states := map[string]AssetState{}
	for _, a := range ordered {
		var id string
		if err = tx.QueryRow(ctx, `SELECT id::text FROM instances WHERE id=$1 FOR UPDATE`, a.InstanceID).Scan(&id); err != nil {
			return fail(err)
		}
		if err = tx.QueryRow(ctx, `SELECT id::text FROM storage_assets WHERE id=$1 FOR UPDATE`, a.ID).Scan(&id); err != nil {
			return fail(err)
		}
		current, err := s.read(ctx, tx, a.ID)
		if err != nil {
			return fail(err)
		}
		states[a.ID] = current
	}
	return tx, states, nil
}

func (s *StorageService) guards(ctx context.Context, states ...AssetState) (func(), error) {
	sort.Slice(states, func(i, j int) bool { return states[i].InstanceID < states[j].InstanceID })
	var releases []func()
	release := func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}
	for _, a := range states {
		unlock, err := s.host.Acquire(ctx, a.InstanceID)
		if err != nil {
			release()
			return nil, err
		}
		releases = append(releases, unlock)
		if err = s.host.AssertAssetIdle(ctx, a.Plan); err != nil {
			release()
			return nil, err
		}
	}
	return release, nil
}

func (s *StorageService) audit(ctx context.Context, tx pgx.Tx, a AssetState, action string, details map[string]any) error {
	details["schema_version"] = 1
	details["operator_ref"] = s.operator
	raw, err := json.Marshal(details)
	if err != nil {
		return err
	}
	outcome := "SUCCEEDED"
	if strings.HasSuffix(action, "_ACCEPTED") {
		outcome = "ACCEPTED"
	} else if strings.HasSuffix(action, "_FAILED") {
		outcome = "FAILED"
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events(id,org_id,owner_id,resource_id,action,outcome,trace_id,details) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$7,$1::text,$6)`, webapi.ID(), a.OrganizationID, a.OwnerID, a.ID, action, raw, outcome)
	return err
}

type assetIntent struct {
	SchemaVersion int    `json:"schema_version"`
	Action        string `json:"action"`
	SourceAssetID string `json:"source_asset_id,omitempty"`
	BackupSHA     string `json:"backup_sha256,omitempty"`
	PreimageSHA   string `json:"preimage_sha256,omitempty"`
	Reason        string `json:"reason_code,omitempty"`
}

type StorageReport struct {
	AssetID       string       `json:"asset_id"`
	SourceAssetID string       `json:"source_asset_id,omitempty"`
	Proof         ArchiveProof `json:"proof"`
	Preimage      ArchiveProof `json:"preimage"`
	Applied       bool         `json:"applied"`
}
