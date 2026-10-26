package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type Handler struct {
	service *Service
	limit   func(context.Context, string, int) error
}

func NewHandler(s *Service, limit func(context.Context, string, int) error) *Handler {
	return &Handler{s, limit}
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return invalid("请使用JSON请求")
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBytes))
	if err != nil {
		return webapi.Fail(413, "PAYLOAD_TOO_LARGE", "请求不能超过1MiB")
	}
	if webapi.StrictUnmarshal(data, v) != nil {
		return invalid("请求字段或格式不正确")
	}
	return nil
}
func (h *Handler) ServeAuthenticated(w http.ResponseWriter, r *http.Request, p identity.Principal) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/connectors")
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if path == "" {
		parts = nil
	}
	action := ""
	if len(parts) > 0 {
		action = parts[len(parts)-1]
	}
	callback := path == "/oauth/callback" && r.Method == "GET"
	permission := "connectors:read"
	if r.Method != "GET" || callback {
		permission = "connectors:manage"
		if action == "invoke" || action == "cancel" {
			permission = "connectors:invoke"
		}
	}
	if !p.Has(permission) {
		identity.WriteError(w, r, identity.ErrForbidden)
		return
	}
	var result any
	var err error
	status := 200
	trace := w.Header().Get("X-Request-ID")
	if r.Method != "GET" || callback {
		if h.limit == nil {
			err = unavailable()
		} else {
			err = h.limit(r.Context(), "connectors:"+p.OrganizationID+":"+p.UserID+":"+permission, 30)
		}
		if err != nil {
			webapi.WriteError(w, r, err)
			return
		}
	}
	ctx := r.Context()
	if callback {
		connection, callbackErr := h.service.FinishOAuth(ctx, p, r.URL.Query(), trace)
		outcome := "failed"
		if callbackErr == nil {
			outcome = "verify_failed"
			if connection.Status == "READY" {
				outcome = "ready"
			}
		}
		if r.URL.Query().Get("error") == "access_denied" {
			outcome = "cancelled"
		}
		http.Redirect(w, r, "/app/connectors?connector_oauth="+outcome, http.StatusSeeOther)
		return
	}
	validQuery := r.URL.RawQuery == "" || len(parts) == 2 && action == "calls" && r.Method == "GET"
	if !validQuery {
		webapi.WriteError(w, r, invalid("不支持的查询参数"))
		return
	}
	switch {
	case len(parts) == 2 && action == "oauth" && r.Method == "POST":
		var in struct {
			ExpectedVersion int64 `json:"expected_version"`
		}
		if err = decode(w, r, &in); err == nil {
			result, err = h.service.StartOAuth(ctx, p, parts[0], in.ExpectedVersion, trace)
		}
	case len(parts) == 0 && r.Method == "GET":
		result, err = h.service.List(ctx, p)
	case len(parts) == 0 && r.Method == "POST":
		var in Input
		if err = decode(w, r, &in); err == nil {
			result, err = h.service.Save(ctx, p, "", in, trace)
			status = 201
		}
	case len(parts) == 1 && parts[0] == "export" && r.Method == "POST":
		var empty struct{}
		if err = decode(w, r, &empty); err == nil {
			result, err = h.service.Export(ctx, p)
			w.Header().Set("Content-Disposition", `attachment; filename="connectors.json"`)
		}
	case len(parts) == 1 && parts[0] == "import" && r.Method == "POST":
		var in struct {
			Package   json.RawMessage `json:"package"`
			Confirmed bool            `json:"confirmed"`
		}
		if err = decode(w, r, &in); err == nil {
			result, err = h.service.Import(ctx, p, in.Package, in.Confirmed, trace)
		}
	case len(parts) == 1 && (r.Method == "PUT" || r.Method == "DELETE"):
		var in Input
		if err = decode(w, r, &in); err == nil {
			if r.Method == "PUT" {
				result, err = h.service.Save(ctx, p, parts[0], in, trace)
			} else {
				err = h.service.Delete(ctx, p, parts[0], in, trace)
				result = map[string]bool{"deleted": true}
			}
		}
	case len(parts) == 2 && parts[0] == "calls" && r.Method == "GET":
		result, err = h.service.GetCall(ctx, p, parts[1])
	case len(parts) == 3 && parts[0] == "calls" && action == "cancel" && r.Method == "POST":
		var empty struct{}
		if err = decode(w, r, &empty); err == nil {
			err = h.service.Cancel(ctx, p, parts[1], trace)
			result = map[string]bool{"cancel_requested": true}
		}
	case len(parts) == 2 && action == "calls" && r.Method == "GET":
		q := r.URL.Query()
		page, limit := 1, 10
		for key, values := range q {
			if len(values) != 1 || key != "page" && key != "limit" {
				err = invalid("分页参数不正确")
			}
		}
		if q.Get("page") != "" {
			page, _ = strconv.Atoi(q.Get("page"))
		}
		if q.Get("limit") != "" {
			limit, _ = strconv.Atoi(q.Get("limit"))
		}
		if page < 1 || page > 1000000 || limit != 10 && limit != 20 && limit != 50 {
			err = invalid("分页参数不正确")
		}
		if err == nil {
			result, err = h.service.Calls(ctx, p, parts[0], (page-1)*limit, limit)
		}
	case len(parts) == 2 && action == "invoke" && r.Method == "POST":
		var in InvokeInput
		if err = decode(w, r, &in); err == nil {
			result, err = h.service.Invoke(ctx, p, parts[0], in, trace)
		}
	case len(parts) == 2 && (action == "sync" || action == "tools") && r.Method == "POST":
		var in struct {
			ExpectedVersion int64    `json:"expected_version"`
			EnabledTools    []string `json:"enabled_tools"`
		}
		if err = decode(w, r, &in); err == nil {
			if action == "sync" {
				result, err = h.service.Sync(ctx, p, parts[0], in.ExpectedVersion, trace)
			} else {
				result, err = h.service.SelectTools(ctx, p, parts[0], in.ExpectedVersion, in.EnabledTools, trace)
			}
		}
	default:
		err = missing()
	}
	if err != nil {
		if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrUnauthenticated) {
			identity.WriteError(w, r, err)
		} else {
			webapi.WriteError(w, r, err)
		}
		return
	}
	webapi.JSON(w, status, result)
}
