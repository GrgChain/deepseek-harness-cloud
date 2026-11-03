package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

var ErrDailyBackup = errors.New("daily backup incomplete; previous verified backups remain protected; inspect private pending set and operator diagnostics")

var dailyName = regexp.MustCompile(`^day-([0-9]{4}-[0-9]{2}-[0-9]{2})-([a-f0-9-]{36})$`)

var dailyHash = regexp.MustCompile(`^[a-f0-9]{64}$`)

type DailySnapshot struct {
	OrganizationID string   `json:"organization_id"`
	Database       string   `json:"database"`
	StateSHA256    string   `json:"state_sha256"`
	Assets         []string `json:"assets,omitempty"`
	Held           bool     `json:"held"`
}

type DailyBackupSource interface {
	Snapshot(context.Context) (DailySnapshot, error)
	Database(context.Context, string, []byte) (ArchiveProof, error)
	Asset(context.Context, string, string, []byte) (ArchiveProof, error)
	Audit(context.Context, string, string, any) error
	RetentionGuard(context.Context, func() error) error
}

type DailyBackupReport struct {
	LocalOnly bool         `json:"local_only"`
	Applied   bool         `json:"applied"`
	Replayed  bool         `json:"replayed"`
	LegalHold bool         `json:"legal_hold"`
	Assets    int          `json:"assets"`
	Retained  int          `json:"retained"`
	Pruned    int          `json:"pruned"`
	Day       string       `json:"day"`
	SetID     string       `json:"set_id,omitempty"`
	Proof     ArchiveProof `json:"proof"`
}

type DailyBackup struct {
	source DailyBackupSource
	root   string
	key    []byte
	now    func() time.Time
}

func NewDailyBackup(source DailyBackupSource, directory string, key []byte) *DailyBackup {
	return &DailyBackup{source: source, root: directory, key: key, now: time.Now}
}

func validDailySnapshot(p DailySnapshot) bool {
	if !uuid.MatchString(p.OrganizationID) || !databaseName.MatchString(p.Database) || !dailyHash.MatchString(p.StateSHA256) || len(p.Assets) > 1000 {
		return false
	}
	for i, id := range p.Assets {
		if !uuid.MatchString(id) || (i > 0 && p.Assets[i-1] >= id) {
			return false
		}
	}
	return true
}

func (s *DailyBackup) Run(ctx context.Context, apply bool) (DailyBackupReport, error) {
	report := DailyBackupReport{LocalOnly: true}
	if s == nil || s.source == nil || s.now == nil || privateDirectory(s.root) != nil || (apply && len(s.key) != 32) {
		return report, ErrInput
	}
	before, err := s.source.Snapshot(ctx)
	if err != nil || !validDailySnapshot(before) {
		return report, ErrDailyBackup
	}
	report.Assets, report.LegalHold = len(before.Assets), before.Held
	now := s.now().UTC()
	report.Day = now.Format("2006-01-02")
	if !apply {
		if err := s.binding(before, false); err != nil {
			return report, err
		}
		return report, nil
	}
	lock, err := os.OpenFile(filepath.Join(s.root, ".lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return report, ErrDailyBackup
	}
	defer lock.Close()
	info, err := lock.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return report, ErrDailyBackup
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return report, ErrDailyBackup
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	if s.binding(before, true) != nil {
		return report, ErrDailyBackup
	}
	completed, pruning, err := s.scan(before)
	if err != nil {
		return report, err
	}
	for _, set := range completed {
		if set.index.Day > report.Day {
			return report, ErrDailyBackup
		}
	}
	for _, set := range completed {
		if set.index.Day == report.Day {
			report.Replayed = true
			report.SetID = set.name
			report.Proof = set.proof
			report.Retained = len(completed)
			return s.finishRetention(ctx, report, completed, pruning)
		}
	}
	id := webapi.ID()
	pending := "pending-" + id
	if err = os.Mkdir(filepath.Join(s.root, pending), 0700); err != nil {
		return report, ErrDailyBackup
	}
	index := dailyIndex{SchemaVersion: 1, ID: id, Day: report.Day, Source: before}
	for _, asset := range before.Assets {
		name := "asset-" + asset + ".hcbk"
		proof, e := s.source.Asset(ctx, asset, filepath.Join(s.root, pending, name), s.key)
		if e != nil {
			return report, ErrDailyBackup
		}
		index.Files = append(index.Files, dailyFile{Name: name, AssetID: asset, Proof: proof})
	}
	proof, err := s.source.Database(ctx, filepath.Join(s.root, pending, "database.hcbk"), s.key)
	if err != nil {
		return report, ErrDailyBackup
	}
	index.Files = append([]dailyFile{{Name: "database.hcbk", Proof: proof}}, index.Files...)
	if s.now().UTC().Format("2006-01-02") != report.Day {
		return report, ErrDailyBackup
	}
	after, err := s.source.Snapshot(ctx)
	a, _ := json.Marshal(before)
	c, _ := json.Marshal(after)
	if err != nil || !bytes.Equal(a, c) {
		return report, ErrDailyBackup
	}
	raw, err := json.Marshal(index)
	if err != nil {
		return report, ErrDailyBackup
	}
	file, err := os.OpenFile(filepath.Join(s.root, pending, "set.hcbk"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return report, ErrDailyBackup
	}
	setProof, err := SealArchive(file, bytes.NewReader(raw), s.key, BackupManifest{SchemaVersion: 1, Kind: "backup-set", OrganizationID: before.OrganizationID, Database: before.Database, CreatedAt: time.Now().UTC(), BackupSetID: id})
	syncErr := file.Sync()
	closeErr := file.Close()
	if err != nil || syncErr != nil || closeErr != nil {
		return report, ErrDailyBackup
	}
	name := "day-" + report.Day + "-" + id
	// Verify through the final parser while the entire set is still pending.
	if syncDirectory(filepath.Join(s.root, pending)) != nil {
		return report, ErrDailyBackup
	}
	// verifySet accepts a physical directory separately from its final identity.
	verified, err := s.verifyPending(pending, name)
	if err != nil || verified.proof != setProof {
		return report, ErrDailyBackup
	}
	if err = s.source.Audit(ctx, id, "BACKUP_SET_CREATE", map[string]any{"schema_version": 1, "set_id": id, "backup_sha256": setProof.SHA256, "day": report.Day, "asset_count": len(before.Assets), "state_sha256": before.StateSHA256, "local_only": true}); err != nil {
		return report, ErrDailyBackup
	}
	if err = os.Rename(filepath.Join(s.root, pending), filepath.Join(s.root, name)); err != nil {
		return report, ErrDailyBackup
	}
	if syncDirectory(s.root) != nil {
		return report, ErrDailyBackup
	}
	report.Applied, report.SetID, report.Proof = true, name, setProof
	completed = append(completed, verified)
	report.Retained = len(completed)
	return s.finishRetention(ctx, report, completed, pruning)
}
