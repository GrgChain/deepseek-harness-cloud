package modelgateway

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
)

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
