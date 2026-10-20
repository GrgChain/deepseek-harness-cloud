package access

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type issuerFunc func(context.Context, identity.Principal, string, string, string) (Handoff, error)

func (f issuerFunc) Issue(c context.Context, p identity.Principal, id, challenge, trace string) (Handoff, error) {
	return f(c, p, id, challenge, trace)
}
func TestTicketAPIRequiresValidChallengeAndCurrentUserRateBudget(t *testing.T) {
	id, challenge := webapi.ID(), webapi.ID()
	p := identity.Principal{UserID: webapi.ID(), OrganizationID: webapi.ID()}
	calls := 0
	issuer := issuerFunc(func(ctx context.Context, current identity.Principal, got, cid, trace string) (Handoff, error) {
		calls++
		if current.UserID != p.UserID || current.OrganizationID != p.OrganizationID || got != id || cid != challenge {
			t.Fatal("ticket binding changed at API boundary")
		}
		return Handoff{}, nil
	})
	limit := func(ctx context.Context, key string, n int) error {
		if key != "access-user:"+p.OrganizationID+":"+p.UserID || n != 30 {
			t.Fatal("ticket limit lost current principal")
		}
		return nil
	}
	h := NewAPIHandler(issuer, limit)
	for _, body := range []string{`{}`, `{"challenge_id":"invalid"}`, `{"challenge_id":"` + challenge + `","ticket":"injected"}`, `{"challenge_id":"` + challenge + `","challenge_id":"` + challenge + `"}`} {
		r := httptest.NewRequest("POST", "/v1/instances/"+id+":access", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeAuthenticated(w, r, p)
		if w.Code < 400 {
			t.Fatal("invalid challenge accepted")
		}
	}
	if calls != 0 {
		t.Fatal("invalid request issued a ticket")
	}
	r := httptest.NewRequest("POST", "/v1/instances/"+id+":access", strings.NewReader(`{"challenge_id":"`+challenge+`"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeAuthenticated(w, r, p)
	if w.Code != 200 || calls != 1 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("ticket result can be cached or was not issued")
	}
}
