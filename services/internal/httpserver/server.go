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

type requestBudget struct {
	write time.Duration
	read  time.Duration
}

func routeBudget(method, path string) requestBudget {
	switch {
	case method == http.MethodPost && path == "/v1/context/knowledge/parse":
		return requestBudget{write: 300 * time.Second, read: 60 * time.Second}
	case method == http.MethodPost && (path == "/v1/skills:preview" || path == "/v1/skills:apply" || path == "/v1/skills:export" || (strings.HasPrefix(path, "/v1/skills/") && strings.HasSuffix(path, ":previous"))):
		return requestBudget{write: 300 * time.Second, read: 60 * time.Second}
	case method == http.MethodPost && (path == "/v1/skills:source" || strings.HasPrefix(path, "/v1/skills/") && strings.HasSuffix(path, ":check-update")):
		return requestBudget{write: 125 * time.Second}
	case strings.HasPrefix(path, "/v1/connectors"):
		return requestBudget{write: 125 * time.Second}
	case method == http.MethodPost && strings.HasPrefix(path, "/v1/context/memory/") && (strings.HasSuffix(path, "/export") || strings.HasSuffix(path, "/preview")):
		return requestBudget{write: 95 * time.Second, read: 60 * time.Second}
	case method == http.MethodGet && strings.HasPrefix(path, "/v1/storage-assets/") && (strings.HasSuffix(path, "/files") || strings.HasSuffix(path, "/file")):
		return requestBudget{write: 65 * time.Second}
	case method == http.MethodPost && (path == "/v1/model-configuration:validate" || (strings.HasPrefix(path, "/v1/my-models/") && strings.HasSuffix(path, ":validate"))):
		return requestBudget{write: 35 * time.Second}
	case method == http.MethodPost && ((strings.HasPrefix(path, "/v1/model-providers/") || strings.HasPrefix(path, "/v1/my-provider-accounts/")) && strings.HasSuffix(path, ":sync")):
		return requestBudget{write: 35 * time.Second}
	case strings.HasPrefix(path, "/v1/plugins"):
		return requestBudget{write: 25 * time.Second, read: 20 * time.Second}
	case strings.HasPrefix(path, "/v1/skills") || strings.HasPrefix(path, "/v1/context/"):
		return requestBudget{write: 20 * time.Second}
	default:
		return requestBudget{write: 9 * time.Second}
	}
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
				budget := routeBudget(r.Method, r.URL.Path)
				if budget.write > 9*time.Second {
					_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(budget.write))
				}
				if budget.read > 0 {
					_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(budget.read))
				}
				ctx, cancel := context.WithTimeout(r.Context(), budget.write)
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
