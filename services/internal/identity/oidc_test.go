package identity

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

func TestOIDCVerifiesSignedIdentityClaimsAndPKCE(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	issuer, rawToken, jwksURL := "", "", ""
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": issuer, "authorization_endpoint": issuer + "/auth", "token_endpoint": issuer + "/token", "jwks_uri": jwksURL, "id_token_signing_alg_values_supported": []string{"RS256"}})
		case "/keys":
			_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "test", Algorithm: "RS256", Use: "sig"}}})
		case "/token":
			_ = r.ParseForm()
			if r.Form.Get("code_verifier") != "synthetic-verifier" {
				t.Error("PKCE verifier missing")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "synthetic-unused-access-token", "token_type": "Bearer", "id_token": rawToken})
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	issuer = server.URL
	jwksURL = issuer + "/keys"
	options := &Options{Issuer: issuer, PublicURL: "https://portal.example", ClientID: "test-client", ClientSecret: "synthetic-secret", HTTPClient: server.Client()}
	provider, err := NewOIDCProvider(options)
	if err != nil {
		t.Fatal(err)
	}
	if auth := provider.Authorize("state", "nonce", "verifier"); !strings.Contains(auth, "code_challenge_method=S256") || !strings.Contains(auth, "nonce=nonce") {
		t.Fatal("authorization lacks PKCE or nonce")
	}
	for _, tc := range []string{"valid", "wrong audience", "wrong issuer", "expired", "missing nonce", "wrong signature", "malformed"} {
		t.Run(tc, func(t *testing.T) {
			claims := map[string]any{"iss": issuer, "sub": "stable-subject", "aud": "test-client", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "nonce": "expected-nonce"}
			signKey := key
			switch tc {
			case "wrong audience":
				claims["aud"] = "other-client"
			case "wrong issuer":
				claims["iss"] = "https://evil.example"
			case "expired":
				claims["exp"] = time.Now().Add(-time.Hour).Unix()
			case "missing nonce":
				delete(claims, "nonce")
			case "wrong signature":
				signKey = other
			}
			signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: signKey}, (&jose.SignerOptions{}).WithHeader("kid", "test"))
			if err != nil {
				t.Fatal(err)
			}
			payload, _ := json.Marshal(claims)
			signed, err := signer.Sign(payload)
			if err != nil {
				t.Fatal(err)
			}
			rawToken, err = signed.CompactSerialize()
			if err != nil {
				t.Fatal(err)
			}
			if tc == "malformed" {
				rawToken = "invalid"
			}
			id, err := provider.Exchange(context.Background(), "synthetic-code", "synthetic-verifier")
			if tc == "valid" {
				if err != nil || id.Subject != "stable-subject" || id.Nonce != "expected-nonce" {
					t.Fatal("signed identity rejected")
				}
			} else if !errors.Is(err, ErrUnauthenticated) {
				t.Fatal("untrusted ID token accepted")
			}
		})
	}
	jwksURL = "http://127.0.0.1/keys"
	if _, err := NewOIDCProvider(options); err == nil {
		t.Fatal("plaintext JWKS endpoint accepted")
	}
}
