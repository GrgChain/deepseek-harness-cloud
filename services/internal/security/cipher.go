package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
)

var ErrDecryption = errors.New("credential decryption failed")

type Context struct{ Environment, OrganizationID, RecordID, Purpose string }

func (c Context) aad() ([]byte, error) {
	if c.Environment == "" || c.OrganizationID == "" || c.RecordID == "" {
		return nil, errors.New("credential context is incomplete")
	}
	switch c.Purpose {
	case "model-key", "provider-key", "portal-csrf", "oidc-pkce", "native-cookie", "identity-password-hash", "skill-bundle", "skill-import", "workspace-skills", "context-credential", "context-operation", "context-preview", "connector-credential", "connector-oauth":
	default:
		return nil, errors.New("credential purpose is invalid")
	}
	return json.Marshal([]string{"hc-aad-v1", c.Environment, c.OrganizationID, c.RecordID, c.Purpose})
}

type Envelope struct {
	KeyID             string
	Nonce, Ciphertext []byte
}
type SecretCipher struct {
	keyID   string
	aead    cipher.AEAD
	readers map[string]cipher.AEAD
}

func NewSecretCipher(keyID string, key []byte) (*SecretCipher, error) {
	return NewKeyring(keyID, map[string][]byte{keyID: key})
}

// NewKeyring accepts a bounded deployment key set. New writes always use the
// current key; record key_id selects an immutable decryption key.
func NewKeyring(keyID string, keys map[string][]byte) (*SecretCipher, error) {
	if !keyName.MatchString(keyID) || len(keys) == 0 || len(keys) > 16 {
		return nil, errors.New("AES-256 requires a key identifier and 32-byte key")
	}
	readers := map[string]cipher.AEAD{}
	for id, key := range keys {
		if !keyName.MatchString(id) || len(key) != 32 {
			return nil, errors.New("invalid AES-256 keyring")
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, err
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
		readers[id] = aead
	}
	current, ok := readers[keyID]
	if !ok {
		return nil, errors.New("current key missing from keyring")
	}
	return &SecretCipher{keyID: keyID, aead: current, readers: readers}, nil
}

func (c *SecretCipher) CurrentKeyID() string { return c.keyID }

func (c *SecretCipher) Encrypt(plaintext []byte, context Context) (Envelope, error) {
	aad, err := context.aad()
	if err != nil {
		return Envelope{}, err
	}
	if len(plaintext) == 0 {
		return Envelope{}, errors.New("credential is empty")
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return Envelope{}, err
	}
	return Envelope{KeyID: c.keyID, Nonce: nonce, Ciphertext: c.aead.Seal(nil, nonce, plaintext, aad)}, nil
}

func (c *SecretCipher) Decrypt(envelope Envelope, context Context) ([]byte, error) {
	aad, err := context.aad()
	reader := c.readers[envelope.KeyID]
	if err != nil || reader == nil || len(envelope.Nonce) != reader.NonceSize() {
		return nil, ErrDecryption
	}
	plaintext, err := reader.Open(nil, envelope.Nonce, envelope.Ciphertext, aad)
	if err != nil {
		return nil, ErrDecryption
	}
	return plaintext, nil
}
