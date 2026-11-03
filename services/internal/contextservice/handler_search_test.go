package contextservice

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestDecodeMemorySearchConditions(t *testing.T) {
	for _, tc := range []struct {
		name, fields, source string
		limit                int
		minimum              *float64
	}{
		{name: "manual source", fields: `,"memory_source":"manual"`, source: "manual", limit: 5},
		{name: "manual zero threshold", fields: `,"memory_source":"manual","min_similarity":0`, source: "manual", limit: 10, minimum: floatPointer(0)},
		{name: "manual positive threshold", fields: `,"memory_source":"manual","min_similarity":0.75`, source: "manual", limit: 20, minimum: floatPointer(0.75)},
		{name: "automatic source", fields: `,"memory_source":"automatic"`, source: "automatic", limit: 5},
		{name: "default source omitted", limit: 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"expected_version":1,"query":"公司叫什么","limit":` + strconv.Itoa(tc.limit) + tc.fields + `}`
			r := httptest.NewRequest(http.MethodPost, "/v1/context/memory/connection/search", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			var input Action
			if err := decode(httptest.NewRecorder(), r, &input); err != nil {
				t.Fatalf("valid memory search request rejected at HTTP decoding: %v", err)
			}
			if input.ExpectedVersion != 1 || input.Query != "公司叫什么" || input.Limit != tc.limit || input.MemorySource != tc.source {
				t.Fatalf("search conditions changed during decoding: %+v", input)
			}
			if (input.MinSimilarity == nil) != (tc.minimum == nil) || tc.minimum != nil && *input.MinSimilarity != *tc.minimum {
				t.Fatalf("minimum similarity was not preserved: got %v, want %v", input.MinSimilarity, tc.minimum)
			}
			if err := validateAction("search", input); err != nil {
				t.Fatalf("decoded memory search conditions rejected: %v", err)
			}
		})
	}
}

func TestDecodeMemorySearchRejectsMalformedConditions(t *testing.T) {
	for _, tc := range []struct{ name, fields string }{
		{"source object", `,"memory_source":{"value":"manual"}`},
		{"source array", `,"memory_source":["manual"]`},
		{"source number", `,"memory_source":1`},
		{"source boolean", `,"memory_source":true`},
		{"source null", `,"memory_source":null`},
		{"threshold string", `,"min_similarity":"0.75"`},
		{"limit string", `,"limit":"10"`},
		{"unknown field", `,"memory_source":"manual","tenant_namespace":"other-user"`},
		{"duplicate source", `,"memory_source":"manual","memory_source":"automatic"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"expected_version":1,"query":"公司叫什么"` + tc.fields + `}`
			r := httptest.NewRequest(http.MethodPost, "/v1/context/memory/connection/search", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			var input Action
			if err := decode(httptest.NewRecorder(), r, &input); !problemIs(err, "CONTEXT_INVALID") {
				t.Fatalf("malformed memory search request was not rejected: %v", err)
			}
		})
	}
}

func floatPointer(value float64) *float64 { return &value }
