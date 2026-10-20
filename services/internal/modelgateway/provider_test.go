package modelgateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestChatProvidersPreserveToolsAndApprovedParameters(t *testing.T) {
	for _, provider := range []string{"openai", "custom_openai", "deepseek", "dashscope", "moonshot", "zai"} {
		t.Run(provider, func(t *testing.T) {
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var payload map[string]any
				if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer provider-secret" || json.NewDecoder(r.Body).Decode(&payload) != nil {
					t.Error("incorrect upstream route or authentication")
				}
				if payload["model"] != "approved-model" || payload["stream"] != true || payload["max_tokens"] != float64(128) || payload["thinking"].(map[string]any)["type"] != "disabled" {
					t.Error("lost frozen parameters")
				}
				messages := payload["messages"].([]any)
				if messages[2].(map[string]any)["role"] != "tool" || messages[2].(map[string]any)["tool_call_id"] != "call-1" {
					t.Error("lost tool result")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				for _, delta := range []map[string]any{
					{"tool_calls": []any{map[string]any{"index": 0, "id": "call-2", "function": map[string]any{"name": "write", "arguments": ""}}}},
					{"tool_calls": []any{map[string]any{"index": 0, "function": map[string]any{"arguments": `{"value":`}}}},
					{"tool_calls": []any{map[string]any{"index": 0, "function": map[string]any{"arguments": `"ok"}`}}}},
				} {
					chunk, _ := json.Marshal(map[string]any{"id": "response", "model": "approved-model", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": nil}}})
					_, _ = w.Write(append(append([]byte("data: "), chunk...), []byte("\n\n")...))
				}
				_, _ = w.Write([]byte("data: {\"model\":\"approved-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":4}}\n\ndata: [DONE]\n\n"))
			}))
			defer upstream.Close()
			request := Request{Model: "private-alias", Stream: true, MaxTokens: 128, Messages: []Message{
				{Role: "user", Content: json.RawMessage(`"hello"`)},
				{Role: "assistant", Content: json.RawMessage(`[{"type":"tool_use","id":"call-1","name":"write","input":{"value":"ok"}}]`)},
				{Role: "user", Content: json.RawMessage(`[{"type":"tool_result","tool_use_id":"call-1","content":"ok"}]`)},
			}}
			route := Route{Alias: "private-alias", Params: map[string]any{"model": provider + "/approved-model", "api_base": upstream.URL + "/v1", "max_retries": float64(0), "extra_body": map[string]any{"thinking": map[string]any{"type": "disabled"}}}, ResponseModels: []string{"approved-model"}, MaxOutputTokens: 4096}
			out := httptest.NewRecorder()
			if err := forward(context.Background(), out, upstream.Client(), route, request, []byte("provider-secret")); err != nil {
				t.Fatal(err)
			}
			body := out.Body.String()
			if calls != 1 || !strings.Contains(body, `"type":"tool_use"`) || !strings.Contains(body, `"partial_json":"{\"value\":\"ok\"}"`) || !strings.Contains(body, `"stop_reason":"tool_use"`) || !strings.Contains(body, "event: message_stop") {
				t.Fatal("incomplete tool stream or hidden retry", body)
			}
		})
	}
}

func TestGeminiRequestAndToolStream(t *testing.T) {
	r := Request{Model: "alias", Stream: true, MaxTokens: 128, Messages: []Message{{Role: "user", Content: json.RawMessage(`"hello"`)}, {Role: "assistant", Content: json.RawMessage(`[{"type":"tool_use","id":"call-1","name":"write","input":{"value":"ok"}}]`)}, {Role: "user", Content: json.RawMessage(`[{"type":"tool_result","tool_use_id":"call-1","content":"ok"}]`)}}}
	payload, err := geminiPayload(r, map[string]any{"reasoning_effort": "none"})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(payload)
	if !strings.Contains(string(data), `"functionResponse":{"name":"write"`) || !strings.Contains(string(data), `"thinkingBudget":0`) {
		t.Fatal("lost Gemini tool replay or thinking mode")
	}
	raw := `data: {"modelVersion":"gemini-fixture","candidates":[{"content":{"parts":[{"functionCall":{"name":"write","args":{"value":"ok"}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":8}}` + "\n\n"
	route := Route{ResponseModels: []string{"gemini-fixture"}, MaxOutputTokens: 128}
	out := httptest.NewRecorder()
	if geminiStream(out, strings.NewReader(raw), route) != nil || !strings.Contains(out.Body.String(), `"stop_reason":"tool_use"`) || !strings.Contains(out.Body.String(), "event: message_stop") {
		t.Fatal("Gemini function stream failed")
	}
	signed := strings.Replace(raw, `"functionCall":`, `"thoughtSignature":"unsupported","functionCall":`, 1)
	if geminiStream(httptest.NewRecorder(), strings.NewReader(signed), route) == nil {
		t.Fatal("unsupported Gemini signature accepted")
	}
}

func TestAnthropicCompleteStreamAndUnsupportedThinking(t *testing.T) {
	raw := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-fixture\",\"type\":\"message\"}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	route := Route{ResponseModels: []string{"claude-fixture"}, MaxOutputTokens: 128}
	if anthropicStream(httptest.NewRecorder(), strings.NewReader(raw), route) != nil {
		t.Fatal("Anthropic stream rejected")
	}
	for _, bad := range []string{
		strings.Replace(raw, "claude-fixture", "other-model", 1),
		strings.Replace(raw, `"type":"text","text":""`, `"type":"thinking","thinking":"secret"`, 1),
		strings.Split(raw, "event: message_stop")[0],
		raw + "event: error\ndata: {\"type\":\"error\"}\n\n",
		strings.Replace(raw, `"index":0,"content_block"`, `"index":0.5,"content_block"`, 1),
		strings.Replace(raw, `"type":"text_delta","text":"ok"`, `"type":"input_json_delta","partial_json":"{}"`, 1),
		strings.Replace(raw, `"stop_reason":"end_turn"}`, `"stop_reason":"end_turn"},"usage":{"output_tokens":129}`, 1),
		strings.Replace(raw, `"stop_reason":"end_turn"}`, `"stop_reason":"end_turn"},"usage":{"output_tokens":-1}`, 1),
	} {
		out := httptest.NewRecorder()
		if anthropicStream(out, strings.NewReader(bad), route) == nil || strings.Contains(out.Body.String(), "event: message_stop") {
			t.Fatal("invalid stream emitted successful finish")
		}
	}
}

func TestAnthropicToolStreamValidatesCompletedArguments(t *testing.T) {
	raw := ""
	for _, value := range []map[string]any{
		{"type": "message_start", "message": map[string]any{"model": "claude-fixture", "usage": map[string]any{"input_tokens": 8, "output_tokens": 0}}},
		{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "tool_use", "id": "call-1", "name": "write", "input": map[string]any{}}},
		{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": `{"value":"ok"}`}},
		{"type": "content_block_stop", "index": 0},
		{"type": "message_delta", "delta": map[string]any{"stop_reason": "tool_use"}, "usage": map[string]any{"output_tokens": 8}},
		{"type": "message_stop"},
	} {
		data, _ := json.Marshal(value)
		raw += fmt.Sprintf("event: %s\ndata: %s\n\n", value["type"], data)
	}
	route := Route{ResponseModels: []string{"claude-fixture"}, MaxOutputTokens: 128}
	if err := anthropicStream(httptest.NewRecorder(), strings.NewReader(raw), route); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		strings.Replace(raw, `ok\"}`, `ok\"`, 1),
		strings.Replace(raw, `"type":"input_json_delta"`, `"text":"unsupported","type":"text_delta"`, 1),
		strings.Replace(raw, `"stop_reason":"tool_use"`, `"stop_reason":"end_turn"`, 1),
	} {
		out := httptest.NewRecorder()
		if anthropicStream(out, strings.NewReader(bad), route) == nil || strings.Contains(out.Body.String(), "event: message_stop") {
			t.Fatal("invalid tool stream emitted a successful finish")
		}
	}
}

func TestManagementKeyLookupDistinguishesDatabaseFailure(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), "postgres://unused:unused@127.0.0.1:54321/unused")
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()
	s := &Server{pool: pool}
	out := httptest.NewRecorder()
	s.management(out, httptest.NewRequest(http.MethodGet, "/key/info?key="+strings.Repeat("00", 32), nil))
	if out.Code != http.StatusServiceUnavailable {
		t.Fatal("database failure was mistaken for an absent key", out.Code)
	}
}

func TestChatStreamRejectsTruncationModelDriftAndReasoning(t *testing.T) {
	base := "data: {\"model\":\"approved\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	route := Route{ResponseModels: []string{"approved"}, MaxOutputTokens: 128}
	for _, bad := range []string{strings.Replace(base, "approved", "unapproved", 1), strings.Replace(base, "data: [DONE]\n\n", "", 1), strings.Replace(base, `"content":"ok"`, `"reasoning_content":"unsupported"`, 1), strings.Replace(base, `"stop"`, `"content_filter"`, 1)} {
		out := httptest.NewRecorder()
		if chatStream(out, strings.NewReader(bad), route) == nil || strings.Contains(out.Body.String(), "event: message_stop") {
			t.Fatal("invalid upstream emitted successful finish")
		}
	}
}

func TestProviderApprovalRejectsUntrustedDestinationsAndOverrides(t *testing.T) {
	for _, base := range []string{"http://api.moonshot.ai/v1", "https://api.moonshot.ai.evil.example/v1", "https://127.0.0.1/v1", "https://api.moonshot.ai/v1?override=1", "https://api.moonshot.ai/other"} {
		if approvedParams(map[string]any{"model": "moonshot/kimi-k2.5", "api_base": base, "max_retries": float64(0)}) {
			t.Fatal("unapproved destination admitted")
		}
	}
	params := map[string]any{"model": "moonshot/kimi-k2.5", "api_base": "https://api.moonshot.ai/v1", "max_retries": float64(0)}
	if !approvedParams(params) {
		t.Fatal("approved provider rejected")
	}
	params["headers"] = map[string]any{"Authorization": "override"}
	if approvedParams(params) {
		t.Fatal("authentication override admitted")
	}
	for _, address := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "192.0.2.1", "::1", "::ffff:127.0.0.1"} {
		if publicAddress(netip.MustParseAddr(address)) {
			t.Fatal("private or special DNS result admitted")
		}
	}
}

func TestNativeAnthropicAndGeminiHTTPAuthentication(t *testing.T) {
	for _, provider := range []string{"anthropic", "custom_anthropic", "gemini"} {
		t.Run(provider, func(t *testing.T) {
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var payload map[string]any
				if json.NewDecoder(r.Body).Decode(&payload) != nil {
					t.Fatal("invalid provider request")
				}
				if r.Header.Get("Authorization") != "" {
					t.Error("wrong authentication scheme")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if provider == "gemini" {
					if r.URL.Path != "/v1beta/models/fixture:streamGenerateContent" || r.URL.Query().Get("alt") != "sse" || r.Header.Get("x-goog-api-key") != "provider-secret" || payload["contents"] == nil {
						t.Error("invalid Gemini HTTP contract")
					}
					_, _ = w.Write([]byte("data: {\"modelVersion\":\"fixture\",\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}\n\n"))
				} else {
					if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "provider-secret" || r.Header.Get("anthropic-version") != "2023-06-01" || payload["model"] != "fixture" {
						t.Error("invalid Anthropic HTTP contract")
					}
					for _, value := range []map[string]any{
						{"type": "message_start", "message": map[string]any{"type": "message", "model": "fixture"}},
						{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": "ok"}},
						{"type": "content_block_stop", "index": 0},
						{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn"}},
						{"type": "message_stop"},
					} {
						data, _ := json.Marshal(value)
						_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", value["type"], data)
					}
				}
			}))
			defer upstream.Close()
			route := Route{Alias: "private", Params: map[string]any{"model": provider + "/fixture", "api_base": upstream.URL}, ResponseModels: []string{"fixture"}, MaxOutputTokens: 128}
			request := Request{Model: "private", Stream: true, MaxTokens: 128, Messages: []Message{{Role: "user", Content: json.RawMessage(`"hello"`)}}}
			out := httptest.NewRecorder()
			if err := forward(context.Background(), out, upstream.Client(), route, request, []byte("provider-secret")); err != nil || calls != 1 || !strings.Contains(out.Body.String(), "event: message_stop") {
				t.Fatal("native HTTP streaming contract failed", err)
			}
		})
	}
}
