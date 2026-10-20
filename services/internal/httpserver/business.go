package httpserver

import (
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	"net/http"
	"strings"
)

type BusinessHandler interface {
	ServeAuthenticated(http.ResponseWriter, *http.Request, identity.Principal)
}
type BusinessMux struct{ Configuration, Resources, Access, Commands, Management BusinessHandler }

func (m BusinessMux) ServeAuthenticated(w http.ResponseWriter, r *http.Request, p identity.Principal) {
	handler := m.Configuration
	if r.URL.Path == "/v1/permissions" || r.URL.Path == "/v1/users" || strings.HasPrefix(r.URL.Path, "/v1/users/") || r.URL.Path == "/v1/roles" || strings.HasPrefix(r.URL.Path, "/v1/roles/") {
		handler = m.Management
	}
	if r.URL.Path == "/v1/instances" || strings.HasPrefix(r.URL.Path, "/v1/instances/") || strings.HasPrefix(r.URL.Path, "/v1/operations/") || r.URL.Path == "/v1/audit-events" || r.URL.Path == "/v1/storage-assets" || strings.HasPrefix(r.URL.Path, "/v1/storage-assets/") {
		handler = m.Resources
	}
	if strings.HasPrefix(r.URL.Path, "/v1/storage-assets/") && strings.HasSuffix(r.URL.Path, ":release") && r.Method == http.MethodPost {
		handler = m.Commands
	} else if strings.HasPrefix(r.URL.Path, "/v1/instances/") && strings.HasSuffix(r.URL.Path, ":access") {
		handler = m.Access
	} else if (r.URL.Path == "/v1/instances" || strings.HasPrefix(r.URL.Path, "/v1/instances/")) && r.Method != http.MethodGet {
		handler = m.Commands
	} else if strings.HasPrefix(r.URL.Path, "/v1/operations/") && strings.HasSuffix(r.URL.Path, ":retry") && r.Method == http.MethodPost {
		handler = m.Commands
	}
	if handler == nil {
		webapi.WriteError(w, r, webapi.Fail(503, "DEPENDENCY_UNAVAILABLE", "平台功能尚未配置"))
		return
	}
	handler.ServeAuthenticated(w, r, p)
}
