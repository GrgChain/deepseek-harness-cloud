package contextservice

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type Handler struct {
	service *Service
	limit   func(context.Context, string, int) error
}

func NewHandler(service *Service, limit func(context.Context, string, int) error) *Handler {
	return &Handler{service, limit}
}
func decode(w http.ResponseWriter, r *http.Request, target any) error {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return invalid("请求须使用 JSON 对象")
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 3<<20))
	if err != nil {
		return webapi.Fail(413, "PAYLOAD_TOO_LARGE", "请求不能超过 3 MiB")
	}
	if webapi.StrictUnmarshal(data, target) != nil {
		return invalid("请求字段或格式不正确")
	}
	return nil
}
func (h *Handler) ServeAuthenticated(w http.ResponseWriter, r *http.Request, p identity.Principal) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/context/"), "/")
	var result any
	var err error
	if len(parts) < 1 || len(parts) > 3 || parts[0] != "memory" && parts[0] != "knowledge" || r.URL.RawQuery != "" {
		webapi.WriteError(w, r, missing())
		return
	}
	kind := parts[0]
	permission := kind + ":read"
	action := ""
	if len(parts) == 3 {
		action = parts[2]
	}
	if r.Method != "GET" && !(r.Method == "POST" && readAction(action)) {
		permission = kind + ":manage"
	}
	if !p.Has(permission) {
		identity.WriteError(w, r, identity.ErrForbidden)
		return
	}
	if r.Method != "GET" {
		if h.limit == nil {
			webapi.WriteError(w, r, unavailable())
			return
		}
		if err = h.limit(r.Context(), "context:"+p.OrganizationID+":"+p.UserID, 30); err != nil {
			webapi.WriteError(w, r, err)
			return
		}
	}
	ctx := r.Context()
	trace := w.Header().Get("X-Request-ID")
	switch {
	case len(parts) == 1 && r.Method == "GET":
		result, err = h.service.List(ctx, p, kind)
	case len(parts) == 1 && r.Method == "POST":
		var input Input
		if err = decode(w, r, &input); err == nil {
			result, err = h.service.Save(ctx, p, kind, "", input, trace)
		}
	case len(parts) == 2 && kind == "knowledge" && parts[1] == "parse" && r.Method == "POST":
		result, err = parseDocument(w, r)
	case len(parts) == 2 && (r.Method == "PUT" || r.Method == "DELETE"):
		var input Input
		if err = decode(w, r, &input); err == nil {
			if r.Method == "PUT" {
				result, err = h.service.Save(ctx, p, kind, parts[1], input, trace)
			} else {
				err = h.service.Delete(ctx, p, kind, parts[1], input, trace)
				result = map[string]bool{"deleted": true}
			}
		}
	case len(parts) == 3 && r.Method == "POST" && action == "reconcile":
		var input struct{}
		if err = decode(w, r, &input); err == nil {
			result, err = h.service.Reconcile(ctx, p, kind, parts[1], trace)
		}
	case len(parts) == 3 && r.Method == "POST" && action == "binding":
		var input struct {
			InstanceID      string `json:"instance_id"`
			BindingVersion  int64  `json:"expected_binding_version"`
			ExpectedVersion int64  `json:"expected_version"`
			Enabled         bool   `json:"enabled"`
		}
		if err = decode(w, r, &input); err == nil {
			result, err = h.service.Bind(ctx, p, kind, parts[1], input.InstanceID, input.ExpectedVersion, input.BindingVersion, input.Enabled, trace)
		}
	case len(parts) == 3 && r.Method == "POST":
		var input Action
		if err = decode(w, r, &input); err == nil {
			var data []byte
			data, err = h.service.Call(ctx, p, kind, parts[1], action, input, trace)
			if err == nil {
				if action == "export" {
					w.Header().Set("Content-Type", "application/json; charset=utf-8")
					w.Header().Set("Content-Disposition", `attachment; filename="memory-export.json"`)
				}
				w.WriteHeader(200)
				_, _ = w.Write(data)
				return
			}
		}
	default:
		err = webapi.Fail(405, "METHOD_NOT_ALLOWED", "不支持此操作")
	}
	if err != nil {
		if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrUnauthenticated) {
			identity.WriteError(w, r, err)
		} else {
			webapi.WriteError(w, r, err)
		}
		return
	}
	webapi.JSON(w, 200, result)
}
