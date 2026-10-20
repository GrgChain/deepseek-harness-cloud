package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/access"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/config"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/worker"
)

// A separate leaf certificate preserves the existing TLS CA and identity
// mappings. The .localhost site needs no machine-wide DNS/hosts modification.
// Changing runtime authority is allowed only before any live hc_dev instance.
func initLocalAccess(ctx context.Context) error {
	invalid := errors.New("local access initialization requires existing private auth/Worker configuration, hc_dev migration identity and no live instances")
	root, err := filepath.Abs(".local")
	if err != nil {
		return invalid
	}
	path := filepath.Join(root, "access-config.json")
	if _, err = os.Lstat(path); !os.IsNotExist(err) {
		return errors.New("access configuration already exists or cannot be inspected; refusing to overwrite")
	}
	dsn, err := config.ReadSecret(os.Getenv("HC_DB_DSN_FILE"))
	if err != nil {
		return invalid
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Path != "/hc_dev" || u.User == nil || u.User.Username() != "hc_migrator" {
		return invalid
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return invalid
	}
	defer pool.Close()
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM instances WHERE deleted_at IS NULL`).Scan(&count); err != nil || count != 0 {
		return invalid
	}
	workerPath := filepath.Join(root, "worker-config.json")
	o, err := worker.LoadOptions(workerPath)
	if err != nil || o.Environment != "local" {
		return invalid
	}
	org, err := config.ReadSecret(filepath.Join(root, "auth/organization-id"))
	if err != nil {
		return invalid
	}
	caPEM, err := config.ReadPrivateFile(filepath.Join(root, "auth/cert.pem"))
	if err != nil {
		return invalid
	}
	caBlock, _ := pem.Decode(caPEM)
	if caBlock == nil {
		return invalid
	}
	ca, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil || !ca.IsCA {
		return invalid
	}
	keyPEM, err := config.ReadPrivateFile(filepath.Join(root, "auth/key.pem"))
	if err != nil {
		return invalid
	}
	defer clear(keyPEM)
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return invalid
	}
	signer, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		return invalid
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return invalid
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return invalid
	}
	leaf := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Harness Cloud Local Site"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(1, 0, 0), DNSNames: []string{"portal.harness.localhost", "*.harness.localhost"}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	if ca.NotAfter.Before(leaf.NotAfter) {
		leaf.NotAfter = ca.NotAfter
	}
	cert, err := x509.CreateCertificate(rand.Reader, leaf, ca, &key.PublicKey, signer)
	if err != nil {
		return invalid
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return invalid
	}
	defer clear(private)
	directory := filepath.Join(root, "access")
	if err = os.Mkdir(directory, 0700); err != nil {
		return invalid
	}
	writeNew := func(path string, raw []byte) error {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return invalid
		}
		defer f.Close()
		if _, err = f.Write(raw); err != nil {
			return invalid
		}
		return f.Sync()
	}
	if err = writeNew(filepath.Join(directory, "cert.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert})); err != nil {
		return err
	}
	if err = writeNew(filepath.Join(directory, "key.pem"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})); err != nil {
		return err
	}
	a := access.Options{SchemaVersion: 1, OrganizationID: org, WorkspaceOrganizationID: o.WorkspaceOrganizationID, ControlUserID: o.ControlUserID, WorkspaceEngine: o.WorkspaceEngine, Environment: o.Environment, DataKeyID: o.DataKeyID, DataKeyFile: o.DataKeyFile, PortalOrigin: "https://portal.harness.localhost:42013", Domain: "harness.localhost"}
	raw, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return invalid
	}
	o.Host.Domain = a.Domain
	o.Host.PublicPort = 42013
	updated, err := json.MarshalIndent(o, "", "  ")
	if err != nil {
		return invalid
	}
	if err = os.WriteFile(workerPath, updated, 0600); err != nil {
		return invalid
	}
	return writeNew(path, raw)
}
