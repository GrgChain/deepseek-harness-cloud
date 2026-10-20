package identity

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type ManagementHandler struct{ service *ManagementService }

func NewManagementHandler(service *ManagementService) *ManagementHandler {
	return &ManagementHandler{service}
}
func managementList(r *http.Request) (ManagementListInput, error) {
	input := ManagementListInput{Limit: 20}
	for key, values := range r.URL.Query() {
		if len(values) != 1 {
			return input, managementInvalid("查询参数不能重复")
		}
		switch key {
		case "page":
			n, err := strconv.Atoi(values[0])
			if err != nil || n < 1 {
				return input, managementInvalid("页码不合法")
			}
			input.Page = n
		case "limit":
			n, err := strconv.Atoi(values[0])
			if err != nil {
				return input, managementInvalid("分页大小不合法")
			}
			input.Limit = n
		case "cursor":
			input.Cursor = values[0]
		case "q":
			input.Query = values[0]
		case "status":
			input.Status = values[0]
		case "role_id":
			input.RoleID = values[0]
		default:
			return input, managementInvalid("未知查询字段")
		}
	}
	return input, nil
}
func (h *ManagementHandler) ServeAuthenticated(w http.ResponseWriter, r *http.Request, p Principal) {
	result, status, err := h.serve(w, r, p)
	if err != nil {
		if errors.Is(err, ErrForbidden) || errors.Is(err, ErrUnauthenticated) || errors.Is(err, ErrCSRF) {
			WriteError(w, r, err)
		} else {
			webapi.WriteError(w, r, err)
		}
		return
	}
	if status == 204 {
		w.WriteHeader(status)
		return
	}
	webapi.JSON(w, status, result)
}
func (h *ManagementHandler) serve(w http.ResponseWriter, r *http.Request, p Principal) (any, int, error) {
	ctx, trace := r.Context(), w.Header().Get("X-Request-ID")
	if r.Method != http.MethodGet && r.URL.RawQuery != "" {
		return nil, 0, managementInvalid("写入请求不接受查询参数")
	}
	path := r.URL.Path
	if path == "/v1/permissions" && r.Method == http.MethodGet {
		if r.URL.RawQuery != "" {
			return nil, 0, managementInvalid("权限目录不接受查询参数")
		}
		v, e := h.service.Permissions(ctx, p)
		return map[string]any{"items": v}, 200, e
	}
	for _, resource := range []string{"users", "roles"} {
		base := "/v1/" + resource
		if path == base {
			if r.Method == http.MethodGet {
				in, err := managementList(r)
				if err != nil {
					return nil, 0, err
				}
				if resource == "users" {
					v, e := h.service.ListUsers(ctx, p, in)
					return v, 200, e
				}
				v, e := h.service.ListRoles(ctx, p, in)
				return v, 200, e
			}
			if r.Method == http.MethodPost {
				if len(r.Header.Values("Idempotency-Key")) != 1 {
					return nil, 0, managementInvalid("新增操作需要唯一请求标识")
				}
				key := r.Header.Get("Idempotency-Key")
				if resource == "users" {
					var in UserCreateInput
					if e := webapi.Decode(w, r, &in); e != nil {
						return nil, 0, e
					}
					v, e := h.service.CreateUser(ctx, p, key, in, trace)
					code := 201
					if v.Status == "PENDING_IDENTITY" {
						code = 202
					}
					return v, code, e
				}
				var in RoleInput
				if e := webapi.Decode(w, r, &in); e != nil {
					return nil, 0, e
				}
				v, e := h.service.SaveRole(ctx, p, "", key, in, trace)
				return v, 201, e
			}
		}
		if strings.HasPrefix(path, base+"/") {
			tail := strings.TrimPrefix(path, base+"/")
			id, action, _ := strings.Cut(tail, ":")
			if !uuidPattern.MatchString(id) {
				return nil, 0, managementInvalid("资源标识不合法")
			}
			if resource == "users" && action == "quota" {
				if r.URL.RawQuery != "" {
					return nil, 0, managementInvalid("配额接口不接受查询参数")
				}
				if r.Method == http.MethodGet {
					v, e := h.service.GetUserQuota(ctx, p, id)
					return v, 200, e
				}
				if r.Method == http.MethodPut {
					var in UserQuotaInput
					if e := webapi.Decode(w, r, &in); e != nil {
						return nil, 0, e
					}
					v, e := h.service.SaveUserQuota(ctx, p, id, in, trace)
					return v, 200, e
				}
			}
			if r.Method == http.MethodGet && action == "" {
				if r.URL.RawQuery != "" {
					return nil, 0, managementInvalid("详情不接受查询参数")
				}
				if resource == "users" {
					v, e := h.service.GetUser(ctx, p, id)
					return v, 200, e
				}
				v, e := h.service.GetRole(ctx, p, id)
				return v, 200, e
			}
			if resource == "roles" && action == "" {
				if r.Method == http.MethodPut {
					var in RoleInput
					if e := webapi.Decode(w, r, &in); e != nil {
						return nil, 0, e
					}
					v, e := h.service.SaveRole(ctx, p, id, "", in, trace)
					return v, 200, e
				}
				if r.Method == http.MethodDelete {
					var in struct {
						ExpectedVersion int64 `json:"expected_version"`
					}
					if e := webapi.Decode(w, r, &in); e != nil {
						return nil, 0, e
					}
					return nil, 204, h.service.DeleteRole(ctx, p, id, in.ExpectedVersion, trace)
				}
			}
			if resource == "users" {
				if r.Method == http.MethodDelete && action == "" {
					var in UserDeleteInput
					if e := webapi.Decode(w, r, &in); e != nil {
						return nil, 0, e
					}
					return nil, 204, h.service.DeleteUser(ctx, p, id, in, trace)
				}
				if r.Method == http.MethodPut && action == "" {
					var in UserEditInput
					if e := webapi.Decode(w, r, &in); e != nil {
						return nil, 0, e
					}
					v, e := h.service.EditUser(ctx, p, id, in, trace)
					return v, 200, e
				}
				if r.Method == http.MethodPost {
					kind := map[string]string{"role": "USER_ROLE", "disable": "USER_DISABLE", "enable": "USER_ENABLE", "archive": "USER_ARCHIVE", "retry-identity": "USER_IDENTITY_RETRY", "reset-password": "USER_PASSWORD_RESET", "retry-password-reset": "USER_PASSWORD_RESET_RETRY"}[action]
					if kind != "" {
						var in UserActionInput
						if e := webapi.Decode(w, r, &in); e != nil {
							return nil, 0, e
						}
						v, e := h.service.UserAction(ctx, p, id, kind, in, trace)
						if e == nil && v.PasswordReset != nil && v.PasswordReset.Status == "PENDING" && (action == "reset-password" || action == "retry-password-reset") {
							return v, 202, nil
						}
						return v, 200, e
					}
				}
			}
		}
	}
	return nil, 0, webapi.Fail(405, "METHOD_NOT_ALLOWED", "管理接口或方法不支持")
}
