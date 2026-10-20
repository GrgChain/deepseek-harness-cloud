package operations

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
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
func (s *StorageService) writeBackup(ctx context.Context, a AssetState, output string, key []byte) (ArchiveProof, error) {
	if len(key) != 32 || !filepath.IsAbs(output) || privateDirectory(filepath.Dir(output)) != nil {
		return ArchiveProof{}, ErrInput
	}
	if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		return ArchiveProof{}, ErrInput
	}
	file, err := os.CreateTemp(filepath.Dir(output), ".hc-asset-backup-*")
	if err != nil {
		return ArchiveProof{}, err
	}
	defer file.Close()
	defer os.Remove(file.Name())
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	done := make(chan error, 1)
	go func() { err := s.host.ArchiveAsset(ctx, a.Plan, writer); _ = writer.CloseWithError(err); done <- err }()
	m := BackupManifest{SchemaVersion: 1, Kind: "storage", OrganizationID: a.OrganizationID, CreatedAt: time.Now().UTC(), InstanceID: a.InstanceID, AssetID: a.ID, OwnerID: a.OwnerID, Capacity: a.Capacity, TemplateVersion: a.Plan.Snapshot.TemplateVersion, ImageDigest: a.Plan.Snapshot.ImageDigest}
	proof, err := SealArchive(file, reader, key, m)
	if err != nil {
		cancel()
		_ = reader.CloseWithError(err)
	}
	hostErr := <-done
	if err != nil {
		return ArchiveProof{}, err
	}
	if hostErr != nil {
		return ArchiveProof{}, hostErr
	}
	if err = file.Sync(); err != nil {
		return ArchiveProof{}, err
	}
	if err = file.Close(); err != nil {
		return ArchiveProof{}, err
	}
	if err = os.Link(file.Name(), output); err != nil {
		return ArchiveProof{}, err
	}
	return proof, syncDirectory(filepath.Dir(output))
}
func (s *StorageService) performBackup(ctx context.Context, id, output string, key []byte, job string) (ArchiveProof, error) {
	if !s.valid() || !uuid.MatchString(id) {
		return ArchiveProof{}, ErrInput
	}
	a, err := s.read(ctx, s.pool, id)
	if err != nil {
		return ArchiveProof{}, err
	}
	unlock, err := s.guards(ctx, a)
	if err != nil {
		return ArchiveProof{}, err
	}
	defer unlock()
	tx, states, err := s.lock(ctx, id)
	if err != nil {
		return ArchiveProof{}, err
	}
	defer tx.Rollback(context.Background())
	a = states[id]
	if err = CheckBackup(a); err != nil {
		return ArchiveProof{}, err
	}
	proof, err := s.writeBackup(ctx, a, output, key)
	if err != nil {
		return ArchiveProof{}, err
	}
	if err = verifyStorageBackup(output, key, a, proof); err != nil {
		return proof, err
	}
	if _, err = tx.Exec(ctx, `UPDATE storage_backups SET status='SUCCEEDED',completed_at=clock_timestamp(),verified_at=clock_timestamp(),backup_sha256=$2 WHERE id=$1 AND status='RUNNING'`, job, proof.SHA256); err != nil {
		return proof, err
	}
	if err = s.audit(ctx, tx, a, "STORAGE_BACKUP", map[string]any{"source_asset_id": a.ID, "backup_id": job, "backup_sha256": proof.SHA256}); err != nil {
		return proof, err
	}
	return proof, tx.Commit(ctx)
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

func (s *StorageService) Restore(ctx context.Context, input, targetID, preimage, confirmed string, key []byte, apply bool) (StorageReport, error) {
	report := StorageReport{AssetID: targetID}
	if !s.valid() || !uuid.MatchString(targetID) || len(key) != 32 || !filepath.IsAbs(input) || privateDirectory(filepath.Dir(input)) != nil || (apply && confirmed != targetID) {
		return report, ErrInput
	}
	info, err := os.Lstat(input)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return report, ErrInput
	}
	file, err := os.Open(input)
	if err != nil {
		return report, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return report, ErrInput
	}
	stage, err := os.CreateTemp(filepath.Dir(input), ".hc-asset-recovery-*")
	if err != nil {
		return report, err
	}
	defer stage.Close()
	defer os.Remove(stage.Name())
	m, proof, err := OpenArchive(stage, file, key)
	if err != nil {
		return report, err
	}
	if m.Kind != "storage" || m.OrganizationID != s.organization {
		return report, ErrStoragePolicy
	}
	report.SourceAssetID = m.AssetID
	report.Proof = proof
	source, err := s.read(ctx, s.pool, m.AssetID)
	if err != nil {
		return report, err
	}
	target, err := s.read(ctx, s.pool, targetID)
	if err != nil {
		return report, err
	}
	if m.InstanceID != source.InstanceID || m.OwnerID != source.OwnerID || m.Capacity != source.Capacity {
		return report, ErrStoragePolicy
	}
	if err = CheckStorageRestore(source, target); err != nil {
		return report, err
	}
	unlock, err := s.guards(ctx, source, target)
	if err != nil {
		return report, err
	}
	defer unlock()
	tx, states, err := s.lock(ctx, source.ID, target.ID)
	if err != nil {
		return report, err
	}
	defer tx.Rollback(context.Background())
	source, target = states[source.ID], states[target.ID]
	if err = CheckStorageRestore(source, target); err != nil {
		return report, err
	}
	if !apply {
		return report, nil
	}
	intent := assetIntent{SchemaVersion: 1, Action: "RESTORE", SourceAssetID: source.ID, BackupSHA: proof.SHA256}
	if target.Status == "RESTORING" {
		var existing assetIntent
		if webapi.StrictUnmarshal(target.Intent, &existing) != nil || existing.SchemaVersion != 1 || existing.Action != "RESTORE" || existing.SourceAssetID != source.ID || existing.BackupSHA != proof.SHA256 {
			return report, ErrStoragePolicy
		}
		p, err := os.Open(preimage)
		if err != nil {
			return report, ErrStoragePolicy
		}
		defer p.Close()
		preInfo, err := p.Stat()
		if err != nil || !preInfo.Mode().IsRegular() || preInfo.Mode().Perm()&0077 != 0 || privateDirectory(filepath.Dir(preimage)) != nil {
			return report, ErrStoragePolicy
		}
		pm, pp, err := OpenArchive(io.Discard, p, key)
		if err != nil || pm.AssetID != target.ID || pm.InstanceID != target.InstanceID || pm.OwnerID != target.OwnerID || pp.SHA256 != existing.PreimageSHA {
			return report, ErrStoragePolicy
		}
		intent = existing
		report.Preimage = pp
	} else {
		if err = s.host.InspectAsset(ctx, source.Plan); err != nil {
			return report, err
		}
		if err = s.host.InspectAsset(ctx, target.Plan); err != nil {
			return report, err
		}
		pp, err := s.writeBackup(ctx, target, preimage, key)
		if err != nil {
			return report, err
		}
		report.Preimage = pp
		intent.PreimageSHA = pp.SHA256
		raw, _ := json.Marshal(intent)
		if _, err = tx.Exec(ctx, `UPDATE storage_assets SET status='RESTORING',maintenance_intent=$2 WHERE id=$1`, target.ID, raw); err != nil {
			return report, err
		}
		if _, err = tx.Exec(ctx, `UPDATE instances SET version=version+1,access_generation=access_generation+1,updated_at=clock_timestamp() WHERE id=$1`, target.InstanceID); err != nil {
			return report, err
		}
		if _, err = tx.Exec(ctx, `UPDATE access_routes SET enabled=false WHERE instance_id=$1`, target.InstanceID); err != nil {
			return report, err
		}
		if _, err = tx.Exec(ctx, `UPDATE access_sessions SET revoked_at=coalesce(revoked_at,clock_timestamp()) WHERE instance_id=$1`, target.InstanceID); err != nil {
			return report, err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM native_sessions WHERE instance_id=$1`, target.InstanceID); err != nil {
			return report, err
		}
		if err = s.audit(ctx, tx, target, "STORAGE_RESTORE_BEGIN", map[string]any{"source_asset_id": source.ID, "target_asset_id": target.ID, "backup_sha256": proof.SHA256, "preimage_sha256": pp.SHA256}); err != nil {
			return report, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return report, err
	}
	if _, err = stage.Seek(0, io.SeekStart); err != nil {
		return report, err
	}
	if err = s.host.RestoreAsset(ctx, target.Plan, source.Plan, proof.SHA256, stage); err != nil {
		return report, err
	}
	if err = s.host.InspectAsset(ctx, target.Plan); err != nil {
		return report, err
	}
	final, states, err := s.lock(ctx, target.ID)
	if err != nil {
		return report, err
	}
	defer final.Rollback(context.Background())
	current := states[target.ID]
	var saved assetIntent
	if current.ActiveOperation || current.Observed != "STOPPED" || current.Desired != "STOPPED" || current.Status != "RESTORING" || webapi.StrictUnmarshal(current.Intent, &saved) != nil || saved != intent {
		return report, ErrStoragePolicy
	}
	if _, err = final.Exec(ctx, `UPDATE storage_assets SET status='READY',maintenance_intent=NULL WHERE id=$1`, target.ID); err != nil {
		return report, err
	}
	if _, err = final.Exec(ctx, `UPDATE instances SET version=version+1,updated_at=clock_timestamp() WHERE id=$1`, target.InstanceID); err != nil {
		return report, err
	}
	if err = s.audit(ctx, final, target, "STORAGE_RESTORE", map[string]any{"source_asset_id": source.ID, "target_asset_id": target.ID, "backup_sha256": proof.SHA256, "preimage_sha256": intent.PreimageSHA}); err != nil {
		return report, err
	}
	err = final.Commit(ctx)
	report.Applied = err == nil
	return report, err
}

func (s *StorageService) Reclaim(ctx context.Context, id, confirmed, reason string, apply bool) (StorageReport, error) {
	report := StorageReport{AssetID: id}
	if !s.valid() || !uuid.MatchString(id) || !keyName.MatchString(reason) || (apply && confirmed != id) {
		return report, ErrInput
	}
	a, err := s.read(ctx, s.pool, id)
	if err != nil {
		return report, err
	}
	unlock, err := s.guards(ctx, a)
	if err != nil {
		return report, err
	}
	defer unlock()
	tx, states, err := s.lock(ctx, id)
	if err != nil {
		return report, err
	}
	defer tx.Rollback(context.Background())
	a = states[id]
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return report, err
	}
	if err = CheckReclaim(a, now); err != nil {
		return report, err
	}
	if !apply {
		return report, nil
	}
	intent := assetIntent{SchemaVersion: 1, Action: "RECLAIM", Reason: reason}
	if a.Status == "RECLAIM_PENDING" {
		var existing assetIntent
		if webapi.StrictUnmarshal(a.Intent, &existing) != nil || existing != intent {
			return report, ErrStoragePolicy
		}
	} else {
		raw, _ := json.Marshal(intent)
		if _, err = tx.Exec(ctx, `UPDATE storage_assets SET status='RECLAIM_PENDING',maintenance_intent=$2 WHERE id=$1`, id, raw); err != nil {
			return report, err
		}
		if err = s.audit(ctx, tx, a, "STORAGE_RECLAIM_BEGIN", map[string]any{"source_asset_id": a.ID, "reason_code": reason}); err != nil {
			return report, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return report, err
	}
	if err = s.host.ReclaimAsset(ctx, a.Plan); err != nil {
		return report, err
	}
	final, states, err := s.lock(ctx, id)
	if err != nil {
		return report, err
	}
	defer final.Rollback(context.Background())
	current := states[id]
	if err = CheckReclaim(current, now); err != nil {
		return report, err
	}
	var saved assetIntent
	if current.Status != "RECLAIM_PENDING" || webapi.StrictUnmarshal(current.Intent, &saved) != nil || saved != intent {
		return report, ErrStoragePolicy
	}
	if _, err = final.Exec(ctx, `UPDATE storage_assets SET status='DELETED',maintenance_intent=NULL WHERE id=$1`, id); err != nil {
		return report, err
	}
	if _, err = final.Exec(ctx, `UPDATE quota_reservations SET status='RELEASED',updated_at=clock_timestamp() WHERE instance_id=$1 AND resource_type='STORAGE' AND status IN ('HELD','USED')`, a.InstanceID); err != nil {
		return report, err
	}
	if _, err = final.Exec(ctx, `UPDATE quota_policies SET version=version+1 WHERE org_id=$1`, a.OrganizationID); err != nil {
		return report, err
	}
	if err = s.audit(ctx, final, a, "STORAGE_RECLAIM", map[string]any{"source_asset_id": a.ID, "reason_code": reason}); err != nil {
		return report, err
	}
	err = final.Commit(ctx)
	report.Applied = err == nil
	return report, err
}
