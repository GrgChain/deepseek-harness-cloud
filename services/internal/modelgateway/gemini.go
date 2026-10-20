package modelgateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

func geminiPayload(r Request, params map[string]any) (map[string]any, error) {
	contents := []any{}
	toolNames := map[string]string{}
	for _, m := range r.Messages {
		bs, err := blocks(m.Content)
		if err != nil {
			return nil, err
		}
		parts := []any{}
		for _, b := range bs {
			switch b.Type {
			case "text":
				parts = append(parts, map[string]any{"text": b.Text})
			case "tool_use":
				var args map[string]any
				if json.Unmarshal(b.Input, &args) != nil {
					return nil, errProtocol
				}
				toolNames[b.ID] = b.Name
				parts = append(parts, map[string]any{"functionCall": map[string]any{"name": b.Name, "args": args}})
			case "tool_result":
				name := toolNames[b.ToolID]
				if name == "" {
					return nil, errProtocol
				}
				value, err := plain(b.Content)
				if err != nil {
					return nil, err
				}
				parts = append(parts, map[string]any{"functionResponse": map[string]any{"name": name, "response": map[string]any{"result": value, "is_error": b.IsError}}})
			}
		}
		role := "user"
		if m.Role == "assistant" {
			role = "model"
		}
		contents = append(contents, map[string]any{"role": role, "parts": parts})
	}
	config := map[string]any{"maxOutputTokens": r.MaxTokens}
	if r.Temperature != nil {
		config["temperature"] = *r.Temperature
	}
	if len(r.Stop) > 0 {
		config["stopSequences"] = r.Stop
	}
	if params["reasoning_effort"] == "none" {
		config["thinkingConfig"] = map[string]any{"thinkingBudget": 0}
	}
	payload := map[string]any{"contents": contents, "generationConfig": config}
	system, err := plain(r.System)
	if err != nil {
		return nil, err
	}
	if system != "" {
		payload["systemInstruction"] = map[string]any{"parts": []any{map[string]any{"text": system}}}
	}
	if len(r.Tools) > 0 {
		declarations := []any{}
		for _, t := range r.Tools {
			declarations = append(declarations, map[string]any{"name": t.Name, "description": t.Description, "parameters": t.Schema})
		}
		payload["tools"] = []any{map[string]any{"functionDeclarations": declarations}}
	}
	if len(r.ToolChoice) > 0 {
		var choice struct{ Type, Name string }
		if json.Unmarshal(r.ToolChoice, &choice) != nil {
			return nil, errProtocol
		}
		config := map[string]any{}
		switch choice.Type {
		case "auto":
			config["mode"] = "AUTO"
		case "any":
			config["mode"] = "ANY"
		case "none":
			config["mode"] = "NONE"
		case "tool":
			config["mode"] = "ANY"
			config["allowedFunctionNames"] = []string{choice.Name}
		default:
			return nil, errProtocol
		}
		payload["toolConfig"] = map[string]any{"functionCallingConfig": config}
	}
	return payload, nil
}
func geminiStream(w http.ResponseWriter, reader io.Reader, route Route) error {
	e := events{w: w, route: route}
	tools := 0
	err := readSSE(reader, func(_ string, data string) error {
		var c struct {
			Model      string `json:"modelVersion"`
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text      string
						Thought   bool
						Signature string `json:"thoughtSignature"`
						Call      *struct {
							Name string
							Args json.RawMessage
						} `json:"functionCall"`
					}
				}
				Finish string `json:"finishReason"`
			}
			Usage struct {
				Input  int64 `json:"promptTokenCount"`
				Output int64 `json:"candidatesTokenCount"`
			} `json:"usageMetadata"`
		}
		if json.Unmarshal([]byte(data), &c) != nil || len(c.Candidates) > 1 {
			return errProtocol
		}
		if err := e.start(c.Model); err != nil {
			return err
		}
		if c.Usage.Input != 0 || c.Usage.Output != 0 {
			e.input = c.Usage.Input
			e.output = c.Usage.Output
		}
		for _, candidate := range c.Candidates {
			if e.stop != "" {
				return errProtocol
			}
			for _, part := range candidate.Content.Parts {
				// Thought signatures require a different cross-turn contract. Fail closed.
				if part.Thought || part.Signature != "" {
					return errProtocol
				}
				if part.Text != "" {
					if err := e.text(part.Text); err != nil {
						return err
					}
				}
				if part.Call != nil {
					tools++
					if tools > 128 {
						return errProtocol
					}
					if err := e.tool(fmt.Sprintf("gemini-call-%d", tools), part.Call.Name, string(part.Call.Args)); err != nil {
						return err
					}
				}
			}
			if candidate.Finish != "" {
				switch candidate.Finish {
				case "STOP":
					e.stop = "end_turn"
					if tools > 0 {
						e.stop = "tool_use"
					}
				case "MAX_TOKENS":
					e.stop = "max_tokens"
				default:
					return errProtocol
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return e.finish()
}
