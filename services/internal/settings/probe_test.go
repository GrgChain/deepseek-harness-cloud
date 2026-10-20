package settings

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMessagesProbeRequiresRealToolCapability(t *testing.T) {
	for _, tc := range []struct {
		name, body, reason string
		status             int
	}{
		{"valid", `{"type":"message","model":"test-model","stop_reason":"tool_use","content":[{"type":"tool_use","id":"tool1","name":"hc_probe","input":{"value":"ok"}}]}`, "", 200},
		{"empty200", `{}`, "MODEL_PROTOCOL_INVALID", 200},
		{"no tool", `{"type":"message","model":"test-model","stop_reason":"end_turn","content":[{"type":"text","text":"ok"}]}`, "MODEL_TOOL_UNAVAILABLE", 200},
		{"bad key", `{"secret":"must never be reflected"}`, "MODEL_AUTH_REJECTED", 401},
		{"redirect", ``, "MODEL_REDIRECT_REJECTED", 302},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/anthropic/v1/messages" || r.Header.Get("x-api-key") != "synthetic-test-key" {
					t.Error("incorrect wire contract")
				}
				var input struct {
					MaxTokens int   `json:"max_tokens"`
					Tools     []any `json:"tools"`
				}
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.MaxTokens != 128 || len(input.Tools) != 1 {
					t.Error("unbounded or missing tool probe")
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			reason, _ := (&MessagesProbe{client: server.Client()}).Check(context.Background(), Model{Endpoint: server.URL + "/anthropic", Model: "test-model"}, "synthetic-test-key")
			if reason != tc.reason {
				t.Fatalf("reason=%s", reason)
			}
		})
	}
}
