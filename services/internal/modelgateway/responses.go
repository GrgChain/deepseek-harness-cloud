package modelgateway

import (
	"encoding/json"
	"strings"
)

type responseState struct {
	ReasoningByCall map[string][]json.RawMessage
}

func responsesPayload(r Request, model string) (map[string]any, error) {
	if r.MaxTokens < 1 || len(r.Stop) > 0 {
		return nil, errProtocol
	}
	instructions, err := plain(r.System)
	if err != nil {
		return nil, err
	}
	input := make([]any, 0, len(r.Messages))
	seenReasoning := map[string]bool{}
	for _, message := range r.Messages {
		content, err := blocks(message.Content)
		if err != nil {
			return nil, err
		}
		var text strings.Builder
		flushText := func() {
			if text.Len() > 0 {
				input = append(input, map[string]any{"role": message.Role, "content": text.String()})
				text.Reset()
			}
		}
		for _, block := range content {
			switch block.Type {
			case "text":
				text.WriteString(block.Text)
			case "tool_use":
				flushText()
				for _, raw := range r.ReasoningItems[block.ID] {
					var reasoning struct {
						ID   string `json:"id"`
						Type string `json:"type"`
					}
					if len(raw) == 0 || len(raw) > 1<<20 || json.Unmarshal(raw, &reasoning) != nil || reasoning.ID == "" || reasoning.Type != "reasoning" {
						return nil, errProtocol
					}
					if !seenReasoning[reasoning.ID] {
						input = append(input, raw)
						seenReasoning[reasoning.ID] = true
					}
				}
				input = append(input, map[string]any{"type": "function_call", "call_id": block.ID, "name": block.Name, "arguments": string(block.Input)})
			case "tool_result":
				flushText()
				value, err := plain(block.Content)
				if err != nil {
					return nil, err
				}
				input = append(input, map[string]any{"type": "function_call_output", "call_id": block.ToolID, "output": value})
			}
		}
		flushText()
	}
	payload := map[string]any{"model": model, "input": input, "max_output_tokens": r.MaxTokens, "stream": true, "store": false, "include": []string{"reasoning.encrypted_content"}}
	if instructions != "" {
		payload["instructions"] = instructions
	}
	if r.Temperature != nil {
		payload["temperature"] = *r.Temperature
	}
	if len(r.Tools) > 0 {
		tools := make([]any, 0, len(r.Tools))
		for _, tool := range r.Tools {
			tools = append(tools, map[string]any{"type": "function", "name": tool.Name, "description": tool.Description, "parameters": tool.Schema, "strict": false})
		}
		payload["tools"] = tools
	}
	if len(r.ToolChoice) > 0 {
		var choice struct {
			Type string `json:"type"`
			Name string `json:"name"`
		}
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
			if choice.Name == "" {
				return nil, errProtocol
			}
			payload["tool_choice"] = map[string]any{"type": "function", "name": choice.Name}
		default:
			return nil, errProtocol
		}
	}
	return payload, nil
}
