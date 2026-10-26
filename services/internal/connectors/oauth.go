package connectors

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type oauthContext struct {
	Config   OAuthClient   `json:"config"`
	Metadata oauthMetadata `json:"metadata"`
	Verifier string        `json:"verifier"`
	Redirect string        `json:"redirect"`
	Endpoint Endpoint      `json:"endpoint"`
}
type oauthTokens struct {
	Context      oauthContext `json:"context"`
	AccessToken  string       `json:"access_token"`
	RefreshToken string       `json:"refresh_token"`
	ExpiresAt    time.Time    `json:"expires_at"`
}

func randomOAuth() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("secure random unavailable")
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}
func oauthAAD(s *Service, p identity.Principal, id string) security.Context {
	return security.Context{Environment: s.environment, OrganizationID: p.OrganizationID, RecordID: id, Purpose: "connector-oauth"}
}
func (s *Service) oauthRedirect() (string, error) {
	u, err := url.Parse(s.portalOrigin)
	if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1")) {
		return "", invalid("平台尚未配置有效的 OAuth 回调地址")
	}
	return s.portalOrigin + "/v1/connectors/oauth/callback", nil
}
func (s *Service) StartOAuth(ctx context.Context, p identity.Principal, id string, version int64, trace string) (any, error) {
	redirect, err := s.oauthRedirect()
	if err != nil {
		return nil, err
	}
	tx, err := s.begin(ctx, p, "connectors:manage")
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	c, err := load(ctx, tx, p, id)
	if err != nil {
		return nil, err
	}
	if c.Version != version {
		return nil, conflict()
	}
	if c.Auth != "oauth2" || !c.Enabled {
		return nil, invalid("请先保存并启用 OAuth 连接器")
	}
	if !uuidPattern.MatchString(p.SessionID) {
		return nil, identity.ErrUnauthenticated
	}
	e, err := s.connectionEndpoint(c)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM mcp_oauth_transactions WHERE org_id=$1 AND owner_id=$2 AND (expires_at<clock_timestamp() OR connector_id=$3)`, p.OrganizationID, p.UserID, id); err != nil {
		return nil, err
	}
	var pending int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM mcp_oauth_transactions WHERE org_id=$1 AND owner_id=$2`, p.OrganizationID, p.UserID).Scan(&pending); err != nil {
		return nil, err
	}
	if pending >= 5 {
		return nil, invalid("待完成的授权过多，请稍后重试")
	}
	config, m, err := s.oauthConfiguration(ctx, e)
	if err != nil {
		return nil, err
	}
	if config.ClientID == "" {
		if m.Registration == "" {
			return nil, invalid("此服务需要管理员配置 OAuth 应用后才能授权")
		}
		method := "none"
		if len(m.AuthMethods) > 0 && !slices.Contains(m.AuthMethods, method) {
			if slices.Contains(m.AuthMethods, "client_secret_basic") {
				method = "client_secret_basic"
			} else if slices.Contains(m.AuthMethods, "client_secret_post") {
				method = "client_secret_post"
			} else {
				return nil, invalid("此服务的 OAuth 客户端认证方式暂不支持")
			}
		}
		body, _ := json.Marshal(map[string]any{"client_name": "DSH Cloud", "redirect_uris": []string{redirect}, "grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"}, "token_endpoint_auth_method": method})
		var registration struct {
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
			AuthMethod   string `json:"token_endpoint_auth_method"`
		}
		if err = s.oauthHTTP(ctx, "POST", m.Registration, body, http.Header{"Content-Type": []string{"application/json"}}, &registration); err != nil {
			return nil, err
		}
		config.ClientID, config.ClientSecret, config.AuthMethod = registration.ClientID, registration.ClientSecret, method
		if registration.AuthMethod != "" {
			config.AuthMethod = registration.AuthMethod
		}
	}
	if config.AuthMethod == "" {
		config.AuthMethod = "none"
		if config.ClientSecret != "" {
			config.AuthMethod = "client_secret_basic"
		}
	}
	if config.ClientID == "" || !validOAuthValue(config.ClientID) || !validOAuthValue(config.ClientSecret) || !slices.Contains([]string{"none", "client_secret_basic", "client_secret_post"}, config.AuthMethod) || config.AuthMethod != "none" && config.ClientSecret == "" {
		return nil, invalid("OAuth 应用配置无效")
	}
	state, verifier := randomOAuth(), randomOAuth()
	challenge := sha256.Sum256([]byte(verifier))
	authURL, _ := url.Parse(m.Authorization)
	q := authURL.Query()
	q.Set("response_type", "code")
	q.Set("client_id", config.ClientID)
	q.Set("redirect_uri", redirect)
	q.Set("state", state)
	q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:]))
	q.Set("code_challenge_method", "S256")
	if config.Scopes != "" {
		q.Set("scope", config.Scopes)
	}
	if config.Resource != "" {
		q.Set("resource", config.Resource)
	}
	authURL.RawQuery = q.Encode()
	transactionID := webapi.ID()
	data, _ := json.Marshal(oauthContext{config, m, verifier, redirect, e})
	defer clear(data)
	envelope, err := s.cipher.Encrypt(data, oauthAAD(s, p, transactionID))
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO mcp_oauth_transactions(id,org_id,owner_id,connector_id,session_id,connector_version,state_sha256,expires_at,key_id,nonce,ciphertext) VALUES($1,$2,$3,$4,$5,$6,$7,clock_timestamp()+interval '10 minutes',$8,$9,$10)`, transactionID, p.OrganizationID, p.UserID, id, p.SessionID, c.Version, digest([]byte(state)), envelope.KeyID, envelope.Nonce, envelope.Ciphertext)
	if err == nil {
		err = audit(ctx, tx, p, id, "CONNECTOR_OAUTH_STARTED", trace)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	return map[string]string{"authorization_url": authURL.String()}, err
}

func (s *Service) exchangeToken(ctx context.Context, c oauthContext, values url.Values, previous string) (oauthTokens, error) {
	values.Set("client_id", c.Config.ClientID)
	if c.Config.Resource != "" {
		values.Set("resource", c.Config.Resource)
	}
	headers := http.Header{"Content-Type": []string{"application/x-www-form-urlencoded"}}
	if c.Config.AuthMethod == "client_secret_post" {
		values.Set("client_secret", c.Config.ClientSecret)
	}
	if c.Config.AuthMethod == "client_secret_basic" {
		headers.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(url.QueryEscape(c.Config.ClientID)+":"+url.QueryEscape(c.Config.ClientSecret))))
	}
	var response struct {
		Access  string      `json:"access_token"`
		Refresh string      `json:"refresh_token"`
		Type    string      `json:"token_type"`
		Expires json.Number `json:"expires_in"`
	}
	if err := s.oauthHTTP(ctx, "POST", c.Metadata.Token, []byte(values.Encode()), headers, &response); err != nil {
		return oauthTokens{}, err
	}
	if response.Access == "" || !validOAuthValue(response.Access) || !validOAuthValue(response.Refresh) || !strings.EqualFold(response.Type, "Bearer") {
		return oauthTokens{}, invalid("授权服务未返回有效的 Bearer 令牌")
	}
	c.Verifier = ""
	t := oauthTokens{Context: c, AccessToken: response.Access, RefreshToken: response.Refresh}
	if t.RefreshToken == "" {
		t.RefreshToken = previous
	}
	if response.Expires != "" {
		seconds, err := response.Expires.Int64()
		if err != nil || seconds <= 0 || seconds > 315360000 {
			return t, invalid("授权令牌有效期无效")
		}
		t.ExpiresAt = time.Now().Add(time.Duration(seconds) * time.Second)
	}
	return t, nil
}

// State is consumed before contacting the token server. An interrupted exchange
// requires a fresh authorization; codes and tool calls are never replayed.
func (s *Service) FinishOAuth(ctx context.Context, p identity.Principal, q url.Values, trace string) (Connection, error) {
	for k, v := range q {
		if len(v) != 1 || !slices.Contains([]string{"state", "code", "error", "error_description", "error_uri", "iss"}, k) || len(v[0]) > 8192 {
			return Connection{}, invalid("授权回调参数无效")
		}
	}
	state := q.Get("state")
	if len(state) != 43 {
		return Connection{}, invalid("授权请求已失效，请重新授权")
	}
	tx, err := s.begin(ctx, p, "connectors:manage")
	if err != nil {
		return Connection{}, err
	}
	defer tx.Rollback(context.Background())
	var id, cid string
	var version int64
	var envelope security.Envelope
	err = tx.QueryRow(ctx, `DELETE FROM mcp_oauth_transactions WHERE state_sha256=$1 AND org_id=$2 AND owner_id=$3 AND session_id=$4 AND expires_at>clock_timestamp() RETURNING id::text,connector_id::text,connector_version,key_id,nonce,ciphertext`, digest([]byte(state)), p.OrganizationID, p.UserID, p.SessionID).Scan(&id, &cid, &version, &envelope.KeyID, &envelope.Nonce, &envelope.Ciphertext)
	if errors.Is(err, pgx.ErrNoRows) {
		return Connection{}, invalid("授权请求已失效，请重新授权")
	}
	if err != nil {
		return Connection{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Connection{}, err
	}
	data, err := s.cipher.Decrypt(envelope, oauthAAD(s, p, id))
	if err != nil {
		return Connection{}, unavailable()
	}
	defer clear(data)
	var auth oauthContext
	if json.Unmarshal(data, &auth) != nil {
		return Connection{}, unavailable()
	}
	if q.Get("error") != "" {
		return Connection{}, invalid("授权未完成，可重新发起连接")
	}
	if q.Get("code") == "" {
		return Connection{}, invalid("授权服务未返回授权码")
	}
	if q.Get("iss") != "" && q.Get("iss") != auth.Config.Issuer {
		return Connection{}, invalid("授权服务不匹配，请重新授权")
	}
	// Reload ownership, permission, version, and current egress policy before sending a code.
	tx, err = s.begin(ctx, p, "connectors:manage")
	if err != nil {
		return Connection{}, err
	}
	defer tx.Rollback(context.Background())
	c, err := load(ctx, tx, p, cid)
	if err != nil {
		return c, err
	}
	if c.Version != version || !c.Enabled || c.Auth != "oauth2" {
		return c, conflict()
	}
	e, err := s.connectionEndpoint(c)
	if err != nil {
		return c, err
	}
	if e != auth.Endpoint {
		return c, conflict()
	}
	tokens, err := s.exchangeToken(ctx, auth, url.Values{"grant_type": {"authorization_code"}, "code": {q.Get("code")}, "redirect_uri": {auth.Redirect}, "code_verifier": {auth.Verifier}}, "")
	if err != nil {
		return c, err
	}
	if err = s.storeOAuth(ctx, tx, p, c, tokens); err != nil {
		return c, err
	}
	_, err = tx.Exec(ctx, `UPDATE mcp_connectors SET status='UNVERIFIED',tools='[]',server_info='{}',verified_at=NULL,version=version+1,catalog_version=catalog_version+1,updated_at=clock_timestamp() WHERE id=$1`, cid)
	if err == nil {
		err = audit(ctx, tx, p, cid, "CONNECTOR_OAUTH_AUTHORIZED", trace)
	}
	if err == nil {
		c, err = load(ctx, tx, p, cid)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		return c, err
	}
	s.cancelConnection(p.UserID, cid)
	// Verification never enables tools or invokes external actions.
	return s.Sync(ctx, p, cid, c.Version, trace)
}
func (s *Service) storeOAuth(ctx context.Context, tx pgx.Tx, p identity.Principal, c Connection, t oauthTokens) error {
	data, _ := json.Marshal(t)
	defer clear(data)
	e, err := s.cipher.Encrypt(data, s.aad(p, c.ID))
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE mcp_connectors SET key_id=$2,nonce=$3,ciphertext=$4 WHERE id=$1`, c.ID, e.KeyID, e.Nonce, e.Ciphertext)
	return err
}
func (s *Service) credential(ctx context.Context, tx pgx.Tx, p identity.Principal, c Connection) (string, error) {
	if c.Auth == "none" {
		return "", nil
	}
	data, err := s.cipher.Decrypt(c.envelope, s.aad(p, c.ID))
	if err != nil {
		return "", unavailable()
	}
	defer clear(data)
	if c.Auth == "bearer" {
		if string(data) == "-" {
			return "", errAuth
		}
		return string(data), nil
	}
	var t oauthTokens
	if json.Unmarshal(data, &t) != nil || t.AccessToken == "" {
		return "", errAuth
	}
	e, err := s.connectionEndpoint(c)
	if err != nil {
		return "", err
	}
	if e != t.Context.Endpoint {
		return "", errAuth
	}
	if !t.ExpiresAt.IsZero() && time.Until(t.ExpiresAt) < 30*time.Second {
		if t.RefreshToken == "" {
			return "", errAuth
		}
		fresh, err := s.exchangeToken(ctx, t.Context, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {t.RefreshToken}}, t.RefreshToken)
		if err != nil {
			return "", err
		}
		if err = s.storeOAuth(ctx, tx, p, c, fresh); err != nil {
			return "", err
		}
		t = fresh
	}
	return t.AccessToken, nil
}
