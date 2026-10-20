package settings

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

// Read-only management-route denial is required; a prefix does not establish
// that an input is a virtual key. No privileged response body is retained.
func checkRestrictedKey(ctx context.Context, client *http.Client, endpoint, key string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/key/list", nil)
	if err != nil {
		return "MODEL_KEY_PRIVILEGE_UNVERIFIED"
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := client.Do(req)
	if err != nil {
		return "MODEL_TRANSPORT_UNAVAILABLE"
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return ""
	}
	if resp.StatusCode == 200 {
		return "MODEL_KEY_PRIVILEGED"
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return "MODEL_REDIRECT_REJECTED"
	}
	return "MODEL_KEY_PRIVILEGE_UNVERIFIED"
}

type wireBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}
type wireMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}
type streamResult struct {
	model, stop string
	blocks      []wireBlock
}

var errMessages = errors.New("invalid Messages stream")

// Parse complete, bounded SSE events. Truncation, duplicated blocks, mismatched
// event types and missing final markers cannot produce a healthy result.
func readMessagesStream(reader io.Reader, manifest *ModelManifest) (streamResult, error) {
	var result streamResult
	scanner := bufio.NewScanner(io.LimitReader(reader, (64<<10)+1))
	scanner.Buffer(make([]byte, 4096), 64<<10)
	var event string
	var data []string
	bytesRead := 0
	started, ended := false, false
	open := -1
	partials := map[int]string{}
	process := func() error {
		if len(data) == 0 {
			return nil
		}
		if ended {
			return errMessages
		}
		var e struct {
			Type    string `json:"type"`
			Index   int    `json:"index"`
			Message struct {
				Type, Model string
				Usage       struct {
					Output int64 `json:"output_tokens"`
				} `json:"usage"`
			} `json:"message"`
			Block wireBlock `json:"content_block"`
			Delta struct {
				Type, Text, PartialJSON string
				StopReason              string `json:"stop_reason"`
			} `json:"delta"`
			Usage struct {
				Output int64 `json:"output_tokens"`
			} `json:"usage"`
		}
		// partial_json needs its exact wire spelling.
		raw := []byte(strings.Join(data, "\n"))
		if json.Unmarshal(raw, &e) != nil || e.Type != event {
			return errMessages
		}
		var delta struct {
			Delta struct {
				Partial string `json:"partial_json"`
			} `json:"delta"`
		}
		if json.Unmarshal(raw, &delta) != nil {
			return errMessages
		}
		switch e.Type {
		case "ping":
			return nil
		case "message_start":
			if started || e.Message.Type != "message" || manifest == nil || !manifest.MatchesResponse(e.Message.Model) || e.Message.Usage.Output > 128 {
				return errMessages
			}
			started = true
			result.model = e.Message.Model
		case "content_block_start":
			if !started || open != -1 || e.Index != len(result.blocks) || len(result.blocks) >= 8 || (e.Block.Type != "text" && e.Block.Type != "tool_use") {
				return errMessages
			}
			if e.Block.Type == "tool_use" && (len(e.Block.ID) == 0 || len(e.Block.ID) > 128 || e.Block.Name != "hc_probe") {
				return errMessages
			}
			result.blocks = append(result.blocks, e.Block)
			open = e.Index
		case "content_block_delta":
			if open < 0 || e.Index != open {
				return errMessages
			}
			block := &result.blocks[open]
			if e.Delta.Type == "text_delta" && block.Type == "text" {
				block.Text += e.Delta.Text
			} else if e.Delta.Type == "input_json_delta" && block.Type == "tool_use" {
				partials[open] += delta.Delta.Partial
			} else {
				return errMessages
			}
		case "content_block_stop":
			if open < 0 || e.Index != open {
				return errMessages
			}
			if raw := partials[open]; raw != "" {
				if !json.Valid([]byte(raw)) {
					return errMessages
				}
				result.blocks[open].Input = json.RawMessage(raw)
			}
			open = -1
		case "message_delta":
			if !started || open != -1 || result.stop != "" || e.Usage.Output > 128 || (e.Delta.StopReason != "tool_use" && e.Delta.StopReason != "end_turn") {
				return errMessages
			}
			result.stop = e.Delta.StopReason
		case "message_stop":
			if !started || open != -1 || result.stop == "" {
				return errMessages
			}
			ended = true
		default:
			return errMessages
		}
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		bytesRead += len(line) + 1
		if bytesRead > 64<<10 {
			return result, errMessages
		}
		if line == "" {
			if err := process(); err != nil {
				return result, err
			}
			event = ""
			data = nil
			continue
		}
		if strings.HasPrefix(line, "event:") {
			if event != "" {
				return result, errMessages
			}
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		} else if !strings.HasPrefix(line, ":") {
			return result, errMessages
		}
	}
	if scanner.Err() != nil || process() != nil || !ended || len(result.blocks) == 0 {
		return result, errMessages
	}
	return result, nil
}
func upstreamReason(status int) string {
	switch {
	case status == 401 || status == 403:
		return "MODEL_AUTH_REJECTED"
	case status >= 300 && status < 400:
		return "MODEL_REDIRECT_REJECTED"
	case status == 404:
		return "MODEL_ALIAS_UNAVAILABLE"
	case status == 429:
		return "MODEL_RATE_LIMITED"
	case status >= 500:
		return "MODEL_UPSTREAM_UNAVAILABLE"
	case status != 200:
		return "MODEL_REQUEST_REJECTED"
	}
	return ""
}
func gatewayProbe(ctx context.Context, client *http.Client, m Model, key string, reserve func() error) (string, []string) {
	if m.Manifest == nil || !m.Manifest.Streaming || !m.Manifest.ToolUse {
		return "MODEL_CAPABILITY_UNAVAILABLE", nil
	}
	if reason := checkRestrictedKey(ctx, client, m.Endpoint, key); reason != "" {
		return reason, nil
	}
	messages := []wireMessage{{"user", "Synthetic connectivity test. Call hc_probe with value exactly ok. After its result, reply with one word ok. Do not run other tools."}}
	models := []string{}
	for step := 0; step < 2; step++ {
		payload := map[string]any{"model": m.Model, "max_tokens": 128, "stream": true, "messages": messages, "tools": []any{map[string]any{"name": "hc_probe", "description": "Synthetic connectivity test with no side effects.", "input_schema": map[string]any{"type": "object", "properties": map[string]any{"value": map[string]string{"type": "string"}}, "required": []string{"value"}, "additionalProperties": false}}}}
		data, _ := json.Marshal(payload)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.Endpoint+"/v1/messages", bytes.NewReader(data))
		if err != nil {
			return "MODEL_PROTOCOL_INVALID", models
		}
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("anthropic-version", "2023-06-01")
		if reserve() != nil {
			return "PROBE_BUDGET_EXCEEDED", models
		}
		resp, err := client.Do(req)
		if err != nil {
			return "MODEL_TRANSPORT_UNAVAILABLE", models
		}
		if reason := upstreamReason(resp.StatusCode); reason != "" {
			resp.Body.Close()
			return reason, models
		}
		if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
			resp.Body.Close()
			return "MODEL_PROTOCOL_INVALID", models
		}
		result, err := readMessagesStream(resp.Body, m.Manifest)
		resp.Body.Close()
		if err != nil {
			return "MODEL_PROTOCOL_INVALID", models
		}
		models = append(models, result.model)
		if step == 0 {
			var tool *wireBlock
			for i := range result.blocks {
				b := &result.blocks[i]
				if b.Type == "tool_use" {
					if tool != nil {
						return "MODEL_TOOL_UNAVAILABLE", models
					}
					tool = b
				}
			}
			var input struct {
				Value string `json:"value"`
			}
			if result.stop != "tool_use" || tool == nil || json.Unmarshal(tool.Input, &input) != nil || input.Value != "ok" {
				return "MODEL_TOOL_UNAVAILABLE", models
			}
			messages = append(messages, wireMessage{"assistant", result.blocks}, wireMessage{"user", []any{map[string]any{"type": "tool_result", "tool_use_id": tool.ID, "content": "ok"}}})
		} else {
			text := false
			for _, block := range result.blocks {
				if block.Type == "tool_use" {
					return "MODEL_TOOL_UNAVAILABLE", models
				}
				text = text || strings.TrimSpace(block.Text) != ""
			}
			if result.stop != "end_turn" || !text {
				return "MODEL_TOOL_UNAVAILABLE", models
			}
		}
	}
	return "", models
}
