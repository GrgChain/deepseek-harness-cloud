package settings

import (
	"errors"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	"net/http"
	"strings"
)

func (h *Handler) serveProviderAccounts(w http.ResponseWriter, r *http.Request, p identity.Principal) bool {
	const root = "/v1/my-provider-accounts"
	if r.URL.Path != root && !strings.HasPrefix(r.URL.Path, root+"/") {
		return false
	}
	var value any
	var err error
	status := 200
	id := strings.TrimPrefix(r.URL.Path, root+"/")
	switch {
	case r.URL.RawQuery != "":
		err = invalid()
	case r.Method == "GET" && r.URL.Path == root:
		var items []ProviderAccount
		items, err = h.service.ListProviderAccounts(r.Context(), p)
		value = map[string]any{"items": items}
	case r.Method == "POST" && r.URL.Path == root:
		var input ProviderAccountInput
		if err = webapi.Decode(w, r, &input); err == nil {
			keys := r.Header.Values("Idempotency-Key")
			if len(keys) != 1 {
				err = invalid()
			} else {
				value, err = h.service.CreateProviderAccount(r.Context(), p, input, keys[0], w.Header().Get("X-Request-ID"))
				status = 202
			}
		}
	case r.Method == "PUT" && uuid.MatchString(id):
		var input ProviderAccountInput
		if err = webapi.Decode(w, r, &input); err == nil {
			value, err = h.service.UpdateProviderAccount(r.Context(), p, id, input, w.Header().Get("X-Request-ID"))
			status = 202
		}
	case r.Method == "POST" && strings.HasSuffix(id, ":delete"):
		var input struct {
			ExpectedVersion int64 `json:"expected_version"`
		}
		if err = webapi.Decode(w, r, &input); err == nil {
			value, err = h.service.DeleteProviderAccount(r.Context(), p, strings.TrimSuffix(id, ":delete"), input.ExpectedVersion, w.Header().Get("X-Request-ID"))
			status = 202
		}
	case r.Method == "POST" && strings.HasSuffix(id, ":sync"):
		var input ProviderAccountSyncInput
		if err = webapi.Decode(w, r, &input); err == nil {
			if !p.Has("personal-model:write") {
				err = identity.ErrForbidden
			} else if h.limit == nil {
				err = errors.New("rate limiter unavailable")
			} else if err = h.limit(r.Context(), "model-discovery:"+p.OrganizationID+":"+p.UserID, 10); err == nil {
				items, e := h.service.DiscoverProviderAccountModels(r.Context(), p, strings.TrimSuffix(id, ":sync"), input)
				value = map[string]any{"items": items}
				err = e
			}
		}
	default:
		err = webapi.Fail(404, "NOT_FOUND", "接口不存在")
	}
	if err != nil {
		webapi.WriteError(w, r, err)
	} else {
		webapi.JSON(w, status, value)
	}
	return true
}
