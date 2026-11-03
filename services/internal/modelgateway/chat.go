package modelgateway

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

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
