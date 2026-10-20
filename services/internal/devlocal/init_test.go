package devlocal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitGeneratesPrivateSecretsWithoutOverwriting(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "local")
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"postgres-password", "migrator-dsn", "api-dsn", "worker-dsn", "gateway-dsn", "model_gateway-dsn", "redis-password", "redis.conf", "postgres-init.sql"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("%s must exist with 0600 permissions", name)
		}
	}
	before, _ := os.ReadFile(filepath.Join(dir, "api-dsn"))
	if err := Init(dir); err == nil {
		t.Fatal("second initialization should refuse overwrite")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "api-dsn"))
	if string(before) != string(after) {
		t.Fatal("existing credentials overwritten")
	}
}
