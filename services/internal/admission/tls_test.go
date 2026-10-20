package admission

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/access"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func TestIngressObservationUsesTrustedTLSAndActualGatewayHostRouting(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "isolated gateway test"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true, DNSNames: []string{"*.harness.localhost"}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}
	port := server.Listener.Addr().String()
	parsed, _ := url.Parse("http://" + port)
	rules, err := access.NewRules("https://portal.harness.localhost:"+parsed.Port(), "harness.localhost")
	if err != nil {
		t.Fatal(err)
	}
	gateway := access.NewGateway(rules, webapi.ID(), nil, nil)
	defer gateway.Close()
	server.Config.Handler = gateway
	server.StartTLS()
	defer server.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	probe, err := newHTTPChecks(rules, "test", "http://127.0.0.1:18081", "http://127.0.0.1:18082", ca)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	if err := probe.ingress(context.Background()); err != nil {
		t.Fatal("valid HTTPS Gateway observation failed")
	}
	wrong, err := newHTTPChecks(rules, "test", "http://127.0.0.1:18081", "http://127.0.0.1:18082", "")
	if err != nil {
		t.Fatal(err)
	}
	defer wrong.Close()
	if wrong.ingress(context.Background()) == nil {
		t.Fatal("untrusted local certificate opened creation")
	}
}
