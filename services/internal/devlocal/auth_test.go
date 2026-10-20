package devlocal

import (
	"bytes"
	"crypto/tls"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/settings"
	"os"
	"path/filepath"
	"testing"
)

func TestAuthInitializationUsesPrivateRandomCredentialsAndValidTLS(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "auth")
	if err := InitAuth(dir); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("identity file permissions are not private")
		}
	}
	cert, err := tls.LoadX509KeyPair(filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem"))
	if err != nil || len(cert.Certificate) != 1 {
		t.Fatal("invalid local TLS material")
	}
	key, _ := os.ReadFile(filepath.Join(dir, "data-key"))
	if len(key) != 32 {
		t.Fatal("data key must contain exactly 256 bits")
	}
	org, _ := os.ReadFile(filepath.Join(dir, "organization-id"))
	mapping, _ := os.ReadFile(filepath.Join(dir, "identity-mapping.json"))
	if _, err := identity.ParseMapping(mapping, "https://127.0.0.1:42013/oidc", string(org)); err != nil {
		t.Fatal("local identities are not authorized correctly")
	}
	policy, _ := os.ReadFile(filepath.Join(dir, "model-allowlist.json"))
	if _, err := settings.ParsePolicy(policy); err != nil {
		t.Fatal("invalid model policy")
	}
	accounts, _ := os.ReadFile(filepath.Join(dir, "bootstrap-accounts.json"))
	if _, err := identity.ParseAccountImport(accounts, string(org)); err != nil {
		t.Fatal(err)
	}
	a, _ := os.ReadFile(filepath.Join(dir, "admin-password"))
	b, _ := os.ReadFile(filepath.Join(dir, "member-password"))
	if len(a) != 64 || bytes.Equal(a, b) || bytes.Contains(accounts, a) || bytes.Contains(accounts, b) {
		t.Fatal("passwords must be random, distinct and hashed in provider configuration")
	}
	if err := InitAuth(dir); err == nil {
		t.Fatal("refusing overwrite is required")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "admin-password"))
	if !bytes.Equal(a, after) {
		t.Fatal("initialization replaced an existing password")
	}
}
