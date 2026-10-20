package identity

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	jose "github.com/go-jose/go-jose/v4"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/config"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/oauth2"
)

const ProviderCookie = "__Host-hc_identity"

var pkceVerifierPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)
var challengePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

type AuthorizationServer struct {
	pool      *pgxpool.Pool
	options   *Options
	key       *rsa.PrivateKey
	signer    jose.Signer
	kid       string
	dummyHash []byte
	limit     func(context.Context, string, int) error
}

type authorizationRequest struct{ ClientID, RedirectURI, State, Nonce, Challenge, Scope string }
type accountIdentity struct {
	ID, Name, Email     string
	Version, Generation int64
}
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	IDToken     string `json:"id_token"`
	Scope       string `json:"scope"`
}
type protocolError string

func (e protocolError) Error() string { return string(e) }

func NewAuthorizationServer(pool *pgxpool.Pool, o *Options, limit func(context.Context, string, int) error) (*AuthorizationServer, error) {
	if o.Issuer != o.PublicURL+"/oidc" || limit == nil {
		return nil, errors.New("embedded OIDC requires HC_OIDC_ISSUER=HC_PUBLIC_URL/oidc and a login limiter")
	}
	raw, err := config.ReadPrivateFile(o.SigningKeyFile)
	if err != nil {
		return nil, errors.New("OIDC signing key unavailable")
	}
	defer clear(raw)
	block, rest := pem.Decode(raw)
	if block == nil || block.Type != "PRIVATE KEY" || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, errors.New("OIDC signing key must be one PKCS8 RSA private key")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("OIDC signing key invalid")
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok || key.N.BitLen() < 2048 || key.N.BitLen() > 4096 || key.Validate() != nil {
		return nil, errors.New("OIDC signing key requires RSA 2048–4096 bits")
	}
	public := jose.JSONWebKey{Key: &key.PublicKey}
	thumb, err := public.Thumbprint(crypto.SHA256)
	if err != nil {
		return nil, err
	}
	kid := base64.RawURLEncoding.EncodeToString(thumb)
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", kid))
	if err != nil {
		return nil, err
	}
	dummy, err := bcrypt.GenerateFromPassword(randomBytes(32), 12)
	if err != nil {
		return nil, err
	}
	return &AuthorizationServer{pool: pool, options: o, key: key, signer: signer, kid: kid, dummyHash: dummy, limit: limit}, nil
}

// The BFF uses the same token issuance and standard signature verifier in-process.
// Startup therefore does not depend on discovering its own not-yet-running URL.
func (s *AuthorizationServer) Client() *OIDCProvider {
	o := s.options
	return &OIDCProvider{options: o, internal: s, config: oauth2.Config{ClientID: o.ClientID, ClientSecret: o.ClientSecret,
		Endpoint: oauth2.Endpoint{AuthURL: o.Issuer + "/authorize", TokenURL: o.Issuer + "/token"}, RedirectURL: o.PublicURL + "/auth/callback", Scopes: []string{"openid", "profile", "email"}},
		verifier: oidc.NewVerifier(o.Issuer, &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{&s.key.PublicKey}}, &oidc.Config{ClientID: o.ClientID, SupportedSigningAlgs: []string{oidc.RS256}})}
}
func (s *AuthorizationServer) validateAuthorization(q url.Values) (authorizationRequest, error) {
	var a authorizationRequest
	allowed := map[string]bool{"client_id": true, "redirect_uri": true, "response_type": true, "scope": true, "state": true, "nonce": true, "code_challenge": true, "code_challenge_method": true, "prompt": true, "response_mode": true}
	for name, values := range q {
		if !allowed[name] || len(values) != 1 || len(values[0]) > 1024 {
			return a, protocolError("invalid_request")
		}
	}
	a = authorizationRequest{q.Get("client_id"), q.Get("redirect_uri"), q.Get("state"), q.Get("nonce"), q.Get("code_challenge"), q.Get("scope")}
	if a.ClientID != s.options.ClientID || a.RedirectURI != s.options.PublicURL+"/auth/callback" {
		return a, protocolError("invalid_request")
	}
	if q.Get("response_type") != "code" {
		return a, protocolError("unsupported_response_type")
	}
	if q.Get("code_challenge_method") != "S256" || !challengePattern.MatchString(a.Challenge) || len(a.State) < 32 || len(a.State) > 256 || len(a.Nonce) < 32 || len(a.Nonce) > 256 {
		return a, protocolError("invalid_request")
	}
	if prompt := q.Get("prompt"); prompt != "" && prompt != "login" {
		return a, protocolError("invalid_request")
	}
	if mode := q.Get("response_mode"); mode != "" && mode != "query" {
		return a, protocolError("invalid_request")
	}
	scopes := strings.Fields(a.Scope)
	unique := map[string]bool{}
	for _, scope := range scopes {
		if unique[scope] || (scope != "openid" && scope != "profile" && scope != "email") {
			return a, protocolError("invalid_scope")
		}
		unique[scope] = true
	}
	if !unique["openid"] {
		return a, protocolError("invalid_scope")
	}
	a.Scope = strings.Join(scopes, " ")
	return a, nil
}

const accountQuery = `SELECT a.id::text,COALESCE(NULLIF(u.display_name,''),a.display_name),a.login_email,a.credential_version,
 COALESCE(m.auth_generation,1),a.password_hash FROM identity_accounts a JOIN organizations o ON o.id=a.org_id
 LEFT JOIN users u ON u.id=a.user_id LEFT JOIN memberships m ON m.org_id=a.org_id AND m.user_id=a.user_id
 WHERE a.org_id=$1 AND a.enabled AND o.status='ACTIVE'
 AND (a.user_id IS NULL OR (u.status='ACTIVE' AND m.status='ACTIVE' AND u.identity_source='local'
 AND u.identity_issuer=$2 AND u.identity_subject=a.id::text))
 AND NOT EXISTS(SELECT 1 FROM identity_password_resets r WHERE r.org_id=a.org_id AND r.user_id=a.user_id AND r.status='PENDING')`

func scanAccount(row pgx.Row) (accountIdentity, []byte, error) {
	var a accountIdentity
	var hash []byte
	err := row.Scan(&a.ID, &a.Name, &a.Email, &a.Version, &a.Generation, &hash)
	return a, hash, err
}
func (s *AuthorizationServer) passwordLogin(ctx context.Context, request, browser, csrf, email, password string) (string, error) {
	if !uuidPattern.MatchString(request) || len(browser) != 43 || len(csrf) != 43 || len(email) > 254 || len(password) > 72 {
		return "", ErrUnauthenticated
	}
	a, hash, err := scanAccount(s.pool.QueryRow(ctx, accountQuery+` AND a.login_email=$3`, s.options.OrganizationID, s.options.Issuer, strings.ToLower(strings.TrimSpace(email))))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", ErrUnavailable
	}
	compare := hash
	if err != nil {
		compare = s.dummyHash
	}
	valid := bcrypt.CompareHashAndPassword(compare, []byte(password)) == nil && err == nil
	clear(hash)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", ErrUnavailable
	}
	defer tx.Rollback(context.Background())
	var ar authorizationRequest
	err = tx.QueryRow(ctx, `SELECT client_id,redirect_uri,state,nonce,challenge,scope FROM identity_authorizations
 WHERE id=$1 AND browser_sha256=$2 AND csrf_sha256=$3 AND consumed_at IS NULL AND expires_at>clock_timestamp() AND attempts<10 FOR UPDATE`, request, digest(browser), digest(csrf)).Scan(&ar.ClientID, &ar.RedirectURI, &ar.State, &ar.Nonce, &ar.Challenge, &ar.Scope)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrUnauthenticated
	}
	if err != nil {
		return "", ErrUnavailable
	}
	if !valid {
		if _, err = tx.Exec(ctx, `UPDATE identity_authorizations SET attempts=attempts+1 WHERE id=$1`, request); err != nil {
			return "", ErrUnavailable
		}
		if tx.Commit(ctx) != nil {
			return "", ErrUnavailable
		}
		return "", ErrUnauthenticated
	}
	current, currentHash, err := scanAccount(tx.QueryRow(ctx, accountQuery+` AND a.id=$3 FOR SHARE OF a`, s.options.OrganizationID, s.options.Issuer, a.ID))
	clear(currentHash)
	if err != nil || current.Version != a.Version || current.Generation != a.Generation {
		return "", ErrUnauthenticated
	}
	if err = s.lockAuthority(ctx, tx, current); err != nil {
		return "", errors.Join(ErrUnauthenticated, err)
	}
	code := randomSecret()
	_, err = tx.Exec(ctx, `INSERT INTO identity_codes(id,code_sha256,account_id,client_id,redirect_uri,nonce,challenge,scope,auth_generation,credential_version,expires_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,clock_timestamp()+interval '60 seconds')`, newID(), digest(code), a.ID, ar.ClientID, ar.RedirectURI, ar.Nonce, ar.Challenge, ar.Scope, a.Generation, a.Version)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE identity_authorizations SET consumed_at=clock_timestamp() WHERE id=$1`, request)
	}
	if err != nil || tx.Commit(ctx) != nil {
		return "", ErrUnavailable
	}
	target, _ := url.Parse(ar.RedirectURI)
	q := target.Query()
	q.Set("code", code)
	q.Set("state", ar.State)
	target.RawQuery = q.Encode()
	return target.String(), nil
}

func (s *AuthorizationServer) exchange(ctx context.Context, client, secret, code, redirect, verifier string) (tokenResponse, error) {
	var result tokenResponse
	if subtle.ConstantTimeCompare(digest(client), digest(s.options.ClientID)) != 1 || subtle.ConstantTimeCompare(digest(secret), digest(s.options.ClientSecret)) != 1 {
		return result, protocolError("invalid_client")
	}
	if len(code) != 43 || !pkceVerifierPattern.MatchString(verifier) || redirect != s.options.PublicURL+"/auth/callback" {
		return result, protocolError("invalid_grant")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return result, ErrUnavailable
	}
	defer tx.Rollback(context.Background())
	var id, account, nonce, challenge, scope, storedClient, storedRedirect string
	var generation, version int64
	var expires, authTime time.Time
	var consumed *time.Time
	err = tx.QueryRow(ctx, `SELECT id::text,account_id::text,nonce,challenge,scope,client_id,redirect_uri,auth_generation,credential_version,expires_at,authenticated_at,consumed_at
 FROM identity_codes WHERE code_sha256=$1 FOR UPDATE`, digest(code)).Scan(&id, &account, &nonce, &challenge, &scope, &storedClient, &storedRedirect, &generation, &version, &expires, &authTime, &consumed)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, protocolError("invalid_grant")
	}
	if err != nil {
		return result, ErrUnavailable
	}
	if consumed != nil {
		if _, err = tx.Exec(ctx, `UPDATE identity_access_tokens SET revoked_at=clock_timestamp() WHERE code_id=$1 AND revoked_at IS NULL`, id); err != nil || tx.Commit(ctx) != nil {
			return result, ErrUnavailable
		}
		return result, protocolError("invalid_grant")
	}
	if _, err = tx.Exec(ctx, `UPDATE identity_codes SET consumed_at=clock_timestamp() WHERE id=$1`, id); err != nil {
		return result, ErrUnavailable
	}
	a, hash, err := scanAccount(tx.QueryRow(ctx, accountQuery+` AND a.id=$3 FOR SHARE OF a`, s.options.OrganizationID, s.options.Issuer, account))
	clear(hash)
	challengeMatch := subtle.ConstantTimeCompare([]byte(challenge), []byte(base64.RawURLEncoding.EncodeToString(digest(verifier)))) == 1
	if err != nil || !expires.After(time.Now()) || storedClient != client || storedRedirect != redirect || !challengeMatch || a.Generation != generation || a.Version != version || s.lockAuthority(ctx, tx, a) != nil {
		if tx.Commit(ctx) != nil {
			return result, ErrUnavailable
		}
		return result, protocolError("invalid_grant")
	}
	access := randomSecret()
	now := time.Now()
	accessHash := digest(access)
	claims := map[string]any{"iss": s.options.Issuer, "sub": a.ID, "aud": client, "iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(), "auth_time": authTime.Unix(), "nonce": nonce, "at_hash": base64.RawURLEncoding.EncodeToString(accessHash[:16]), "hc_auth_generation": a.Generation, "hc_credential_version": a.Version}
	if strings.Contains(" "+scope+" ", " profile ") {
		claims["name"] = a.Name
	}
	if strings.Contains(" "+scope+" ", " email ") {
		claims["email"] = a.Email
		claims["email_verified"] = false
	}
	payload, _ := json.Marshal(claims)
	signed, err := s.signer.Sign(payload)
	if err != nil {
		return result, ErrUnavailable
	}
	raw, err := signed.CompactSerialize()
	if err != nil {
		return result, ErrUnavailable
	}
	_, err = tx.Exec(ctx, `INSERT INTO identity_access_tokens(id,token_sha256,code_id,account_id,scope,auth_generation,credential_version,expires_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,clock_timestamp()+interval '5 minutes')`, newID(), accessHash, id, a.ID, scope, a.Generation, a.Version)
	if err != nil || tx.Commit(ctx) != nil {
		return result, ErrUnavailable
	}
	return tokenResponse{access, "Bearer", 300, raw, scope}, nil
}
func rateKey(value string) string { return hex.EncodeToString(digest(value)) }

// IAM changes lock memberships before publishing generation/status changes.
// Hold the same rows through code/token issuance to close the stale-read window.
func (s *AuthorizationServer) lockAuthority(ctx context.Context, tx pgx.Tx, a accountIdentity) error {
	var valid bool
	if err := tx.QueryRow(ctx, `SELECT public.hc_identity_lock_authority($1,$2,$3,$4,$5)`, s.options.OrganizationID, a.ID, s.options.Issuer, a.Version, a.Generation).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return ErrUnauthenticated
	}
	return nil
}
