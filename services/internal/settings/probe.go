package settings

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

type MessagesProbe struct {
	client *http.Client
	policy *Policy
}

func (p *MessagesProbe) Check(ctx context.Context, m Model, key string) (string, bool) {
	if m.Provider == "platform-models" {
		client := p.client
		if p.policy != nil {
			gateway, ok := p.policy.Gateway(m.Endpoint)
			if !ok {
				return "MODEL_NOT_APPROVED", false
			}
			client = gateway.HTTPClient()
		}
		reason, _ := gatewayProbe(ctx, client, m, key, func() error { return nil })
		return reason, false
	}
	root := strings.TrimRight(m.Endpoint, "/")
	if !strings.HasSuffix(root, "/v1") {
		root += "/v1"
	}
	data, _ := json.Marshal(map[string]any{"model": m.Model, "max_tokens": 128, "stream": false, "thinking": map[string]string{"type": "disabled"}, "messages": []any{map[string]any{"role": "user", "content": "This is a synthetic connectivity test. Call hc_probe with value exactly ok. Do not run any other tools."}}, "tools": []any{map[string]any{"name": "hc_probe", "description": "Synthetic validation only; this tool has no side effects.", "input_schema": map[string]any{"type": "object", "properties": map[string]any{"value": map[string]string{"type": "string"}}, "required": []string{"value"}, "additionalProperties": false}}}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, root+"/messages", bytes.NewReader(data))
	if err != nil {
		return "MODEL_PROTOCOL_INVALID", false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("x-api-key", key)
	resp, err := p.client.Do(req)
	if err != nil {
		return "MODEL_TRANSPORT_UNAVAILABLE", true
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return "MODEL_AUTH_REJECTED", false
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return "MODEL_REDIRECT_REJECTED", false
	case resp.StatusCode == 429:
		return "MODEL_RATE_LIMITED", true
	case resp.StatusCode >= 500:
		return "MODEL_UPSTREAM_UNAVAILABLE", true
	case resp.StatusCode != 200:
		return "MODEL_REQUEST_REJECTED", false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (64<<10)+1))
	if err != nil || len(body) > 64<<10 {
		return "MODEL_PROTOCOL_INVALID", false
	}
	var result struct {
		Type       string `json:"type"`
		Model      string `json:"model"`
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type, ID, Name string
			Input          struct {
				Value string `json:"value"`
			} `json:"input"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &result); err != nil || result.Type != "message" || result.Model != m.Model || len(result.Content) == 0 {
		return "MODEL_PROTOCOL_INVALID", false
	}
	for _, block := range result.Content {
		if result.StopReason == "tool_use" && block.Type == "tool_use" && block.ID != "" && block.Name == "hc_probe" && block.Input.Value == "ok" {
			return "", false
		}
	}
	return "MODEL_TOOL_UNAVAILABLE", false
}
