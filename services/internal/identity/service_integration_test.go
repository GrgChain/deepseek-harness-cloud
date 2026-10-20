package identity

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
)

type fixtureProvider struct {
	issuer, subject string
	mu              sync.Mutex
	nonces          map[string]string
}

func (p *fixtureProvider) Authorize(state, nonce, verifier string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.nonces[state] = nonce
	return "https://idp.example/auth?state=" + state
}
func (p *fixtureProvider) Exchange(_ context.Context, code, verifier string) (VerifiedIdentity, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	nonce := p.nonces[code]
	if code == "bad" || verifier == "" {
		return VerifiedIdentity{}, ErrUnauthenticated
	}
	return VerifiedIdentity{Issuer: p.issuer, Subject: p.subject, Nonce: nonce}, nil
}

func TestIdentityWithAuthoritativePostgres(t *testing.T) {
	path := os.Getenv("HC_TEST_DB_DSN_FILE")
	if path == "" {
		t.Skip("isolated PostgreSQL 17 identity database required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("cannot read test connection file")
	}
	u, err := url.Parse(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal("invalid test connection")
	}
	u.Path = "/hc_test_identity"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.Apply(ctx, u.String()); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	org := "00000000-0000-4000-8000-000000000011"
	if err := ApplyMapping(ctx, pool, Mapping{SchemaVersion: 1, OrganizationID: org, Members: []MemberMapping{{Issuer: "https://idp.example", Subject: "admin", Role: "ADMIN", WorkspaceOwnerID: "00000000-0000-4000-8000-000000000012"}}}, "test-runner"); err != nil {
		t.Fatal(err)
	}
	provider := &fixtureProvider{issuer: "https://idp.example", subject: "admin", nonces: map[string]string{}}
	cipher, _ := security.NewSecretCipher("test-key", bytes.Repeat([]byte{7}, 32))
	s := NewService(pool, provider, cipher, "test", org, "https://portal.example")
	login := func(old string) (LoginResult, string) {
		t.Helper()
		result, err := s.Login(ctx, old, "/app/instances")
		if err != nil {
			t.Fatal(err)
		}
		parsed, _ := url.Parse(result.URL)
		return result, parsed.Query().Get("state")
	}

	t.Run("browser binding and one-time consumption", func(t *testing.T) {
		l, state := login("")
		if _, err := s.Callback(ctx, state, randomSecret(), state, ""); !errors.Is(err, ErrUnauthenticated) {
			t.Fatal("cross-browser state accepted")
		}
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		results := make(chan CallbackResult, 2)
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r, e := s.Callback(ctx, state, l.BrowserSecret, state, "")
				errs <- e
				results <- r
			}()
		}
		wg.Wait()
		close(errs)
		close(results)
		success := 0
		for e := range errs {
			if e == nil {
				success++
			} else if !errors.Is(e, ErrUnauthenticated) {
				t.Fatal(e)
			}
		}
		if success != 1 {
			t.Fatalf("callback successes=%d", success)
		}
		for r := range results {
			if r.SessionSecret != "" {
				p, e := s.Authenticate(ctx, r.SessionSecret)
				if e != nil || p.Role != "ADMIN" || len(p.CSRFToken) != 43 {
					t.Fatal("session not authorized")
				}
				if e := s.Logout(ctx, p, p.CSRFToken, "https://sibling.example"); !errors.Is(e, ErrCSRF) {
					t.Fatal("sibling origin logout accepted")
				}
				if e := s.Logout(ctx, p, "wrong", "https://portal.example"); !errors.Is(e, ErrCSRF) {
					t.Fatal("wrong CSRF accepted")
				}
				if e := s.Logout(ctx, p, p.CSRFToken, "https://portal.example"); e != nil {
					t.Fatal(e)
				}
				if _, e := s.Authenticate(ctx, r.SessionSecret); !errors.Is(e, ErrUnauthenticated) {
					t.Fatal("revoked session accepted")
				}
			}
		}
	})
	t.Run("new login invalidates prior pending transaction", func(t *testing.T) {
		a, state := login("")
		_, _ = login(a.BrowserSecret)
		if _, err := s.Callback(ctx, state, a.BrowserSecret, state, ""); !errors.Is(err, ErrUnauthenticated) {
			t.Fatal("old binding accepted")
		}
	})
	t.Run("nonce and provider denial fail closed", func(t *testing.T) {
		l, state := login("")
		provider.mu.Lock()
		provider.nonces[state] = "wrong"
		provider.mu.Unlock()
		if _, err := s.Callback(ctx, state, l.BrowserSecret, state, ""); !errors.Is(err, ErrUnauthenticated) {
			t.Fatal("wrong nonce accepted")
		}
		l, state = login("")
		if _, err := s.Callback(ctx, state, l.BrowserSecret, "", ""); !errors.Is(err, ErrUnauthenticated) {
			t.Fatal("denial accepted")
		}
		if _, err := s.Callback(ctx, state, l.BrowserSecret, state, ""); !errors.Is(err, ErrUnauthenticated) {
			t.Fatal("denied state reused")
		}
	})
	t.Run("unknown subject cannot self-register", func(t *testing.T) {
		provider.subject = "unknown"
		defer func() { provider.subject = "admin" }()
		l, state := login("")
		if _, err := s.Callback(ctx, state, l.BrowserSecret, state, ""); !errors.Is(err, ErrForbidden) {
			t.Fatal("unknown identity authorized")
		}
	})
	t.Run("idle expiry and role generation", func(t *testing.T) {
		l, state := login("")
		r, err := s.Callback(ctx, state, l.BrowserSecret, state, "")
		if err != nil {
			t.Fatal(err)
		}
		p, err := s.Authenticate(ctx, r.SessionSecret)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, "UPDATE portal_sessions SET last_seen_at=clock_timestamp()-interval '31 minutes' WHERE id=$1", p.SessionID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Authenticate(ctx, r.SessionSecret); !errors.Is(err, ErrUnauthenticated) {
			t.Fatal("idle expired session refreshed")
		}
		l, state = login("")
		r, err = s.Callback(ctx, state, l.BrowserSecret, state, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, "UPDATE memberships SET auth_generation=auth_generation+1 WHERE org_id=$1", org); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Authenticate(ctx, r.SessionSecret); !errors.Is(err, ErrUnauthenticated) {
			t.Fatal("old role generation authorized")
		}
	})
	t.Run("controlled Workspace Owner rebind revokes old sessions and preserves last admin", func(t *testing.T) {
		l, state := login("")
		r, err := s.Callback(ctx, state, l.BrowserSecret, state, "")
		if err != nil {
			t.Fatal(err)
		}
		mapping := Mapping{SchemaVersion: 1, OrganizationID: org, Members: []MemberMapping{{Issuer: "https://idp.example", Subject: "admin", Role: "ADMIN", WorkspaceOwnerID: "00000000-0000-4000-8000-000000000013"}}}
		if err := ApplyMapping(ctx, pool, mapping, "test-rebind"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Authenticate(ctx, r.SessionSecret); !errors.Is(err, ErrUnauthenticated) {
			t.Fatal("old Workspace Owner mapping session survived")
		}
		mapping.Members[0].Role = "MEMBER"
		if err := ApplyMapping(ctx, pool, mapping, "test-demote"); err == nil {
			t.Fatal("last administrator was removed")
		}
		mapping.Members[0].Role = "ADMIN"
		mapping.Members[0].WorkspaceOwnerID = "00000000-0000-4000-8000-000000000012"
		if err := ApplyMapping(ctx, pool, mapping, "test-restore"); err != nil {
			t.Fatal(err)
		}
	})
}
