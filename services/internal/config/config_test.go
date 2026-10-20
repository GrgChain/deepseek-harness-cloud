package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadSecretRequiresPrivateRegularFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dsn")
	if err := os.WriteFile(path, []byte("postgres://secret@localhost/hc\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadSecret(path)
	if err != nil || got != "postgres://secret@localhost/hc" {
		t.Fatal("private file not loaded")
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSecret(path); err == nil {
		t.Fatal("public secret file accepted")
	}
	if _, err := ReadSecret(dir); err == nil {
		t.Fatal("directory accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSecret(link); err == nil {
		t.Fatal("symlink accepted")
	}
}

func TestLoadBootstrapConfiguration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dsn")
	if err := os.WriteFile(path, []byte("postgres://hc@127.0.0.1:5432/hc?sslmode=disable"), 0600); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{"HC_DB_DSN_FILE": path, "HC_REDIS_ADDR": "127.0.0.1:6379", "HC_REDIS_TLS": "false"}
	load := func(role string) (Config, error) { return Load(role, func(key string) string { return values[key] }) }
	for _, role := range []string{"api", "worker", "gateway"} {
		c, err := load(role)
		if err != nil || c.Role != role {
			t.Fatalf("%s: %v", role, err)
		}
		want := map[string]string{"api": "127.0.0.1:18080", "worker": "127.0.0.1:18081", "gateway": "127.0.0.1:18082"}[role]
		if c.HTTPAddress != want {
			t.Fatalf("%s: listener %s, want %s", role, c.HTTPAddress, want)
		}
	}
	if _, err := load("untrusted"); err == nil {
		t.Fatal("unknown role accepted")
	}
	values["HC_REDIS_ADDR"] = "redis.example:6379"
	if _, err := load("api"); err == nil {
		t.Fatal("plaintext remote redis accepted")
	}
	values["HC_REDIS_TLS"] = "sometimes"
	if _, err := load("api"); err == nil {
		t.Fatal("invalid boolean accepted")
	}
	values["HC_REDIS_TLS"] = "true"
	values["HC_HTTP_ADDR"] = "0.0.0.0:bad"
	if _, err := load("api"); err == nil {
		t.Fatal("invalid listen address accepted")
	}
	delete(values, "HC_HTTP_ADDR")
	values["HC_DB_DSN_FILE"] = ""
	if _, err := load("api"); err == nil {
		t.Fatal("missing database secret accepted")
	}
}
