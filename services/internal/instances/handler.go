package instances

import (
	"errors"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type QueryHandler struct{ service *QueryService }

func NewQueryHandler(service *QueryService) *QueryHandler { return &QueryHandler{service: service} }
func parameters(raw string, allowed ...string) (url.Values, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return nil, invalidQuery()
	}
	keys := map[string]bool{}
	for _, key := range allowed {
		keys[key] = true
	}
	for key, entries := range values {
		if !keys[key] || len(entries) != 1 {
			return nil, invalidQuery()
		}
	}
	return values, nil
}
func pageParameters(values url.Values) (int, string, error) {
	limit := 20
	if raw, ok := values["limit"]; ok {
		var err error
		limit, err = strconv.Atoi(raw[0])
		if err != nil || limit < 1 || limit > 100 {
			return 0, "", invalidQuery()
		}
	}
	cursor := values.Get("cursor")
	if _, ok := values["cursor"]; ok && cursor == "" {
		return 0, "", invalidCursor()
	}
	return limit, cursor, nil
}
func numberedPage(values url.Values) (int, error) {
	raw, present := values["page"]
	if !present {
		return 0, nil
	}
	page, err := strconv.Atoi(raw[0])
	if err != nil || page < 1 || page > 1_000_000_000 || values.Has("cursor") {
		return 0, invalidQuery()
	}
	return page, nil
}
func listParameters(raw string) (ListInput, error) {
	values, err := parameters(raw, "limit", "page", "cursor", "state", "include_deleted", "q")
	if err != nil {
		return ListInput{}, err
	}
	limit, cursor, err := pageParameters(values)
	if err != nil {
		return ListInput{}, err
	}
	result := ListInput{Limit: limit, Cursor: cursor, State: values.Get("state"), Query: values.Get("q")}
	if result.Page, err = numberedPage(values); err != nil {
		return ListInput{}, err
	}
	if raw, ok := values["include_deleted"]; ok {
		switch raw[0] {
		case "true":
			result.IncludeDeleted = true
		case "false":
		default:
			return ListInput{}, invalidQuery()
		}
	}
	return result, nil
}
func operationLogParameters(raw string) (OperationLogInput, error) {
	values, err := parameters(raw, "limit", "page", "cursor", "action", "resource_id", "from", "to", "outcome")
	if err != nil {
		return OperationLogInput{}, err
	}
	limit, cursor, err := pageParameters(values)
	if err != nil {
		return OperationLogInput{}, err
	}
	result := OperationLogInput{Limit: limit, Cursor: cursor, Action: values.Get("action"), ResourceID: values.Get("resource_id"), Outcome: values.Get("outcome")}
	if result.Page, err = numberedPage(values); err != nil {
		return OperationLogInput{}, err
	}
	for name, target := range map[string]**time.Time{"from": &result.From, "to": &result.To} {
		if raw, ok := values[name]; ok {
			value, err := time.Parse(time.RFC3339Nano, raw[0])
			if err != nil || !strings.HasSuffix(raw[0], "Z") {
				return OperationLogInput{}, invalidQuery()
			}
			*target = &value
		}
	}
	return result, nil
}
func (h *QueryHandler) ServeAuthenticated(w http.ResponseWriter, r *http.Request, p identity.Principal) {
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/storage-assets/") && (strings.HasSuffix(r.URL.Path, "/files") || strings.HasSuffix(r.URL.Path, "/file")) {
		h.storageFile(w, r, p)
		return
	}
	var result any
	var err error
	if r.Method != "GET" && (r.URL.Path == "/v1/storage-assets" || strings.HasPrefix(r.URL.Path, "/v1/storage-assets/")) {
		err = webapi.Fail(405, "METHOD_NOT_ALLOWED", "不支持此操作")
	} else if r.Method != "GET" {
		err = webapi.Fail(503, "DEPENDENCY_UNAVAILABLE", "实例生命周期执行服务尚未开放")
	} else {
		switch {
		case r.URL.Path == "/v1/storage-assets" || strings.HasPrefix(r.URL.Path, "/v1/storage-assets/"):
			result, err = h.storage(r, p)
		case r.URL.Path == "/v1/instances":
			var input ListInput
			input, err = listParameters(r.URL.RawQuery)
			if err == nil {
				result, err = h.service.List(r.Context(), p, input)
			}
		case strings.HasPrefix(r.URL.Path, "/v1/instances/"):
			if r.URL.RawQuery != "" {
				err = invalidQuery()
			} else {
				result, err = h.service.Get(r.Context(), p, strings.TrimPrefix(r.URL.Path, "/v1/instances/"))
			}
		case strings.HasPrefix(r.URL.Path, "/v1/operations/"):
			if r.URL.RawQuery != "" {
				err = invalidQuery()
			} else {
				result, err = h.service.Operation(r.Context(), p, strings.TrimPrefix(r.URL.Path, "/v1/operations/"))
			}
		case r.URL.Path == "/v1/audit-events":
			var input OperationLogInput
			input, err = operationLogParameters(r.URL.RawQuery)
			if err == nil {
				result, err = h.service.OperationLogs(r.Context(), p, input)
			}
		default:
			err = queryNotFound()
		}
	}
	if err != nil {
		if errors.Is(err, identity.ErrUnauthenticated) || errors.Is(err, identity.ErrForbidden) {
			identity.WriteError(w, r, err)
		} else {
			webapi.WriteError(w, r, err)
		}
		return
	}
	webapi.JSON(w, 200, result)
}

func (h *QueryHandler) storageFile(w http.ResponseWriter, r *http.Request, p identity.Principal) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/storage-assets/"), "/")
	if len(parts) != 2 || !idPattern.MatchString(parts[0]) {
		webapi.WriteError(w, r, queryNotFound())
		return
	}
	switch parts[1] {
	case "files":
		input, err := storageBrowseParameters(r.URL.RawQuery, "LIST")
		if err == nil {
			var result StorageDirectory
			result, err = h.service.StorageDirectory(r.Context(), p, parts[0], input.Root, input.Path, input.After)
			if err == nil {
				webapi.JSON(w, http.StatusOK, result)
				return
			}
		}
		if errors.Is(err, identity.ErrUnauthenticated) || errors.Is(err, identity.ErrForbidden) {
			identity.WriteError(w, r, err)
		} else {
			webapi.WriteError(w, r, err)
		}
	case "file":
		input, err := storageFileParameters(r.URL.RawQuery)
		if err == nil {
			var result StorageFile
			result, err = h.service.StorageFile(r.Context(), p, parts[0], input.Root, input.Path, input.Action)
			if err == nil {
				w.Header().Set("Cache-Control", "no-store")
				w.Header().Set("X-Content-Type-Options", "nosniff")
				contentDisposition := "attachment"
				if input.Action == "PREVIEW" {
					contentDisposition = "inline"
					w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				} else {
					w.Header().Set("Content-Type", "application/octet-stream")
				}
				header := mime.FormatMediaType(contentDisposition, map[string]string{"filename": result.Name})
				if header == "" {
					header = contentDisposition + `; filename="file"`
				}
				w.Header().Set("Content-Disposition", header)
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(result.Content)
				return
			}
		}
		if errors.Is(err, identity.ErrUnauthenticated) || errors.Is(err, identity.ErrForbidden) {
			identity.WriteError(w, r, err)
		} else {
			webapi.WriteError(w, r, err)
		}
	default:
		webapi.WriteError(w, r, queryNotFound())
	}
}

func (h *QueryHandler) storage(r *http.Request, p identity.Principal) (any, error) {
	if r.URL.Path == "/v1/storage-assets" {
		values, err := parameters(r.URL.RawQuery, "page", "limit", "status", "q")
		if err != nil {
			return nil, err
		}
		limit, _, err := pageParameters(values)
		if err != nil {
			return nil, err
		}
		page, err := numberedPage(values)
		if err != nil {
			return nil, err
		}
		if page == 0 {
			page = 1
		}
		return h.service.StorageAssets(r.Context(), p, StorageListInput{Page: page, Limit: limit, Status: values.Get("status"), Query: values.Get("q")})
	}
	path := strings.TrimPrefix(r.URL.Path, "/v1/storage-assets/")
	if strings.HasSuffix(path, "/backups") {
		values, err := parameters(r.URL.RawQuery, "page", "limit")
		if err != nil {
			return nil, err
		}
		limit, _, err := pageParameters(values)
		if err != nil {
			return nil, err
		}
		page, err := numberedPage(values)
		if err != nil {
			return nil, err
		}
		if page == 0 {
			page = 1
		}
		return h.service.StorageBackups(r.Context(), p, strings.TrimSuffix(path, "/backups"), page, limit)
	}
	if r.URL.RawQuery != "" {
		return nil, invalidQuery()
	}
	return h.service.StorageAsset(r.Context(), p, path)
}
