package instances

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type commandHTTPFake struct {
	calls  int
	action Action
	input  CommandInput
	key    string
	err    error
}

func (f *commandHTTPFake) Create(_ context.Context, _ identity.Principal, key string, _ CreateInput, _ string) (Accepted, error) {
	f.calls++
	f.key = key
	return Accepted{InstanceID: webapi.ID(), OperationID: webapi.ID(), State: "PENDING"}, f.err
}
func (f *commandHTTPFake) Command(_ context.Context, _ identity.Principal, _ string, action Action, key string, input CommandInput, _ string) (Accepted, error) {
	f.calls++
	f.action = action
	f.input = input
	f.key = key
	return Accepted{InstanceID: webapi.ID(), OperationID: webapi.ID(), State: "STOPPING"}, f.err
}
func (f *commandHTTPFake) Retry(_ context.Context, _ identity.Principal, _ string, key, _ string, _ func(context.Context, string, int) error) (Accepted, error) {
	f.calls++
	f.key = key
	return Accepted{InstanceID: webapi.ID(), OperationID: webapi.ID(), State: "STARTING"}, f.err
}
func TestCommandHTTPBoundaryRejectsAmbiguityBeforeAcceptance(t *testing.T) {
	id := webapi.ID()
	for _, tc := range []struct {
		method, path, body, key, name string
		status                        int
	}{
		{"POST", "/v1/instances", "{}", "", "", 422},
		{"POST", "/v1/instances", `{"name":"one","name":"two"}`, "http-command-key-01", "", 422},
		{"POST", "/v1/instances", `{"permanent":true}`, "http-command-key-01", "", 422},
		{"POST", "/v1/instances?autostart=true", "{}", "http-command-key-01", "", 404},
		{"POST", "/v1/instances/" + id + ":start", "{}", "http-command-key-01", "", 422},
		{"DELETE", "/v1/instances/" + id, "", "http-command-key-01", "", 422},
		{"DELETE", "/v1/instances/" + id, "{}", "http-command-key-01", "work-one", 422},
		{"POST", "/v1/instances/" + id + ":configure", `{"expected_instance_version":1}`, "http-command-key-01", "", 422},
		{"PUT", "/v1/instances/" + id, "{}", "http-command-key-01", "", 404},
		{"POST", "/v1/operations/" + id + ":retry", "{}", "http-command-key-01", "", 422},
		{"POST", "/v1/operations/" + id + ":retry", "", "", "", 422},
		{"POST", "/v1/operations/" + id + ":retry?replace=true", "", "http-command-key-01", "", 404},
	} {
		f := &commandHTTPFake{}
		h := NewCommandHandler(f, func(context.Context, string, int) error { return nil })
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", tc.key)
		if tc.name != "" {
			r.Header.Set("X-Confirm-Instance-Name", tc.name)
		}
		w := httptest.NewRecorder()
		h.ServeAuthenticated(w, r, identity.Principal{})
		if w.Code != tc.status || f.calls != 0 {
			t.Fatalf("%s %s: %d, calls=%d", tc.method, tc.path, w.Code, f.calls)
		}
	}
	for _, header := range []string{"Idempotency-Key", "X-Confirm-Instance-Name"} {
		f := &commandHTTPFake{}
		r := httptest.NewRequest("DELETE", "/v1/instances/"+id, nil)
		r.Header.Set("Idempotency-Key", "http-command-key-01")
		r.Header.Set("X-Confirm-Instance-Name", "work-one")
		r.Header.Add(header, r.Header.Get(header))
		w := httptest.NewRecorder()
		NewCommandHandler(f, nil).ServeAuthenticated(w, r, identity.Principal{})
		if w.Code != 422 || f.calls != 0 {
			t.Fatalf("duplicate %s accepted", header)
		}
	}
}
func TestCommandHTTPRateLimitAppliesToCreateStartButAllowsSafetyCommands(t *testing.T) {
	id := webapi.ID()
	p := identity.Principal{OrganizationID: webapi.ID(), UserID: webapi.ID()}
	for _, tc := range []struct {
		method, path, body, name string
		limit                    bool
	}{
		{"POST", "/v1/instances", "{}", "", true},
		{"POST", "/v1/instances/" + id + ":start", "", "", true},
		{"POST", "/v1/instances/" + id + ":stop", "", "", false},
		{"DELETE", "/v1/instances/" + id, "", "work-one", false},
		{"POST", "/v1/instances/" + id + ":configure", `{"expected_instance_version":3,"model_configuration_id":"` + webapi.ID() + `","model_configuration_version":2}`, "", false},
		{"POST", "/v1/operations/" + id + ":retry", "", "", false},
	} {
		f := &commandHTTPFake{}
		limited := 0
		h := NewCommandHandler(f, func(_ context.Context, key string, n int) error {
			limited++
			if key != "instance-run:"+p.OrganizationID+":"+p.UserID || n != 10 {
				t.Fatal("rate scope")
			}
			return webapi.Fail(503, "DEPENDENCY_UNAVAILABLE", "unavailable")
		})
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", "http-command-key-01")
		if tc.name != "" {
			r.Header.Set("X-Confirm-Instance-Name", tc.name)
		}
		w := httptest.NewRecorder()
		h.ServeAuthenticated(w, r, p)
		if tc.limit {
			if w.Code != 503 || limited != 1 || f.calls != 0 {
				t.Fatal("unsafe admission while limiter unavailable")
			}
		} else {
			if w.Code != 202 || limited != 0 || f.calls != 1 || f.key != "http-command-key-01" || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("safety acceptance: %d %d %d", w.Code, limited, f.calls)
			}
			if tc.name != "" && (f.input.ConfirmedName == nil || *f.input.ConfirmedName != tc.name) {
				t.Fatal("confirmation lost")
			}
		}
	}
}
