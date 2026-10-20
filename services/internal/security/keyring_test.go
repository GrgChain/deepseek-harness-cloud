package security

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestKeyringReadsOldKeysAndWritesOnlyCurrent(t *testing.T) {
	oldKey, newKey := bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32)
	old, _ := NewSecretCipher("old", oldKey)
	aad := Context{"test", "org", "record", "model-key"}
	e, _ := old.Encrypt([]byte("private fixture"), aad)
	ring, err := NewKeyring("new", map[string][]byte{"old": oldKey, "new": newKey})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := ring.Decrypt(e, aad)
	if err != nil || string(plain) != "private fixture" {
		t.Fatal("old key unreadable")
	}
	updated, err := ring.Encrypt(plain, aad)
	clear(plain)
	if err != nil || updated.KeyID != "new" {
		t.Fatal("new write did not select current key")
	}
	if _, err := old.Decrypt(updated, aad); err == nil {
		t.Fatal("old-only reader accepted new key")
	}
	e.KeyID = "unknown"
	if _, err := ring.Decrypt(e, aad); err == nil {
		t.Fatal("unknown key accepted")
	}
	for _, keys := range []map[string][]byte{{"old": oldKey}, {"new": nil}, {"new": newKey, "bad id": oldKey}} {
		if _, err := NewKeyring("new", keys); err == nil {
			t.Fatal("invalid ring accepted")
		}
	}
}

func TestPrivateKeyringRequiresExactCurrentKeyAndStrictReferences(t *testing.T) {
	dir := t.TempDir()
	key := bytes.Repeat([]byte{3}, 32)
	current, old, file := filepath.Join(dir, "new"), filepath.Join(dir, "old"), filepath.Join(dir, "ring.json")
	for _, name := range []string{current, old} {
		if err := os.WriteFile(name, key, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(file, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	good := `{"schema_version":1,"keys":{"new":"` + current + `","old":"` + old + `"}}`
	write(good)
	if _, err := LoadSecretCipher("new", current, file); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"schema_version":1,"keys":{"new":"relative"}}`,
		`{"schema_version":1,"keys":{"old":"` + old + `"}}`,
		`{"schema_version":1,"keys":{"new":"` + current + `","new":"` + old + `"}}`,
		`{"schema_version":1,"keys":null}`,
		`{"schema_version":2,"keys":{"new":"` + current + `"}}`,
		`{"schema_version":1,"extra":true,"keys":{"new":"` + current + `"}}`,
	} {
		write(body)
		if _, err := LoadSecretCipher("new", current, file); err == nil {
			t.Fatal("invalid ring file accepted")
		}
	}
	write(good)
	if err := os.Chmod(old, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSecretCipher("new", current, file); err == nil {
		t.Fatal("public old key accepted")
	}
	if err := os.Chmod(old, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(current, bytes.Repeat([]byte{4}, 32), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewSecretCipherWithKeyring("new", key, file); err == nil {
		t.Fatal("different current material accepted")
	}
}
