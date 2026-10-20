package modelgateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func appendResponseEvent(builder *strings.Builder, kind string, value map[string]any) {
	value["type"] = kind
	data, _ := json.Marshal(value)
	fmt.Fprintf(builder, "event: %s\ndata: %s\n\n", kind, data)
}

func TestResponsesPayloadPreservesStatelessToolOrder(t *testing.T) {
	reasoning := json.RawMessage(`{"id":"rs_1","type":"reasoning","encrypted_content":"opaque"}`)
	request := Request{
		Model: "alias", Stream: true, MaxTokens: 128,
		System: json.RawMessage(`"follow the tool contract"`),
		Messages: []Message{
			{Role: "assistant", Content: json.RawMessage(`[{"type":"text","text":"checking"},{"type":"tool_use","id":"call-1","name":"lookup","input":{"q":"x"}}]`)},
			{Role: "user", Content: json.RawMessage(`[{"type":"tool_result","tool_use_id":"call-1","content":"found"}]`)},
		},
		ReasoningItems: map[string][]json.RawMessage{"call-1": {reasoning}},
		Tools:          []Tool{{Name: "lookup", Schema: map[string]any{"type": "object"}}},
		ToolChoice:     json.RawMessage(`{"type":"auto"}`),
	}
	payload, err := responsesPayload(request, "approved-model")
	if err != nil {
		t.Fatal(err)
	}
	input := payload["input"].([]any)
	if len(input) != 4 {
		t.Fatalf("unexpected input item count: %d", len(input))
	}
	text := input[0].(map[string]any)
	if text["role"] != "assistant" || text["content"] != "checking" {
		t.Fatal("assistant text order was lost", input)
	}
	if input[1].(json.RawMessage)[0] != '{' {
		t.Fatal("reasoning item was not preserved as an opaque object")
	}
	call := input[2].(map[string]any)
	result := input[3].(map[string]any)
	if call["type"] != "function_call" || call["call_id"] != "call-1" || result["type"] != "function_call_output" || result["call_id"] != "call-1" {
		t.Fatal("function call and result order was lost", input)
	}
	if payload["instructions"] != "follow the tool contract" || payload["max_output_tokens"] != int64(128) || payload["stream"] != true || payload["store"] != false || payload["tool_choice"] != "auto" || payload["include"].([]string)[0] != "reasoning.encrypted_content" {
		t.Fatal("Responses request options were not mapped", payload)
	}
}

func TestResponsesForwardKeepsReasoningOutOfMessagesStream(t *testing.T) {
	var received map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer provider-secret" {
			t.Error("incorrect Responses route or authentication")
		}
		if json.NewDecoder(r.Body).Decode(&received) != nil {
			t.Error("invalid Responses request")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		var stream strings.Builder
		appendResponseEvent(&stream, "response.created", map[string]any{"response": map[string]any{"id": "resp_1", "model": "approved-model", "status": "in_progress"}})
		appendResponseEvent(&stream, "response.output_item.added", map[string]any{"output_index": 0, "item": map[string]any{"id": "rs_1", "type": "reasoning"}})
		appendResponseEvent(&stream, "response.output_item.done", map[string]any{"output_index": 0, "item": map[string]any{"id": "rs_1", "type": "reasoning", "encrypted_content": "opaque-reasoning"}})
		appendResponseEvent(&stream, "response.output_item.added", map[string]any{"output_index": 1, "item": map[string]any{"id": "fc_1", "type": "function_call", "call_id": "call-1", "name": "lookup", "arguments": ""}})
		appendResponseEvent(&stream, "response.function_call_arguments.delta", map[string]any{"item_id": "fc_1", "delta": `{"q":"x"}`})
		appendResponseEvent(&stream, "response.function_call_arguments.done", map[string]any{"item_id": "fc_1", "arguments": `{"q":"x"}`})
		call := map[string]any{"id": "fc_1", "type": "function_call", "call_id": "call-1", "name": "lookup", "arguments": `{"q":"x"}`}
		appendResponseEvent(&stream, "response.output_item.done", map[string]any{"output_index": 1, "item": call})
		appendResponseEvent(&stream, "response.completed", map[string]any{"response": map[string]any{
			"id": "resp_1", "model": "approved-model", "status": "completed",
			"usage":  map[string]any{"input_tokens": 10, "output_tokens": 8},
			"output": []any{map[string]any{"id": "rs_1", "type": "reasoning", "encrypted_content": "opaque-reasoning"}, call},
		}})
		_, _ = w.Write([]byte(stream.String()))
	}))
	defer upstream.Close()

	request := Request{Model: "private-alias", Stream: true, MaxTokens: 128, Messages: []Message{{Role: "user", Content: json.RawMessage(`"hello"`)}}}
	request.responseState = &responseState{}
	route := Route{ID: "route-1", Alias: "private-alias", Params: map[string]any{"model": "custom_openai_responses/approved-model", "api_base": upstream.URL + "/v1"}, ResponseModels: []string{"approved-model"}, MaxOutputTokens: 128}
	out := httptest.NewRecorder()
	if err := forward(context.Background(), out, upstream.Client(), route, request, []byte("provider-secret")); err != nil {
		t.Fatal("Responses stream failed", err)
	}
	body := out.Body.String()
	if received["store"] != false || !strings.Contains(body, `"type":"tool_use"`) || !strings.Contains(body, "event: message_stop") || strings.Contains(body, "opaque-reasoning") {
		t.Fatal("tool stream leaked reasoning or omitted completion", body)
	}
	items := request.responseState.ReasoningByCall["call-1"]
	if len(items) != 1 || !strings.Contains(string(items[0]), "opaque-reasoning") {
		t.Fatal("reasoning item was not captured for tool replay", request.responseState)
	}
}

func TestResponsesTextDoneSupportsMultipleContentParts(t *testing.T) {
	var stream strings.Builder
	appendResponseEvent(&stream, "response.created", map[string]any{"response": map[string]any{"id": "resp_text", "model": "approved-model", "status": "in_progress"}})
	appendResponseEvent(&stream, "response.output_item.added", map[string]any{"output_index": 0, "item": map[string]any{"id": "msg_1", "type": "message", "role": "assistant", "content": []any{}}})
	for index, value := range []string{"one", "two"} {
		content := map[string]any{"type": "output_text", "text": value}
		appendResponseEvent(&stream, "response.content_part.added", map[string]any{"output_index": 0, "item_id": "msg_1", "content_index": index, "part": content})
		appendResponseEvent(&stream, "response.output_text.delta", map[string]any{"output_index": 0, "item_id": "msg_1", "content_index": index, "delta": value})
		appendResponseEvent(&stream, "response.output_text.done", map[string]any{"output_index": 0, "item_id": "msg_1", "content_index": index, "text": value})
		appendResponseEvent(&stream, "response.content_part.done", map[string]any{"output_index": 0, "item_id": "msg_1", "content_index": index, "part": content})
	}
	message := map[string]any{"id": "msg_1", "type": "message", "role": "assistant", "content": []any{
		map[string]any{"type": "output_text", "text": "one"}, map[string]any{"type": "output_text", "text": "two"},
	}}
	appendResponseEvent(&stream, "response.output_item.done", map[string]any{"output_index": 0, "item": message})
	appendResponseEvent(&stream, "response.completed", map[string]any{"response": map[string]any{
		"id": "resp_text", "model": "approved-model", "status": "completed",
		"usage": map[string]any{"input_tokens": 2, "output_tokens": 2}, "output": []any{message},
	}})
	out := httptest.NewRecorder()
	route := Route{ResponseModels: []string{"approved-model"}, MaxOutputTokens: 128}
	if err := responsesStream(out, strings.NewReader(stream.String()), route, nil); err != nil || !strings.Contains(out.Body.String(), "event: message_stop") {
		t.Fatal("multi-part text output was rejected", err, out.Body.String())
	}
}

func TestResponsesMetadataExtension(t *testing.T) {
	for _, test := range []struct {
		name        string
		kind        string
		responseID  string
		metadata    any
		beforeStart bool
		completed   bool
		afterEnd    bool
		valid       bool
	}{
		{name: "provider metadata", kind: "response.metadata", responseID: "resp_1", metadata: map[string]any{"provider_details": "private-metadata"}, completed: true, valid: true},
		{name: "different response", kind: "response.metadata", responseID: "resp_other", metadata: map[string]any{}, completed: true},
		{name: "missing response id", kind: "response.metadata", metadata: map[string]any{}, completed: true},
		{name: "invalid metadata", kind: "response.metadata", responseID: "resp_1", metadata: "invalid", completed: true},
		{name: "before response", kind: "response.metadata", responseID: "resp_1", metadata: map[string]any{}, beforeStart: true, completed: true},
		{name: "missing completion", kind: "response.metadata", responseID: "resp_1", metadata: map[string]any{}},
		{name: "after completion", kind: "response.metadata", responseID: "resp_1", metadata: map[string]any{}, completed: true, afterEnd: true},
		{name: "unknown extension", kind: "response.unknown", responseID: "resp_1", metadata: map[string]any{}, completed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stream strings.Builder
			metadata := func() {
				appendResponseEvent(&stream, test.kind, map[string]any{"response_id": test.responseID, "metadata": test.metadata})
			}
			if test.beforeStart {
				metadata()
			}
			appendResponseEvent(&stream, "response.created", map[string]any{"response": map[string]any{"id": "resp_1", "model": "approved-model", "status": "in_progress"}})
			appendResponseEvent(&stream, "response.output_item.added", map[string]any{"output_index": 0, "item": map[string]any{"id": "fc_1", "type": "function_call", "call_id": "call-1", "name": "hc_probe", "arguments": ""}})
			appendResponseEvent(&stream, "response.function_call_arguments.delta", map[string]any{"item_id": "fc_1", "delta": `{"value":"ok"}`})
			call := map[string]any{"id": "fc_1", "type": "function_call", "call_id": "call-1", "name": "hc_probe", "arguments": `{"value":"ok"}`}
			appendResponseEvent(&stream, "response.function_call_arguments.done", map[string]any{"item_id": "fc_1", "arguments": `{"value":"ok"}`})
			appendResponseEvent(&stream, "response.output_item.done", map[string]any{"output_index": 0, "item": call})
			if !test.beforeStart && !test.afterEnd {
				metadata()
			}
			if test.completed {
				appendResponseEvent(&stream, "response.completed", map[string]any{"response": map[string]any{
					"id": "resp_1", "model": "approved-model", "status": "completed",
					"usage": map[string]any{"input_tokens": 10, "output_tokens": 8}, "output": []any{call},
				}})
			}
			if test.afterEnd {
				metadata()
			}
			out := httptest.NewRecorder()
			route := Route{ResponseModels: []string{"approved-model"}, MaxOutputTokens: 128}
			err := responsesStream(out, strings.NewReader(stream.String()), route, nil)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v: %v", test.valid, err)
			}
			body := out.Body.String()
			if strings.Contains(body, "private-metadata") || strings.Contains(body, "response.metadata") {
				t.Fatal("provider metadata leaked to the messages stream")
			}
			if test.valid && (!strings.Contains(body, `"type":"tool_use"`) || !strings.Contains(body, "event: message_stop")) {
				t.Fatal("tool call or completion was lost", body)
			}
		})
	}
}

func TestResponseReasoningCacheScopesByRouteAndToolCall(t *testing.T) {
	server := &Server{}
	route := Route{ID: "route-a", Params: map[string]any{"model": "custom_openai_responses/model"}}
	server.storeResponseReasoning(route, &responseState{ReasoningByCall: map[string][]json.RawMessage{
		"call-1": {json.RawMessage(`{"id":"rs_1","type":"reasoning","encrypted_content":"opaque"}`)},
	}})
	request := Request{Messages: []Message{{Role: "user", Content: json.RawMessage(`[{"type":"tool_result","tool_use_id":"call-1","content":"ok"}]`)}}}
	if err := server.loadResponseReasoning(route, &request); err != nil || len(request.ReasoningItems["call-1"]) != 1 {
		t.Fatal("cached reasoning was not available for the matching route", err)
	}
	other := Route{ID: "route-b", Params: route.Params}
	otherRequest := request
	otherRequest.ReasoningItems = nil
	if err := server.loadResponseReasoning(other, &otherRequest); err != nil || len(otherRequest.ReasoningItems["call-1"]) != 0 {
		t.Fatal("reasoning crossed route boundaries", err)
	}
	clearRequestReasoning(&request)
	clearRequestReasoning(&otherRequest)
}
