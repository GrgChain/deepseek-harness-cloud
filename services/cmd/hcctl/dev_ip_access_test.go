package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/access"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/devlocal"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func TestIPAccessInitializationPreservesExistingDeploymentAndIdentities(t *testing.T) {
	t.Chdir(t.TempDir())
	root, _ := filepath.Abs(".local")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	auth := filepath.Join(root, "auth")
	if err := devlocal.InitAuth(auth); err != nil {
		t.Fatal(err)
	}
	before := map[string][]byte{}
	files, _ := os.ReadDir(auth)
	for _, file := range files {
		before[file.Name()], _ = os.ReadFile(filepath.Join(auth, file.Name()))
	}
	original := access.Options{SchemaVersion: 1, OrganizationID: webapi.ID(), WorkspaceOrganizationID: webapi.ID(), ControlUserID: webapi.ID(), WorkspaceEngine: "embedded", Environment: "local", DataKeyID: "local-v1", DataKeyFile: filepath.Join(auth, "data-key"), PortalOrigin: "https://portal.harness.localhost:42013", Domain: "harness.localhost"}
	path := filepath.Join(root, "access-config.json")
	raw, _ := json.Marshal(original)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HC_PUBLIC_IP", "192.168.1.20")
	for range 2 {
		if err := initLocalIPAccess(); err != nil {
			t.Fatal(err)
		}
		updated, err := access.LoadOptions(path)
		if err != nil {
			t.Fatal(err)
		}
		o := updated.IPAccess
		if o == nil || o.Origin(o.PortalPort) != "https://192.168.1.20:42013" {
			t.Fatal("IP entry not configured")
		}
		certificate, err := tls.LoadX509KeyPair(o.CertificateFile, o.KeyFile)
		if err != nil {
			t.Fatal(err)
		}
		leaf, err := x509.ParseCertificate(certificate.Certificate[0])
		if err != nil || leaf.VerifyHostname(o.Address) != nil {
			t.Fatal("IP certificate does not match configured IP")
		}
		for _, p := range []string{path, o.CertificateFile, o.KeyFile} {
			info, err := os.Stat(p)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatal("IP private file permissions changed")
			}
		}
		updated.IPAccess = nil
		if !reflect.DeepEqual(original, updated) {
			t.Fatal("IP initialization changed existing deployment facts")
		}
		for name, raw := range before {
			after, _ := os.ReadFile(filepath.Join(auth, name))
			if !bytes.Equal(raw, after) {
				t.Fatal("IP initialization replaced existing identity/TLS material")
			}
		}
	}
}
