package identity

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/config"
)

type Options struct {
	PublicURL, Issuer, ClientID, ClientSecret, OrganizationID, Environment, KeyID string
	KeyringFile, SigningKeyFile                                                   string
	Key                                                                           []byte
	HTTPClient                                                                    *http.Client
}

func LoadOptions(getenv func(string) string) (*Options, error) {
	if mode := getenv("HC_AUTH_MODE"); mode != "" && mode != "oidc" {
		return nil, errors.New("HC_AUTH_MODE must be oidc")
	}
	if getenv("HC_OIDC_ISSUER") == "" {
		for _, name := range []string{"HC_OIDC_CLIENT_ID", "HC_OIDC_CLIENT_SECRET_FILE", "HC_PUBLIC_URL", "HC_ORGANIZATION_ID", "HC_DATA_KEY_FILE"} {
			if getenv(name) != "" {
				return nil, errors.New("identity configuration is incomplete")
			}
		}
		return nil, nil
	}
	o := &Options{PublicURL: getenv("HC_PUBLIC_URL"), Issuer: getenv("HC_OIDC_ISSUER"), ClientID: getenv("HC_OIDC_CLIENT_ID"), OrganizationID: getenv("HC_ORGANIZATION_ID"), Environment: getenv("HC_ENVIRONMENT"), KeyID: getenv("HC_DATA_KEY_ID")}
	for _, value := range []string{o.PublicURL, o.Issuer} {
		u, err := url.Parse(value)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return nil, errors.New("Portal and OIDC issuer require approved HTTPS URLs")
		}
	}
	u, _ := url.Parse(o.PublicURL)
	if u.Path != "" && u.Path != "/" {
		return nil, errors.New("HC_PUBLIC_URL must be an HTTPS origin")
	}
	o.PublicURL = strings.TrimSuffix(o.PublicURL, "/")
	o.SigningKeyFile = getenv("HC_OIDC_SIGNING_KEY_FILE")
	if o.Issuer != o.PublicURL+"/oidc" || o.SigningKeyFile == "" {
		return nil, errors.New("identity requires the embedded same-origin issuer and signing key file")
	}
	if o.ClientID == "" || !uuidPattern.MatchString(o.OrganizationID) || o.Environment == "" || o.KeyID == "" {
		return nil, errors.New("identity organization, client, environment and key identifier are required")
	}
	var err error
	o.ClientSecret, err = config.ReadSecret(getenv("HC_OIDC_CLIENT_SECRET_FILE"))
	if err != nil {
		return nil, err
	}
	o.Key, err = config.ReadPrivateFile(getenv("HC_DATA_KEY_FILE"))
	if err != nil || len(o.Key) != 32 {
		return nil, errors.New("HC_DATA_KEY_FILE requires a private 32-byte key")
	}
	o.KeyringFile = getenv("HC_DATA_KEYRING_FILE")
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	if path := getenv("HC_OIDC_CA_FILE"); path != "" {
		pem, err := config.ReadPrivateFile(path)
		if err != nil {
			return nil, err
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, errors.New("HC_OIDC_CA_FILE is invalid")
		}
		transport.TLSClientConfig.RootCAs = roots
	}
	o.HTTPClient = &http.Client{Transport: transport, Timeout: 8 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	return o, nil
}
