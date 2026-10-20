package devlocal

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// InitAuth creates a real local OIDC provider configuration and TLS certificate.
// Local workspace owner identifiers are controlled identity fixtures.
func InitAuth(dir string) error {
	if err := os.Mkdir(dir, 0700); err != nil {
		return fmt.Errorf("local identity configuration already exists or cannot be created")
	}
	secret := func() string {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			panic(err)
		}
		return hex.EncodeToString(b)
	}
	id := func() string {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			panic(err)
		}
		b[6] = b[6]&15 | 64
		b[8] = b[8]&63 | 128
		return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Harness Cloud Local Development"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(1, 0, 0), DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true}
	cert, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return err
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	clientSecret := secret()
	org := id()
	files := map[string][]byte{
		"cert.pem":        pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert}),
		"key.pem":         pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}),
		"client-secret":   []byte(clientSecret),
		"organization-id": []byte(org),
	}
	dataKey := make([]byte, 32)
	if _, err := rand.Read(dataKey); err != nil {
		return err
	}
	files["data-key"] = dataKey
	signing, err := rsa.GenerateKey(rand.Reader, 3072)
	if err != nil {
		return err
	}
	signingDER, err := x509.MarshalPKCS8PrivateKey(signing)
	if err != nil {
		return err
	}
	files["signing-key.pem"] = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: signingDER})
	issuer := "https://127.0.0.1:42013/oidc"
	members := []map[string]string{}
	accounts := []map[string]string{}
	for _, role := range []string{"admin", "member", "unmapped"} {
		accountID, password := id(), secret()
		hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
		if err != nil {
			return err
		}
		files[role+"-password"] = []byte(password)
		account := map[string]string{"id": accountID, "email": role + "@harness.local", "name": role, "password_hash": string(hash), "previous_subject": "", "role": "", "workspace_owner_id": ""}
		if role != "unmapped" {
			owner, code := id(), map[string]string{"admin": "ADMIN", "member": "MEMBER"}[role]
			account["role"], account["workspace_owner_id"], account["previous_subject"] = code, owner, accountID
			members = append(members, map[string]string{"issuer": issuer, "subject": accountID, "role": code, "workspace_owner_id": owner})
		}
		accounts = append(accounts, account)
	}
	files["bootstrap-accounts.json"], err = json.MarshalIndent(map[string]any{"schema_version": 1, "organization_id": org, "previous_issuer": issuer, "accounts": accounts}, "", "  ")
	if err != nil {
		return err
	}
	mapping, err := json.MarshalIndent(map[string]any{"schema_version": 1, "organization_id": org, "members": members}, "", "  ")
	if err != nil {
		return err
	}
	files["identity-mapping.json"] = mapping
	files["model-allowlist.json"] = []byte("{\"schema_version\":1,\"endpoints\":[\"https://api.deepseek.com/anthropic\"],\"models\":[\"deepseek-flash\",\"deepseek-v4-pro\"]}\n")
	for name, value := range files {
		if err := os.WriteFile(filepath.Join(dir, name), value, 0600); err != nil {
			return err
		}
	}
	return nil
}
