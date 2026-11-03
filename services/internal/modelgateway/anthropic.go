package modelgateway

import (
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
)

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
