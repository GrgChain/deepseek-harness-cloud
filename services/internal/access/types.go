package access

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/instances"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

const ChallengeCookie = "__Host-hc_challenge"
const SessionCookie = "__Host-hc_access"

var uuid = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`)
var domainName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+$`)
var opaque = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
var nativeValue = regexp.MustCompile(`^v1\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$`)

func base64Host(host string) string {
	sum := sha256.Sum256([]byte(host))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
func validNativeHeader(host string, cookie []byte) bool {
	prefix := "dsh-auth-" + base64Host(host) + "="
	return len(cookie) <= 8192 && strings.HasPrefix(string(cookie), prefix) && nativeValue.Match(cookie[len(prefix):])
}

type Rules struct{ PortalOrigin, Domain, port string }

func NewRules(portal, domain string) (Rules, error) {
	u, err := url.Parse(portal)
	if err != nil || !domainName.MatchString(domain) || len(domain) > 200 || net.ParseIP(domain) != nil || u.Scheme != "https" || u.Hostname() != "portal."+domain || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return Rules{}, denied()
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
		return Rules{}, denied()
	}
	return Rules{strings.TrimSuffix(portal, "/"), domain, port}, nil
}
func (r Rules) Host(id string) string { return "i-" + strings.ReplaceAll(id, "-", "") + "." + r.Domain }
func (r Rules) authority(host string) string {
	if r.port == "443" {
		return host
	}
	return net.JoinHostPort(host, r.port)
}
func (r Rules) URL(host string) string { return "https://" + r.authority(host) }
func (r Rules) Resolve(authority string) (string, bool) {
	host := authority
	if r.port != "443" {
		var port string
		var err error
		host, port, err = net.SplitHostPort(authority)
		if err != nil || port != r.port {
			return "", false
		}
	}
	prefix, suffix := "i-", "."+r.Domain
	if !strings.HasPrefix(host, prefix) || !strings.HasSuffix(host, suffix) {
		return "", false
	}
	raw := strings.TrimSuffix(strings.TrimPrefix(host, prefix), suffix)
	if len(raw) != 32 {
		return "", false
	}
	id := raw[:8] + "-" + raw[8:12] + "-" + raw[12:16] + "-" + raw[16:20] + "-" + raw[20:]
	return id, uuid.MatchString(id) && r.Host(id) == host && r.authority(host) == authority
}
func denied() error {
	return webapi.Fail(403, "FORBIDDEN", "访问授权已失效，请从控制台重新进入")
}
func unavailable() error {
	return webapi.Fail(503, "DEPENDENCY_UNAVAILABLE", "工作空间访问暂不可用，请稍后重试")
}
func notFound() error { return webapi.Fail(404, "NOT_FOUND", "工作空间不存在或不可访问") }
func newSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("secure random source unavailable")
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func hash(secret string) []byte { d := sha256.Sum256([]byte(secret)); return d[:] }

type Target struct {
	InstanceID, OrganizationID, OwnerID, WorkspaceOwner, WorkspaceID, WorkspaceName, AssetID string
	Host, IP, Generation, BuildID                                                            string
	Version, AccessGeneration                                                                int64
	Snapshot                                                                                 instances.Snapshot
}

func (t Target) String() string               { return "[redacted access target]" }
func (t Target) GoString() string             { return t.String() }
func (t Target) MarshalJSON() ([]byte, error) { return json.Marshal(t.String()) }
func (t Target) same(other Target) bool       { return t == other }

type Challenge struct {
	ID        string    `json:"challenge_id"`
	ExpiresAt time.Time `json:"expires_at"`
}
type Handoff struct {
	URL         string    `json:"handoff_url"`
	Ticket      string    `json:"ticket"`
	ChallengeID string    `json:"challenge_id"`
	ExpiresAt   time.Time `json:"expires_at"`
}

func (h Handoff) String() string   { return "[redacted access handoff]" }
func (h Handoff) GoString() string { return h.String() }

type Authorization struct {
	Target    Target
	Principal identity.Principal
	SessionID string
	ExpiresAt time.Time
	Cookie    []byte
}

func (a Authorization) String() string               { return "[redacted access authorization]" }
func (a Authorization) GoString() string             { return a.String() }
func (a Authorization) MarshalJSON() ([]byte, error) { return json.Marshal(a.String()) }
func (a *Authorization) Destroy()                    { clear(a.Cookie); a.Cookie = nil }
