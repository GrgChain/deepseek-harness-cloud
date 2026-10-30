package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// Verify the same persisted-session lookup and directory listing used by the
// browser before publishing a replacement runtime's access route.
func checkNativeWorkspace(ctx context.Context, endpoint, host string, cookie []byte, model ...string) bool {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.ResponseHeaderTimeout = 12 * time.Second
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	read := func(method string, args any, value any) bool {
		raw, err := json.Marshal(map[string]any{"type": "client-request", "rpcId": "harness-workspace-readiness", "method": method, "payload": map[string]any{"args": args}})
		if err != nil {
			return false
		}
		r, err := http.NewRequestWithContext(ctx, "POST", endpoint+"/api/"+method, bytes.NewReader(raw))
		if err != nil {
			return false
		}
		r.Host = host
		r.Header.Set("Cookie", string(cookie))
		r.Header.Set("Origin", "https://"+host)
		r.Header.Set("Content-Type", "application/json")
		response, err := client.Do(r)
		if err != nil {
			return false
		}
		defer response.Body.Close()
		if response.StatusCode != 200 || len(response.Header.Values("Set-Cookie")) != 0 {
			return false
		}
		raw, err = io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
		if err != nil || len(raw) > 1<<20 {
			return false
		}
		var envelope struct {
			Type   string `json:"type"`
			RPCID  string `json:"rpcId"`
			Result struct {
				OK    bool            `json:"ok"`
				Value json.RawMessage `json:"value"`
			} `json:"result"`
		}
		return json.Unmarshal(raw, &envelope) == nil && envelope.Type == "server-response" && envelope.RPCID == "harness-workspace-readiness" && envelope.Result.OK && json.Unmarshal(envelope.Result.Value, value) == nil
	}
	var sessions struct {
		Items []struct {
			ID  string `json:"sessionId"`
			CWD string `json:"cwd"`
		} `json:"items"`
	}
	if !read("session/list", map[string]any{"_request": map[string]any{}}, &sessions) {
		return false
	}
	if len(model) == 2 {
		for _, session := range sessions.Items {
			if session.ID == "" || session.CWD != "/data/workspace" {
				continue
			}
			var projection struct {
				Kind   string `json:"kind"`
				Values struct {
					Subagent       json.RawMessage `json:"subagent"`
					ModelSelection struct {
						Next *struct {
							Provider string `json:"provider"`
							Model    string `json:"model"`
						} `json:"next"`
					} `json:"modelSelection"`
				} `json:"values"`
			}
			if !read("session/projections", map[string]any{"request": map[string]string{"sessionId": session.ID}}, &projection) || projection.Kind != "sequenced" {
				return false
			}
			selection := projection.Values.ModelSelection.Next
			if (len(projection.Values.Subagent) > 0 && string(projection.Values.Subagent) != "null") || selection == nil || (selection.Provider == model[0] && selection.Model == model[1]) {
				continue
			}
			var result struct {
				Selected struct {
					Provider string `json:"provider"`
					Model    string `json:"model"`
				} `json:"selected"`
			}
			if !read("session/selectModel", map[string]any{"request": map[string]string{"sessionId": session.ID, "provider": model[0], "model": model[1]}}, &result) || result.Selected.Provider != model[0] || result.Selected.Model != model[1] {
				return false
			}
		}
	}
	for _, session := range sessions.Items {
		if session.ID == "" || session.CWD != "/data/workspace" {
			continue
		}
		var files struct {
			Path    *string            `json:"path"`
			Entries *[]json.RawMessage `json:"entries"`
		}
		if !read("workspaceFiles/list", map[string]string{"workspaceFileScopeId": session.ID, "path": "."}, &files) {
			return false
		}
		return files.Path != nil && *files.Path == "" && files.Entries != nil
	}
	return false
}
