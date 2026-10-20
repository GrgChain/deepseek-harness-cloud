package config

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Role, HTTPAddress, DatabaseDSN, RedisAddress, RedisUsername, RedisPassword string
	RedisTLS                                                                   bool
}

func ReadPrivateFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("secret requires a private regular file (mode 0600 or 0400)")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("secret file is unreadable")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() || opened.Mode().Perm()&0077 != 0 {
		return nil, errors.New("secret file changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil || len(data) > 65536 {
		return nil, errors.New("secret file is unreadable or too large")
	}
	return data, nil
}

func ReadSecret(path string) (string, error) {
	data, err := ReadPrivateFile(path)
	if err != nil {
		return "", err
	}
	value := strings.TrimRight(string(data), "\r\n")
	if value == "" || strings.ContainsAny(value, "\r\n\x00") {
		return "", errors.New("secret must contain a single nonempty value")
	}
	return value, nil
}

func Load(role string, getenv func(string) string) (Config, error) {
	ports := map[string]string{"api": "18080", "worker": "18081", "gateway": "18082"}
	port, exists := ports[role]
	if !exists {
		return Config{}, errors.New("unknown process role")
	}
	c := Config{Role: role, HTTPAddress: "127.0.0.1:" + port, RedisTLS: true}
	if value := getenv("HC_HTTP_ADDR"); value != "" {
		c.HTTPAddress = value
	}
	if _, port, err := net.SplitHostPort(c.HTTPAddress); err != nil {
		return Config{}, errors.New("HC_HTTP_ADDR must be host:port")
	} else if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return Config{}, errors.New("HC_HTTP_ADDR port is invalid")
	}
	var err error
	c.DatabaseDSN, err = ReadSecret(getenv("HC_DB_DSN_FILE"))
	if err != nil {
		return Config{}, fmt.Errorf("HC_DB_DSN_FILE: %w", err)
	}
	c.RedisAddress = getenv("HC_REDIS_ADDR")
	host, port, err := net.SplitHostPort(c.RedisAddress)
	if err != nil || host == "" {
		return Config{}, errors.New("HC_REDIS_ADDR must be host:port")
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return Config{}, errors.New("HC_REDIS_ADDR port is invalid")
	}
	if value := getenv("HC_REDIS_TLS"); value != "" {
		if value != "true" && value != "false" {
			return Config{}, errors.New("HC_REDIS_TLS must be true or false")
		}
		c.RedisTLS = value == "true"
	}
	if !c.RedisTLS {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return Config{}, errors.New("plaintext Redis requires an explicit loopback address")
		}
	}
	c.RedisUsername = getenv("HC_REDIS_USERNAME")
	if path := getenv("HC_REDIS_PASSWORD_FILE"); path != "" {
		c.RedisPassword, err = ReadSecret(path)
		if err != nil {
			return Config{}, fmt.Errorf("HC_REDIS_PASSWORD_FILE: %w", err)
		}
	}
	return c, nil
}
