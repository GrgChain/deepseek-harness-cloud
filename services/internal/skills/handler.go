package skills

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
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
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 7<<20))
	if err != nil {
		return webapi.Fail(413, "PAYLOAD_TOO_LARGE", "上传请求不能超过 7 MiB")
	}
	if webapi.StrictUnmarshal(raw, target) != nil {
		return invalid("请求字段或格式不正确")
	}
	return nil
}
func (h *Handler) ServeAuthenticated(w http.ResponseWriter, r *http.Request, p identity.Principal) {
	path := r.URL.Path
	export := r.Method == "POST" && (path == "/v1/skills:export" || strings.HasSuffix(path, ":previous"))
	permission := "skills:read"
	if r.Method != "GET" && !export {
		permission = "skills:manage"
	}
	if !p.Has(permission) {
		identity.WriteError(w, r, identity.ErrForbidden)
		return
	}
	if r.Method != "GET" {
		if h.limit == nil {
			webapi.WriteError(w, r, errors.New("skill limiter unavailable"))
			return
		}
		if err := h.limit(r.Context(), "skills:"+p.OrganizationID+":"+p.UserID, 30); err != nil {
			webapi.WriteError(w, r, err)
			return
		}
	}
	var result any
	var err error
	ctx := r.Context()
	trace := w.Header().Get("X-Request-ID")
	if r.URL.RawQuery != "" && !(r.Method == "GET" && (path == "/v1/skills" || strings.HasSuffix(path, ":file"))) {
		webapi.WriteError(w, r, invalid("此操作不接受查询参数"))
		return
	}
	version := struct {
		ExpectedVersion int64 `json:"expected_version"`
	}{}
	// Paths are matched exactly; arbitrary suffixes never fall through to file access.
	switch {
	case path == "/v1/skills" && r.Method == "GET":
		query, parseErr := url.ParseQuery(r.URL.RawQuery)
		page, limit := 1, 20
		if parseErr != nil {
			err = invalid("搜索或分页参数无效")
			break
		}
		for key, values := range query {
			if len(values) != 1 || key != "q" && key != "page" && key != "limit" {
				err = invalid("搜索或分页参数无效")
				break
			}
		}
		if err == nil && query.Has("page") {
			page, err = strconv.Atoi(query.Get("page"))
		}
		if err == nil && query.Has("limit") {
			limit, err = strconv.Atoi(query.Get("limit"))
		}
		if err == nil {
			result, err = h.service.List(ctx, p, query.Get("q"), page, limit)
		} else {
			err = invalid("搜索或分页参数无效")
		}
	case path == "/v1/skills" && r.Method == "POST":
		var input struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if err = decode(w, r, &input); err == nil {
			result, err = h.service.Create(ctx, p, input.Name, input.Description, trace)
		}
	case path == "/v1/skills:preview" && r.Method == "POST":
		var input struct {
			Filename string `json:"filename"`
			Content  string `json:"content_base64"`
		}
		if err = decode(w, r, &input); err == nil {
			var content []byte
			content, err = base64.StdEncoding.Strict().DecodeString(input.Content)
			if err == nil {
				result, err = h.service.Preview(ctx, p, input.Filename, content, "", "", 0)
			} else {
				err = invalid("上传文件编码无效")
			}
		}
	case path == "/v1/skills:source" && r.Method == "POST":
		var input struct {
			URL string `json:"url"`
		}
		if err = decode(w, r, &input); err == nil {
			if _, err = sourceURL(input.URL); err == nil {
				result, err = h.service.Preview(ctx, p, "", nil, input.URL, "", 0)
			}
		}
	case path == "/v1/skills:search" && r.Method == "POST":
		var input struct {
			Provider string `json:"provider"`
			Query    string `json:"query"`
		}
		if err = decode(w, r, &input); err == nil {
			result, err = h.service.SearchHub(ctx, p, input.Provider, input.Query)
		}
	case path == "/v1/skills:apply" && r.Method == "POST":
		var input struct {
			Token string `json:"token"`
			Mode  string `json:"mode"`
		}
		if err = decode(w, r, &input); err == nil {
			result, err = h.service.Apply(ctx, p, input.Token, input.Mode, trace)
		}
	case path == "/v1/skills:export" && r.Method == "POST":
		var input struct {
			IDs []string `json:"ids"`
		}
		if err = decode(w, r, &input); err == nil {
			err = h.download(w, r, p, input.IDs, false)
			if err == nil {
				return
			}
		}
	case strings.HasPrefix(path, "/v1/skills/"):
		parts := strings.SplitN(strings.TrimPrefix(path, "/v1/skills/"), ":", 2)
		id := parts[0]
		action := ""
		if len(parts) == 2 {
			action = parts[1]
		}
		if !idPattern.MatchString(id) {
			err = missing()
			break
		}
		switch {
		case action == "file" && r.Method == "GET":
			query, parseErr := url.ParseQuery(r.URL.RawQuery)
			if parseErr != nil || len(query) != 1 || len(query["path"]) != 1 || query.Get("path") == "" {
				err = invalid("请提供唯一的文件路径")
			} else {
				result, err = h.service.ReadFile(ctx, p, id, query.Get("path"))
			}
		case action == "file" && (r.Method == "PUT" || r.Method == "POST"):
			var input struct {
				Path            string  `json:"path"`
				Content         *string `json:"content"`
				ExpectedVersion int64   `json:"expected_version"`
			}
			if err = decode(w, r, &input); err == nil {
				if input.Content == nil {
					err = invalid("请提供文件内容，空文件请传入空字符串")
				} else {
					if r.Method == "POST" {
						result, err = h.service.CreateFile(ctx, p, id, input.ExpectedVersion, input.Path, *input.Content, trace)
					} else {
						result, err = h.service.EditFile(ctx, p, id, input.ExpectedVersion, input.Path, *input.Content, trace)
					}
				}
			}
		case action == "" && r.Method == "GET":
			result, err = h.service.Get(ctx, p, id)
		case action == "" && r.Method == "PUT":
			var input struct {
				Content         string `json:"content"`
				ExpectedVersion int64  `json:"expected_version"`
			}
			if err = decode(w, r, &input); err == nil {
				result, err = h.service.Edit(ctx, p, id, input.ExpectedVersion, input.Content, nil, trace)
			}
		case action == "policy" && r.Method == "POST":
			var input struct {
				Model           *bool `json:"model_invocable"`
				User            *bool `json:"user_invocable"`
				ExpectedVersion int64 `json:"expected_version"`
			}
			if err = decode(w, r, &input); err == nil {
				if input.Model == nil || input.User == nil {
					err = invalid("请提供两项调用设置")
				} else {
					policy := [2]bool{*input.Model, *input.User}
					result, err = h.service.Edit(ctx, p, id, input.ExpectedVersion, "", &policy, trace)
				}
			}
		case action == "" && r.Method == "DELETE":
			var input struct {
				Name            string `json:"confirmed_name"`
				ExpectedVersion int64  `json:"expected_version"`
			}
			if err = decode(w, r, &input); err == nil {
				err = h.service.Delete(ctx, p, id, input.ExpectedVersion, input.Name, trace)
				result = map[string]bool{"deleted": true}
			}
		case action == "check-update" && r.Method == "POST":
			if err = decode(w, r, &version); err == nil {
				result, err = h.service.CheckUpdate(ctx, p, id, version.ExpectedVersion)
			}
		case action == "previous" && r.Method == "POST":
			var input struct{}
			if err = decode(w, r, &input); err == nil {
				err = h.download(w, r, p, []string{id}, true)
				if err == nil {
					return
				}
			}
		default:
			err = webapi.Fail(405, "METHOD_NOT_ALLOWED", "不支持此操作")
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
	webapi.JSON(w, 200, result)
}
func (h *Handler) download(w http.ResponseWriter, r *http.Request, p identity.Principal, ids []string, previous bool) error {
	data, name, err := h.service.Export(r.Context(), p, ids, previous)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(200)
	_, _ = w.Write(data)
	return nil
}
