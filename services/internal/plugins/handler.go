package plugins

import (
	"context"
	"encoding/base64"
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

func NewHandler(service *Service, limit func(context.Context, string, int) error) *Handler {
	return &Handler{service, limit}
}

type pluginInput struct {
	PackageName   *string `json:"package_name"`
	Version       *string `json:"version"`
	Category      *string `json:"category"`
	Filename      *string `json:"filename"`
	ContentBase64 *string `json:"content_base64"`
}

func decodeInput(w http.ResponseWriter, r *http.Request, preview bool) (pluginInput, []byte, error) {
	var input pluginInput
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return input, nil, invalid("请求必须使用 JSON 对象")
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<20))
	defer clear(body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return input, nil, webapi.Fail(413, "PLUGIN_TOO_LARGE", "插件压缩包不能超过 5 MiB")
		}
		return input, nil, invalid("无法读取插件请求")
	}
	if webapi.StrictUnmarshal(body, &input) != nil {
		return input, nil, invalid("插件请求字段、类型或格式不正确")
	}
	online := input.PackageName != nil && input.Filename == nil && input.ContentBase64 == nil
	offline := input.PackageName == nil && input.Version == nil && input.Filename != nil && input.ContentBase64 != nil
	if !online && !offline || preview && (input.Version != nil || input.Category != nil) || !preview && input.Category == nil || !preview && online && input.Version == nil {
		return input, nil, invalid("请选择在线包名或离线插件包，并填写对应字段")
	}
	if online {
		return input, nil, nil
	}
	if len(*input.ContentBase64) > base64.StdEncoding.EncodedLen(maxArchiveBytes) {
		return input, nil, webapi.Fail(413, "PLUGIN_TOO_LARGE", "插件压缩包不能超过 5 MiB")
	}
	archive, err := base64.StdEncoding.Strict().DecodeString(*input.ContentBase64)
	if err != nil || len(archive) == 0 {
		return input, nil, invalid("插件包内容不是有效的 Base64 数据")
	}
	if len(archive) > maxArchiveBytes {
		clear(archive)
		return input, nil, webapi.Fail(413, "PLUGIN_TOO_LARGE", "插件压缩包不能超过 5 MiB")
	}
	return input, archive, nil
}

func decodeEditInput(w http.ResponseWriter, r *http.Request) (EditInput, error) {
	var input EditInput
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return input, invalid("请求必须使用 JSON 对象")
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	defer clear(body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return input, webapi.Fail(413, "PLUGIN_TOO_LARGE", "插件资料请求不能超过 64 KiB")
		}
		return input, invalid("无法读取插件资料")
	}
	var fields map[string]json.RawMessage
	if webapi.StrictUnmarshal(body, &input) != nil || json.Unmarshal(body, &fields) != nil || len(fields) != 13 {
		return input, invalid("请提交完整插件资料，字段、类型或格式不正确")
	}
	for _, name := range []string{"name", "summary", "description", "author", "version", "category", "keywords", "repository", "documentation", "compatibility", "checked_at", "source_note", "expected_revision"} {
		if _, exists := fields[name]; !exists {
			return input, invalid("请提交完整插件资料，字段、类型或格式不正确")
		}
	}
	return input, nil
}

func (h *Handler) ServeAuthenticated(w http.ResponseWriter, r *http.Request, p identity.Principal) {
	permission := "instances:read"
	if r.Method != http.MethodGet {
		permission = "plugins:manage"
	}
	if !p.Has(permission) {
		identity.WriteError(w, r, identity.ErrForbidden)
		return
	}
	if r.URL.RawQuery != "" {
		webapi.WriteError(w, r, invalid("插件请求不接受查询参数"))
		return
	}
	if r.Method != http.MethodGet {
		if h.limit == nil {
			webapi.WriteError(w, r, errors.New("plugin limiter unavailable"))
			return
		}
		if err := h.limit(r.Context(), "plugins:"+p.OrganizationID+":"+p.UserID, 30); err != nil {
			webapi.WriteError(w, r, err)
			return
		}
	}
	var result any
	var err error
	status := http.StatusOK
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/plugins":
		var items []Plugin
		items, err = h.service.List(r.Context(), p)
		result = map[string]any{"items": items}
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/plugins/"):
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/plugins/"), "/")
		if len(parts) != 2 || parts[1] != "archive" {
			err = webapi.Fail(404, "PLUGIN_ARCHIVE_NOT_FOUND", "插件包不存在，或无权访问")
			break
		}
		var archive ArchiveDownload
		archive, err = h.service.DownloadArchive(r.Context(), p, parts[0])
		if err == nil {
			defer clear(archive.Data)
			w.Header().Set("Content-Type", "application/gzip")
			w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": archive.Filename}))
			w.Header().Set("Content-Length", strconv.Itoa(len(archive.Data)))
			w.Header().Set("Cache-Control", "private, no-store")
			w.Header().Set("X-Checksum-SHA256", archive.SHA256)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(archive.Data)
			return
		}
	case r.Method == http.MethodPost && r.URL.Path == "/v1/plugins:preview":
		var input pluginInput
		var archive []byte
		if input, archive, err = decodeInput(w, r, true); err == nil {
			defer clear(archive)
			if input.PackageName != nil {
				result, err = h.service.Preview(r.Context(), p, *input.PackageName)
			} else {
				result, err = h.service.PreviewArchive(r.Context(), p, *input.Filename, archive)
			}
		}
	case r.Method == http.MethodPost && r.URL.Path == "/v1/plugins":
		var input pluginInput
		var archive []byte
		if input, archive, err = decodeInput(w, r, false); err == nil {
			defer clear(archive)
			if input.PackageName != nil {
				result, err = h.service.Create(r.Context(), p, *input.PackageName, *input.Version, *input.Category, w.Header().Get("X-Request-ID"))
			} else {
				result, err = h.service.CreateArchive(r.Context(), p, *input.Filename, archive, *input.Category, w.Header().Get("X-Request-ID"))
			}
			status = http.StatusCreated
		}
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/v1/plugins/") && !strings.Contains(strings.TrimPrefix(r.URL.Path, "/v1/plugins/"), "/"):
		var input EditInput
		if input, err = decodeEditInput(w, r); err == nil {
			result, err = h.service.Edit(r.Context(), p, strings.TrimPrefix(r.URL.Path, "/v1/plugins/"), input, w.Header().Get("X-Request-ID"))
		}
	default:
		err = webapi.Fail(404, "NOT_FOUND", "插件接口不存在")
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
