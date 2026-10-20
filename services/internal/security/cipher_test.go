package security

import (
	"bytes"
	"testing"
)

func TestCipherRoundTripAndIsolation(t *testing.T) {
	c, err := NewSecretCipher("test-key", bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	context := Context{Environment: "test", OrganizationID: "org", RecordID: "record", Purpose: "model-key"}
	first, err := c.Encrypt([]byte("sensitive-value"), context)
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.Encrypt([]byte("sensitive-value"), context)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Nonce) != 12 || bytes.Equal(first.Nonce, second.Nonce) {
		t.Fatal("nonce must be random and 96 bits")
	}
	plain, err := c.Decrypt(first, context)
	if err != nil || string(plain) != "sensitive-value" {
		t.Fatal("round trip failed")
	}
	for _, changed := range []Context{
		{Environment: "other", OrganizationID: "org", RecordID: "record", Purpose: "model-key"},
		{Environment: "test", OrganizationID: "other", RecordID: "record", Purpose: "model-key"},
		{Environment: "test", OrganizationID: "org", RecordID: "other", Purpose: "model-key"},
		{Environment: "test", OrganizationID: "org", RecordID: "record", Purpose: "portal-csrf"},
	} {
		if _, err := c.Decrypt(first, changed); err == nil {
			t.Fatal("cross-context decryption allowed")
		}
	}
	first.Ciphertext[0] ^= 1
	if _, err := c.Decrypt(first, context); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
	second.KeyID = "wrong"
	if _, err := c.Decrypt(second, context); err == nil {
		t.Fatal("wrong key identifier accepted")
	}
	second.KeyID = "test-key"
	second.Nonce = nil
	if _, err := c.Decrypt(second, context); err == nil {
		t.Fatal("invalid nonce accepted")
	}
}

func TestCipherRejectsInvalidInputs(t *testing.T) {
	for _, size := range []int{0, 16, 24, 33} {
		if _, err := NewSecretCipher("key", make([]byte, size)); err == nil {
			t.Fatalf("accepted %d-byte key", size)
		}
	}
	c, _ := NewSecretCipher("key", make([]byte, 32))
	if _, err := c.Encrypt([]byte("secret"), Context{}); err == nil {
		t.Fatal("empty AAD context accepted")
	}
	if _, err := c.Encrypt(nil, Context{Environment: "test", OrganizationID: "org", RecordID: "record", Purpose: "shell"}); err == nil {
		t.Fatal("unknown purpose accepted")
	}
}
