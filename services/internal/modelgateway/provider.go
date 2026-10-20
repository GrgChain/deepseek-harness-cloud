// Package modelgateway implements the platform's model execution boundary.
// It uses provider HTTP APIs directly; no Python SDK or external model proxy.
package modelgateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

var errProtocol = errors.New("model protocol unavailable")

type Message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Schema      map[string]any `json:"input_schema"`
}
type Request struct {
	Model          string                       `json:"model"`
	MaxTokens      int64                        `json:"max_tokens"`
	Stream         bool                         `json:"stream"`
	Messages       []Message                    `json:"messages"`
	System         json.RawMessage              `json:"system,omitempty"`
	Tools          []Tool                       `json:"tools,omitempty"`
	ToolChoice     json.RawMessage              `json:"tool_choice,omitempty"`
	Temperature    *float64                     `json:"temperature,omitempty"`
	Stop           []string                     `json:"stop_sequences,omitempty"`
	ReasoningItems map[string][]json.RawMessage `json:"-"`
	responseState  *responseState               `json:"-"`
}
type Block struct {
	Type    string          `json:"type"`
	Text    string          `json:"text,omitempty"`
	ID      string          `json:"id,omitempty"`
	Name    string          `json:"name,omitempty"`
	Input   json.RawMessage `json:"input,omitempty"`
	ToolID  string          `json:"tool_use_id,omitempty"`
	Content json.RawMessage `json:"content,omitempty"`
	IsError bool            `json:"is_error,omitempty"`
}
type Route struct {
	ID, Alias, Org, Owner, Connection string
	Name                              string
	ContextWindow                     int64
	Version                           int64
	Params                            map[string]any
	ResponseModels                    []string
	MaxOutputTokens                   int64
}

func blocks(raw json.RawMessage) ([]Block, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return []Block{{Type: "text", Text: text}}, nil
	}
	var result []Block
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil || len(result) > 1024 {
		return nil, errProtocol
	}
	for _, b := range result {
		switch b.Type {
		case "text":
		case "tool_use":
			var object map[string]any
			if b.ID == "" || b.Name == "" || json.Unmarshal(b.Input, &object) != nil || object == nil {
				return nil, errProtocol
			}
		case "tool_result":
			if b.ToolID == "" {
				return nil, errProtocol
			}
			child, err := blocks(b.Content)
			if err != nil {
				return nil, err
			}
			for _, c := range child {
				if c.Type != "text" {
					return nil, errProtocol
				}
			}
		default:
			return nil, errProtocol
		}
	}
	return result, nil
}
func plain(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	bs, err := blocks(raw)
	if err != nil {
		return "", err
	}
	var text strings.Builder
	for _, b := range bs {
		if b.Type != "text" {
			return "", errProtocol
		}
		text.WriteString(b.Text)
	}
	return text.String(), nil
}
func (r Request) validate(route Route) error {
	if r.Model != route.Alias || !r.Stream || r.MaxTokens < 1 || r.MaxTokens > route.MaxOutputTokens || len(r.Messages) == 0 || len(r.Messages) > 1024 || len(r.Tools) > 128 || len(r.Stop) > 16 {
		return errProtocol
	}
	if r.Temperature != nil && (*r.Temperature < 0 || *r.Temperature > 2) {
		return errProtocol
	}
	if _, err := plain(r.System); err != nil {
		return err
	}
	tools := map[string]bool{}
	for _, t := range r.Tools {
		if len(t.Name) == 0 || len(t.Name) > 128 || tools[t.Name] || t.Schema == nil {
			return errProtocol
		}
		tools[t.Name] = true
	}
	pending := map[string]bool{}
	for _, m := range r.Messages {
		if m.Role != "user" && m.Role != "assistant" {
			return errProtocol
		}
		bs, err := blocks(m.Content)
		if err != nil {
			return err
		}
		for _, b := range bs {
			if b.Type == "tool_use" {
				if m.Role != "assistant" || pending[b.ID] {
					return errProtocol
				}
				pending[b.ID] = true
			}
			if b.Type == "tool_result" {
				if m.Role != "user" || !pending[b.ToolID] {
					return errProtocol
				}
				delete(pending, b.ToolID)
			}
		}
	}
	if len(pending) != 0 {
		return errProtocol
	}
	return nil
}
func chatPayload(r Request, model string, params map[string]any) (map[string]any, error) {
	messages := []any{}
	system, err := plain(r.System)
	if err != nil {
		return nil, err
	}
	if system != "" {
		messages = append(messages, map[string]any{"role": "system", "content": system})
	}
	for _, m := range r.Messages {
		bs, err := blocks(m.Content)
		if err != nil {
			return nil, err
		}
		var text strings.Builder
		calls := []any{}
		results := []any{}
		for _, b := range bs {
			switch b.Type {
			case "text":
				text.WriteString(b.Text)
			case "tool_use":
				calls = append(calls, map[string]any{"id": b.ID, "type": "function", "function": map[string]any{"name": b.Name, "arguments": string(b.Input)}})
			case "tool_result":
				value, err := plain(b.Content)
				if err != nil {
					return nil, err
				}
				results = append(results, map[string]any{"role": "tool", "tool_call_id": b.ToolID, "content": value})
			}
		}
		// Results must follow the assistant's tool calls before the next user text.
		messages = append(messages, results...)
		if text.Len() > 0 || len(calls) > 0 {
			item := map[string]any{"role": m.Role, "content": text.String()}
			if len(calls) > 0 {
				item["tool_calls"] = calls
			}
			messages = append(messages, item)
		}
	}
	payload := map[string]any{"model": model, "messages": messages, "max_tokens": r.MaxTokens, "stream": true, "stream_options": map[string]any{"include_usage": true}}
	if r.Temperature != nil {
		payload["temperature"] = *r.Temperature
	}
	if len(r.Stop) > 0 {
		payload["stop"] = r.Stop
	}
	if len(r.Tools) > 0 {
		tools := []any{}
		for _, t := range r.Tools {
			tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": t.Name, "description": t.Description, "parameters": t.Schema}})
		}
		payload["tools"] = tools
	}
	if len(r.ToolChoice) > 0 {
		var choice struct{ Type, Name string }
		if json.Unmarshal(r.ToolChoice, &choice) != nil {
			return nil, errProtocol
		}
		switch choice.Type {
		case "auto":
			payload["tool_choice"] = "auto"
		case "any":
			payload["tool_choice"] = "required"
		case "none":
			payload["tool_choice"] = "none"
		case "tool":
			payload["tool_choice"] = map[string]any{"type": "function", "function": map[string]string{"name": choice.Name}}
		default:
			return nil, errProtocol
		}
	}
	// GPT-5/6 Chat Completions tool calls require non-reasoning mode and the newer output budget field.
	if prefix, _ := params["model"].(string); strings.HasPrefix(prefix, "openai/") && (strings.HasPrefix(model, "gpt-5") || strings.HasPrefix(model, "gpt-6")) {
		payload["reasoning_effort"] = "none"
		payload["max_completion_tokens"] = r.MaxTokens
		delete(payload, "max_tokens")
	}
	// Only frozen provider-specific fields can supplement the fixed request.
	if extra, ok := params["extra_body"].(map[string]any); ok {
		for k, v := range extra {
			if _, exists := payload[k]; exists {
				return nil, errProtocol
			}
			payload[k] = v
		}
	}
	return payload, nil
}
func forward(ctx context.Context, w http.ResponseWriter, client *http.Client, route Route, r Request, key []byte) error {
	if err := r.validate(route); err != nil {
		return err
	}
	providerModel, ok := route.Params["model"].(string)
	if !ok {
		return errProtocol
	}
	provider, model, ok := strings.Cut(providerModel, "/")
	if !ok || model == "" {
		return errProtocol
	}
	base, ok := route.Params["api_base"].(string)
	if !ok {
		return errProtocol
	}
	endpoint := strings.TrimRight(base, "/")
	var payload any
	var err error
	switch provider {
	case "openai", "custom_openai", "dashscope", "deepseek", "moonshot", "zai":
		if provider == "deepseek" && !strings.HasSuffix(endpoint, "/v1") {
			endpoint += "/v1"
		}
		endpoint += "/chat/completions"
		payload, err = chatPayload(r, model, route.Params)
	case "custom_openai_responses":
		endpoint += "/responses"
		payload, err = responsesPayload(r, model)
	case "anthropic", "custom_anthropic":
		if provider == "custom_anthropic" && strings.HasSuffix(endpoint, "/v1") {
			endpoint += "/messages"
		} else {
			endpoint += "/v1/messages"
		}
		copy := r
		copy.Model = model
		payload = copy
	case "gemini":
		endpoint += "/v1beta/models/" + url.PathEscape(model) + ":streamGenerateContent?alt=sse"
		payload, err = geminiPayload(r, route.Params)
	default:
		return errProtocol
	}
	if err != nil {
		return err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return errProtocol
	}
	defer clear(raw)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return errProtocol
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	switch provider {
	case "anthropic", "custom_anthropic":
		request.Header.Set("x-api-key", string(key))
		request.Header.Set("anthropic-version", "2023-06-01")
	case "gemini":
		request.Header.Set("x-goog-api-key", string(key))
	default:
		request.Header.Set("Authorization", "Bearer "+string(key))
	}
	if (provider == "custom_openai" || provider == "custom_openai_responses" || provider == "custom_anthropic") && string(key) == "-" {
		request.Header.Del("Authorization")
		request.Header.Del("x-api-key")
	}
	response, err := client.Do(request)
	if err != nil {
		return errProtocol
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		status := response.StatusCode
		if status != 401 && status != 403 && status != 404 && status != 429 {
			status = http.StatusBadGateway
		}
		writeError(w, status, "MODEL_UPSTREAM_REJECTED")
		return nil
	}
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		return errProtocol
	}
	response.Body = struct {
		io.Reader
		io.Closer
	}{io.LimitReader(response.Body, (16<<20)+1), response.Body}
	// Validate reported usage against this request's limit as well as the catalog.
	route.MaxOutputTokens = r.MaxTokens
	switch provider {
	case "anthropic", "custom_anthropic":
		return anthropicStream(w, response.Body, route)
	case "custom_openai_responses":
		return responsesStream(w, response.Body, route, r.responseState)
	case "gemini":
		return geminiStream(w, response.Body, route)
	default:
		return chatStream(w, response.Body, route)
	}
}

type events struct {
	w             http.ResponseWriter
	route         Route
	started       bool
	index         int
	textOpen      bool
	stop          string
	input, output int64
}

func (e *events) emit(kind string, value map[string]any) error {
	value["type"] = kind
	data, err := json.Marshal(value)
	if err != nil {
		return errProtocol
	}
	e.w.Header().Set("Content-Type", "text/event-stream")
	e.w.Header().Set("Cache-Control", "no-store")
	e.w.Header().Set("X-Accel-Buffering", "no")
	_, err = fmt.Fprintf(e.w, "event: %s\ndata: %s\n\n", kind, data)
	if f, ok := e.w.(http.Flusher); ok {
		f.Flush()
	}
	return err
}
func (e *events) start(model string) error {
	if !slices.Contains(e.route.ResponseModels, model) {
		return errProtocol
	}
	if e.started {
		return nil
	}
	e.started = true
	return e.emit("message_start", map[string]any{"message": map[string]any{"id": "msg-platform", "type": "message", "role": "assistant", "model": model, "content": []any{}, "stop_reason": nil, "usage": map[string]int64{"input_tokens": 0, "output_tokens": 0}}})
}
func (e *events) text(value string) error {
	if value == "" {
		return nil
	}
	if !e.textOpen {
		if err := e.emit("content_block_start", map[string]any{"index": e.index, "content_block": map[string]any{"type": "text", "text": ""}}); err != nil {
			return err
		}
		e.textOpen = true
	}
	return e.emit("content_block_delta", map[string]any{"index": e.index, "delta": map[string]any{"type": "text_delta", "text": value}})
}
func (e *events) closeText() error {
	if !e.textOpen {
		return nil
	}
	err := e.emit("content_block_stop", map[string]any{"index": e.index})
	e.index++
	e.textOpen = false
	return err
}
func (e *events) tool(id, name, args string) error {
	var object map[string]any
	if id == "" || name == "" || json.Unmarshal([]byte(args), &object) != nil || object == nil || e.index >= 1024 {
		return errProtocol
	}
	if err := e.closeText(); err != nil {
		return err
	}
	if err := e.emit("content_block_start", map[string]any{"index": e.index, "content_block": map[string]any{"type": "tool_use", "id": id, "name": name, "input": map[string]any{}}}); err != nil {
		return err
	}
	if err := e.emit("content_block_delta", map[string]any{"index": e.index, "delta": map[string]any{"type": "input_json_delta", "partial_json": args}}); err != nil {
		return err
	}
	if err := e.emit("content_block_stop", map[string]any{"index": e.index}); err != nil {
		return err
	}
	e.index++
	return nil
}
func (e *events) finish() error {
	if !e.started || e.stop == "" || e.input < 0 || e.output < 0 || e.output > e.route.MaxOutputTokens {
		return errProtocol
	}
	if err := e.closeText(); err != nil {
		return err
	}
	if e.index == 0 {
		return errProtocol
	}
	if err := e.emit("message_delta", map[string]any{"delta": map[string]any{"stop_reason": e.stop, "stop_sequence": nil}, "usage": map[string]int64{"input_tokens": e.input, "output_tokens": e.output}}); err != nil {
		return err
	}
	return e.emit("message_stop", map[string]any{})
}
func readSSE(reader io.Reader, handle func(string, string) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var data []string
	kind := ""
	total := 0
	for scanner.Scan() {
		line := scanner.Text()
		total += len(line) + 1
		if total > 16<<20 {
			return errProtocol
		}
		if line == "" {
			if len(data) > 0 {
				if err := handle(kind, strings.Join(data, "\n")); err != nil {
					return err
				}
			}
			data = nil
			kind = ""
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		} else if strings.HasPrefix(line, "event:") {
			kind = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else {
			return errProtocol
		}
	}
	if scanner.Err() != nil || len(data) > 0 || kind != "" {
		return errProtocol
	}
	return nil
}
func chatStream(w http.ResponseWriter, reader io.Reader, route Route) error {
	e := events{w: w, route: route}
	type call struct{ id, name, args string }
	calls := map[int]*call{}
	done := false
	err := readSSE(reader, func(_ string, data string) error {
		if done {
			return errProtocol
		}
		if data == "[DONE]" {
			done = true
			return nil
		}
		var c struct {
			Model   string
			Choices []struct {
				Index int
				Delta struct {
					Content   string
					Reasoning string `json:"reasoning_content"`
					Tools     []struct {
						Index    int
						ID       string
						Function struct{ Name, Arguments string }
					} `json:"tool_calls"`
				}
				Finish *string `json:"finish_reason"`
			}
			Usage struct {
				Input  int64 `json:"prompt_tokens"`
				Output int64 `json:"completion_tokens"`
			}
		}
		if json.Unmarshal([]byte(data), &c) != nil || len(c.Choices) > 1 {
			return errProtocol
		}
		if err := e.start(c.Model); err != nil {
			return err
		}
		if c.Usage.Input != 0 || c.Usage.Output != 0 {
			e.input = c.Usage.Input
			e.output = c.Usage.Output
		}
		for _, choice := range c.Choices {
			if choice.Index != 0 || choice.Delta.Reasoning != "" {
				return errProtocol
			}
			if e.stop != "" {
				return errProtocol
			}
			if err := e.text(choice.Delta.Content); err != nil {
				return err
			}
			for _, t := range choice.Delta.Tools {
				if t.Index < 0 || t.Index >= 128 {
					return errProtocol
				}
				item := calls[t.Index]
				if item == nil {
					item = &call{}
					calls[t.Index] = item
				}
				if t.ID != "" {
					if item.id != "" && item.id != t.ID {
						return errProtocol
					}
					item.id = t.ID
				}
				if t.Function.Name != "" {
					if item.name != "" && item.name != t.Function.Name {
						return errProtocol
					}
					item.name = t.Function.Name
				}
				item.args += t.Function.Arguments
				if len(item.args) > 1<<20 {
					return errProtocol
				}
			}
			if choice.Finish != nil {
				e.stop = map[string]string{"stop": "end_turn", "tool_calls": "tool_use", "length": "max_tokens"}[*choice.Finish]
				if e.stop == "" {
					return errProtocol
				}
			}
		}
		return nil
	})
	if err != nil || !done {
		return errProtocol
	}
	if len(calls) > 0 && e.stop != "tool_use" {
		return errProtocol
	}
	if len(calls) == 0 && e.stop == "tool_use" {
		return errProtocol
	}
	for i := 0; i < len(calls); i++ {
		c := calls[i]
		if c == nil {
			return errProtocol
		}
		if err := e.tool(c.id, c.name, c.args); err != nil {
			return err
		}
	}
	return e.finish()
}
func anthropicStream(w http.ResponseWriter, reader io.Reader, route Route) error {
	e := events{w: w, route: route}
	ended := false
	open := -1
	count := 0
	blockKind := ""
	var arguments strings.Builder
	toolIDs := map[string]bool{}
	usage := func(value any) error {
		if value == nil {
			return nil
		}
		fields, ok := value.(map[string]any)
		if !ok {
			return errProtocol
		}
		for _, name := range []string{"input_tokens", "output_tokens"} {
			if number, exists := fields[name]; exists {
				n, ok := number.(float64)
				if !ok || n < 0 || n > 1<<53 || n != float64(int64(n)) || (name == "output_tokens" && n > float64(route.MaxOutputTokens)) {
					return errProtocol
				}
			}
		}
		return nil
	}
	err := readSSE(reader, func(kind, data string) error {
		if ended {
			return errProtocol
		}
		var value map[string]any
		if json.Unmarshal([]byte(data), &value) != nil || value["type"] != kind {
			return errProtocol
		}
		switch kind {
		case "ping":
			return nil
		case "message_start":
			if e.started {
				return errProtocol
			}
			message, ok := value["message"].(map[string]any)
			if !ok {
				return errProtocol
			}
			model, _ := message["model"].(string)
			if !slices.Contains(route.ResponseModels, model) {
				return errProtocol
			}
			if err := usage(message["usage"]); err != nil {
				return err
			}
			e.started = true
		case "content_block_start":
			i, ok := value["index"].(float64)
			if !e.started || !ok || i != float64(count) || open != -1 || e.stop != "" {
				return errProtocol
			}
			block, ok := value["content_block"].(map[string]any)
			if !ok || (block["type"] != "text" && block["type"] != "tool_use") || count >= 1024 {
				return errProtocol
			}
			blockKind = block["type"].(string)
			arguments.Reset()
			if blockKind == "tool_use" {
				id, _ := block["id"].(string)
				name, _ := block["name"].(string)
				if id == "" || name == "" || toolIDs[id] {
					return errProtocol
				}
				if input, ok := block["input"].(map[string]any); !ok || input == nil || len(input) != 0 {
					return errProtocol
				}
				toolIDs[id] = true
			} else if _, ok := block["text"].(string); !ok {
				return errProtocol
			}
			open = count
			count++
		case "content_block_delta":
			if open < 0 || value["index"] != float64(open) {
				return errProtocol
			}
			delta, ok := value["delta"].(map[string]any)
			if !ok {
				return errProtocol
			}
			if blockKind == "text" {
				if _, ok := delta["text"].(string); delta["type"] != "text_delta" || !ok {
					return errProtocol
				}
			} else {
				partial, ok := delta["partial_json"].(string)
				if delta["type"] != "input_json_delta" || !ok || arguments.Len()+len(partial) > 1<<20 {
					return errProtocol
				}
				arguments.WriteString(partial)
			}
		case "content_block_stop":
			if open < 0 || value["index"] != float64(open) {
				return errProtocol
			}
			if blockKind == "tool_use" && arguments.Len() > 0 {
				var object map[string]any
				if json.Unmarshal([]byte(arguments.String()), &object) != nil || object == nil {
					return errProtocol
				}
			}
			open = -1
		case "message_delta":
			if !e.started || open != -1 || e.stop != "" {
				return errProtocol
			}
			delta, ok := value["delta"].(map[string]any)
			if !ok {
				return errProtocol
			}
			e.stop, _ = delta["stop_reason"].(string)
			if !slices.Contains([]string{"end_turn", "tool_use", "max_tokens", "stop_sequence"}, e.stop) {
				return errProtocol
			}
			if (e.stop == "tool_use") != (len(toolIDs) > 0) {
				return errProtocol
			}
			if err := usage(value["usage"]); err != nil {
				return err
			}
		case "message_stop":
			if !e.started || open != -1 || e.stop == "" || count == 0 {
				return errProtocol
			}
			ended = true
			// Withhold success until the complete SSE body has been checked.
			return nil
		default:
			return errProtocol
		}
		delete(value, "type")
		return e.emit(kind, value)
	})
	if err != nil || !ended {
		return errProtocol
	}
	return e.emit("message_stop", map[string]any{})
}
