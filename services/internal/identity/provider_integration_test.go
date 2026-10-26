package identity

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/retention"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

type providerFixture struct {
	*managementFixture
	server   *AuthorizationServer
	http     *httptest.Server
	user     ManagedUser
	admin    Principal
	password string
}

func providerDatabase(t *testing.T) *providerFixture {
	t.Helper()
	f := managementDatabase(t)
	var provider *AuthorizationServer
	h := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { provider.ServeHTTP(w, r) }))
	t.Cleanup(h.Close)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "signing.pem")
	if err = os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	o := &Options{PublicURL: h.URL, Issuer: h.URL + "/oidc", ClientID: "platform", ClientSecret: "synthetic-private-client-secret", OrganizationID: f.org, SigningKeyFile: path, HTTPClient: h.Client()}
	provider, err = NewAuthorizationServer(f.app, o, func(context.Context, string, int) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	f.service.issuer = o.Issuer
	f.service.local = NewAccounts(f.app, f.org)
	admin := f.login(t, "admin-a")
	var role string
	if err = f.root.QueryRow(f.ctx, `SELECT id::text FROM roles WHERE org_id=$1 AND code='MEMBER'`, f.org).Scan(&role); err != nil {
		t.Fatal(err)
	}
	password := "synthetic-private-login-password"
	user, err := f.service.CreateUser(f.ctx, admin, "embedded-provider-create", UserCreateInput{DisplayName: "Platform User", Email: "login@example.com", IdentitySource: "local", RoleID: role, InitialPassword: password}, "provider-test")
	if err != nil || user.Status != "ACTIVE" {
		t.Fatal("could not create platform account", err)
	}
	return &providerFixture{f, provider, h, user, admin, password}
}
func (f *providerFixture) authorize(t *testing.T, verifier string) (string, string, *http.Cookie) {
	t.Helper()
	target := f.server.Client().Authorize(strings.Repeat("s", 43), strings.Repeat("n", 43), verifier)
	req := httptest.NewRequest("GET", target, nil)
	w := httptest.NewRecorder()
	f.server.ServeHTTP(w, req)
	if w.Header().Get("Referrer-Policy") != "strict-origin" {
		t.Fatal("login form must preserve exact Origin without leaking query")
	}
	if w.Code != 200 {
		t.Fatal("authorize", w.Code, w.Body.String())
	}
	field := func(name string) string {
		m := regexp.MustCompile(`name="` + name + `" value="([^"]+)"`).FindStringSubmatch(w.Body.String())
		if len(m) != 2 {
			t.Fatal("missing login challenge")
		}
		return m[1]
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || !cookies[0].Secure {
		t.Fatal("provider login cookie is insecure")
	}
	return field("request"), field("csrf"), cookies[0]
}
func (f *providerFixture) submit(id, csrf, password string, cookie *http.Cookie) *httptest.ResponseRecorder {
	form := url.Values{"request": {id}, "csrf": {csrf}, "login": {f.user.LoginEmail}, "password": {password}}
	r := httptest.NewRequest("POST", f.server.options.Issuer+"/login", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", f.server.options.PublicURL)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	f.server.ServeHTTP(w, r)
	return w
}
func (f *providerFixture) code(t *testing.T, verifier string) string {
	t.Helper()
	id, csrf, cookie := f.authorize(t, verifier)
	w := f.submit(id, csrf, f.password, cookie)
	if w.Code != 303 {
		_, cause := f.server.passwordLogin(f.ctx, id, cookie.Value, csrf, f.user.LoginEmail, f.password)
		t.Fatal("password login", w.Code, cause)
	}
	target, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if target.Query().Get("state") != strings.Repeat("s", 43) {
		t.Fatal("authorization state changed")
	}
	return target.Query().Get("code")
}
func (f *providerFixture) tokens(t *testing.T, code, verifier string) tokenResponse {
	t.Helper()
	o := f.server.options
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {o.PublicURL + "/auth/callback"}, "code_verifier": {verifier}}
	req := httptest.NewRequest("POST", o.Issuer+"/token", strings.NewReader(form.Encode()))
	req.SetBasicAuth(o.ClientID, o.ClientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	f.server.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatal("token endpoint", w.Code, w.Body.String())
	}
	var result tokenResponse
	if json.Unmarshal(w.Body.Bytes(), &result) != nil {
		t.Fatal("invalid token response")
	}
	return result
}
func (f *providerFixture) userinfo(token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", f.server.options.Issuer+"/userinfo", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	f.server.ServeHTTP(w, r)
	return w
}
func TestEmbeddedOIDCStandardDiscoverySignedCodeFlowAndReplay(t *testing.T) {
	f := providerDatabase(t)
	verifier := randomSecret()
	code := f.code(t, verifier)
	tokens := f.tokens(t, code, verifier)
	if tokens.ExpiresIn != 300 || tokens.TokenType != "Bearer" || len(tokens.AccessToken) != 43 {
		t.Fatal("invalid protocol token response")
	}
	// A standard discovery client validates the actual HTTPS JWKS and signature.
	client, err := NewOIDCProvider(f.server.options)
	if err != nil {
		t.Fatal(err)
	}
	id, err := client.verifier.Verify(f.ctx, tokens.IDToken)
	if err != nil || id.Subject != f.user.ID || id.Nonce != strings.Repeat("n", 43) {
		t.Fatal("signed OIDC identity not interoperable", err)
	}
	if f.userinfo(tokens.AccessToken).Code != 200 {
		t.Fatal("issued bearer cannot read userinfo")
	}
	if _, err = f.server.exchange(f.ctx, "platform", f.server.options.ClientSecret, code, f.server.options.PublicURL+"/auth/callback", verifier); err == nil {
		t.Fatal("replayed authorization code accepted")
	}
	if f.userinfo(tokens.AccessToken).Code != 401 {
		t.Fatal("authorization code replay did not revoke its issued bearer")
	}
	for _, unsupported := range []string{"password", "refresh_token", "client_credentials"} {
		r := httptest.NewRequest("POST", "/oidc/token", strings.NewReader(url.Values{"grant_type": {unsupported}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.SetBasicAuth("platform", f.server.options.ClientSecret)
		w := httptest.NewRecorder()
		f.server.ServeHTTP(w, r)
		if w.Code != 400 || !strings.Contains(w.Body.String(), "unsupported_grant_type") {
			t.Fatal("unsupported grant accepted")
		}
	}
}
func TestEmbeddedOIDCRejectsCSRFPasswordChallengeReplayAndStaleCredentials(t *testing.T) {
	f := providerDatabase(t)
	verifier := randomSecret()
	id, csrf, cookie := f.authorize(t, verifier)
	if w := f.submit(id, randomSecret(), f.password, cookie); w.Code != 401 {
		t.Fatal("forged CSRF accepted")
	}
	if w := f.submit(id, csrf, "synthetic-wrong-password", cookie); w.Code != 401 || strings.Contains(w.Body.String(), f.password) || strings.Contains(w.Body.String(), "synthetic-wrong-password") || !strings.Contains(w.Body.String(), `value="`+f.user.LoginEmail+`"`) {
		t.Fatal("wrong password accepted, exposed, or login email lost")
	}
	if w := f.submit(id, csrf, f.password, cookie); w.Code != 303 {
		t.Fatal("correct password rejected")
	}
	if w := f.submit(id, csrf, f.password, cookie); w.Code != 401 {
		t.Fatal("consumed login challenge replayed")
	}
	for _, failure := range []string{"pkce", "expired", "client", "redirect"} {
		t.Run(failure, func(t *testing.T) {
			code := f.code(t, verifier)
			secret, redirect, proof := f.server.options.ClientSecret, f.server.options.PublicURL+"/auth/callback", verifier
			switch failure {
			case "pkce":
				proof = randomSecret()
			case "expired":
				_, err := f.root.Exec(f.ctx, `UPDATE identity_codes SET expires_at=clock_timestamp()-interval '1 second' WHERE code_sha256=$1`, digest(code))
				if err != nil {
					t.Fatal(err)
				}
			case "client":
				secret = "wrong-client-secret"
			case "redirect":
				redirect = "https://evil.example/callback"
			}
			if _, err := f.server.exchange(f.ctx, "platform", secret, code, redirect, proof); err == nil {
				t.Fatal("invalid token request accepted")
			}
		})
	}
	code := f.code(t, verifier)
	user, err := f.service.UserAction(f.ctx, f.admin, f.user.ID, "USER_PASSWORD_RESET", UserActionInput{ExpectedVersion: f.user.Version, Password: "synthetic-replacement-password"}, "reset")
	if err != nil || user.PasswordReset.Status != "SUCCEEDED" {
		t.Fatal(err)
	}
	if _, err = f.server.exchange(f.ctx, "platform", f.server.options.ClientSecret, code, f.server.options.PublicURL+"/auth/callback", verifier); err == nil {
		t.Fatal("pre-reset code created a new session")
	}
	id, csrf, cookie = f.authorize(t, verifier)
	if f.submit(id, csrf, f.password, cookie).Code != 401 {
		t.Fatal("old password still signs in")
	}
	if f.submit(id, csrf, "synthetic-replacement-password", cookie).Code != 303 {
		t.Fatal("reset password cannot sign in")
	}
}
func TestEmbeddedOIDCBFFSessionAndDisableRevokeAuthority(t *testing.T) {
	f := providerDatabase(t)
	client := f.server.Client()
	service := NewService(f.app, client, f.cipher, "test", f.org, f.server.options.PublicURL)
	login, err := service.Login(f.ctx, "", "/app/instances")
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	f.server.ServeHTTP(w, httptest.NewRequest("GET", login.URL, nil))
	field := func(name string) string {
		m := regexp.MustCompile(`name="` + name + `" value="([^"]+)"`).FindStringSubmatch(w.Body.String())
		if len(m) != 2 {
			t.Fatal("challenge missing")
		}
		return m[1]
	}
	result := f.submit(field("request"), field("csrf"), f.password, w.Result().Cookies()[0])
	if result.Code != 303 {
		t.Fatal("login failed")
	}
	callback, _ := url.Parse(result.Header().Get("Location"))
	session, err := service.Callback(f.ctx, callback.Query().Get("state"), login.BrowserSecret, callback.Query().Get("code"), "")
	if err != nil {
		t.Fatal(err)
	}
	p, err := service.Authenticate(f.ctx, session.SessionSecret)
	if err != nil || p.UserID != f.user.ID || p.Role != "MEMBER" {
		t.Fatal("BFF lost identity or role", err)
	}
	proof := randomSecret()
	bearer := f.tokens(t, f.code(t, proof), proof)
	if err = service.Logout(f.ctx, p, p.CSRFToken, f.server.options.PublicURL); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Authenticate(f.ctx, session.SessionSecret); err == nil {
		t.Fatal("logout retained BFF session")
	}
	disabled, err := f.service.UserAction(f.ctx, f.admin, f.user.ID, "USER_DISABLE", UserActionInput{ExpectedVersion: f.user.Version}, "disable")
	if err != nil || disabled.Status != "DISABLED" {
		t.Fatal(err)
	}
	if f.userinfo(bearer.AccessToken).Code != 401 {
		t.Fatal("disabled user retained provider bearer")
	}
	challenge, csrf, cookie := f.authorize(t, randomSecret())
	if f.submit(challenge, csrf, f.password, cookie).Code != 401 {
		t.Fatal("disabled user can log in")
	}
	reloaded, err := NewAuthorizationServer(f.app, f.server.options, func(context.Context, string, int) error { return nil })
	if err != nil || reloaded.kid != f.server.kid {
		t.Fatal("restart changed the persisted signing key", err)
	}
	if _, err = reloaded.Client().verifier.Verify(f.ctx, bearer.IDToken); err != nil {
		t.Fatal("persisted signing key cannot verify prior signature", err)
	}
}

func TestBrowserLoginRejectsExpiredOrUnboundBFFTransactions(t *testing.T) {
	f := providerDatabase(t)
	service := NewService(f.app, f.server.Client(), f.cipher, "test", f.org, f.server.options.PublicURL)
	for _, scenario := range []string{"active", "expired", "missing", "foreign-browser", "missing-cookie", "expired-form"} {
		t.Run(scenario, func(t *testing.T) {
			login, err := service.Login(f.ctx, "", "/app/skills")
			if err != nil {
				t.Fatal(err)
			}
			browser := &http.Cookie{Name: LoginCookie, Value: login.BrowserSecret}
			authorize := func(cookie *http.Cookie) *httptest.ResponseRecorder {
				r := httptest.NewRequest("GET", login.URL, nil)
				r.Header.Set("Accept", "text/html")
				if cookie != nil {
					r.AddCookie(cookie)
				}
				w := httptest.NewRecorder()
				f.server.ServeHTTP(w, r)
				return w
			}
			w := authorize(browser)
			if w.Code != 200 {
				t.Fatal("active browser login rejected", w.Code)
			}
			field := func(name string) string {
				m := regexp.MustCompile(`name="` + name + `" value="([^"]+)"`).FindStringSubmatch(w.Body.String())
				if len(m) != 2 {
					t.Fatal("missing login challenge")
				}
				return m[1]
			}
			id, csrf := field("request"), field("csrf")
			providerCookie := w.Result().Cookies()[0]
			target, _ := url.Parse(login.URL)
			stateHash := digest(target.Query().Get("state"))
			expected := "/auth/login"
			switch scenario {
			case "expired":
				_, err = f.root.Exec(f.ctx, `UPDATE oidc_transactions SET expires_at=clock_timestamp()-interval '1 second' WHERE state_sha256=$1`, stateHash)
				expected += "?return_to=%2Fapp%2Fskills"
			case "missing":
				_, err = f.root.Exec(f.ctx, `DELETE FROM oidc_transactions WHERE state_sha256=$1`, stateHash)
			case "foreign-browser":
				browser = &http.Cookie{Name: LoginCookie, Value: randomSecret()}
			case "missing-cookie":
				browser = nil
			case "expired-form":
				_, err = f.root.Exec(f.ctx, `UPDATE identity_authorizations SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, id)
			}
			if err != nil {
				t.Fatal(err)
			}
			if scenario != "active" && scenario != "expired-form" {
				refresh := authorize(browser)
				if refresh.Code != 303 || refresh.Header().Get("Location") != expected {
					t.Fatal("stale or foreign authorization was not restarted safely")
				}
			}
			form := url.Values{"request": {id}, "csrf": {csrf}, "login": {f.user.LoginEmail}, "password": {f.password}}
			r := httptest.NewRequest("POST", f.server.options.Issuer+"/login", strings.NewReader(form.Encode()))
			r.Header.Set("Accept", "text/html")
			r.Header.Set("Origin", f.server.options.PublicURL)
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.AddCookie(providerCookie)
			if browser != nil {
				r.AddCookie(browser)
			}
			result := httptest.NewRecorder()
			f.server.ServeHTTP(result, r)
			if scenario == "active" {
				callback, _ := url.Parse(result.Header().Get("Location"))
				session, err := service.Callback(f.ctx, callback.Query().Get("state"), login.BrowserSecret, callback.Query().Get("code"), "")
				if result.Code != 303 || err != nil || session.ReturnPath != "/app/skills" {
					t.Fatal("valid browser flow lost its session or destination", err)
				}
				return
			}
			if result.Code != 303 || result.Header().Get("Location") != expected {
				t.Fatal("stale form submission did not restart login")
			}
			var codes int
			if err = f.root.QueryRow(f.ctx, `SELECT count(*) FROM identity_codes WHERE nonce=$1`, target.Query().Get("nonce")).Scan(&codes); err != nil || codes != 0 {
				t.Fatal("stale form minted an unusable authorization code", err)
			}
		})
	}
}
func TestAuthorizationValidationRejectsOpenRedirectAndProtocolDowngrade(t *testing.T) {
	s := &AuthorizationServer{options: &Options{PublicURL: "https://portal.example", Issuer: "https://portal.example/oidc", ClientID: "platform"}}
	q := url.Values{"client_id": {"platform"}, "redirect_uri": {"https://portal.example/auth/callback"}, "response_type": {"code"}, "scope": {"openid profile email"}, "state": {randomSecret()}, "nonce": {randomSecret()}, "code_challenge": {base64.RawURLEncoding.EncodeToString(digest(randomSecret()))}, "code_challenge_method": {"S256"}}
	if _, err := s.validateAuthorization(q); err != nil {
		t.Fatal(err)
	}
	for _, change := range [][2]string{{"redirect_uri", "https://evil.example/auth/callback"}, {"redirect_uri", "https://portal.example/auth/callback?next=x"}, {"client_id", "unknown"}, {"response_type", "token"}, {"code_challenge_method", "plain"}, {"scope", "openid offline_access"}, {"prompt", "none"}, {"nonce", ""}, {"response_mode", "fragment"}, {"request_uri", "https://evil.example"}} {
		copy := url.Values{}
		for k, v := range q {
			copy[k] = append([]string{}, v...)
		}
		copy.Set(change[0], change[1])
		if _, err := s.validateAuthorization(copy); err == nil {
			t.Fatal("invalid authorization accepted", change[0])
		}
	}
	q.Add("client_id", "platform")
	if _, err := s.validateAuthorization(q); err == nil {
		t.Fatal("duplicate protocol parameter accepted")
	}
}

func TestEmbeddedOIDCRateLimitPrivacyAndBoundedCleanup(t *testing.T) {
	f := providerDatabase(t)
	var workerReads, gatewayReads, apiGovernance bool
	if err := f.root.QueryRow(f.ctx, `SELECT has_table_privilege('hc_worker','identity_accounts','SELECT'),has_table_privilege('hc_gateway','identity_accounts','SELECT'),has_column_privilege('hc_api','organizations','status','UPDATE')`).Scan(&workerReads, &gatewayReads, &apiGovernance); err != nil || workerReads || gatewayReads || apiGovernance {
		t.Fatal("identity implementation widened runtime privileges", err)
	}
	f.server.limit = func(context.Context, string, int) error { return webapi.Fail(429, "RATE_LIMITED", "rate test") }
	w := httptest.NewRecorder()
	f.server.ServeHTTP(w, httptest.NewRequest("GET", f.server.Client().Authorize(randomSecret(), randomSecret(), randomSecret()), nil))
	if w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatal("login limiter was bypassed")
	}
	f.server.limit = func(context.Context, string, int) error {
		return webapi.Fail(503, "DEPENDENCY_UNAVAILABLE", "synthetic dependency detail")
	}
	w = httptest.NewRecorder()
	f.server.ServeHTTP(w, httptest.NewRequest("GET", f.server.Client().Authorize(randomSecret(), randomSecret(), randomSecret()), nil))
	if w.Code != 503 || strings.Contains(w.Body.String(), "synthetic dependency detail") {
		t.Fatal("identity did not fail closed without disclosing dependency errors")
	}
	if _, err := f.root.Exec(f.ctx, `INSERT INTO identity_authorizations(id,browser_sha256,csrf_sha256,client_id,redirect_uri,state,nonce,challenge,scope,expires_at)
 SELECT gen_random_uuid(),$1,$2,'platform','https://portal.example/auth/callback','state','nonce',$3,'openid',clock_timestamp()-interval '1 second' FROM generate_series(1,25)`, digest("browser"), digest("csrf"), randomSecret()); err != nil {
		t.Fatal(err)
	}
	cleaner := retention.NewCleaner(f.root)
	preview, err := cleaner.Preview(f.ctx, 10)
	if err != nil || preview.Total() != 10 || preview.IdentityAuthorizations != 10 {
		t.Fatal("cleanup preview did not honor identity row budget", err)
	}
	applied, err := cleaner.Once(f.ctx, 10)
	if err != nil || applied.Total() != 10 {
		t.Fatal("cleanup exceeded identity row budget", err)
	}
	var remaining int
	if err = f.root.QueryRow(f.ctx, `SELECT count(*) FROM identity_authorizations`).Scan(&remaining); err != nil || remaining != 15 {
		t.Fatal("cleanup preview mutated or apply exceeded budget", err)
	}
}

type providerAfterExchange struct {
	Provider
	after func()
}

func (p providerAfterExchange) Exchange(ctx context.Context, code, verifier string) (VerifiedIdentity, error) {
	identity, err := p.Provider.Exchange(ctx, code, verifier)
	if err == nil {
		p.after()
	}
	return identity, err
}
func TestEmbeddedOIDCCallbackRejectsAuthorityChangedAfterTokenIssuance(t *testing.T) {
	for _, action := range []string{"USER_PASSWORD_RESET", "USER_DISABLE", "USER_ROLE"} {
		t.Run(action, func(t *testing.T) {
			f := providerDatabase(t)
			var changeError error
			wrapped := providerAfterExchange{f.server.Client(), func() {
				input := UserActionInput{ExpectedVersion: f.user.Version}
				if action == "USER_PASSWORD_RESET" {
					input.Password = "synthetic-post-token-reset-password"
				}
				if action == "USER_ROLE" {
					changeError = f.root.QueryRow(f.ctx, `SELECT id::text FROM roles WHERE org_id=$1 AND code='ADMIN'`, f.org).Scan(&input.RoleID)
					if changeError != nil {
						return
					}
				}
				_, changeError = f.service.UserAction(f.ctx, f.admin, f.user.ID, action, input, "post-token-authority-change")
			}}
			service := NewService(f.app, wrapped, f.cipher, "test", f.org, f.server.options.PublicURL)
			login, err := service.Login(f.ctx, "", "/app/instances")
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			f.server.ServeHTTP(w, httptest.NewRequest("GET", login.URL, nil))
			field := func(name string) string {
				m := regexp.MustCompile(`name="` + name + `" value="([^"]+)"`).FindStringSubmatch(w.Body.String())
				if len(m) != 2 {
					t.Fatal("missing challenge")
				}
				return m[1]
			}
			response := f.submit(field("request"), field("csrf"), f.password, w.Result().Cookies()[0])
			if response.Code != 303 {
				t.Fatal("could not authenticate user")
			}
			callback, _ := url.Parse(response.Header().Get("Location"))
			_, err = service.Callback(f.ctx, callback.Query().Get("state"), login.BrowserSecret, callback.Query().Get("code"), "")
			if err == nil || changeError != nil {
				t.Fatal("stale signed identity created a new BFF session", changeError)
			}
			var sessions int
			if err = f.root.QueryRow(f.ctx, `SELECT count(*) FROM portal_sessions WHERE user_id=$1`, f.user.ID).Scan(&sessions); err != nil || sessions != 0 {
				t.Fatal("post-token authority change retained a new session", err)
			}
		})
	}
}
