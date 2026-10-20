package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestBackupKeySeparationIncludesRetiredKeys(t *testing.T) {
	dir := t.TempDir()
	current, backup, old, ring := filepath.Join(dir, "current"), filepath.Join(dir, "backup"), filepath.Join(dir, "old"), filepath.Join(dir, "ring.json")
	write := func(path string, value []byte) {
		t.Helper()
		if err := os.WriteFile(path, value, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(current, bytes.Repeat([]byte{1}, 32))
	write(backup, bytes.Repeat([]byte{2}, 32))
	write(old, bytes.Repeat([]byte{3}, 32))
	t.Setenv("HC_DATA_KEY_FILE", current)
	t.Setenv("HC_BACKUP_KEY_FILE", backup)
	t.Setenv("HC_DATA_KEYRING_FILE", "")
	key, err := backupKey()
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	write(backup, bytes.Repeat([]byte{1}, 32))
	if _, err = backupKey(); err == nil {
		t.Fatal("current data key reused for backups")
	}
	write(backup, bytes.Repeat([]byte{3}, 32))
	write(ring, []byte(`{"schema_version":1,"keys":{"current":"`+current+`","old":"`+old+`"}}`))
	t.Setenv("HC_DATA_KEYRING_FILE", ring)
	if _, err = backupKey(); err == nil {
		t.Fatal("retired data key reused for backups")
	}
	write(backup, bytes.Repeat([]byte{2}, 32))
	key, err = backupKey()
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(backup, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = backupKey(); err == nil {
		t.Fatal("public backup key accepted")
	}
}

func TestDestructiveOperationsRequireExplicitMaintenanceAndBoundedInput(t *testing.T) {
	t.Setenv("HC_MAINTENANCE", "false")
	for _, name := range []string{"restore", "storage-restore", "storage-reclaim", "secrets-reencrypt", "dev-runtime-upgrade", "dev-runtime-rollback", "recovery-inventory", "recovery-retain", "recovery-adopt", "recovery-fence-source", "audit-archive", "audit-hold", "backup-daily"} {
		if err := runOperations(name, []string{"--apply"}); err == nil {
			t.Fatal("destructive apply bypassed maintenance", name)
		}
	}
	if err := runOperations("cleanup", []string{"--batch", "501"}); err == nil {
		t.Fatal("unbounded cleanup accepted")
	}
}
