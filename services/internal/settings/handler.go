package settings

import (
	"context"
	"errors"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	"net/http"
	"strconv"
)

type Handler struct {
	service    *Service
	redisProbe func(context.Context) error
	limit      func(context.Context, string, int) error
}

func NewHandler(s *Service, redisProbe func(context.Context) error, limit func(context.Context, string, int) error) *Handler {
	return &Handler{service: s, redisProbe: redisProbe, limit: limit}
}
func (h *Handler) ServeAuthenticated(w http.ResponseWriter, r *http.Request, p identity.Principal) {
	if h.servePersonal(w, r, p) {
		return
	}
	permission := "configuration:read"
	switch {
	case r.Method == "PUT" && r.URL.Path == "/v1/model-configuration":
		permission = "model:write"
	case r.Method == "POST" && r.URL.Path == "/v1/model-configuration:validate":
		permission = "model:validate"
	case r.Method == "POST" && r.URL.Path == "/v1/model-directory:refresh":
		permission = "model:validate"
	case r.Method == "POST" && r.URL.Path == "/v1/model-configuration:revoke":
		permission = "model:revoke"
	case r.Method == "PUT" && r.URL.Path == "/v1/defaults":
		permission = "defaults:write"
	case r.Method == "PUT" && r.URL.Path == "/v1/organization-quota":
		permission = "defaults:write"
	}
	if !p.Has(permission) {
		identity.WriteError(w, r, identity.ErrForbidden)
		return
	}
	var value any
	var err error
	switch {
	case r.Method == "GET" && r.URL.Path == "/v1/platform-checks":
		if r.URL.RawQuery != "" {
			err = invalid()
			break
		}
		value, err = h.service.PlatformChecks(r.Context(), p, h.redisProbe)
	case r.Method == "GET" && r.URL.Path == "/v1/preflight":
		query := r.URL.Query()
		id := query.Get("model_configuration_id")
		specID := query.Get("spec_id")
		valid := true
		for name, values := range query {
			if len(values) != 1 || (name != "spec_id" && name != "model_configuration_id" && name != "model_configuration_version") {
				valid = false
			}
		}
		if _, exists := query["spec_id"]; exists && !uuid.MatchString(specID) {
			valid = false
		}
		var version int64
		if _, hasID := query["model_configuration_id"]; hasID || query.Has("model_configuration_version") {
			version, err = strconv.ParseInt(query.Get("model_configuration_version"), 10, 64)
			if err != nil || !uuid.MatchString(id) || version < 1 {
				valid = false
			}
		}
		if !valid {
			err = invalid()
			break
		}
		value, err = h.service.PreflightForModel(r.Context(), p, h.redisProbe, id, version, specID)
	case r.Method == "GET" && r.URL.Path == "/v1/templates":
		var items []Template
		items, err = h.service.Templates(r.Context(), p)
		value = map[string]any{"items": items}
	case r.Method == "GET" && r.URL.Path == "/v1/model-directory":
		version, parseErr := strconv.ParseInt(r.URL.Query().Get("version"), 10, 64)
		if parseErr != nil || len(r.URL.Query()) != 1 || len(r.URL.Query()["version"]) != 1 {
			err = invalid()
			break
		}
		value, err = h.service.GetDirectory(r.Context(), p, version)
	case r.Method == "POST" && r.URL.Path == "/v1/model-directory:refresh":
		var input VersionInput
		if err = webapi.Decode(w, r, &input); err != nil {
			break
		}
		if h.limit == nil {
			err = errors.New("rate limiter unavailable")
			break
		}
		if err = h.limit(r.Context(), "model-directory:"+p.OrganizationID+":"+p.UserID, 5); err != nil {
			break
		}
		value, err = h.service.RefreshDirectory(r.Context(), p, input.Version, w.Header().Get("X-Request-ID"))
	case r.Method == "GET" && r.URL.Path == "/v1/model-configuration":
		var version int64
		if values, ok := r.URL.Query()["version"]; ok {
			if len(values) != 1 {
				err = invalid()
				break
			}
			version, err = strconv.ParseInt(values[0], 10, 64)
			if err != nil || version < 1 {
				err = invalid()
				break
			}
		}
		value, err = h.service.GetModel(r.Context(), p, version)
	case r.Method == "PUT" && r.URL.Path == "/v1/model-configuration":
		var input ModelInput
		if err = webapi.Decode(w, r, &input); err == nil {
			value, err = h.service.SaveModel(r.Context(), p, input, w.Header().Get("X-Request-ID"))
		}
	case r.Method == "POST" && (r.URL.Path == "/v1/model-configuration:validate" || r.URL.Path == "/v1/model-configuration:revoke"):
		var input VersionInput
		if err = webapi.Decode(w, r, &input); err != nil {
			break
		}
		if r.URL.Path == "/v1/model-configuration:validate" {
			if !p.Has("model:validate") {
				err = identity.ErrForbidden
				break
			}
			if h.limit == nil {
				err = errors.New("rate limiter unavailable")
				break
			}
			if err = h.limit(r.Context(), "model-validate:"+p.OrganizationID+":"+p.UserID, 5); err != nil {
				break
			}
			value, err = h.service.Validate(r.Context(), p, input.Version, w.Header().Get("X-Request-ID"))
		} else {
			value, err = h.service.Revoke(r.Context(), p, input.Version, w.Header().Get("X-Request-ID"))
		}
	case r.Method == "GET" && r.URL.Path == "/v1/defaults":
		value, err = h.service.GetDefaults(r.Context(), p)
	case r.Method == "PUT" && r.URL.Path == "/v1/defaults":
		var input DefaultsInput
		if err = webapi.Decode(w, r, &input); err == nil {
			value, err = h.service.SaveDefaults(r.Context(), p, input, w.Header().Get("X-Request-ID"))
		}
	case r.Method == "GET" && r.URL.Path == "/v1/organization-quota":
		value, err = h.service.GetOrganizationQuota(r.Context(), p)
	case r.Method == "PUT" && r.URL.Path == "/v1/organization-quota":
		var input OrganizationQuotaInput
		if err = webapi.Decode(w, r, &input); err == nil {
			value, err = h.service.SaveOrganizationQuota(r.Context(), p, input, w.Header().Get("X-Request-ID"))
		}
	default:
		err = webapi.Fail(404, "NOT_FOUND", "接口不存在")
	}
	if err != nil {
		if errors.Is(err, identity.ErrUnauthenticated) || errors.Is(err, identity.ErrForbidden) {
			identity.WriteError(w, r, err)
		} else {
			webapi.WriteError(w, r, err)
		}
		return
	}
	webapi.JSON(w, 200, value)
}
