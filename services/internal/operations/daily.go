package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/config"
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

type dailyBinding struct {
	SchemaVersion  int    `json:"schema_version"`
	OrganizationID string `json:"organization_id"`
	Database       string `json:"database"`
}
type dailyFile struct {
	Name    string       `json:"name"`
	AssetID string       `json:"asset_id,omitempty"`
	Proof   ArchiveProof `json:"proof"`
}
type dailyIndex struct {
	SchemaVersion int           `json:"schema_version"`
	ID            string        `json:"id"`
	Day           string        `json:"day"`
	Source        DailySnapshot `json:"source"`
	Files         []dailyFile   `json:"files"`
}
type dailySet struct {
	name  string
	index dailyIndex
	proof ArchiveProof
}
type boundedIndex struct{ bytes.Buffer }

func (b *boundedIndex) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 2<<20 {
		return 0, ErrDailyBackup
	}
	return b.Buffer.Write(p)
}

func privateArchive(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, ErrDailyBackup
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		f.Close()
		return nil, ErrDailyBackup
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Uid != uint32(os.Geteuid()) {
		f.Close()
		return nil, ErrDailyBackup
	}
	return f, nil
}
func exclusiveJSON(path string, value any) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err = json.NewEncoder(f).Encode(value); err != nil {
		return err
	}
	return f.Sync()
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
func (s *DailyBackup) binding(p DailySnapshot, apply bool) error {
	expected := dailyBinding{SchemaVersion: 1, OrganizationID: p.OrganizationID, Database: p.Database}
	path := filepath.Join(s.root, "repository.json")
	_, statErr := os.Lstat(path)
	if os.IsNotExist(statErr) {
		entries, e := os.ReadDir(s.root)
		if e != nil {
			return ErrDailyBackup
		}
		for _, entry := range entries {
			if entry.Name() != ".lock" {
				return ErrDailyBackup
			}
		}
		if !apply {
			return nil
		}
		if exclusiveJSON(path, expected) != nil || syncDirectory(s.root) != nil {
			return ErrDailyBackup
		}
		return nil
	}
	raw, err := config.ReadPrivateFile(path)
	var got dailyBinding
	if err != nil || webapi.StrictUnmarshal(raw, &got) != nil || got != expected {
		return ErrDailyBackup
	}
	return nil
}
func (s *DailyBackup) verifySet(name string, missing bool) (dailySet, error) {
	return s.verifySetAt(name, name, missing)
}
func (s *DailyBackup) verifySetAt(name, directory string, missing bool) (dailySet, error) {
	set := dailySet{name: name}
	parts := dailyName.FindStringSubmatch(strings.TrimPrefix(name, "pruning-"))
	if len(parts) != 3 || !uuid.MatchString(parts[2]) || privateDirectory(filepath.Join(s.root, directory)) != nil {
		return set, ErrDailyBackup
	}
	f, err := privateArchive(filepath.Join(s.root, directory, "set.hcbk"))
	if err != nil {
		return set, ErrDailyBackup
	}
	var body boundedIndex
	manifest, proof, err := OpenArchive(&body, f, s.key)
	f.Close()
	if err != nil || manifest.Kind != "backup-set" || manifest.BackupSetID != parts[2] || webapi.StrictUnmarshal(body.Bytes(), &set.index) != nil {
		return set, ErrDailyBackup
	}
	i := set.index
	if i.SchemaVersion != 1 || i.ID != parts[2] || i.Day != parts[1] || !validDailySnapshot(i.Source) || manifest.OrganizationID != i.Source.OrganizationID || manifest.Database != i.Source.Database || len(i.Files) != len(i.Source.Assets)+1 {
		return set, ErrDailyBackup
	}
	seen := map[string]bool{"set.hcbk": true}
	for index, item := range i.Files {
		expected := "database.hcbk"
		asset := ""
		if index > 0 {
			asset = i.Source.Assets[index-1]
			expected = "asset-" + asset + ".hcbk"
		}
		if item.Name != expected || item.AssetID != asset || !dailyHash.MatchString(item.Proof.SHA256) || !dailyHash.MatchString(item.Proof.PlainSHA256) {
			return set, ErrDailyBackup
		}
		seen[expected] = true
		path := filepath.Join(s.root, directory, expected)
		if missing {
			if _, e := os.Lstat(path); os.IsNotExist(e) {
				continue
			}
		}
		f, e := privateArchive(path)
		if e != nil {
			return set, ErrDailyBackup
		}
		m, p, e := OpenArchive(io.Discard, f, s.key)
		f.Close()
		if e != nil || p != item.Proof || m.OrganizationID != i.Source.OrganizationID || (index == 0 && (m.Kind != "postgres" || m.Database != i.Source.Database)) || (index > 0 && (m.Kind != "storage" || m.AssetID != asset)) {
			return set, ErrDailyBackup
		}
	}
	entries, err := os.ReadDir(filepath.Join(s.root, directory))
	if err != nil {
		return set, ErrDailyBackup
	}
	for _, entry := range entries {
		if !seen[entry.Name()] {
			return set, ErrDailyBackup
		}
	}
	set.proof = proof
	return set, nil
}

func (s *DailyBackup) scan(p DailySnapshot) ([]dailySet, []dailySet, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil || len(entries) > 1000 {
		return nil, nil, ErrDailyBackup
	}
	var completed, pruning []dailySet
	days := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if name == "repository.json" || name == ".lock" {
			continue
		}
		if strings.HasPrefix(name, "pending-") {
			if !uuid.MatchString(strings.TrimPrefix(name, "pending-")) || privateDirectory(filepath.Join(s.root, name)) != nil {
				return nil, nil, ErrDailyBackup
			}
			continue
		}
		isPruning := strings.HasPrefix(name, "pruning-")
		if isPruning {
			// A crash between the final marker unlink and rmdir leaves only this
			// generated empty tombstone. It never authorizes deleting other data.
			if files, e := os.ReadDir(filepath.Join(s.root, name)); e == nil && len(files) == 0 && dailyName.MatchString(strings.TrimPrefix(name, "pruning-")) && privateDirectory(filepath.Join(s.root, name)) == nil {
				pruning = append(pruning, dailySet{name: name})
				continue
			}
		}
		set, e := s.verifySet(name, isPruning)
		if e != nil || set.index.Source.OrganizationID != p.OrganizationID || set.index.Source.Database != p.Database {
			return nil, nil, ErrDailyBackup
		}
		if isPruning {
			pruning = append(pruning, set)
			continue
		}
		if days[set.index.Day] {
			return nil, nil, ErrDailyBackup
		}
		days[set.index.Day] = true
		completed = append(completed, set)
	}
	sort.Slice(completed, func(i, j int) bool { return completed[i].index.Day < completed[j].index.Day })
	return completed, pruning, nil
}
func (s *DailyBackup) prune(ctx context.Context, set dailySet) error {
	return s.source.RetentionGuard(ctx, func() error { return s.pruneFiles(ctx, set) })
}
func (s *DailyBackup) pruneFiles(ctx context.Context, set dailySet) error {
	if len(set.index.Files) == 0 {
		return os.Remove(filepath.Join(s.root, set.name))
	}
	if !strings.HasPrefix(set.name, "pruning-") {
		if err := s.source.Audit(ctx, set.index.ID, "BACKUP_SET_PRUNE_ACCEPTED", map[string]any{"schema_version": 1, "set_id": set.index.ID, "backup_sha256": set.proof.SHA256}); err != nil {
			return ErrDailyBackup
		}
		newName := "pruning-" + set.name
		if err := os.Rename(filepath.Join(s.root, set.name), filepath.Join(s.root, newName)); err != nil {
			return ErrDailyBackup
		}
		set.name = newName
		if syncDirectory(s.root) != nil {
			return ErrDailyBackup
		}
	}
	for _, file := range set.index.Files {
		if err := os.Remove(filepath.Join(s.root, set.name, file.Name)); err != nil && !os.IsNotExist(err) {
			return ErrDailyBackup
		}
	}
	if syncDirectory(filepath.Join(s.root, set.name)) != nil {
		return ErrDailyBackup
	}
	if err := s.source.Audit(ctx, set.index.ID, "BACKUP_SET_PRUNE", map[string]any{"schema_version": 1, "set_id": set.index.ID, "backup_sha256": set.proof.SHA256}); err != nil {
		return ErrDailyBackup
	}
	if err := os.Remove(filepath.Join(s.root, set.name, "set.hcbk")); err != nil {
		return ErrDailyBackup
	}
	if err := os.Remove(filepath.Join(s.root, set.name)); err != nil {
		return ErrDailyBackup
	}
	return syncDirectory(s.root)
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

func (s *DailyBackup) finishRetention(ctx context.Context, report DailyBackupReport, completed, pruning []dailySet) (DailyBackupReport, error) {
	if report.LegalHold || len(completed) <= 7 && len(pruning) == 0 {
		return report, nil
	}
	if len(completed) < 7 {
		return report, ErrDailyBackup
	}
	latest, err := s.source.Snapshot(ctx)
	if err != nil || latest.Held {
		return report, ErrDailyBackup
	}
	for _, set := range pruning {
		if set.index.Day != "" && set.index.Day >= completed[len(completed)-7].index.Day {
			return report, ErrDailyBackup
		}
		if err = s.prune(ctx, set); err != nil {
			return report, err
		}
	}
	if len(completed) > 7 {
		for _, set := range completed[:len(completed)-7] {
			if err = s.prune(ctx, set); err != nil {
				return report, err
			}
			report.Pruned++
			report.Retained--
		}
	}
	return report, nil
}

func (s *DailyBackup) verifyPending(pending, final string) (dailySet, error) {
	return s.verifySetAt(final, pending, false)
}
