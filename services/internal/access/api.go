package access

import (
	"context"
	"net/http"
	"strings"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type TicketIssuer interface {
	Issue(context.Context, identity.Principal, string, string, string) (Handoff, error)
}
type APIHandler struct {
	issuer TicketIssuer
	limit  Limit
}

func NewAPIHandler(issuer TicketIssuer, limit Limit) *APIHandler { return &APIHandler{issuer, limit} }
func (h *APIHandler) ServeAuthenticated(w http.ResponseWriter, r *http.Request, p identity.Principal) {
	if strings.HasSuffix(r.URL.Path, ":access-endpoint") {
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/instances/"), ":access-endpoint")
		provider, ok := h.issuer.(interface {
			Endpoint(context.Context, identity.Principal, string) (Endpoint, error)
		})
		if r.Method != http.MethodPost || r.URL.Path != "/v1/instances/"+id+":access-endpoint" || !uuid.MatchString(id) || r.URL.RawQuery != "" {
			webapi.WriteError(w, r, notFound())
			return
		}
		if !ok {
			webapi.WriteError(w, r, unavailable())
			return
		}
		var input struct{}
		if err := webapi.Decode(w, r, &input); err != nil {
			webapi.WriteError(w, r, err)
			return
		}
		result, err := provider.Endpoint(r.Context(), p, id)
		if err != nil {
			webapi.WriteError(w, r, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		webapi.JSON(w, 200, result)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/instances/"), ":access")
	if r.Method != "POST" || r.URL.Path != "/v1/instances/"+id+":access" || !uuid.MatchString(id) || r.URL.RawQuery != "" {
		webapi.WriteError(w, r, webapi.Fail(404, "NOT_FOUND", "资源不存在"))
		return
	}
	var input struct {
		ChallengeID string `json:"challenge_id"`
	}
	if err := webapi.Decode(w, r, &input); err != nil {
		webapi.WriteError(w, r, err)
		return
	}
	if !uuid.MatchString(input.ChallengeID) {
		webapi.WriteError(w, r, webapi.Fail(422, "INVALID_SPEC", "挑战编号不合法"))
		return
	}
	if h.limit == nil || h.issuer == nil {
		webapi.WriteError(w, r, unavailable())
		return
	}
	if err := h.limit(r.Context(), "access-user:"+p.OrganizationID+":"+p.UserID, 30); err != nil {
		webapi.WriteError(w, r, err)
		return
	}
	result, err := h.issuer.Issue(r.Context(), p, id, input.ChallengeID, w.Header().Get("X-Request-ID"))
	if err != nil {
		webapi.WriteError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	webapi.JSON(w, 200, result)
}
