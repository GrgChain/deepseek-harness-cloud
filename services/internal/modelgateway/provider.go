// Package modelgateway implements the platform's model execution boundary.
// It uses provider HTTP APIs directly; no Python SDK or external model proxy.
package modelgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
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
