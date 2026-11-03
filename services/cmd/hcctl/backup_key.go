package main

import (
	"bytes"
	"errors"
	"os"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/config"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func backupKey() ([]byte, error) {
	key, err := config.ReadPrivateFile(os.Getenv("HC_BACKUP_KEY_FILE"))
	if err != nil || len(key) != 32 {
		return nil, errors.New("HC_BACKUP_KEY_FILE requires a private independent 32-byte key")
	}
	path := os.Getenv("HC_DATA_KEY_FILE")
	if path == "" {
		clear(key)
		return nil, errors.New("HC_DATA_KEY_FILE is required to verify backup-key separation")
	}
	if path != "" {
		data, err := config.ReadPrivateFile(path)
		same := bytes.Equal(key, data)
		clear(data)
		if err != nil || same {
			clear(key)
			return nil, errors.New("backup key must be independent from data encryption keys")
		}
	}
	if ringPath := os.Getenv("HC_DATA_KEYRING_FILE"); ringPath != "" {
		raw, err := config.ReadPrivateFile(ringPath)
		var ring struct {
			Schema int               `json:"schema_version"`
			Keys   map[string]string `json:"keys"`
		}
		if err != nil || webapi.StrictUnmarshal(raw, &ring) != nil || ring.Schema != 1 || len(ring.Keys) == 0 || len(ring.Keys) > 16 {
			clear(key)
			return nil, errors.New("invalid data keyring for backup separation")
		}
		for _, path := range ring.Keys {
			value, err := config.ReadPrivateFile(path)
			same := bytes.Equal(key, value)
			clear(value)
			if err != nil || same {
				clear(key)
				return nil, errors.New("backup key must be independent from all data keyring entries")
			}
		}
	}
	return key, nil
}
