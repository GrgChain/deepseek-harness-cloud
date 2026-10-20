package worker

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/access"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/httpserver"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/instances"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/pagination"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	workspaceengine "github.com/jonluo94/deepseek-harness-cloud/services/internal/workspace"
)

func verifyRealGateway(t *testing.T, ctx context.Context, ingress *httptest.Server, admin, apiPool, gatewayPool *pgxpool.Pool, cipher *security.SecretCipher, client *workspaceengine.Client, principal identity.Principal, options HostOptions, instance, controlOrg, controlID string) {
	t.Helper()
	portal := "https://portal." + options.Domain + ":" + strconv.Itoa(options.PublicPort)
	rules, err := access.NewRules(portal, options.Domain)
	if err != nil {
		t.Fatal(err)
	}
	ownership := access.NewWorkspaceOwnership(client, controlOrg, controlID)
	native := access.NewNativeVerifier(rules)
	defer native.Close()
	apiAccess := access.NewService(access.NewStore(apiPool, cipher, "test", principal.OrganizationID, rules), ownership, nil)
	gatewayAccess := access.NewService(access.NewStore(gatewayPool, cipher, "test", principal.OrganizationID, rules), ownership, native)
	allow := func(context.Context, string, int) error { return nil } // Limiter is covered independently with real Redis.
	gateway := access.NewGateway(rules, principal.OrganizationID, gatewayAccess, allow)
	defer gateway.Close()
	// Keep both authorities on the same HTTPS site and real Portal bundle.
	frontURL, _ := url.Parse("https://127.0.0.1:42013")
	front := httputil.NewSingleHostReverseProxy(frontURL)
	director := front.Director
	front.Director = func(r *http.Request) { director(r); r.Host = frontURL.Host }
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // Isolated test Vite certificate.
	front.Transport = transport
	defer transport.CloseIdleConnections()
	api := httpserver.New("api", func(context.Context) error { return nil })
	api.WithIdentity(identity.NewHandler(identity.NewService(apiPool, nil, cipher, "test", principal.OrganizationID, portal)))
	codec, _ := pagination.NewCodec(bytes.Repeat([]byte{9}, 32), "test")
	api.WithBusiness(httpserver.BusinessMux{Resources: instances.NewQueryHandler(instances.NewQueryService(apiPool, codec)), Access: access.NewAPIHandler(apiAccess, allow)}, portal)
	portalHost := strings.TrimPrefix(portal, "https://")
	ingress.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != portalHost {
			gateway.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/") || strings.HasPrefix(r.URL.Path, "/auth/") || r.URL.Path == "/livez" || r.URL.Path == "/readyz" {
			api.ServeHTTP(w, r)
			return
		}
		front.ServeHTTP(w, r)
	})
	ingress.Config.ErrorLog = log.New(io.Discard, "", 0)
	ingress.StartTLS()
	// The browser fixture uses an already authenticated BFF session. Real OIDC
	// login has its own tests; no static/model/control-plane key enters browser context.
	secret := func() string {
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	portalSecret, csrf := secret(), secret()
	portalHash, csrfHash := sha256.Sum256([]byte(portalSecret)), sha256.Sum256([]byte(csrf))
	envelope, err := cipher.Encrypt([]byte(csrf), security.Context{Environment: "test", OrganizationID: principal.OrganizationID, RecordID: principal.SessionID, Purpose: "portal-csrf"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, `UPDATE portal_sessions SET secret_sha256=$2,csrf_sha256=$3,csrf_cipher=$4,csrf_nonce=$5,csrf_key_id=$6,last_seen_at=clock_timestamp(),revoked_at=NULL WHERE id=$1`, principal.SessionID, portalHash[:], csrfHash[:], envelope.Ciphertext, envelope.Nonce, envelope.KeyID); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("HC_GATEWAY_BROWSER") != "true" {
		t.Log("real Gateway browser validation requires HC_GATEWAY_BROWSER=true and the local HTTPS Portal")
		return
	}
	fixture := filepath.Join(t.TempDir(), "browser.json")
	raw, _ := json.Marshal(map[string]string{"portal_url": portal, "instance_id": instance, "portal_cookie": portalSecret})
	if err = os.WriteFile(fixture, raw, 0600); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	node := os.Getenv("HC_NODE_EXECUTABLE")
	if node == "" {
		node = "node"
	}
	command := exec.CommandContext(ctx, node, filepath.Join(root, "infrastructure/runtime/tests/fixtures/gateway-browser.mjs"))
	command.Env = append(os.Environ(), "HC_GATEWAY_BROWSER_FIXTURE_FILE="+fixture)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err = command.Run(); err != nil {
		var status struct {
			Stage       string         `json:"stage"`
			Diagnostics map[string]int `json:"diagnostics"`
		}
		if raw, e := os.ReadFile(fixture + ".status.json"); e == nil {
			_ = json.Unmarshal(raw, &status)
		}
		t.Fatalf("real Gateway/Portal browser acceptance failed at stage %q, counters %v; raw diagnostics withheld", status.Stage, status.Diagnostics)
	}
	t.Log("real Portal handoff, DSH HTTP/WS, host cookies, copied ticket rejection and logout socket closure passed; no model inference")
	// Restore only the fixture session for later lifecycle checks, leaving the
	// consumed ticket and revoked Gateway session invalid.
	if _, err = admin.Exec(ctx, `UPDATE portal_sessions SET revoked_at=NULL,last_seen_at=clock_timestamp() WHERE id=$1`, principal.SessionID); err != nil {
		t.Fatal(err)
	}
}
