package worker

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/instances"
)

const StorageBrowseInterval = 250 * time.Millisecond
const StorageBrowseTimeout = 50 * time.Second

type storageBrowseHost interface {
	Acquire(context.Context, string) (func(), error)
	BrowseStorage(context.Context, ExecutionPlan, instances.StorageBrowseInput) (instances.StorageDirectory, instances.StorageFile, error)
}

type StorageBrowser struct {
	pool        *pgxpool.Pool
	host        storageBrowseHost
	nextCleanup time.Time
}

func NewStorageBrowser(pool *pgxpool.Pool, host storageBrowseHost) *StorageBrowser {
	return &StorageBrowser{pool: pool, host: host}
}

type storageBrowseRequest struct {
	ID, InstanceID  string
	ResultPublicKey []byte
	Input           instances.StorageBrowseInput
}

func (b *StorageBrowser) claim(ctx context.Context) (storageBrowseRequest, error) {
	var out storageBrowseRequest
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(context.Background())
	if time.Now().After(b.nextCleanup) {
		if _, err = tx.Exec(ctx, `DELETE FROM storage_browse_requests WHERE expires_at<=clock_timestamp()`); err != nil {
			return out, err
		}
		b.nextCleanup = time.Now().Add(time.Minute)
	}
	var action string
	err = tx.QueryRow(ctx, `SELECT id::text,instance_id::text,action,root,path,after_name,result_public_key FROM storage_browse_requests WHERE status='QUEUED' AND expires_at>clock_timestamp() ORDER BY created_at,id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&out.ID, &out.InstanceID, &action, &out.Input.Root, &out.Input.Path, &out.Input.After, &out.ResultPublicKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, tx.Commit(ctx)
	}
	if err != nil {
		return out, err
	}
	out.Input.Action = action
	if instances.ValidateStorageBrowse(out.Input) != nil || (action == "LIST" && len(out.ResultPublicKey) != 0) || (action != "LIST" && len(out.ResultPublicKey) != 32) {
		_, err = tx.Exec(ctx, `UPDATE storage_browse_requests SET status='FAILED',error_code='FILE_UNAVAILABLE' WHERE id=$1 AND status='QUEUED'`, out.ID)
		if err != nil {
			return out, err
		}
		return storageBrowseRequest{}, tx.Commit(ctx)
	}
	if _, err = tx.Exec(ctx, `UPDATE storage_browse_requests SET status='RUNNING' WHERE id=$1 AND status='QUEUED'`, out.ID); err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

func (b *StorageBrowser) load(ctx context.Context, request storageBrowseRequest) (ExecutionPlan, error) {
	var p ExecutionPlan
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return p, err
	}
	defer tx.Rollback(context.Background())
	var raw []byte
	var owner, org, observed, desired string
	var deleted bool
	var authorized, busy bool
	err = tx.QueryRow(ctx, `SELECT r.org_id::text,r.owner_id::text,i.id::text,i.version,i.workspace_name,i.observed_state,i.desired_state,i.deleted_at IS NOT NULL,i.workspace_id::text,i.config_snapshot,a.id::text,a.status,a.resource_name,coalesce(a.filesystem_id,''),a.capacity_bytes,u.workspace_owner_id::text,
 public.hc_worker_lock_authority(r.org_id,r.owner_id) AND public.hc_has_permission(r.org_id,r.owner_id,'instances:read') AND u.status='ACTIVE' AND m.status='ACTIVE' AND org.status='ACTIVE',
 a.maintenance_intent IS NOT NULL OR EXISTS(SELECT 1 FROM operations op WHERE op.instance_id=i.id AND op.status IN ('ACCEPTED','RUNNING','UNCERTAIN')) OR EXISTS(SELECT 1 FROM storage_backups backup WHERE backup.asset_id=a.id AND backup.status IN ('RUNNING','UNCERTAIN')) OR EXISTS(SELECT 1 FROM storage_release_requests release WHERE release.asset_id=a.id AND release.status<>'SUCCEEDED') OR EXISTS(SELECT 1 FROM recovery_quota_holds hold WHERE hold.org_id=i.org_id AND hold.instance_id=i.id)
 FROM storage_browse_requests r JOIN instances i ON i.id=r.instance_id AND i.org_id=r.org_id AND i.owner_id=r.owner_id AND i.storage_asset_id=r.asset_id
 JOIN storage_assets a ON a.id=r.asset_id AND a.instance_id=i.id AND a.org_id=i.org_id AND a.owner_id=i.owner_id
 JOIN users u ON u.id=i.owner_id JOIN memberships m ON m.org_id=i.org_id AND m.user_id=i.owner_id JOIN organizations org ON org.id=i.org_id
	 WHERE r.id=$1 AND r.status='RUNNING' AND r.expires_at>clock_timestamp() FOR UPDATE OF r,i,a`, request.ID).Scan(&org, &owner, &p.InstanceID, &p.Version, &p.WorkspaceName, &observed, &desired, &deleted, &p.WorkspaceID, &raw, &p.AssetID, &p.AssetStatus, &p.ResourceName, &p.FilesystemID, &p.Capacity, &p.WorkspaceOwner, &authorized, &busy)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, instances.ErrStorageBrowseUnavailable
	}
	if err != nil {
		return p, err
	}
	if !authorized {
		return p, ErrExecutionRevoked
	}
	if busy {
		return p, instances.ErrStorageBrowseBusy
	}
	p.Observed, p.Desired = observed, desired
	validRunning := observed == "RUNNING" && desired == "RUNNING" && !deleted && p.AssetStatus == "READY"
	validStopped := observed == "STOPPED" && desired == "STOPPED" && !deleted && p.AssetStatus == "READY"
	validDeleted := observed == "DELETED" && desired == "DELETED" && deleted && p.AssetStatus == "RETAINED"
	if !validRunning && !validStopped && !validDeleted {
		return p, instances.ErrStorageBrowseUnavailable
	}
	p.OrganizationID, p.OwnerID = org, owner
	if !uuidPattern.MatchString(p.InstanceID) || !uuidPattern.MatchString(p.AssetID) || p.ResourceName != "hc-"+p.AssetID || p.FilesystemID != p.AssetID {
		return p, ErrHostIdentity
	}
	p.Snapshot, err = instances.DecodeSnapshot(raw)
	if err != nil || p.Snapshot.Spec.Storage != p.Capacity {
		return p, ErrHostIdentity
	}
	if err = tx.Commit(ctx); err != nil {
		return p, err
	}
	return p, nil
}

func (b *StorageBrowser) finish(_ context.Context, request storageBrowseRequest, directory instances.StorageDirectory, file instances.StorageFile, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	status, code := "SUCCEEDED", ""
	var listing, content []byte
	var fileName *string
	if err != nil {
		status = "FAILED"
		switch {
		case errors.Is(err, instances.ErrStorageBrowseBusy):
			code = "STORAGE_BUSY"
		case errors.Is(err, instances.ErrStorageFileTooLarge):
			code = "FILE_TOO_LARGE"
		case errors.Is(err, instances.ErrStorageDirectoryTooLarge):
			code = "DIRECTORY_TOO_LARGE"
		case errors.Is(err, instances.ErrStoragePreviewUnavailable):
			code = "PREVIEW_UNAVAILABLE"
		case errors.Is(err, instances.ErrStorageFileUnavailable):
			code = "FILE_UNAVAILABLE"
		default:
			code = "STORAGE_UNAVAILABLE"
		}
	} else if request.Input.Action == "LIST" {
		listing, err = json.Marshal(directory)
		if err != nil {
			status, code = "FAILED", "STORAGE_UNAVAILABLE"
		}
	} else {
		content, err = instances.SealStorageBrowseContent(request.ID, request.ResultPublicKey, file.Content)
		if err != nil {
			status, code = "FAILED", "STORAGE_UNAVAILABLE"
		}
		fileName = &file.Name
	}
	if status == "SUCCEEDED" {
		if _, err = b.pool.Exec(ctx, `UPDATE storage_browse_requests SET status=$2,listing=$3,content=$4,file_name=$5 WHERE id=$1 AND status='RUNNING'`, request.ID, status, nullableBytes(listing), nullableBytes(content), fileName); err != nil {
			slog.Warn("storage browse result could not be recorded")
		}
		return
	}
	if _, err = b.pool.Exec(ctx, `UPDATE storage_browse_requests SET status='FAILED',error_code=$2 WHERE id=$1 AND status='RUNNING'`, request.ID, code); err != nil {
		slog.Warn("storage browse failure could not be recorded")
	}
}

func nullableBytes(value []byte) any {
	if value == nil {
		return nil
	}
	return value
}

func (b *StorageBrowser) process(ctx context.Context, request storageBrowseRequest) {
	bounded, cancel := context.WithTimeout(ctx, StorageBrowseTimeout)
	defer cancel()
	unlock, err := b.host.Acquire(bounded, request.InstanceID)
	if err != nil {
		b.finish(bounded, request, instances.StorageDirectory{}, instances.StorageFile{}, instances.ErrStorageBrowseBusy)
		return
	}
	defer unlock()
	p, err := b.load(bounded, request)
	if err != nil {
		b.finish(bounded, request, instances.StorageDirectory{}, instances.StorageFile{}, err)
		return
	}
	directory, file, err := b.host.BrowseStorage(bounded, p, request.Input)
	b.finish(bounded, request, directory, file, err)
}

func (b *StorageBrowser) tick(ctx context.Context) {
	claimCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	request, err := b.claim(claimCtx)
	cancel()
	if err != nil || request.ID == "" || ctx.Err() != nil {
		return
	}
	b.process(ctx, request)
}

func (b *StorageBrowser) Run(ctx context.Context) error {
	ticker := time.NewTicker(StorageBrowseInterval)
	defer ticker.Stop()
	for {
		b.tick(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
