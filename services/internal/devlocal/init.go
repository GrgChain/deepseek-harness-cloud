package devlocal

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Init creates isolated development credentials. It never resets existing data.
func Init(dir string) error {
	if err := os.Mkdir(dir, 0700); err != nil {
		return errors.New("local directory already exists or cannot be created; credentials were not changed")
	}
	password := func() (string, error) {
		b := make([]byte, 32)
		_, err := rand.Read(b)
		return hex.EncodeToString(b), err
	}
	pgPassword, err := password()
	if err != nil {
		return err
	}
	redisPassword, err := password()
	if err != nil {
		return err
	}
	files := map[string]string{
		"postgres-password": pgPassword,
		"redis-password":    redisPassword,
		"migrator-dsn":      fmt.Sprintf("postgres://hc_migrator:%s@127.0.0.1:55432/hc_dev?sslmode=disable", pgPassword),
		"redis.conf":        fmt.Sprintf("bind 0.0.0.0\nprotected-mode yes\nrequirepass %s\nmaxmemory 256mb\nmaxmemory-policy noeviction\nsave \"\"\nappendonly no\n", redisPassword),
	}
	bootstrapSQL := "REVOKE CREATE ON SCHEMA public FROM PUBLIC;\n"
	for _, role := range []string{"api", "worker", "gateway", "model_gateway"} {
		secret, err := password()
		if err != nil {
			return err
		}
		files[role+"-dsn"] = fmt.Sprintf("postgres://hc_%s:%s@127.0.0.1:55432/hc_dev?sslmode=disable", role, secret)
		bootstrapSQL += fmt.Sprintf("CREATE ROLE hc_%s LOGIN PASSWORD '%s';\n", role, secret)
	}
	files["postgres-init.sql"] = bootstrapSQL
	for name, value := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value+"\n"), 0600); err != nil {
			return fmt.Errorf("write private local configuration: %w", err)
		}
	}
	return nil
}
