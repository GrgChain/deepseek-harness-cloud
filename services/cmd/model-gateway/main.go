package main

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/config"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/modelgateway"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
)

func run() error {
	o, err := modelgateway.LoadOptions(os.Getenv("HC_MODEL_GATEWAY_CONFIG_FILE"))
	if err != nil {
		return err
	}
	dsn, err := config.ReadSecret(os.Getenv("HC_DB_DSN_FILE"))
	if err != nil {
		return err
	}
	c, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return errors.New("invalid model gateway database")
	}
	c.MaxConns = 5
	c.MinConns = 0
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, c)
	if err != nil {
		return errors.New("model gateway database unavailable")
	}
	defer pool.Close()
	startup, stop := context.WithTimeout(ctx, 10*time.Second)
	defer stop()
	if db.Check(startup, pool) != nil {
		return errors.New("model gateway schema unavailable")
	}
	cipher, err := security.LoadSecretCipher(o.DataKeyID, o.DataKeyFile, o.DataKeyringFile)
	if err != nil {
		return err
	}
	master, err := config.ReadPrivateFile(o.ControlKeyFile)
	if err != nil {
		return err
	}
	defer clear(master)
	maintenance := os.Getenv("HC_MAINTENANCE")
	if maintenance != "" && maintenance != "false" && maintenance != "true" {
		return errors.New("invalid maintenance mode")
	}
	handler, err := modelgateway.NewServer(pool, cipher, o.Environment, master, maintenance == "true", o.CustomAPIBases...)
	if err != nil {
		return err
	}
	cert, err := config.ReadPrivateFile(o.CertificateFile)
	if err != nil {
		return err
	}
	key, err := config.ReadPrivateFile(o.CertificateKeyFile)
	if err != nil {
		return err
	}
	defer clear(key)
	pair, err := tls.X509KeyPair(cert, key)
	if err != nil {
		return errors.New("model gateway TLS unavailable")
	}
	listener, err := net.Listen("tcp", o.Address)
	if err != nil {
		return errors.New("model gateway address unavailable")
	}
	defer listener.Close()
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 125 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192, ErrorLog: log.New(io.Discard, "", 0), TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}}}
	done := make(chan error, 1)
	go func() { done <- server.ServeTLS(listener, "", "") }()
	slog.Info("Platform model gateway started")
	select {
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		shutdown, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		return server.Shutdown(shutdown)
	}
	return nil
}
func main() {
	if run() != nil {
		slog.Error("Platform model gateway stopped; check controlled configuration")
		os.Exit(1)
	}
}
