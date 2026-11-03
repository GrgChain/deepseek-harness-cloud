package operations

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

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
