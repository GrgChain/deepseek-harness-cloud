package operations

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type dailyFixtureSource struct {
	snapshot                           DailySnapshot
	calls                              int
	failAsset, failAudit, change, hold bool
	failAction                         string
}

func (s *dailyFixtureSource) Snapshot(context.Context) (DailySnapshot, error) {
	s.calls++
	p := s.snapshot
	p.Held = s.hold
	if s.change && s.calls%2 == 0 {
		p.StateSHA256 = "changed"
	}
	return p, nil
}
func (s *dailyFixtureSource) Database(_ context.Context, path string, key []byte) (ArchiveProof, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return ArchiveProof{}, err
	}
	defer f.Close()
	return SealArchive(f, bytes.NewBufferString("synthetic database backup"), key, BackupManifest{SchemaVersion: 1, Kind: "postgres", OrganizationID: s.snapshot.OrganizationID, Database: s.snapshot.Database, CreatedAt: time.Now().UTC()})
}
func (s *dailyFixtureSource) Asset(_ context.Context, id, path string, key []byte) (ArchiveProof, error) {
	if s.failAsset {
		return ArchiveProof{}, errors.New("controlled asset failure")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return ArchiveProof{}, err
	}
	defer f.Close()
	return SealArchive(f, bytes.NewBufferString("synthetic storage backup"), key, BackupManifest{SchemaVersion: 1, Kind: "storage", OrganizationID: s.snapshot.OrganizationID, AssetID: id, InstanceID: "00000000-0000-4000-8000-000000000004", OwnerID: "00000000-0000-4000-8000-000000000005", Capacity: 10 << 30, CreatedAt: time.Now().UTC()})
}
func (s *dailyFixtureSource) Audit(_ context.Context, _ string, action string, _ any) error {
	if s.failAudit || s.failAction == action {
		return errors.New("controlled audit failure")
	}
	return nil
}
func (s *dailyFixtureSource) RetentionGuard(_ context.Context, action func() error) error {
	if s.hold {
		return ErrDailyBackup
	}
	return action()
}

func TestDailyPruneResumesDurableIntentOnSameDay(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repository")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	source := &dailyFixtureSource{snapshot: DailySnapshot{OrganizationID: "00000000-0000-4000-8000-000000000001", Database: "hc_test_daily", StateSHA256: "0000000000000000000000000000000000000000000000000000000000000000"}}
	runner := NewDailyBackup(source, root, bytes.Repeat([]byte{27}, 32))
	now := time.Now().UTC().Add(-7 * 24 * time.Hour)
	runner.now = func() time.Time { return now }
	ctx := context.Background()
	for range 7 {
		if _, err := runner.Run(ctx, true); err != nil {
			t.Fatal(err)
		}
		now = now.Add(24 * time.Hour)
	}
	source.failAction = "BACKUP_SET_PRUNE"
	if _, err := runner.Run(ctx, true); err == nil {
		t.Fatal("late prune failure reported success")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	markers := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "pruning-") {
			markers++
		}
	}
	if markers != 1 {
		t.Fatal("late prune failure lost durable intent")
	}
	source.failAction = ""
	replay, err := runner.Run(ctx, true)
	if err != nil || !replay.Replayed || replay.Retained != 7 {
		t.Fatal("same-day replay did not resume pruning", err)
	}
	entries, err = os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "pruning-") {
			t.Fatal("completed pruning intent remained")
		}
	}
}
func TestLocalDailyBackupOnlyPrunesVerifiedCompletedDays(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repository")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	source := &dailyFixtureSource{snapshot: DailySnapshot{OrganizationID: "00000000-0000-4000-8000-000000000001", Database: "hc_test_daily", StateSHA256: "0000000000000000000000000000000000000000000000000000000000000000", Assets: []string{"00000000-0000-4000-8000-000000000003"}}}
	key := bytes.Repeat([]byte{27}, 32)
	runner := NewDailyBackup(source, root, key)
	now := time.Now().UTC().Add(-8 * 24 * time.Hour)
	runner.now = func() time.Time { return now }
	ctx := context.Background()
	if p, err := runner.Run(ctx, false); err != nil || p.Applied || p.Assets != 1 {
		t.Fatal("daily preview failed", err)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Fatal("preview wrote repository state")
	}
	var first string
	for day := 0; day < 7; day++ {
		r, err := runner.Run(ctx, true)
		if err != nil || !r.Applied || r.Retained != day+1 || r.Pruned != 0 {
			t.Fatal("complete daily set failed", day, err)
		}
		if day == 0 {
			first = r.SetID
		}
		now = now.Add(24 * time.Hour)
	}
	assertFirst := func() {
		t.Helper()
		if _, err := os.Stat(filepath.Join(root, first)); err != nil {
			t.Fatal("old backup pruned before a complete replacement")
		}
	}
	source.failAsset = true
	if _, err := runner.Run(ctx, true); err == nil {
		t.Fatal("partial set marked complete")
	}
	assertFirst()
	source.failAsset = false
	source.failAudit = true
	if _, err := runner.Run(ctx, true); err == nil {
		t.Fatal("failed completion audit marked complete")
	}
	assertFirst()
	source.failAudit = false
	source.change = true
	source.calls = 0
	if _, err := runner.Run(ctx, true); err == nil {
		t.Fatal("changing product snapshot published as consistent")
	}
	assertFirst()
	source.change = false
	source.hold = true
	held, err := runner.Run(ctx, true)
	if err != nil || held.Retained != 8 || held.Pruned != 0 || !held.LegalHold {
		t.Fatal("legal hold did not preserve old sets", err)
	}
	assertFirst()
	replay, err := runner.Run(ctx, true)
	if err != nil || !replay.Replayed || replay.SetID != held.SetID {
		t.Fatal("same-day replay duplicated backup", err)
	}
	source.hold = false
	now = now.Add(24 * time.Hour)
	last, err := runner.Run(ctx, true)
	if err != nil || last.Retained != 7 || last.Pruned != 2 {
		t.Fatal("seven completed days retention failed", err)
	}
	if _, err := os.Stat(filepath.Join(root, first)); !os.IsNotExist(err) {
		t.Fatal("expired owned verified backup was not reclaimed")
	}
	now = now.Add(-24 * time.Hour)
	if _, err := runner.Run(ctx, true); err == nil {
		t.Fatal("clock rollback replay ignored a future completed set")
	}
	now = now.Add(24 * time.Hour)
	// A ciphertext mutation blocks the next run and every deletion, even if
	// other complete sets still verify. It is never silently excluded.
	path := filepath.Join(root, last.SetID, "database.hcbk")
	f, err := os.OpenFile(path, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteAt([]byte{0}, 0)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(24 * time.Hour)
	if _, err := runner.Run(ctx, true); err == nil {
		t.Fatal("corrupted retained archive ignored")
	}
}

func TestDailyRepositoryRejectsUnownedPathsAndBindings(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	source := &dailyFixtureSource{snapshot: DailySnapshot{OrganizationID: "00000000-0000-4000-8000-000000000001", Database: "hc_test_daily", StateSHA256: "0000000000000000000000000000000000000000000000000000000000000000"}}
	key := bytes.Repeat([]byte{27}, 32)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(root, "unrelated"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewDailyBackup(source, root, key).Run(ctx, true); err == nil {
		t.Fatal("nonempty unowned directory adopted")
	}
	if _, err := os.Stat(filepath.Join(root, "unrelated")); err != nil {
		t.Fatal("unrelated data deleted")
	}
	owned := filepath.Join(parent, "owned")
	if err := os.Mkdir(owned, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := NewDailyBackup(source, owned, key).Run(ctx, true); err != nil {
		t.Fatal(err)
	}
	source.snapshot.Database = "other"
	if _, err := NewDailyBackup(source, owned, key).Run(ctx, true); err == nil {
		t.Fatal("another database adopted repository")
	}
	source.snapshot.Database = "hc_test_daily"
	symlink := filepath.Join(parent, "symlink")
	if err := os.Symlink(owned, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := NewDailyBackup(source, symlink, key).Run(ctx, true); err == nil {
		t.Fatal("symlink repository accepted")
	}
	if err := os.Chmod(owned, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := NewDailyBackup(source, owned, key).Run(ctx, true); err == nil {
		t.Fatal("public repository accepted")
	}
}
