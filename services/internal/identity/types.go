package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	ErrUnauthenticated = errors.New("authentication required")
	ErrForbidden       = errors.New("identity is not authorized")
	ErrCSRF            = errors.New("request verification failed")
	ErrUnavailable     = errors.New("identity service unavailable")
)

const SessionCookie = "__Host-hc_session"
const LoginCookie = "__Host-hc_oidc"

var uuidPattern = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`)
var returnPattern = regexp.MustCompile(`^/app/[A-Za-z0-9/_-]*$`)

func ReturnPath(path string) (string, error) {
	if path == "" {
		return "/app/instances", nil
	}
	if len(path) > 256 || !returnPattern.MatchString(path) || strings.Contains(path, "//") {
		return "", ErrForbidden
	}
	return path, nil
}

func randomSecret() string { return base64.RawURLEncoding.EncodeToString(randomBytes(32)) }
func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("secure random source unavailable")
	}
	return b
}
func newID() string {
	b := randomBytes(16)
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func digest(value string) []byte { sum := sha256.Sum256([]byte(value)); return sum[:] }

type Membership struct {
	OrganizationID string `json:"org_id"`
	Role           string `json:"role"`
	RoleID         string `json:"role_id"`
	RoleName       string `json:"role_name"`
	Status         string `json:"status"`
}
type CurrentUser struct {
	ID          string       `json:"id"`
	Memberships []Membership `json:"memberships"`
	Permissions []string     `json:"permissions"`
	Generation  int64        `json:"auth_generation"`
	CSRFToken   string       `json:"csrf_token"`
	ExpiresAt   time.Time    `json:"session_expires_at"`
}
type Principal struct {
	SessionID, OrganizationID, UserID, Role string
	RoleID, RoleName                        string
	Permissions                             []string
	Generation                              int64
	ExpiresAt                               time.Time
	CSRFHash                                []byte
	CSRFToken                               string
}

func ValidateWrite(p Principal, csrf, origin, expectedOrigin string) error {
	if expectedOrigin == "" || origin != expectedOrigin || len(csrf) < 32 || len(csrf) > 128 || subtle.ConstantTimeCompare(p.CSRFHash, digest(csrf)) != 1 {
		return ErrCSRF
	}
	return nil
}

func (p Principal) CurrentUser() CurrentUser {
	permissions := append([]string{}, p.Permissions...)
	return CurrentUser{ID: p.UserID, Memberships: []Membership{{OrganizationID: p.OrganizationID, Role: p.Role, RoleID: p.RoleID, RoleName: p.RoleName, Status: "ACTIVE"}}, Permissions: permissions, Generation: p.Generation, CSRFToken: p.CSRFToken, ExpiresAt: p.ExpiresAt}
}

type LoginResult struct{ URL, BrowserSecret string }
type CallbackResult struct {
	ReturnPath, SessionSecret string
	ExpiresAt                 time.Time
}

type Authentication interface {
	Login(context.Context, string, string) (LoginResult, error)
	Callback(context.Context, string, string, string, string) (CallbackResult, error)
	Authenticate(context.Context, string) (Principal, error)
	Logout(context.Context, Principal, string, string) error
}

type VerifiedIdentity struct {
	Issuer, Subject, Nonce        string
	Generation, CredentialVersion int64
}
type Provider interface {
	Authorize(state, nonce, verifier string) string
	Exchange(context.Context, string, string) (VerifiedIdentity, error)
}
