package instances

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type Commands interface {
	Create(context.Context, identity.Principal, string, CreateInput, string) (Accepted, error)
	Command(context.Context, identity.Principal, string, Action, string, CommandInput, string) (Accepted, error)
}
type RetryCommands interface {
	Retry(context.Context, identity.Principal, string, string, string, func(context.Context, string, int) error) (Accepted, error)
}
type CommandHandler struct {
	service Commands
	limit   func(context.Context, string, int) error
}

func NewCommandHandler(service Commands, limit func(context.Context, string, int) error) *CommandHandler {
	return &CommandHandler{service, limit}
}
func noBody(r *http.Request) error {
	if r.Body == nil {
		return nil
	}
	var one [1]byte
	if n, err := io.ReadFull(r.Body, one[:]); n != 0 || err != io.EOF {
		return invalidQuery()
	}
	return nil
}
func (h *CommandHandler) ServeAuthenticated(w http.ResponseWriter, r *http.Request, p identity.Principal) {
	w.Header().Set("Cache-Control", "no-store")
	if strings.HasPrefix(r.URL.Path, "/v1/storage-assets/") && strings.HasSuffix(r.URL.Path, ":release") {
		h.releaseStorage(w, r, p)
		return
	}
	accepted, err := h.accept(w, r, p)
	if err != nil {
		if errors.Is(err, identity.ErrUnauthenticated) || errors.Is(err, identity.ErrForbidden) {
			identity.WriteError(w, r, err)
		} else {
			webapi.WriteError(w, r, err)
		}
		return
	}
	webapi.JSON(w, http.StatusAccepted, accepted)
}
func (h *CommandHandler) accept(w http.ResponseWriter, r *http.Request, p identity.Principal) (Accepted, error) {
	if r.URL.RawQuery != "" {
		return Accepted{}, queryNotFound()
	}
	if r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/v1/operations/") && strings.HasSuffix(r.URL.Path, ":retry") {
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/operations/"), ":retry")
		if !idPattern.MatchString(id) {
			return Accepted{}, queryNotFound()
		}
		key := r.Header.Get("Idempotency-Key")
		if len(r.Header.Values("Idempotency-Key")) != 1 || !keyPattern.MatchString(key) || len(r.Header.Values("X-Confirm-Instance-Name")) != 0 {
			return Accepted{}, invalidQuery()
		}
		if err := noBody(r); err != nil {
			return Accepted{}, err
		}
		retries, ok := h.service.(RetryCommands)
		if !ok {
			return Accepted{}, webapi.Fail(503, "DEPENDENCY_UNAVAILABLE", "重试服务尚未配置")
		}
		return retries.Retry(r.Context(), p, id, key, w.Header().Get("X-Request-ID"), h.limit)
	}
	create := r.Method == "POST" && r.URL.Path == "/v1/instances"
	id := strings.TrimPrefix(r.URL.Path, "/v1/instances/")
	var action Action
	if !create {
		switch {
		case r.Method == "DELETE":
			action = Delete
		case r.Method == "POST" && strings.HasSuffix(id, ":start"):
			action = Start
			id = strings.TrimSuffix(id, ":start")
		case r.Method == "POST" && strings.HasSuffix(id, ":stop"):
			action = Stop
			id = strings.TrimSuffix(id, ":stop")
		case r.Method == "POST" && strings.HasSuffix(id, ":configure"):
			action = Configure
			id = strings.TrimSuffix(id, ":configure")
		default:
			return Accepted{}, queryNotFound()
		}
		if !idPattern.MatchString(id) || !strings.HasPrefix(r.URL.Path, "/v1/instances/") {
			return Accepted{}, queryNotFound()
		}
	}
	key := r.Header.Get("Idempotency-Key")
	if len(r.Header.Values("Idempotency-Key")) != 1 || !keyPattern.MatchString(key) {
		return Accepted{}, invalidQuery()
	}
	if action != Delete && len(r.Header.Values("X-Confirm-Instance-Name")) != 0 {
		return Accepted{}, invalidQuery()
	}
	var input CommandInput
	var creation CreateInput
	switch {
	case create:
		if err := webapi.Decode(w, r, &creation); err != nil {
			return Accepted{}, err
		}
		if !creation.valid() {
			return Accepted{}, invalidQuery()
		}
	case action == Configure:
		if err := webapi.Decode(w, r, &input); err != nil {
			return Accepted{}, err
		}
		if !input.valid(action) {
			return Accepted{}, invalidQuery()
		}
	default:
		if err := noBody(r); err != nil {
			return Accepted{}, err
		}
		if action == Delete {
			if len(r.Header.Values("X-Confirm-Instance-Name")) != 1 {
				return Accepted{}, invalidQuery()
			}
			name, err := url.PathUnescape(r.Header.Get("X-Confirm-Instance-Name"))
			if err != nil {
				return Accepted{}, invalidQuery()
			}
			input.ConfirmedName = &name
			if !input.valid(action) {
				return Accepted{}, invalidQuery()
			}
		}
	}
	if h.service == nil {
		return Accepted{}, webapi.Fail(503, "DEPENDENCY_UNAVAILABLE", "实例命令服务尚未配置")
	}
	if create || action == Start {
		if h.limit == nil {
			return Accepted{}, webapi.Fail(503, "DEPENDENCY_UNAVAILABLE", "无法确认请求流量限制")
		}
		if err := h.limit(r.Context(), "instance-run:"+p.OrganizationID+":"+p.UserID, 10); err != nil {
			return Accepted{}, err
		}
	}
	trace := w.Header().Get("X-Request-ID")
	if create {
		return h.service.Create(r.Context(), p, key, creation, trace)
	}
	return h.service.Command(r.Context(), p, id, action, key, input, trace)
}
