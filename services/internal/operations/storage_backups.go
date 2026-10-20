package operations

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func verifyStorageBackup(path string, key []byte, a AssetState, expected ArchiveProof) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return errArchive
	}
	f, err := os.Open(path)
	if err != nil {
		return errArchive
	}
	defer f.Close()
	m, proof, err := OpenArchive(io.Discard, f, key)
	if err != nil || proof != expected || m.Kind != "storage" || m.OrganizationID != a.OrganizationID || m.OwnerID != a.OwnerID || m.InstanceID != a.InstanceID || m.AssetID != a.ID || m.Capacity != a.Capacity {
		return errArchive
	}
	return nil
}
func (s *StorageService) Backup(ctx context.Context, id, output string, key []byte) (ArchiveProof, error) {
	return s.backup(ctx, id, output, key, "")
}
func (s *StorageService) backup(ctx context.Context, id, output string, key []byte, setID string) (ArchiveProof, error) {
	var proof ArchiveProof
	if !s.valid() || !uuid.MatchString(id) || len(key) != 32 || !filepath.IsAbs(output) || privateDirectory(filepath.Dir(output)) != nil || setID != "" && !uuid.MatchString(setID) {
		return proof, ErrInput
	}
	if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		return proof, ErrInput
	}
	a, err := s.read(ctx, s.pool, id)
	if err != nil {
		return proof, err
	}
	if err = CheckBackup(a); err != nil {
		return proof, err
	}
	job := webapi.ID()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return proof, err
	}
	defer tx.Rollback(context.Background())
	_, err = tx.Exec(ctx, `INSERT INTO storage_backups(id,org_id,owner_id,instance_id,asset_id,capacity_bytes,template_version,image_digest,status,archive_path,set_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'RUNNING',$9,NULLIF($10,'')::uuid)`, job, a.OrganizationID, a.OwnerID, a.InstanceID, a.ID, a.Capacity, a.Plan.Snapshot.TemplateVersion, a.Plan.Snapshot.ImageDigest, output, setID)
	if err != nil {
		return proof, err
	}
	if err = s.audit(ctx, tx, a, "STORAGE_BACKUP_ACCEPTED", map[string]any{"backup_id": job}); err != nil {
		return proof, err
	}
	if err = tx.Commit(ctx); err != nil {
		return proof, err
	}
	proof, err = s.performBackup(ctx, id, output, key, job)
	if err != nil {
		state := "FAILED"
		if proof.Bytes > 0 {
			state = "UNCERTAIN"
		}
		finish, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		// Publishing a backup and its final audit is atomic. Lost final results stay pending.
		failedTx, e := s.pool.Begin(finish)
		if e == nil {
			defer failedTx.Rollback(context.Background())
			tag, e := failedTx.Exec(finish, `UPDATE storage_backups SET status=$2,completed_at=clock_timestamp() WHERE id=$1 AND status='RUNNING'`, job, state)
			if e == nil && tag.RowsAffected() == 1 {
				e = s.audit(finish, failedTx, a, "STORAGE_BACKUP_FAILED", map[string]any{"backup_id": job, "status": state})
			}
			if e == nil {
				_ = failedTx.Commit(finish)
			}
		}
	}
	return proof, err
}

// Reconcile reads the exact persisted archive for an interrupted attempt; it never creates another backup.
func (s *StorageService) ReconcileBackup(ctx context.Context, id string, key []byte) (string, error) {
	if !s.valid() || !uuid.MatchString(id) || len(key) != 32 {
		return "", ErrInput
	}
	var originalAsset string
	if err := s.pool.QueryRow(ctx, `SELECT asset_id::text FROM storage_backups WHERE id=$1 AND org_id=$2`, id, s.organization).Scan(&originalAsset); err != nil {
		return "", err
	}
	original, err := s.read(ctx, s.pool, originalAsset)
	if err != nil {
		return "", err
	}
	unlock, err := s.host.Acquire(ctx, original.InstanceID)
	if err != nil {
		return "", err
	}
	defer unlock()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(context.Background())
	var asset, path, status string
	err = tx.QueryRow(ctx, `SELECT asset_id::text,archive_path,status FROM storage_backups WHERE id=$1 AND org_id=$2 FOR UPDATE`, id, s.organization).Scan(&asset, &path, &status)
	if err != nil {
		return "", err
	}
	if status != "RUNNING" && status != "UNCERTAIN" {
		return status, nil
	}
	a, err := s.read(ctx, tx, asset)
	if err != nil {
		return "", err
	}
	state := "FAILED"
	sha := ""
	if _, err = os.Lstat(path); err == nil {
		f, e := os.Open(path)
		if e != nil {
			return "", e
		}
		m, proof, e := OpenArchive(io.Discard, f, key)
		f.Close()
		if e != nil {
			return "", errArchive
		} // A corrupt archive needs operator investigation; keep pending.
		if m.Kind != "storage" || m.AssetID != a.ID || m.InstanceID != a.InstanceID || m.OwnerID != a.OwnerID || m.OrganizationID != a.OrganizationID || m.Capacity != a.Capacity {
			return "", errArchive
		}
		if e = verifyStorageBackup(path, key, a, proof); e != nil {
			return "", e
		}
		state = "SUCCEEDED"
		sha = proof.SHA256
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	_, err = tx.Exec(ctx, `UPDATE storage_backups SET status=$2,completed_at=clock_timestamp(),verified_at=CASE WHEN $2='SUCCEEDED' THEN clock_timestamp() END,backup_sha256=NULLIF($3,'') WHERE id=$1`, id, state, sha)
	if err != nil {
		return "", err
	}
	if err = s.audit(ctx, tx, a, "STORAGE_BACKUP_RECONCILE", map[string]any{"backup_id": id, "status": state}); err != nil {
		return "", err
	}
	return state, tx.Commit(ctx)
}

// SetStorageHold serializes with ordinary maintenance and the daily repository retention guard.
func (s *StorageService) SetStorageHold(ctx context.Context, id, confirm, reason string, held, apply bool) error {
	if !s.valid() || !uuid.MatchString(id) || !keyName.MatchString(reason) || apply && id != confirm {
		return ErrInput
	}
	original, err := s.read(ctx, s.pool, id)
	if err != nil {
		return err
	}
	unlock, err := s.host.Acquire(ctx, original.InstanceID)
	if err != nil {
		return err
	}
	defer unlock()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SELECT id FROM organizations WHERE id=$1 FOR UPDATE`, s.organization); err != nil {
		return err
	}
	a, err := s.read(ctx, tx, id)
	if err != nil {
		return err
	}
	if a.Status == "DELETED" {
		return ErrStoragePolicy
	}
	if !apply {
		return nil
	}
	if _, err = tx.Exec(ctx, `UPDATE storage_assets SET legal_hold=$2 WHERE id=$1 AND org_id=$3`, id, held, s.organization); err != nil {
		return err
	}
	if err = s.audit(ctx, tx, a, "STORAGE_HOLD", map[string]any{"legal_hold": held, "reason_code": reason}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
