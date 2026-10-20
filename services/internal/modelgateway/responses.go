package modelgateway

import (
	"encoding/json"
	"io"
	"net/http"
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

type responseOutputItem struct {
	id, kind, callID, name string
	text, arguments        strings.Builder
	contentParts           map[int]*strings.Builder
	contentPartsDone       map[int]bool
	textSent               int
	argumentsSeen          bool
	done                   bool
}

func responseString(value any) (string, bool) {
	text, ok := value.(string)
	return text, ok
}

func responseIndex(value any) (int, bool) {
	n, ok := value.(float64)
	if !ok || n < 0 || n > 1024 || n != float64(int(n)) {
		return 0, false
	}
	return int(n), true
}

func responseText(item map[string]any) (string, error) {
	if item["type"] != "message" || item["role"] != "assistant" {
		return "", errProtocol
	}
	content, ok := item["content"].([]any)
	if !ok {
		return "", errProtocol
	}
	var text strings.Builder
	for _, raw := range content {
		part, ok := raw.(map[string]any)
		if !ok || part["type"] != "output_text" {
			return "", errProtocol
		}
		value, ok := responseString(part["text"])
		if !ok || text.Len()+len(value) > 16<<20 {
			return "", errProtocol
		}
		if annotations, exists := part["annotations"]; exists {
			items, ok := annotations.([]any)
			if !ok || len(items) > 0 {
				return "", errProtocol
			}
		}
		text.WriteString(value)
	}
	return text.String(), nil
}

func responseArguments(item map[string]any) (string, error) {
	if item["type"] != "function_call" {
		return "", errProtocol
	}
	arguments, ok := responseString(item["arguments"])
	callID, callOK := responseString(item["call_id"])
	name, nameOK := responseString(item["name"])
	if !ok || !callOK || callID == "" || !nameOK || name == "" {
		return "", errProtocol
	}
	return arguments, nil
}

func responseUsage(response map[string]any, e *events) error {
	raw, exists := response["usage"]
	if !exists || raw == nil {
		return nil
	}
	usage, ok := raw.(map[string]any)
	if !ok {
		return errProtocol
	}
	for field, destination := range map[string]*int64{"input_tokens": &e.input, "output_tokens": &e.output} {
		value, exists := usage[field]
		if !exists {
			return errProtocol
		}
		n, ok := value.(float64)
		if !ok || n < 0 || n > 1<<53 || n != float64(int64(n)) || (field == "output_tokens" && n > float64(e.route.MaxOutputTokens)) {
			return errProtocol
		}
		*destination = int64(n)
	}
	return nil
}

func responsesStream(w http.ResponseWriter, reader io.Reader, route Route, state *responseState) error {
	e := events{w: w, route: route}
	items := map[int]*responseOutputItem{}
	itemIndexes := map[string]int{}
	callIDs := map[string]bool{}
	cursor := 0
	responseID, responseModel := "", ""
	terminal, doneMarker := false, false
	var streamedText strings.Builder
	reasoningByCall := map[string][]json.RawMessage{}

	flush := func() error {
		for {
			item := items[cursor]
			if item == nil {
				return nil
			}
			if item.kind == "message" {
				if item.textSent < item.text.Len() {
					value := item.text.String()[item.textSent:]
					if err := e.text(value); err != nil {
						return err
					}
					item.textSent = item.text.Len()
				}
				if !item.done {
					return nil
				}
			} else if item.kind == "function_call" {
				if !item.done {
					return nil
				}
				if err := e.tool(item.callID, item.name, item.arguments.String()); err != nil {
					return err
				}
			} else if item.kind == "reasoning" {
				if !item.done {
					return nil
				}
			} else {
				return errProtocol
			}
			cursor++
		}
	}

	setResponse := func(response map[string]any) error {
		id, idOK := responseString(response["id"])
		model, modelOK := responseString(response["model"])
		if !idOK || id == "" || !modelOK || model == "" {
			return errProtocol
		}
		if responseID != "" && responseID != id || responseModel != "" && responseModel != model {
			return errProtocol
		}
		responseID, responseModel = id, model
		return e.start(model)
	}

	finalizeOutput := func(response map[string]any) error {
		output, ok := response["output"].([]any)
		if !ok || len(output) > 1024 || len(items) != len(output) {
			return errProtocol
		}
		var finalText strings.Builder
		var pendingReasoning []json.RawMessage
		reasoningBytes := 0
		toolCount := 0
		for index, raw := range output {
			final, ok := raw.(map[string]any)
			item := items[index]
			if !ok || item == nil {
				return errProtocol
			}
			id, idOK := responseString(final["id"])
			kind, kindOK := responseString(final["type"])
			if !idOK || !kindOK || id != item.id || kind != item.kind {
				return errProtocol
			}
			switch kind {
			case "message":
				value, err := responseText(final)
				if err != nil || (item.text.Len() > 0 && item.text.String() != value) {
					return errProtocol
				}
				if item.text.Len() == 0 {
					item.text.WriteString(value)
				}
				finalText.WriteString(value)
			case "function_call":
				args, err := responseArguments(final)
				callID, _ := responseString(final["call_id"])
				name, _ := responseString(final["name"])
				if err != nil || item.callID != callID || item.name != name || (item.argumentsSeen && item.arguments.String() != args) {
					return errProtocol
				}
				if !item.argumentsSeen {
					item.arguments.WriteString(args)
				}
				if len(pendingReasoning) > 0 {
					for _, raw := range pendingReasoning {
						var reasoning struct {
							EncryptedContent string `json:"encrypted_content"`
						}
						if json.Unmarshal(raw, &reasoning) != nil || reasoning.EncryptedContent == "" {
							return errProtocol
						}
					}
					reasoningByCall[item.callID] = append([]json.RawMessage(nil), pendingReasoning...)
					pendingReasoning = nil
				}
				item.done = true
				toolCount++
			case "reasoning":
				encryptedContent, encryptedOK := responseString(final["encrypted_content"])
				if !encryptedOK || encryptedContent == "" {
					return errProtocol
				}
				encoded, err := json.Marshal(final)
				if err != nil || len(encoded) == 0 || reasoningBytes+len(encoded) > 1<<20 {
					clear(encoded)
					return errProtocol
				}
				reasoningBytes += len(encoded)
				pendingReasoning = append(pendingReasoning, json.RawMessage(encoded))
			default:
				return errProtocol
			}
			item.done = true
		}
		if finalText.String() != streamedText.String() {
			return errProtocol
		}
		if err := flush(); err != nil || cursor != len(output) {
			return errProtocol
		}
		if toolCount > 0 {
			e.stop = "tool_use"
		} else {
			e.stop = "end_turn"
		}
		return nil
	}

	err := readSSE(reader, func(kind, data string) error {
		if terminal {
			if data == "[DONE]" && !doneMarker {
				doneMarker = true
				return nil
			}
			return errProtocol
		}
		if data == "[DONE]" {
			return errProtocol
		}
		var value map[string]any
		if kind == "" || json.Unmarshal([]byte(data), &value) != nil || value["type"] != kind {
			return errProtocol
		}
		switch kind {
		case "response.created", "response.in_progress":
			response, ok := value["response"].(map[string]any)
			if !ok {
				return errProtocol
			}
			return setResponse(response)
		case "response.metadata":
			// Some compatible providers send out-of-band metadata before completion.
			// Validate its scope and discard it without changing output or completion.
			id, idOK := responseString(value["response_id"])
			_, metadataOK := value["metadata"].(map[string]any)
			if !e.started || !idOK || id != responseID || !metadataOK {
				return errProtocol
			}
			return nil
		case "response.output_item.added":
			if !e.started {
				return errProtocol
			}
			index, ok := responseIndex(value["output_index"])
			item, itemOK := value["item"].(map[string]any)
			if !ok || !itemOK || items[index] != nil || index != len(items) || len(items) >= 1024 {
				return errProtocol
			}
			id, idOK := responseString(item["id"])
			kind, kindOK := responseString(item["type"])
			if !idOK || id == "" || !kindOK || itemIndexes[id] != 0 {
				return errProtocol
			}
			state := &responseOutputItem{id: id, kind: kind}
			switch kind {
			case "message":
				if item["role"] != "assistant" {
					return errProtocol
				}
				state.contentParts = map[int]*strings.Builder{}
				state.contentPartsDone = map[int]bool{}
			case "function_call":
				state.callID, _ = responseString(item["call_id"])
				state.name, _ = responseString(item["name"])
				initial, ok := responseString(item["arguments"])
				if !ok || state.callID == "" || state.name == "" || callIDs[state.callID] || initial != "" {
					return errProtocol
				}
				callIDs[state.callID] = true
			case "reasoning":
			default:
				return errProtocol
			}
			items[index] = state
			itemIndexes[id] = index + 1
			return flush()
		case "response.output_text.delta":
			index, ok := responseIndex(value["output_index"])
			contentIndex, contentOK := responseIndex(value["content_index"])
			itemID, idOK := responseString(value["item_id"])
			delta, deltaOK := responseString(value["delta"])
			item := items[index]
			part := (*strings.Builder)(nil)
			if item != nil {
				part = item.contentParts[contentIndex]
			}
			if !ok || !contentOK || !idOK || !deltaOK || item == nil || part == nil || item.contentPartsDone[contentIndex] || item.id != itemID || item.kind != "message" || item.done || part.Len()+len(delta) > 16<<20 || item.text.Len()+len(delta) > 16<<20 || streamedText.Len()+len(delta) > 16<<20 {
				return errProtocol
			}
			part.WriteString(delta)
			item.text.WriteString(delta)
			streamedText.WriteString(delta)
			return flush()
		case "response.output_text.done":
			index, ok := responseIndex(value["output_index"])
			contentIndex, contentOK := responseIndex(value["content_index"])
			itemID, idOK := responseString(value["item_id"])
			text, textOK := responseString(value["text"])
			item := items[index]
			part := (*strings.Builder)(nil)
			if item != nil {
				part = item.contentParts[contentIndex]
			}
			if !ok || !contentOK || !idOK || !textOK || item == nil || part == nil || part.String() != text || item.contentPartsDone[contentIndex] || item.id != itemID || item.kind != "message" {
				return errProtocol
			}
			item.contentPartsDone[contentIndex] = true
			return nil
		case "response.function_call_arguments.delta":
			itemID, ok := responseString(value["item_id"])
			delta, deltaOK := responseString(value["delta"])
			indexPlusOne := itemIndexes[itemID]
			var item *responseOutputItem
			if indexPlusOne > 0 {
				item = items[indexPlusOne-1]
			}
			if !ok || !deltaOK || item == nil || item.kind != "function_call" || item.done || item.arguments.Len()+len(delta) > 1<<20 {
				return errProtocol
			}
			item.arguments.WriteString(delta)
			item.argumentsSeen = true
			return nil
		case "response.function_call_arguments.done":
			itemID, ok := responseString(value["item_id"])
			arguments, argumentsOK := responseString(value["arguments"])
			indexPlusOne := itemIndexes[itemID]
			var item *responseOutputItem
			if indexPlusOne > 0 {
				item = items[indexPlusOne-1]
			}
			if !ok || !argumentsOK || item == nil || item.kind != "function_call" || item.done || (item.argumentsSeen && item.arguments.String() != arguments) {
				return errProtocol
			}
			if !item.argumentsSeen {
				item.arguments.WriteString(arguments)
				item.argumentsSeen = true
			}
			return nil
		case "response.output_item.done":
			index, ok := responseIndex(value["output_index"])
			final, finalOK := value["item"].(map[string]any)
			item := items[index]
			id, idOK := responseString(final["id"])
			kind, kindOK := responseString(final["type"])
			if !ok || !finalOK || item == nil || !idOK || !kindOK || item.id != id || item.kind != kind || item.done {
				return errProtocol
			}
			if kind == "message" {
				text, err := responseText(final)
				if err != nil || item.text.Len() > 0 && item.text.String() != text {
					return errProtocol
				}
				for contentIndex := range item.contentParts {
					if !item.contentPartsDone[contentIndex] {
						return errProtocol
					}
				}
				if item.text.Len() == 0 {
					item.text.WriteString(text)
				}
			} else if kind == "function_call" {
				arguments, err := responseArguments(final)
				callID, _ := responseString(final["call_id"])
				name, _ := responseString(final["name"])
				if err != nil || item.callID != callID || item.name != name || item.argumentsSeen && item.arguments.String() != arguments {
					return errProtocol
				}
				if !item.argumentsSeen {
					item.arguments.WriteString(arguments)
					item.argumentsSeen = true
				}
			} else if kind != "reasoning" {
				return errProtocol
			}
			item.done = true
			return flush()
		case "response.content_part.added", "response.content_part.done":
			index, indexOK := responseIndex(value["output_index"])
			contentIndex, contentOK := responseIndex(value["content_index"])
			itemID, idOK := responseString(value["item_id"])
			part, partOK := value["part"].(map[string]any)
			item := items[index]
			if !indexOK || !contentOK || !idOK || !partOK || item == nil || item.kind != "message" || item.id != itemID || part["type"] != "output_text" {
				return errProtocol
			}
			if annotations, exists := part["annotations"]; exists {
				items, ok := annotations.([]any)
				if !ok || len(items) > 0 {
					return errProtocol
				}
			}
			if kind == "response.content_part.added" {
				if contentIndex != len(item.contentParts) || item.contentParts[contentIndex] != nil {
					return errProtocol
				}
				item.contentParts[contentIndex] = &strings.Builder{}
				return nil
			}
			if item.contentParts[contentIndex] == nil || !item.contentPartsDone[contentIndex] {
				return errProtocol
			}
			return nil
		case "response.completed", "response.incomplete":
			response, ok := value["response"].(map[string]any)
			if !ok || setResponse(response) != nil {
				return errProtocol
			}
			status, _ := responseString(response["status"])
			if kind == "response.completed" && status != "completed" || kind == "response.incomplete" && status != "incomplete" {
				return errProtocol
			}
			if err := responseUsage(response, &e); err != nil {
				return err
			}
			if kind == "response.incomplete" {
				details, _ := response["incomplete_details"].(map[string]any)
				reason, _ := responseString(details["reason"])
				if reason != "max_output_tokens" {
					return errProtocol
				}
			}
			if err := finalizeOutput(response); err != nil {
				return err
			}
			if kind == "response.incomplete" {
				if len(callIDs) > 0 {
					return errProtocol
				}
				e.stop = "max_tokens"
			}
			terminal = true
			return nil
		case "error":
			return errProtocol
		default:
			return errProtocol
		}
	})
	if err != nil || !terminal || !e.started {
		return errProtocol
	}
	if err := e.finish(); err != nil {
		return errProtocol
	}
	if state != nil {
		state.ReasoningByCall = reasoningByCall
	}
	return nil
}
