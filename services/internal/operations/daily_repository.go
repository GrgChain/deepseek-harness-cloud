package operations

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/config"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

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

func (s *DailyBackup) verifyPending(pending, final string) (dailySet, error) {
	return s.verifySetAt(final, pending, false)
}
