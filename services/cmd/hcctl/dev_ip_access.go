package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/access"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/config"
)

// Add an IP alias without changing the OIDC issuer, identities, runtime
// authorities, storage manifests, or credentials of existing workspaces.
func initLocalIPAccess() error {
	invalid := errors.New("IP access initialization requires HC_PUBLIC_IP, existing local access configuration and its private TLS CA")
	root, err := filepath.Abs(".local")
	if err != nil {
		return invalid
	}
	path := filepath.Join(root, "access-config.json")
	o, err := access.LoadOptions(path)
	if err != nil || o.Environment != "local" {
		return invalid
	}
	ip := net.ParseIP(os.Getenv("HC_PUBLIC_IP"))
	if ip == nil {
		return invalid
	}
	port := func(name string, fallback int) int {
		if value := os.Getenv(name); value != "" {
			n, err := strconv.Atoi(value)
			if err != nil {
				return 0
			}
			return n
		}
		return fallback
	}
	directory := filepath.Join(root, "ip-access")
	opts := &access.IPOptions{Address: ip.String(), PortalPort: port("HC_IP_PORTAL_PORT", 42013),
		CertificateFile: filepath.Join(directory, "cert.pem"), KeyFile: filepath.Join(directory, "key.pem"), PortalUpstream: "https://127.0.0.1:42013", UpstreamCAFile: filepath.Join(root, "auth/cert.pem")}
	if opts.Validate() != nil {
		return invalid
	}
	caPEM, err := config.ReadPrivateFile(opts.UpstreamCAFile)
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
	leaf := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Harness Cloud IP Access"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(1, 0, 0), IPAddresses: []net.IP{ip}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
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
	if err = os.MkdirAll(directory, 0700); err != nil {
		return invalid
	}
	write := func(path string, raw []byte) error {
		f, err := os.CreateTemp(filepath.Dir(path), ".ip-access-*")
		if err != nil {
			return invalid
		}
		defer os.Remove(f.Name())
		if _, err = f.Write(raw); err != nil {
			f.Close()
			return invalid
		}
		if err = f.Sync(); err != nil {
			f.Close()
			return invalid
		}
		if err = f.Close(); err != nil {
			return invalid
		}
		if err = os.Rename(f.Name(), path); err != nil {
			return invalid
		}
		return nil
	}
	if err = write(opts.CertificateFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert})); err != nil {
		return err
	}
	if err = write(opts.KeyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})); err != nil {
		return err
	}
	o.IPAccess = opts
	raw, err := json.MarshalIndent(o, "", "  ")
	if err != nil {
		return invalid
	}
	return write(path, raw)
}
