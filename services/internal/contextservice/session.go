package contextservice

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
)

// SessionRequest is a native OpenViking request, carried only over the private
// workspace broker. The companion enforces the native endpoint/tool allowlist.
type SessionRequest struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Body    string            `json:"body,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// Session is deliberately separate from Call: the public context API cannot
// enable native access. The broker requires an opted-in immutable workspace
// selection and checks its current binding before and after every request.
func (s *Service) Session(ctx context.Context, p identity.Principal, id string, version int64, selectedScope string, request *SessionRequest) (json.RawMessage, error) {
	if request == nil || len(request.Path) > 4096 || !strings.HasPrefix(request.Path, "/") || len(request.Body) > 512<<10 || len(request.Headers) > 4 || (request.Method != "GET" && request.Method != "POST" && request.Method != "DELETE") {
		return nil, invalid("会话记忆请求不正确")
	}
	tx, err := s.begin(ctx, p, "memory", true)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	v, err := load(ctx, tx, p, "memory", id)
	if err != nil {
		return nil, err
	}
	if v.Version != version {
		return nil, conflict()
	}
	if v.Backend != "openviking" || !v.Enabled || v.Status != "READY" {
		return nil, unavailable()
	}
	_, err = scope(ctx, tx, p, selectedScope)
	if err != nil {
		return nil, err
	}
	e, err := s.endpoint(v.EndpointID, v.Backend)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	// Native operations share the bridge envelope's validated limit field.
	return s.remote(ctx, e, v, p, "session", Action{ExpectedVersion: version, Scope: "personal", Limit: 5, SessionRequest: request})
}
