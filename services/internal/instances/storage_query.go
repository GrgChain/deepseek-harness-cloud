package instances

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
)

const StorageUsageTTL = 5 * time.Minute

type StorageUsageView struct {
	Status      string     `json:"status"`
	Used        *int64     `json:"used_bytes"`
	Available   *int64     `json:"available_bytes"`
	SampledAt   *time.Time `json:"sampled_at"`
	AttemptedAt *time.Time `json:"attempted_at"`
}
type StorageBackupView struct {
	ID              string     `json:"id"`
	Status          string     `json:"status"`
	CreatedAt       time.Time  `json:"created_at"`
	CompletedAt     *time.Time `json:"completed_at"`
	VerifiedAt      *time.Time `json:"verified_at"`
	ExpiresAt       *time.Time `json:"expires_at"`
	RetentionPolicy string     `json:"retention_policy"`
}
type StorageMaintenanceView struct {
	BackupBlockers  []string `json:"backup_blockers"`
	RestoreBlockers []string `json:"restore_blockers"`
	ReclaimBlockers []string `json:"reclaim_blockers"`
}
type StorageAssetView struct {
	ID                   string                 `json:"id"`
	OwnerID              string                 `json:"owner_id"`
	InstanceID           string                 `json:"instance_id"`
	WorkspaceName        string                 `json:"workspace_name"`
	WorkspaceState       string                 `json:"workspace_state"`
	Capacity             int64                  `json:"capacity_bytes"`
	Status               string                 `json:"status"`
	CreatedAt            time.Time              `json:"created_at"`
	DeletedAt            *time.Time             `json:"deleted_at"`
	RetentionUntil       *time.Time             `json:"retention_until"`
	LegalHold            bool                   `json:"legal_hold"`
	RetentionExpired     bool                   `json:"retention_expired"`
	Usage                StorageUsageView       `json:"usage"`
	LatestBackup         *StorageBackupView     `json:"latest_backup"`
	LatestVerifiedBackup *StorageBackupView     `json:"latest_verified_backup"`
	Maintenance          StorageMaintenanceView `json:"maintenance"`
}
type StorageListInput struct {
	Page, Limit   int
	Status, Query string
}
type StorageAssetPage struct {
	Items      []StorageAssetView `json:"items"`
	Total      int                `json:"total"`
	NextCursor *string            `json:"next_cursor"`
}
type StorageBackupPage struct {
	Items      []StorageBackupView `json:"items"`
	Total      int                 `json:"total"`
	NextCursor *string             `json:"next_cursor"`
}

const backupJSON = `jsonb_build_object('id',b.id,'status',b.status,'created_at',b.created_at,'completed_at',b.completed_at,'verified_at',b.verified_at,'expires_at',b.expires_at,'retention_policy',CASE WHEN b.set_id IS NULL THEN 'MANUAL' ELSE 'LAST_7_SUCCESSFUL_DATES' END)`
const storageFrom = ` FROM storage_assets a JOIN instances i ON i.id=a.instance_id AND i.storage_asset_id=a.id AND i.org_id=a.org_id AND i.owner_id=a.owner_id WHERE a.org_id=$1 AND a.owner_id=$2 AND a.owner_removed_at IS NULL`
const selectStorage = `SELECT a.id::text,a.owner_id::text,i.id::text,i.name,i.observed_state,a.capacity_bytes,a.status,i.created_at,i.deleted_at,a.retention_until,a.legal_hold,a.usage_used_bytes,a.usage_available_bytes,a.usage_sampled_at,a.usage_attempted_at,a.usage_error,
 EXISTS(SELECT 1 FROM operations o WHERE o.instance_id=i.id AND o.status IN ('ACCEPTED','RUNNING','UNCERTAIN')),
 COALESCE(a.filesystem_id=a.id::text AND a.init_version>=1,false),
 (SELECT COALESCE(sum(q.amount),0)=a.capacity_bytes FROM quota_commitments q WHERE q.org_id=a.org_id AND q.owner_id=a.owner_id AND q.instance_id=i.id AND q.resource_type='STORAGE'),
 i.desired_state,
 EXISTS(SELECT 1 FROM model_configurations c JOIN secrets sec ON sec.id=c.credential_ref WHERE c.id=i.model_config_id AND c.version=i.model_config_version AND c.org_id=i.org_id AND c.status='VALID' AND c.revoked_at IS NULL AND sec.revoked_at IS NULL),
 (SELECT ` + backupJSON + ` FROM storage_backups b WHERE b.asset_id=a.id AND b.org_id=a.org_id AND b.owner_id=a.owner_id ORDER BY b.created_at DESC,b.id DESC LIMIT 1),
 (SELECT ` + backupJSON + ` FROM storage_backups b WHERE b.asset_id=a.id AND b.org_id=a.org_id AND b.owner_id=a.owner_id AND b.status='SUCCEEDED' AND (b.expires_at IS NULL OR b.expires_at>clock_timestamp()) ORDER BY b.created_at DESC,b.id DESC LIMIT 1)`

func normalizeBackup(b *StorageBackupView, now time.Time) {
	if b == nil {
		return
	}
	if b.Status == "RUNNING" && b.CreatedAt.Add(30*time.Minute).Before(now) {
		b.Status = "UNCERTAIN"
	}
	if b.Status == "SUCCEEDED" && b.ExpiresAt != nil && !b.ExpiresAt.After(now) {
		b.Status = "EXPIRED"
	}
}
func scanStorage(row pgx.Row, now time.Time) (StorageAssetView, error) {
	var v StorageAssetView
	var active, initialized, reserved, failed, modelUsable bool
	var desired string
	var latest, verified []byte
	err := row.Scan(&v.ID, &v.OwnerID, &v.InstanceID, &v.WorkspaceName, &v.WorkspaceState, &v.Capacity, &v.Status, &v.CreatedAt, &v.DeletedAt, &v.RetentionUntil, &v.LegalHold, &v.Usage.Used, &v.Usage.Available, &v.Usage.SampledAt, &v.Usage.AttemptedAt, &failed, &active, &initialized, &reserved, &desired, &modelUsable, &latest, &verified)
	if err != nil {
		return v, err
	}
	v.Usage.Status = "UNSAMPLED"
	if failed {
		v.Usage.Status = "UNAVAILABLE"
	} else if v.Usage.SampledAt != nil {
		v.Usage.Status = "CURRENT"
		if v.Usage.SampledAt.After(now) || !v.Usage.SampledAt.Add(StorageUsageTTL).After(now) {
			v.Usage.Status = "STALE"
		}
	}
	if len(latest) > 0 {
		if err = json.Unmarshal(latest, &v.LatestBackup); err != nil {
			return v, err
		}
	}
	if len(verified) > 0 {
		if err = json.Unmarshal(verified, &v.LatestVerifiedBackup); err != nil {
			return v, err
		}
	}
	normalizeBackup(v.LatestBackup, now)
	normalizeBackup(v.LatestVerifiedBackup, now)
	m := StorageMaintenanceView{BackupBlockers: []string{}, RestoreBlockers: []string{}, ReclaimBlockers: []string{}}
	common := []string{}
	if active {
		common = append(common, "OPERATION_PENDING")
	}
	if !initialized {
		common = append(common, "BINDING_UNCONFIRMED")
	}
	if !reserved {
		common = append(common, "QUOTA_UNCONFIRMED")
	}
	m.BackupBlockers = append(m.BackupBlockers, common...)
	if !((v.Status == "READY" && v.WorkspaceState == "STOPPED" && desired == "STOPPED") || (v.Status == "RETAINED" && v.WorkspaceState == "DELETED" && desired == "DELETED")) {
		m.BackupBlockers = append(m.BackupBlockers, "COMPUTE_NOT_STOPPED")
	}
	if v.LatestBackup != nil && (v.LatestBackup.Status == "RUNNING" || v.LatestBackup.Status == "UNCERTAIN") {
		m.BackupBlockers = append(m.BackupBlockers, "BACKUP_PENDING")
	}
	m.RestoreBlockers = append(m.RestoreBlockers, common...)
	if !modelUsable {
		m.RestoreBlockers = append(m.RestoreBlockers, "RUNTIME_BINDING_UNAVAILABLE")
	}
	if v.Status != "READY" || v.WorkspaceState != "STOPPED" || desired != "STOPPED" {
		m.RestoreBlockers = append(m.RestoreBlockers, "TARGET_NOT_STOPPED")
	}
	if v.LegalHold {
		m.RestoreBlockers = append(m.RestoreBlockers, "LEGAL_HOLD")
	}
	m.ReclaimBlockers = append(m.ReclaimBlockers, common...)
	if v.Status != "RETAINED" && v.Status != "RECLAIM_PENDING" || v.WorkspaceState != "DELETED" || desired != "DELETED" {
		m.ReclaimBlockers = append(m.ReclaimBlockers, "NOT_SOFT_DELETED")
	}
	if v.LegalHold {
		m.ReclaimBlockers = append(m.ReclaimBlockers, "LEGAL_HOLD")
	}
	if v.RetentionUntil == nil || v.RetentionUntil.After(now) {
		m.ReclaimBlockers = append(m.ReclaimBlockers, "RETENTION_ACTIVE")
	}
	if v.DeletedAt == nil || v.DeletedAt.Add(7*24*time.Hour).After(now) {
		m.ReclaimBlockers = append(m.ReclaimBlockers, "MINIMUM_7_DAYS")
	}
	if v.Status == "DELETED" {
		m.ReclaimBlockers = append(m.ReclaimBlockers, "ALREADY_RECLAIMED")
	}
	v.RetentionExpired = v.RetentionUntil != nil && !v.RetentionUntil.After(now) && v.Status != "DELETED"
	v.Maintenance = m
	return v, nil
}
func validStorageStatus(s string) bool {
	switch s {
	case "", "PREPARING", "READY", "RETAINED", "RESTORING", "RECLAIM_PENDING", "DELETED", "PROCESSING":
		return true
	}
	return false
}
func (s *QueryService) StorageAssets(ctx context.Context, p identity.Principal, in StorageListInput) (StorageAssetPage, error) {
	out := StorageAssetPage{Items: []StorageAssetView{}}
	if in.Page < 1 || in.Page > 1_000_000_000 || in.Limit < 1 || in.Limit > 100 || !validStorageStatus(in.Status) || !prefixPattern.MatchString(in.Query) {
		return out, invalidQuery()
	}
	tx, now, err := s.begin(ctx, p, "instances:read")
	if err != nil {
		return out, err
	}
	defer tx.Rollback(context.Background())
	where := storageFrom + ` AND ($3='' OR a.status=$3 OR ($3='PROCESSING' AND a.status IN ('PREPARING','RESTORING','RECLAIM_PENDING'))) AND ($4='' OR i.name LIKE $4||'%' ESCAPE '!')`
	prefix := strings.NewReplacer("!", "!!", "_", "!_").Replace(in.Query)
	if err = tx.QueryRow(ctx, `SELECT count(*)`+where, p.OrganizationID, p.UserID, in.Status, prefix).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := tx.Query(ctx, selectStorage+where+` ORDER BY i.created_at DESC,a.id DESC LIMIT $5 OFFSET $6`, p.OrganizationID, p.UserID, in.Status, prefix, in.Limit, int64(in.Page-1)*int64(in.Limit))
	if err != nil {
		return out, err
	}
	for rows.Next() {
		v, e := scanStorage(rows, now)
		if e != nil {
			rows.Close()
			return out, e
		}
		out.Items = append(out.Items, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (s *QueryService) StorageAsset(ctx context.Context, p identity.Principal, id string) (StorageAssetView, error) {
	var out StorageAssetView
	if !idPattern.MatchString(id) {
		return out, invalidQuery()
	}
	tx, now, err := s.begin(ctx, p, "instances:read")
	if err != nil {
		return out, err
	}
	defer tx.Rollback(context.Background())
	out, err = scanStorage(tx.QueryRow(ctx, selectStorage+storageFrom+` AND a.id=$3`, p.OrganizationID, p.UserID, id), now)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, queryNotFound()
	}
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (s *QueryService) StorageBackups(ctx context.Context, p identity.Principal, id string, page, limit int) (StorageBackupPage, error) {
	out := StorageBackupPage{Items: []StorageBackupView{}}
	if !idPattern.MatchString(id) || page < 1 || page > 1_000_000_000 || limit < 1 || limit > 100 {
		return out, invalidQuery()
	}
	tx, now, err := s.begin(ctx, p, "instances:read")
	if err != nil {
		return out, err
	}
	defer tx.Rollback(context.Background())
	var found bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM storage_assets WHERE id=$3 AND org_id=$1 AND owner_id=$2 AND owner_removed_at IS NULL)`, p.OrganizationID, p.UserID, id).Scan(&found); err != nil {
		return out, err
	}
	if !found {
		return out, queryNotFound()
	}
	where := ` FROM storage_backups b WHERE b.org_id=$1 AND b.owner_id=$2 AND b.asset_id=$3`
	if err = tx.QueryRow(ctx, `SELECT count(*)`+where, p.OrganizationID, p.UserID, id).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := tx.Query(ctx, `SELECT `+backupJSON+where+` ORDER BY b.created_at DESC,b.id DESC LIMIT $4 OFFSET $5`, p.OrganizationID, p.UserID, id, limit, int64(page-1)*int64(limit))
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var raw []byte
		var v StorageBackupView
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return out, err
		}
		if err = json.Unmarshal(raw, &v); err != nil {
			rows.Close()
			return out, err
		}
		normalizeBackup(&v, now)
		out.Items = append(out.Items, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
