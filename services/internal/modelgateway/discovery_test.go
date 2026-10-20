package modelgateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDiscoveryPaginationAndAuthentication(t *testing.T) {
	for _, prefix := range []string{"custom_openai", "custom_openai_responses", "custom_anthropic", "gemini"} {
		t.Run(prefix, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" {
					t.Error("discovery initiated inference")
				}
				switch prefix {
				case "custom_openai", "custom_openai_responses":
					if r.Header.Get("Authorization") != "Bearer synthetic-discovery" {
						t.Error("missing OpenAI authentication")
					}
					fmt.Fprint(w, `{"data":[{"id":"model-current"},{"id":"model-current"},{"id":"model-new"}]}`)
				case "custom_anthropic":
					if r.Header.Get("x-api-key") != "synthetic-discovery" || r.Header.Get("anthropic-version") != "2023-06-01" {
						t.Error("missing Anthropic authentication")
					}
					if calls == 1 {
						fmt.Fprint(w, `{"data":[{"id":"claude-current"}],"has_more":true,"last_id":"claude-current"}`)
					} else {
						if r.URL.Query().Get("after_id") != "claude-current" {
							t.Error("missing cursor")
						}
						fmt.Fprint(w, `{"data":[{"id":"claude-new"}],"has_more":false}`)
					}
				case "gemini":
					if r.Header.Get("x-goog-api-key") != "synthetic-discovery" || r.URL.Query().Get("key") != "" {
						t.Error("key leaked in URL")
					}
					if calls == 1 {
						fmt.Fprint(w, `{"models":[{"name":"models/gemini-current","supportedGenerationMethods":["generateContent"]},{"name":"models/embedding","supportedGenerationMethods":["embedContent"]}],"nextPageToken":"next"}`)
					} else {
						if r.URL.Query().Get("pageToken") != "next" {
							t.Error("missing page token")
						}
						fmt.Fprint(w, `{"models":[{"name":"models/gemini-new","supportedGenerationMethods":["generateContent"]}]}`)
					}
				}
			}))
			defer server.Close()
			items, err := discoverModels(context.Background(), server.Client(), prefix, server.URL+"/models", "synthetic-discovery")
			if err != nil || len(items) != 2 {
				t.Fatal("incomplete discovery", err, len(items))
			}
			expected := 2
			if prefix == "custom_openai" || prefix == "custom_openai_responses" {
				expected = 1
			}
			if calls != expected {
				t.Fatal("unexpected pagination", calls)
			}
		})
	}
}

func TestCustomDiscoveryRequiresExactConfiguredRootAndDoesNotFollowRedirect(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "" || r.Header.Get("x-api-key") != "" {
			t.Error("no-auth service received a credential")
		}
		if r.URL.Path == "/v1/models" {
			fmt.Fprint(w, `{"data":[{"id":"local-model"}]}`)
		} else {
			http.Redirect(w, r, "/v1/models", 302)
		}
	}))
	defer server.Close()
	root := server.URL + "/v1"
	if _, err := DiscoverModels(context.Background(), "custom_openai", root, "", nil); err == nil || calls != 0 {
		t.Fatal("unconfigured local service contacted")
	}
	if _, err := DiscoverModels(context.Background(), "custom_openai", root+"/other", "", []string{root}); err == nil || calls != 0 {
		t.Fatal("root prefix accepted")
	}
	items, err := DiscoverModels(context.Background(), "custom_openai", root, "", []string{root})
	if err != nil || len(items) != 1 || calls != 1 {
		t.Fatal("no-auth discovery failed", err)
	}
	if _, err = DiscoverModels(context.Background(), "custom_openai", server.URL, "", []string{server.URL}); err == nil || calls != 2 {
		t.Fatal("redirect accepted")
	}
}

func TestDiscoveryRejectsMalformedOversizedAndEndlessLists(t *testing.T) {
	for _, body := range []string{`{"error":{"message":"synthetic-secret"}}`, `{"data":[{"id":"bad model"}]}`, `{"data":[{"id":"ok"}],"has_more":true,"last_id":"same"}`, strings.Repeat("x", (1<<20)+1)} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
		if _, err := discoverModels(context.Background(), server.Client(), "custom_anthropic", server.URL, "synthetic"); err == nil || strings.Contains(err.Error(), "synthetic-secret") {
			t.Error("invalid list accepted or error leaked")
		}
		server.Close()
	}
}

func TestCustomInferenceProtocolsNoAuth(t *testing.T) {
	for _, prefix := range []string{"custom_openai", "custom_openai_responses", "custom_anthropic"} {
		t.Run(prefix, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("Authorization") != "" || r.Header.Get("x-api-key") != "" {
					t.Error("no-auth upstream received sentinel credential")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if prefix == "custom_openai" {
					if r.URL.Path != "/v1/chat/completions" {
						t.Error("wrong OpenAI endpoint", r.URL.Path)
					}
					fmt.Fprint(w, "data: {\"model\":\"local\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				} else if prefix == "custom_openai_responses" {
					if r.URL.Path != "/v1/responses" {
						t.Error("wrong Responses endpoint", r.URL.Path)
					}
					var stream strings.Builder
					appendResponseEvent(&stream, "response.created", map[string]any{"response": map[string]any{"id": "resp_local", "model": "local", "status": "in_progress"}})
					appendResponseEvent(&stream, "response.output_item.added", map[string]any{"output_index": 0, "item": map[string]any{"id": "msg_local", "type": "message", "role": "assistant", "content": []any{}}})
					part := map[string]any{"type": "output_text", "text": "ok"}
					appendResponseEvent(&stream, "response.content_part.added", map[string]any{"output_index": 0, "item_id": "msg_local", "content_index": 0, "part": part})
					appendResponseEvent(&stream, "response.output_text.delta", map[string]any{"output_index": 0, "item_id": "msg_local", "content_index": 0, "delta": "ok"})
					appendResponseEvent(&stream, "response.output_text.done", map[string]any{"output_index": 0, "item_id": "msg_local", "content_index": 0, "text": "ok"})
					appendResponseEvent(&stream, "response.content_part.done", map[string]any{"output_index": 0, "item_id": "msg_local", "content_index": 0, "part": part})
					message := map[string]any{"id": "msg_local", "type": "message", "role": "assistant", "content": []any{part}}
					appendResponseEvent(&stream, "response.output_item.done", map[string]any{"output_index": 0, "item": message})
					appendResponseEvent(&stream, "response.completed", map[string]any{"response": map[string]any{"id": "resp_local", "model": "local", "status": "completed", "usage": map[string]any{"input_tokens": 1, "output_tokens": 1}, "output": []any{message}}})
					fmt.Fprint(w, stream.String())
				} else {
					if r.URL.Path != "/v1/messages" || r.Header.Get("anthropic-version") != "2023-06-01" {
						t.Error("wrong Anthropic endpoint", r.URL.Path)
					}
					fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"type\":\"message\",\"model\":\"local\"}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"ok\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
				}
			}))
			defer server.Close()
			base := server.URL + "/v1"
			params := map[string]any{"model": prefix + "/local", "api_base": base, "max_retries": float64(0)}
			if !approvedParams(params, base) || approvedParams(params) {
				t.Fatal("custom approval not exact")
			}
			client := providerClient(base)
			defer client.CloseIdleConnections()
			route := Route{Alias: "private", Params: params, ResponseModels: []string{"local"}, MaxOutputTokens: 128}
			request := Request{Model: "private", Stream: true, MaxTokens: 128, Messages: []Message{{Role: "user", Content: []byte(`"hello"`)}}}
			out := httptest.NewRecorder()
			if err := forward(context.Background(), out, client, route, request, []byte("-")); err != nil || calls != 1 || !strings.Contains(out.Body.String(), "event: message_stop") {
				t.Fatal("custom no-auth inference failed", err)
			}
		})
	}
}

func TestLatestOpenAIChatBudgetAndReasoning(t *testing.T) {
	request := Request{MaxTokens: 128, Messages: []Message{{Role: "user", Content: []byte(`"hello"`)}}}
	payload, err := chatPayload(request, "gpt-6-luna", map[string]any{"model": "openai/gpt-6-luna"})
	if err != nil || payload["reasoning_effort"] != "none" || payload["max_completion_tokens"] != int64(128) || payload["max_tokens"] != nil {
		t.Fatal("latest Chat Completions contract missing", err)
	}
	old, err := chatPayload(request, "gpt-4.1", map[string]any{"model": "openai/gpt-4.1"})
	if err != nil || old["reasoning_effort"] != nil || old["max_tokens"] != int64(128) {
		t.Fatal("existing model contract changed", err)
	}
}
