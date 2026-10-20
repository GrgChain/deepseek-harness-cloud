package identity

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type OIDCProvider struct {
	config   oauth2.Config
	verifier *oidc.IDTokenVerifier
	options  *Options
	internal *AuthorizationServer
}

func NewOIDCProvider(o *Options) (*OIDCProvider, error) {
	ctx := oidc.ClientContext(context.Background(), o.HTTPClient)
	discovery, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	provider, err := oidc.NewProvider(discovery, o.Issuer)
	if err != nil {
		return nil, errors.New("OIDC discovery failed")
	}
	endpoint := provider.Endpoint()
	var discoveryClaims struct {
		JWKS string `json:"jwks_uri"`
	}
	if err := provider.Claims(&discoveryClaims); err != nil {
		return nil, errors.New("OIDC discovery claims are invalid")
	}
	for _, value := range []string{endpoint.AuthURL, endpoint.TokenURL, discoveryClaims.JWKS} {
		u, err := url.Parse(value)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return nil, errors.New("OIDC endpoints must use HTTPS")
		}
	}
	verifier := provider.VerifierContext(ctx, &oidc.Config{ClientID: o.ClientID, SupportedSigningAlgs: []string{oidc.RS256, oidc.ES256}})
	return &OIDCProvider{options: o, config: oauth2.Config{ClientID: o.ClientID, ClientSecret: o.ClientSecret, Endpoint: endpoint, RedirectURL: o.PublicURL + "/auth/callback", Scopes: []string{oidc.ScopeOpenID, "profile", "email"}}, verifier: verifier}, nil
}

func (p *OIDCProvider) Authorize(state, nonce, verifier string) string {
	return p.config.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier), oauth2.SetAuthURLParam("prompt", "login"))
}

func (p *OIDCProvider) Exchange(ctx context.Context, code, verifier string) (VerifiedIdentity, error) {
	ctx = oidc.ClientContext(ctx, p.options.HTTPClient)
	var raw string
	if p.internal != nil {
		response, err := p.internal.exchange(ctx, p.options.ClientID, p.options.ClientSecret, code, p.config.RedirectURL, verifier)
		if err != nil {
			return VerifiedIdentity{}, ErrUnauthenticated
		}
		raw = response.IDToken
	} else {
		token, err := p.config.Exchange(ctx, code, oauth2.VerifierOption(verifier))
		if err != nil {
			return VerifiedIdentity{}, ErrUnauthenticated
		}
		var ok bool
		raw, ok = token.Extra("id_token").(string)
		if !ok || raw == "" {
			return VerifiedIdentity{}, ErrUnauthenticated
		}
	}
	id, err := p.verifier.Verify(ctx, raw)
	if err != nil || id.Subject == "" || len(id.Subject) > 1024 || id.Nonce == "" || id.Expiry.Before(time.Now()) {
		return VerifiedIdentity{}, ErrUnauthenticated
	}
	var claims struct {
		Generation        int64 `json:"hc_auth_generation"`
		CredentialVersion int64 `json:"hc_credential_version"`
	}
	if err := id.Claims(&claims); err != nil {
		return VerifiedIdentity{}, ErrUnauthenticated
	}
	return VerifiedIdentity{Issuer: id.Issuer, Subject: id.Subject, Nonce: id.Nonce, Generation: claims.Generation, CredentialVersion: claims.CredentialVersion}, nil
}
