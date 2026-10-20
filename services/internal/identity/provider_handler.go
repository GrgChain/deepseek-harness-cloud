package identity

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func (s *AuthorizationServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	w.Header().Set("X-Frame-Options", "DENY")
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	switch r.URL.Path {
	case "/oidc/.well-known/openid-configuration":
		if r.Method != http.MethodGet {
			protocolFailure(w, 405, "invalid_request")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": s.options.Issuer, "authorization_endpoint": s.options.Issuer + "/authorize", "token_endpoint": s.options.Issuer + "/token", "userinfo_endpoint": s.options.Issuer + "/userinfo", "jwks_uri": s.options.Issuer + "/jwks",
			"response_types_supported": []string{"code"}, "response_modes_supported": []string{"query"}, "grant_types_supported": []string{"authorization_code"}, "subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"}, "token_endpoint_auth_methods_supported": []string{"client_secret_basic"}, "code_challenge_methods_supported": []string{"S256"}, "scopes_supported": []string{"openid", "profile", "email"}, "claims_supported": []string{"iss", "sub", "aud", "exp", "iat", "auth_time", "nonce", "name", "email", "email_verified", "at_hash"}})
	case "/oidc/jwks":
		if r.Method != http.MethodGet {
			protocolFailure(w, 405, "invalid_request")
			return
		}
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &s.key.PublicKey, KeyID: s.kid, Algorithm: "RS256", Use: "sig"}}})
	case "/oidc/login.css":
		if r.Method != http.MethodGet {
			w.WriteHeader(405)
			return
		}
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		_, _ = w.Write([]byte(loginCSS))
	case "/oidc/authorize":
		if r.Method != http.MethodGet || len(r.URL.RawQuery) > 4096 {
			protocolFailure(w, 400, "invalid_request")
			return
		}
		if !s.allow(w, r, "authorize", 90) {
			return
		}
		a, err := s.validateAuthorization(r.URL.Query())
		if err != nil {
			protocolFailure(w, 400, err.Error())
			return
		}
		id, browser, csrf := newID(), randomSecret(), randomSecret()
		_, err = s.pool.Exec(ctx, `INSERT INTO identity_authorizations(id,browser_sha256,csrf_sha256,client_id,redirect_uri,state,nonce,challenge,scope,expires_at)
   VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,clock_timestamp()+interval '5 minutes')`, id, digest(browser), digest(csrf), a.ClientID, a.RedirectURI, a.State, a.Nonce, a.Challenge, a.Scope)
		if err != nil {
			protocolFailure(w, 503, "temporarily_unavailable")
			return
		}
		setCookie(w, ProviderCookie, browser, 300)
		s.loginPage(w, 200, id, csrf, "")
	case "/oidc/login":
		if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.Header.Get("Origin") != s.options.PublicURL || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
			protocolFailure(w, 403, "invalid_request")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if r.ParseForm() != nil {
			protocolFailure(w, 400, "invalid_request")
			return
		}
		for k, v := range r.PostForm {
			if len(v) != 1 || (k != "request" && k != "csrf" && k != "login" && k != "password") {
				protocolFailure(w, 400, "invalid_request")
				return
			}
		}
		if !s.allow(w, r, "login", 120) {
			return
		}
		if err := s.limit(ctx, "identity:account:"+rateKey(strings.ToLower(strings.TrimSpace(r.PostForm.Get("login")))), 15); err != nil {
			rateFailure(w, err)
			return
		}
		id, csrf := r.PostForm.Get("request"), r.PostForm.Get("csrf")
		target, err := s.passwordLogin(ctx, id, cookieValue(r, ProviderCookie), csrf, r.PostForm.Get("login"), r.PostForm.Get("password"))
		if err != nil {
			if !errors.Is(err, ErrUnauthenticated) {
				protocolFailure(w, 503, "temporarily_unavailable")
				return
			}
			s.loginPage(w, 401, id, csrf, "邮箱或密码不正确，或账户暂不可用")
			return
		}
		setCookie(w, ProviderCookie, "", -1)
		w.Header().Del("Content-Type")
		http.Redirect(w, r, target, http.StatusSeeOther)
	case "/oidc/token":
		if r.Method != http.MethodPost || r.URL.RawQuery != "" || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
			protocolFailure(w, 400, "invalid_request")
			return
		}
		if !s.allow(w, r, "token", 120) {
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if r.ParseForm() != nil {
			protocolFailure(w, 400, "invalid_request")
			return
		}
		for k, v := range r.PostForm {
			if len(v) != 1 || (k != "grant_type" && k != "code" && k != "redirect_uri" && k != "code_verifier") {
				protocolFailure(w, 400, "invalid_request")
				return
			}
		}
		client, secret, ok := r.BasicAuth()
		decodedClient, e1 := url.QueryUnescape(client)
		decodedSecret, e2 := url.QueryUnescape(secret)
		if !ok || e1 != nil || e2 != nil {
			w.Header().Set("WWW-Authenticate", `Basic realm="Harness Cloud OIDC"`)
			protocolFailure(w, 401, "invalid_client")
			return
		}
		if r.PostForm.Get("grant_type") != "authorization_code" {
			protocolFailure(w, 400, "unsupported_grant_type")
			return
		}
		result, err := s.exchange(ctx, decodedClient, decodedSecret, r.PostForm.Get("code"), r.PostForm.Get("redirect_uri"), r.PostForm.Get("code_verifier"))
		if err != nil {
			status, code := 400, err.Error()
			var pe protocolError
			if !errors.As(err, &pe) {
				status, code = 503, "temporarily_unavailable"
			} else if pe == "invalid_client" {
				status = 401
				w.Header().Set("WWW-Authenticate", `Basic realm="Harness Cloud OIDC"`)
			}
			protocolFailure(w, status, code)
			return
		}
		_ = json.NewEncoder(w).Encode(result)
	case "/oidc/userinfo":
		if r.Method != http.MethodGet || r.URL.RawQuery != "" {
			protocolFailure(w, 400, "invalid_request")
			return
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if len(token) != 43 || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			bearerFailure(w)
			return
		}
		var account, scope string
		var generation, version int64
		err := s.pool.QueryRow(ctx, `SELECT account_id::text,scope,auth_generation,credential_version FROM identity_access_tokens WHERE token_sha256=$1 AND revoked_at IS NULL AND expires_at>clock_timestamp()`, digest(token)).Scan(&account, &scope, &generation, &version)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				bearerFailure(w)
			} else {
				protocolFailure(w, 503, "temporarily_unavailable")
			}
			return
		}
		a, hash, err := scanAccount(s.pool.QueryRow(ctx, accountQuery+` AND a.id=$3`, s.options.OrganizationID, s.options.Issuer, account))
		clear(hash)
		if err != nil || a.Generation != generation || a.Version != version {
			bearerFailure(w)
			return
		}
		result := map[string]any{"sub": a.ID}
		if strings.Contains(" "+scope+" ", " profile ") {
			result["name"] = a.Name
		}
		if strings.Contains(" "+scope+" ", " email ") {
			result["email"] = a.Email
			result["email_verified"] = false
		}
		_ = json.NewEncoder(w).Encode(result)
	default:
		protocolFailure(w, 404, "invalid_request")
	}
}
func (s *AuthorizationServer) allow(w http.ResponseWriter, r *http.Request, purpose string, limit int) bool {
	// Ignore arbitrary forwarded headers. An unconfigured shared proxy may only
	// make this bucket stricter, never let callers select their own rate identity.
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if err = s.limit(r.Context(), "identity:"+purpose+":"+rateKey(host), limit); err != nil {
		rateFailure(w, err)
		return false
	}
	return true
}
func protocolFailure(w http.ResponseWriter, status int, code string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}
func bearerFailure(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
	protocolFailure(w, 401, "invalid_token")
}

func rateFailure(w http.ResponseWriter, err error) {
	var problem *webapi.Problem
	if errors.As(err, &problem) && problem.Status == 429 {
		w.Header().Set("Retry-After", "60")
		protocolFailure(w, 429, "slow_down")
		return
	}
	protocolFailure(w, 503, "temporarily_unavailable")
}
