package settings

import (
	"errors"
	"net/http"
	"strings"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func (h *Handler) servePersonal(w http.ResponseWriter, r *http.Request, p identity.Principal) bool {
	if h.serveProviderAccounts(w, r, p) {
		return true
	}
	path := r.URL.Path
	if path != "/v1/model-providers" && !strings.HasPrefix(path, "/v1/model-providers/") && path != "/v1/model-catalog" && path != "/v1/model-catalog/manage" && !strings.HasPrefix(path, "/v1/model-catalog/manage/") && path != "/v1/my-model-default" && path != "/v1/my-model-default:clear" && path != "/v1/my-models" && path != "/v1/my-models:batch" && !strings.HasPrefix(path, "/v1/my-models/") {
		return false
	}
	var value any
	var err error
	status := 200
	if len(r.URL.Query()) != 0 {
		err = invalid()
	} else {
		switch {
		case r.Method == "POST" && strings.HasPrefix(path, "/v1/model-providers/") && strings.HasSuffix(path, ":sync"):
			var input ModelDiscoveryInput
			if err = webapi.Decode(w, r, &input); err == nil {
				if !p.Has("personal-model:write") {
					err = identity.ErrForbidden
				} else if h.limit == nil {
					err = errors.New("rate limiter unavailable")
				} else if err = h.limit(r.Context(), "model-discovery:"+p.OrganizationID+":"+p.UserID, 10); err == nil {
					provider := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/model-providers/"), ":sync")
					items, e := h.service.DiscoverProviderModels(r.Context(), p, provider, input)
					value = map[string]any{"items": items}
					err = e
				}
			}
		case r.Method == "POST" && path == "/v1/my-models:batch":
			var input struct {
				Items []PersonalModelInput `json:"items"`
			}
			if err = webapi.Decode(w, r, &input); err == nil {
				values := r.Header.Values("Idempotency-Key")
				if len(values) != 1 {
					err = invalid()
				} else {
					items, e := h.service.CreatePersonalModels(r.Context(), p, input.Items, values[0], w.Header().Get("X-Request-ID"))
					value = map[string]any{"items": items}
					err = e
					status = 202
				}
			}
		case r.Method == "POST" && path == "/v1/my-model-default:clear":
			var input struct {
				ExpectedVersion int64 `json:"expected_version"`
			}
			if err = webapi.Decode(w, r, &input); err == nil {
				value, err = h.service.ClearPersonalDefault(r.Context(), p, input.ExpectedVersion, w.Header().Get("X-Request-ID"))
			}
		case r.Method == "GET" && path == "/v1/model-catalog/manage":
			var items []ManagedCatalogEntry
			items, err = h.service.ManagedCatalog(r.Context(), p)
			value = map[string]any{"items": items}
		case (r.Method == "POST" && path == "/v1/model-catalog/manage") || (r.Method == "PUT" && strings.HasPrefix(path, "/v1/model-catalog/manage/")):
			var input CatalogInput
			if err = webapi.Decode(w, r, &input); err == nil {
				id := ""
				if r.Method == "PUT" {
					id = strings.TrimPrefix(path, "/v1/model-catalog/manage/")
				}
				value, err = h.service.SaveCatalog(r.Context(), p, id, input, w.Header().Get("X-Request-ID"))
			}
		case r.Method == "GET" && path == "/v1/my-model-default":
			value, err = h.service.GetPersonalDefault(r.Context(), p)
		case r.Method == "POST" && path == "/v1/my-model-default":
			var input struct {
				ConnectionID      string `json:"connection_id"`
				ConnectionVersion int64  `json:"connection_version"`
				ExpectedVersion   int64  `json:"expected_version"`
			}
			if err = webapi.Decode(w, r, &input); err == nil {
				value, err = h.service.SetPersonalDefault(r.Context(), p, input.ConnectionID, input.ConnectionVersion, input.ExpectedVersion, w.Header().Get("X-Request-ID"))
			}
		case r.Method == "PUT" && strings.HasPrefix(path, "/v1/my-models/"):
			var input PersonalModelUpdate
			if err = webapi.Decode(w, r, &input); err == nil {
				value, err = h.service.UpdatePersonalModel(r.Context(), p, strings.TrimPrefix(path, "/v1/my-models/"), input, w.Header().Get("X-Request-ID"))
				status = 202
			}
		case r.Method == "POST" && strings.HasPrefix(path, "/v1/my-models/"):
			var input VersionInput
			if err = webapi.Decode(w, r, &input); err == nil {
				target := strings.TrimPrefix(path, "/v1/my-models/")
				id, action, ok := strings.Cut(target, ":")
				if !ok {
					err = invalid()
				} else {
					switch action {
					case "validate":
						if !p.Has("personal-model:validate") {
							err = identity.ErrForbidden
						} else if h.limit == nil {
							err = errors.New("rate limiter unavailable")
						} else if err = h.limit(r.Context(), "personal-model-validation:"+p.OrganizationID+":"+p.UserID, 5); err == nil {
							value, err = h.service.ValidatePersonalModel(r.Context(), p, id, input.Version, w.Header().Get("X-Request-ID"))
						}
					case "revoke":
						value, err = h.service.RevokePersonalModel(r.Context(), p, id, input.Version, w.Header().Get("X-Request-ID"))
					case "archive":
						value, err = h.service.ArchivePersonalModel(r.Context(), p, id, input.Version, w.Header().Get("X-Request-ID"))
					default:
						err = invalid()
					}
				}
			}
		case r.Method == "GET" && path == "/v1/model-providers":
			var items []ModelProvider
			items, err = h.service.ModelProviders(r.Context(), p)
			value = map[string]any{"items": items}
		case r.Method == "GET" && path == "/v1/model-catalog":
			var items []ModelCatalogEntry
			items, err = h.service.ModelCatalog(r.Context(), p)
			value = map[string]any{"items": items}
		case r.Method == "GET" && path == "/v1/my-models":
			var items []PersonalModel
			items, err = h.service.ListPersonalModels(r.Context(), p)
			value = map[string]any{"items": items, "next_cursor": nil}
		case r.Method == "GET" && strings.HasPrefix(path, "/v1/my-models/"):
			value, err = h.service.GetPersonalModel(r.Context(), p, strings.TrimPrefix(path, "/v1/my-models/"))
		case r.Method == "POST" && path == "/v1/my-models":
			var input PersonalModelInput
			if err = webapi.Decode(w, r, &input); err == nil {
				values := r.Header.Values("Idempotency-Key")
				if len(values) != 1 {
					err = invalid()
				} else {
					value, err = h.service.CreatePersonalModel(r.Context(), p, input, values[0], w.Header().Get("X-Request-ID"))
					status = 202
				}
			}
		default:
			err = webapi.Fail(404, "NOT_FOUND", "接口不存在")
		}
	}
	if err != nil {
		if errors.Is(err, identity.ErrUnauthenticated) || errors.Is(err, identity.ErrForbidden) {
			identity.WriteError(w, r, err)
		} else {
			webapi.WriteError(w, r, err)
		}
	} else {
		webapi.JSON(w, status, value)
	}
	return true
}
