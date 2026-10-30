package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type Server struct {
	role     string
	probe    func(context.Context) error
	identity *identity.Handler
	provider http.Handler
	business interface {
		ServeAuthenticated(http.ResponseWriter, *http.Request, identity.Principal)
	}
	origin string
}

func (s *Server) WithBusiness(handler interface {
	ServeAuthenticated(http.ResponseWriter, *http.Request, identity.Principal)
}, origin string) {
	s.business = handler
	s.origin = origin
}

func (s *Server) WithIdentity(handler *identity.Handler) { s.identity = handler }
func (s *Server) WithOIDCProvider(handler http.Handler)  { s.provider = handler }

func New(role string, probe func(context.Context) error) *Server {
	return &Server{role: role, probe: probe}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	requestID := hex.EncodeToString(id)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Request-ID", requestID)
	w.Header().Set("Referrer-Policy", "no-referrer")
	if s.provider != nil && strings.HasPrefix(r.URL.Path, "/oidc/") {
		s.provider.ServeHTTP(w, r)
		return
	}
	if s.identity != nil && (r.URL.Path == "/v1/me" || r.URL.Path == "/auth/login" || r.URL.Path == "/auth/callback" || r.URL.Path == "/auth/logout") {
		ctx, cancel := context.WithTimeout(r.Context(), 9*time.Second)
		defer cancel()
		s.identity.ServeHTTP(w, r.WithContext(ctx))
		return
	}
	fail := func(status int, code, message string) {
		webapi.WriteError(w, r, webapi.Fail(status, code, message))
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/livez":
		webapi.JSON(w, 200, map[string]any{"status": "alive", "process": s.role, "capabilities": map[string]bool{"authentication": s.identity != nil, "instances": false}})
	case r.Method == http.MethodGet && r.URL.Path == "/readyz":
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if s.probe == nil || s.probe(ctx) != nil {
			fail(503, "DEPENDENCY_UNAVAILABLE", "Process dependencies are unavailable")
			return
		}
		webapi.JSON(w, 200, map[string]string{"status": "ready", "process": s.role})
	case strings.HasPrefix(r.URL.Path, "/v1/"):
		if s.identity != nil {
			p, err := s.identity.Authenticate(r)
			if err != nil {
				if r.Method == "GET" && r.URL.Path == "/v1/connectors/oauth/callback" && strings.Contains(r.Header.Get("Accept"), "text/html") && errors.Is(err, identity.ErrUnauthenticated) {
					http.Redirect(w, r, "/app/connectors?connector_oauth=failed", http.StatusSeeOther)
					return
				}
				identity.WriteError(w, r, err)
				return
			}
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				if err := identity.ValidateWrite(p, r.Header.Get("X-CSRF-Token"), r.Header.Get("Origin"), s.origin); err != nil {
					identity.WriteError(w, r, err)
					return
				}
			}
			if s.business != nil {
				budget := 9 * time.Second
				if strings.HasPrefix(r.URL.Path, "/v1/skills") || strings.HasPrefix(r.URL.Path, "/v1/context/") {
					budget = 20 * time.Second
					_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(budget))
				}
				if r.Method == http.MethodPost && (r.URL.Path == "/v1/skills:source" || strings.HasPrefix(r.URL.Path, "/v1/skills/") && strings.HasSuffix(r.URL.Path, ":check-update")) {
					budget = 125 * time.Second
					_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(budget))
				}
				if strings.HasPrefix(r.URL.Path, "/v1/connectors") {
					budget = 125 * time.Second
					_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(budget))
				}
				if r.Method == http.MethodPost && r.URL.Path == "/v1/context/knowledge/parse" {
					budget = 300 * time.Second
					_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(budget))
					_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(60 * time.Second))
				}
				if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/storage-assets/") && (strings.HasSuffix(r.URL.Path, "/files") || strings.HasSuffix(r.URL.Path, "/file")) {
					budget = 65 * time.Second
					_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(budget))
				}
				if r.Method == http.MethodPost && (r.URL.Path == "/v1/model-configuration:validate" || (strings.HasPrefix(r.URL.Path, "/v1/my-models/") && strings.HasSuffix(r.URL.Path, ":validate"))) {
					budget = 35 * time.Second
					_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(budget))
				}
				var ctx context.Context
				var cancel context.CancelFunc
				if r.Method == http.MethodPost && (r.URL.Path == "/v1/skills:preview" || r.URL.Path == "/v1/skills:apply" || r.URL.Path == "/v1/skills:export" || strings.HasSuffix(r.URL.Path, ":previous")) {
					_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
					ctx, cancel = context.WithCancel(r.Context())
				} else {
					ctx, cancel = context.WithTimeout(r.Context(), budget)
				}
				defer cancel()
				s.business.ServeAuthenticated(w, r.WithContext(ctx), p)
				return
			}
			fail(503, "DEPENDENCY_UNAVAILABLE", "This platform feature is not yet available")
			return
		}
		fail(401, "UNAUTHENTICATED", "Authentication required")
	case strings.HasPrefix(r.URL.Path, "/auth/") || strings.HasPrefix(r.URL.Path, "/__dsh/"):
		fail(503, "DEPENDENCY_UNAVAILABLE", "Identity and runtime integration are not configured")
	default:
		fail(404, "NOT_FOUND", "Resource not found")
	}
}
