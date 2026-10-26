package connectors

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func testOAuthFlow(t *testing.T, ctx context.Context, s *Service, root *pgxpool.Pool, owner, other identity.Principal, mcpEndpoint Endpoint) {
	t.Helper()
	previousPolicy, previousOrigin := s.policy, s.portalOrigin
	defer func() { s.policy = previousPolicy; s.portalOrigin = previousOrigin }()
	s.portalOrigin = "https://portal.example"
	var exchanges, refreshes atomic.Int32
	var rejectRefresh atomic.Bool
	var challenge string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Cookie") != "" {
			t.Error("ambient cookie leaked")
		}
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(oauthMetadata{Issuer: srv.URL, Authorization: srv.URL + "/authorize", Token: srv.URL + "/token", Registration: srv.URL + "/register", ChallengeMethods: []string{"S256"}, AuthMethods: []string{"none"}})
		case "/register":
			var in struct {
				Redirects []string `json:"redirect_uris"`
				Auth      string   `json:"token_endpoint_auth_method"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			if len(in.Redirects) != 1 || in.Redirects[0] != "https://portal.example/v1/connectors/oauth/callback" || in.Auth != "none" {
				t.Error("invalid DCR configuration")
			}
			_, _ = w.Write([]byte(`{"client_id":"test-public-client","token_endpoint_auth_method":"none"}`))
		case "/token":
			_ = r.ParseForm()
			if r.Form.Get("grant_type") == "refresh_token" {
				n := refreshes.Add(1)
				if rejectRefresh.Load() {
					w.WriteHeader(400)
					_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
					return
				}
				want := "secret-refresh-token"
				if n > 1 {
					want = "rotated-refresh-token"
				}
				if r.Form.Get("refresh_token") != want {
					t.Error("wrong refresh token")
				}
				_, _ = w.Write([]byte(`{"access_token":"private-test-token","token_type":"Bearer","refresh_token":"rotated-refresh-token","expires_in":3600}`))
				return
			}
			exchanges.Add(1)
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if base64.RawURLEncoding.EncodeToString(sum[:]) != challenge || r.Form.Get("code") != "once-only-code" || r.Form.Get("redirect_uri") != "https://portal.example/v1/connectors/oauth/callback" {
				t.Error("PKCE or redirect mismatch")
			}
			_, _ = w.Write([]byte(`{"access_token":"private-test-token","token_type":"Bearer","refresh_token":"secret-refresh-token","expires_in":3600}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	endpoints := []Endpoint{mcpEndpoint}
	for _, path := range []string{"", "/.well-known/oauth-authorization-server", "/authorize", "/token", "/register"} {
		endpoints = append(endpoints, Endpoint{ID: "oauth-test", URL: srv.URL + path, AllowHTTP: true, AllowPrivate: true})
	}
	s.policy = func() (Policy, error) {
		return Policy{Endpoints: endpoints, OAuthClients: []OAuthClient{{URL: mcpEndpoint.URL, Issuer: srv.URL, Resource: mcpEndpoint.URL}}}, nil
	}
	conn, err := s.Save(ctx, owner, "", Input{Name: "OAuth 测试", EndpointID: mcpEndpoint.ID, Protocol: "2025-11-25", Auth: "oauth2", Enabled: true}, "")
	if err != nil || conn.Status != "AUTH_REQUIRED" {
		t.Fatal("cannot create OAuth connection", err)
	}
	// The fixture supports the default protocol too; keep the same choice as other tests.
	conn, err = s.Save(ctx, owner, conn.ID, Input{Name: conn.Name, EndpointID: mcpEndpoint.ID, Protocol: "2026-07-28", Auth: "oauth2", Enabled: true, ExpectedVersion: conn.Version}, "")
	if err != nil {
		t.Fatal(err)
	}
	start := func() url.Values {
		t.Helper()
		result, e := s.StartOAuth(ctx, owner, conn.ID, conn.Version, "")
		if e != nil {
			t.Fatal(e)
		}
		u, e := url.Parse(result.(map[string]string)["authorization_url"])
		if e != nil {
			t.Fatal(e)
		}
		q := u.Query()
		challenge = q.Get("code_challenge")
		if q.Get("code_challenge_method") != "S256" || len(q.Get("state")) != 43 || q.Get("client_id") != "test-public-client" {
			t.Fatal("invalid authorization URL")
		}
		return url.Values{"state": {q.Get("state")}, "code": {"once-only-code"}}
	}
	callback := start()
	if _, err = s.FinishOAuth(ctx, other, callback, ""); err == nil {
		t.Fatal("other owner consumed OAuth state")
	}
	wrongSession := owner
	wrongSession.SessionID = other.SessionID
	if _, err = s.FinishOAuth(ctx, wrongSession, callback, ""); err == nil {
		t.Fatal("different browser session consumed OAuth state")
	}
	duplicate := url.Values{"state": {callback.Get("state"), callback.Get("state")}, "code": {"once-only-code"}}
	if _, err = s.FinishOAuth(ctx, owner, duplicate, ""); err == nil {
		t.Fatal("duplicate query accepted")
	}
	conn, err = s.FinishOAuth(ctx, owner, callback, "")
	if err != nil || conn.Status != "READY" || len(conn.Tools) == 0 || conn.Tools[0].Enabled {
		t.Fatal("authorization or verification failed", conn.Status, err)
	}
	if _, err = s.FinishOAuth(ctx, owner, callback, ""); err == nil || exchanges.Load() != 1 {
		t.Fatal("authorization code replayed")
	}
	listed, err := s.List(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(listed)
	for _, secret := range []string{"private-test-token", "secret-refresh-token", "test-public-client", "code_verifier"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("secret exposed in public response")
		}
	}
	var e security.Envelope
	if err = root.QueryRow(ctx, `SELECT key_id,nonce,ciphertext FROM mcp_connectors WHERE id=$1`, conn.ID).Scan(&e.KeyID, &e.Nonce, &e.Ciphertext); err != nil {
		t.Fatal(err)
	}
	data, err := s.cipher.Decrypt(e, s.aad(owner, conn.ID))
	if err != nil {
		t.Fatal(err)
	}
	var tokens oauthTokens
	if json.Unmarshal(data, &tokens) != nil {
		t.Fatal("token envelope invalid")
	}
	clear(data)
	tokens.ExpiresAt = time.Now().Add(-time.Minute)
	tx, err := s.begin(ctx, owner, "connectors:manage")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.storeOAuth(ctx, tx, owner, conn, tokens); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	conn, err = s.Sync(ctx, owner, conn.ID, conn.Version, "")
	if err != nil || conn.Status != "READY" || refreshes.Load() != 1 {
		t.Fatal("expired access token not refreshed", err)
	}
	conn, err = s.Sync(ctx, owner, conn.ID, conn.Version, "")
	if err != nil || refreshes.Load() != 1 {
		t.Fatal("valid access token refreshed again", err)
	}
	// Invocation refreshes before discovery/dispatch and persists rotated refresh tokens.
	conn, err = s.SelectTools(ctx, owner, conn.ID, conn.Version, []string{"echo"}, "")
	if err != nil {
		t.Fatal(err)
	}
	expire := func() {
		t.Helper()
		tx, err := s.begin(ctx, owner, "connectors:manage")
		if err != nil {
			t.Fatal(err)
		}
		current, err := load(ctx, tx, owner, conn.ID)
		if err != nil {
			t.Fatal(err)
		}
		data, err := s.cipher.Decrypt(current.envelope, s.aad(owner, conn.ID))
		if err != nil {
			t.Fatal(err)
		}
		var currentTokens oauthTokens
		if json.Unmarshal(data, &currentTokens) != nil {
			t.Fatal("invalid refreshed envelope")
		}
		clear(data)
		if currentTokens.RefreshToken != "rotated-refresh-token" {
			t.Fatal("rotated token not saved")
		}
		currentTokens.ExpiresAt = time.Now().Add(-time.Minute)
		if err = s.storeOAuth(ctx, tx, owner, current, currentTokens); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	expire()
	call, err := s.Invoke(ctx, owner, conn.ID, InvokeInput{ExpectedVersion: conn.Version, CatalogVersion: conn.CatalogVersion, Tool: "echo", Arguments: map[string]any{"text": "OAuth 调用"}, IdempotencyKey: webapi.ID(), Confirmed: true, RiskConfirmed: true}, "")
	if err != nil || call.Status != "SUCCEEDED" || refreshes.Load() != 2 {
		t.Fatal("OAuth invocation refresh failed", err)
	}
	expire()
	rejectRefresh.Store(true)
	conn, err = s.Sync(ctx, owner, conn.ID, conn.Version, "")
	if err != nil || conn.Status != "AUTH_REQUIRED" || refreshes.Load() != 3 {
		t.Fatal("revoked refresh token did not require authorization", err)
	}
	rejectRefresh.Store(false)
	// Cancellation, expiry and configuration changes cannot install credentials.
	callback = start()
	callback.Del("code")
	callback.Set("error", "access_denied")
	if _, err = s.FinishOAuth(ctx, owner, callback, ""); err == nil {
		t.Fatal("cancelled authorization accepted")
	}
	callback = start()
	_, err = root.Exec(ctx, `UPDATE mcp_oauth_transactions SET expires_at=clock_timestamp()-interval '1 second' WHERE connector_id=$1`, conn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.FinishOAuth(ctx, owner, callback, ""); err == nil {
		t.Fatal("expired state accepted")
	}
	callback = start()
	conn, err = s.Save(ctx, owner, conn.ID, Input{Name: conn.Name, EndpointID: mcpEndpoint.ID, Protocol: conn.Protocol, Auth: "oauth2", Enabled: true, ExpectedVersion: conn.Version}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.FinishOAuth(ctx, owner, callback, ""); !problemIs(err, "VERSION_CONFLICT") {
		t.Fatal("changed connection accepted authorization", err)
	}
	if exchanges.Load() != 1 {
		t.Fatal("invalid callback reached token endpoint")
	}
	if err = s.Delete(ctx, owner, conn.ID, Input{ExpectedVersion: conn.Version, ConfirmedName: conn.Name}, ""); err != nil {
		t.Fatal(err)
	}
}

func TestOAuthNetworkBoundaries(t *testing.T) {
	s := NewService(nil, nil, "test", false, "", "https://portal.example")
	for _, address := range []string{"http://127.0.0.1/token", "https://127.0.0.1/token", "https://169.254.169.254/token", "https://user:pass@example.com/token", "https://example.com/token#fragment"} {
		if err := s.oauthHTTP(context.Background(), "POST", address, []byte("secret"), nil, &map[string]any{}); err == nil {
			t.Fatal("unsafe OAuth endpoint accepted", address)
		}
	}
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetHits.Add(1) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer redirect.Close()
	s.policy = func() (Policy, error) {
		return Policy{Endpoints: []Endpoint{{URL: redirect.URL, AllowPrivate: true, AllowHTTP: true}}}, nil
	}
	if s.oauthHTTP(context.Background(), "POST", redirect.URL, []byte("secret"), nil, &map[string]any{}) == nil || targetHits.Load() != 0 {
		t.Fatal("OAuth redirect followed")
	}
}

func TestCustomOAuthDiscoveryBindsResourceAndRequiresPKCE(t *testing.T) {
	s := NewService(nil, nil, "test", false, "")
	var mismatch, plain atomic.Bool
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			resource := srv.URL + "/mcp"
			if mismatch.Load() {
				resource = srv.URL + "/other"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"resource": resource, "authorization_servers": []string{srv.URL}, "scopes_supported": []string{"tools:read"}})
		case "/.well-known/oauth-authorization-server":
			method := "S256"
			if plain.Load() {
				method = "plain"
			}
			_ = json.NewEncoder(w).Encode(oauthMetadata{Issuer: srv.URL, Authorization: srv.URL + "/authorize", Token: srv.URL + "/token", Registration: srv.URL + "/register", ChallengeMethods: []string{method}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	var endpoints []Endpoint
	for _, path := range []string{"", "/mcp", "/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-protected-resource", "/.well-known/oauth-authorization-server", "/authorize", "/token", "/register"} {
		endpoints = append(endpoints, Endpoint{URL: srv.URL + path, AllowHTTP: true, AllowPrivate: true})
	}
	s.policy = func() (Policy, error) { return Policy{Endpoints: endpoints}, nil }
	cfg, _, err := s.oauthConfiguration(context.Background(), Endpoint{URL: srv.URL + "/mcp"})
	if err != nil || cfg.Issuer != srv.URL || cfg.Resource != srv.URL+"/mcp" || cfg.Scopes != "tools:read" {
		t.Fatal("custom discovery failed", err)
	}
	mismatch.Store(true)
	if _, _, err = s.oauthConfiguration(context.Background(), Endpoint{URL: srv.URL + "/mcp"}); err == nil {
		t.Fatal("resource mismatch accepted")
	}
	mismatch.Store(false)
	plain.Store(true)
	if _, _, err = s.oauthConfiguration(context.Background(), Endpoint{URL: srv.URL + "/mcp"}); err == nil {
		t.Fatal("plain PKCE accepted")
	}
}
