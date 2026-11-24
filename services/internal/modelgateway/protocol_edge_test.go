package modelgateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeAcceptsJSONWithCharset(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"m"}`))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	var payload struct {
		Model string `json:"model"`
	}
	if err := decode(httptest.NewRecorder(), req, &payload); err != nil {
		t.Fatalf("decode rejected valid charset header: %v", err)
	}
	if payload.Model != "m" {
		t.Fatalf("got model %q, want m", payload.Model)
	}
}

func TestReadSSEIgnoresStandardIDAndRetryFields(t *testing.T) {
	stream := strings.Join([]string{
		": keepalive",
		"id: evt-1",
		"retry: 3000",
		"event: content_block_delta",
		`data: {"text":"ok"}`,
		"",
		"",
	}, "\n")
	var gotKind, gotData string
	err := readSSE(strings.NewReader(stream), func(kind, data string) error {
		gotKind, gotData = kind, data
		return nil
	})
	if err != nil {
		t.Fatalf("readSSE failed on standard id/retry fields: %v", err)
	}
	if gotKind != "content_block_delta" || gotData != `{"text":"ok"}` {
		t.Fatalf("unexpected SSE event: kind=%q data=%q", gotKind, gotData)
	}
}
