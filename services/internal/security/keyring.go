package security

import (
	"bytes"
	"errors"
	"path/filepath"
	"regexp"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/config"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

var keyName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var errKeyring = errors.New("keyring requires private absolute key references and matching current key")

func LoadSecretCipher(id, currentFile, ringFile string) (*SecretCipher, error) {
	key, err := config.ReadPrivateFile(currentFile)
	if err != nil || len(key) != 32 {
		return nil, errKeyring
	}
	defer clear(key)
	return NewSecretCipherWithKeyring(id, key, ringFile)
}
func NewSecretCipherWithKeyring(id string, key []byte, ringFile string) (*SecretCipher, error) {
	if ringFile == "" {
		return NewSecretCipher(id, key)
	}
	if !filepath.IsAbs(ringFile) || filepath.Clean(ringFile) != ringFile {
		return nil, errKeyring
	}
	raw, err := config.ReadPrivateFile(ringFile)
	if err != nil {
		return nil, errKeyring
	}
	var spec struct {
		Schema int               `json:"schema_version"`
		Keys   map[string]string `json:"keys"`
	}
	if webapi.StrictUnmarshal(raw, &spec) != nil || spec.Schema != 1 || len(spec.Keys) == 0 || len(spec.Keys) > 16 {
		return nil, errKeyring
	}
	keys := map[string][]byte{}
	defer func() {
		for _, k := range keys {
			clear(k)
		}
	}()
	for name, path := range spec.Keys {
		if !keyName.MatchString(name) || !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return nil, errKeyring
		}
		value, err := config.ReadPrivateFile(path)
		if err != nil || len(value) != 32 {
			clear(value)
			return nil, errKeyring
		}
		keys[name] = value
	}
	if !bytes.Equal(keys[id], key) {
		return nil, errKeyring
	}
	return NewKeyring(id, keys)
}
