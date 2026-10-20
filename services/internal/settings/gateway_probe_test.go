package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func messagesFixture(tool, complete bool) string {
	event := func(name string, data any) string {
		raw, _ := json.Marshal(data)
		return "event: " + name + "\ndata: " + string(raw) + "\n\n"
	}
	result := event("message_start", map[string]any{"type": "message_start", "message": map[string]any{"type": "message", "model": "actual-provider-model"}})
	block := map[string]any{"type": "text", "text": "ok"}
	stop := "end_turn"
	if tool {
		block = map[string]any{"type": "tool_use", "id": "test-tool", "name": "hc_probe", "input": map[string]string{"value": "ok"}}
		stop = "tool_use"
	}
	result += event("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": block}) + event("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0}) + event("message_delta", map[string]any{"type": "message_delta", "delta": map[string]string{"stop_reason": stop}, "usage": map[string]int{"output_tokens": 10}})
	if complete {
		result += event("message_stop", map[string]string{"type": "message_stop"})
	}
	return result
}
func Test平台模型网关ProbeStreamsToolResultWithTwoPhysicalRequests(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		privileged, truncated bool
		reason                string
		requests              int
	}{{"valid", false, false, "", 2}, {"privileged", true, false, "MODEL_KEY_PRIVILEGED", 0}, {"truncated", false, true, "MODEL_PROTOCOL_INVALID", 1}} {
		t.Run(tc.name, func(t *testing.T) {
			calls, reservations := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer synthetic-virtual-key" || r.Header.Get("x-api-key") != "" {
					t.Error("incorrect gateway authentication")
				}
				if r.URL.Path == "/key/list" {
					if tc.privileged {
						w.WriteHeader(200)
					} else {
						w.WriteHeader(403)
					}
					return
				}
				if r.URL.Path != "/v1/messages" || r.Method != "POST" {
					t.Error("unexpected gateway route")
				}
				calls++
				var payload map[string]json.RawMessage
				if json.NewDecoder(r.Body).Decode(&payload) != nil {
					t.Error("invalid request")
				}
				if string(payload["max_tokens"]) != "128" || string(payload["stream"]) != "true" || payload["thinking"] != nil || payload["reasoning_effort"] != nil {
					t.Error("wrong probe parameters")
				}
				if calls == 2 && !strings.Contains(string(payload["messages"]), `"tool_use_id":"test-tool"`) {
					t.Error("tool result not returned")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, messagesFixture(calls == 1, !tc.truncated))
			}))
			defer server.Close()
			m := Model{Provider: "platform-models", Protocol: "anthropic-messages", Endpoint: server.URL, Model: "approved-alias", Manifest: &ModelManifest{GatewayModel: GatewayModel{Streaming: true, ToolUse: true, ResponseModels: []string{"actual-provider-model"}}}}
			reason, models := gatewayProbe(context.Background(), server.Client(), m, "synthetic-virtual-key", func() error { reservations++; return nil })
			if reason != tc.reason || calls != tc.requests || reservations != calls {
				t.Fatalf("reason=%s calls=%d reservations=%d", reason, calls, reservations)
			}
			if reason == "" && (len(models) != 2 || models[0] == m.Model) {
				t.Fatal("did not verify actual response model mapping")
			}
		})
	}
}
