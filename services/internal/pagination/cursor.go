// Package pagination signs short-lived keyset positions. It carries no
// authorization grant; every page still requires current database authorization.
package pagination

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

var ErrInvalidCursor = errors.New("cursor is invalid or expired")
var uuid = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`)

type Scope struct{ Subject, Organization, Resource, Filters string }
type Position struct {
	Timestamp time.Time `json:"timestamp"`
	ID        string    `json:"id"`
}
type payload struct {
	Version      int       `json:"version"`
	Subject      string    `json:"subject"`
	Organization string    `json:"organization"`
	Resource     string    `json:"resource"`
	FilterHash   string    `json:"filter_hash"`
	Position     Position  `json:"position"`
	IssuedAt     time.Time `json:"issued_at"`
	ExpiresAt    time.Time `json:"expires_at"`
}
type Codec struct{ key []byte }

func NewCodec(masterKey []byte, environment string) (*Codec, error) {
	if len(masterKey) != 32 || environment == "" {
		return nil, errors.New("cursor signing configuration is incomplete")
	}
	derive := hmac.New(sha256.New, masterKey)
	derive.Write([]byte("hc-cursor-key-v1\x00" + environment))
	return &Codec{key: derive.Sum(nil)}, nil
}
func (c *Codec) signature(data []byte) []byte {
	mac := hmac.New(sha256.New, c.key)
	mac.Write(data)
	return mac.Sum(nil)
}
func filterHash(scope Scope) string {
	hash := sha256.Sum256([]byte(scope.Filters))
	return hex.EncodeToString(hash[:])
}
func (scope Scope) valid() bool {
	return uuid.MatchString(scope.Subject) && uuid.MatchString(scope.Organization) && (scope.Resource == "instances" || scope.Resource == "audit-events" || scope.Resource == "users" || scope.Resource == "roles")
}
func (c *Codec) Encode(scope Scope, position Position, now time.Time) (string, error) {
	if !scope.valid() || !uuid.MatchString(position.ID) || position.Timestamp.IsZero() || now.IsZero() {
		return "", ErrInvalidCursor
	}
	position.Timestamp = position.Timestamp.UTC()
	data, err := json.Marshal(payload{Version: 1, Subject: scope.Subject, Organization: scope.Organization, Resource: scope.Resource, FilterHash: filterHash(scope), Position: position, IssuedAt: now.UTC(), ExpiresAt: now.UTC().Add(15 * time.Minute)})
	if err != nil {
		return "", ErrInvalidCursor
	}
	encoding := base64.RawURLEncoding
	return encoding.EncodeToString(data) + "." + encoding.EncodeToString(c.signature(data)), nil
}
func (c *Codec) Decode(token string, scope Scope, now time.Time) (Position, error) {
	if len(token) > 2048 || !scope.valid() {
		return Position{}, ErrInvalidCursor
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return Position{}, ErrInvalidCursor
	}
	encoding := base64.RawURLEncoding.Strict()
	data, err := encoding.DecodeString(parts[0])
	if err != nil {
		return Position{}, ErrInvalidCursor
	}
	signature, err := encoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(signature, c.signature(data)) {
		return Position{}, ErrInvalidCursor
	}
	var value payload
	if webapi.StrictUnmarshal(data, &value) != nil || value.Version != 1 || value.Subject != scope.Subject || value.Organization != scope.Organization || value.Resource != scope.Resource || value.FilterHash != filterHash(scope) || !uuid.MatchString(value.Position.ID) || value.Position.Timestamp.IsZero() || value.IssuedAt.IsZero() || value.IssuedAt.After(now) || !value.ExpiresAt.After(now) || value.ExpiresAt.Sub(value.IssuedAt) != 15*time.Minute {
		return Position{}, ErrInvalidCursor
	}
	return value.Position, nil
}
