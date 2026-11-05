package contextservice

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMemoryArchivePreviewPreservesNullableGraphMetadata(t *testing.T) {
	archive := `{"schema":"hc-memory-v1","backend":"graphiti","entries":[{"id":"release","graph":{"edges":[{"expired_at":null,"invalid_at":null,"valid_at":"2026-11-04T00:00:00Z"}],"nodes":[{"name_embedding":null}],"sequence":9007199254740993,"weight":1.0}}]}`
	r := httptest.NewRequest(http.MethodPost, "/v1/context/memory/connection/preview", strings.NewReader(`{"expected_version":1,"package":`+archive+`}`))
	r.Header.Set("Content-Type", "application/json")
	var input Action
	if err := decode(httptest.NewRecorder(), r, &input); err != nil {
		t.Fatal("valid Graphiti archive was rejected before archive validation", err)
	}
	if !bytes.Equal(input.Package, []byte(archive)) {
		t.Fatal("archive bytes changed; nulls and numeric literals must retain their checksum")
	}
}

func TestMemoryArchivePreviewRetainsStrictEnvelopeValidation(t *testing.T) {
	for _, body := range []string{
		`{"expected_version":null,"package":{"graph":null}}`,
		`{"expected_version":1,"package":null}`,
		`{"expected_version":1,"session_namespace":"` + strings.Repeat("a", 64) + `","package":{"graph":null}}`,
		`{"expected_version":1,"package":{"graph":{"invalid_at":null,"invalid_at":"2026-11-04"}}}`,
		`{"expected_version":1,"package":{"graph":null},"package":{}}`,
	} {
		t.Run(body, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/v1/context/memory/connection/preview", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			var input Action
			if err := decode(httptest.NewRecorder(), r, &input); !problemIs(err, "CONTEXT_INVALID") {
				t.Fatal("malformed archive envelope accepted", err)
			}
		})
	}
	for _, path := range []string{"/v1/context/memory/connection/apply", "/v1/context/knowledge/connection/preview"} {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"expected_version":1,"package":{"graph":null}}`))
		r.Header.Set("Content-Type", "application/json")
		var input Action
		if err := decode(httptest.NewRecorder(), r, &input); !problemIs(err, "CONTEXT_INVALID") {
			t.Fatal("nullable archive allowance leaked to another action", path, err)
		}
	}
}

func TestMemoryArchiveInputLimitsAndTenantBoundary(t *testing.T) {
	large := `{"entries":["` + strings.Repeat("x", 3<<20) + `"]}`
	request := func(path, body string) error {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		var input Action
		return decode(httptest.NewRecorder(), r, &input)
	}
	if err := request("/v1/context/memory/connection/preview", `{"expected_version":1,"package":`+large+`}`); err != nil {
		t.Fatal("complete memory package still has the old request limit", err)
	}
	if err := request("/v1/context/knowledge/connection/preview", `{"expected_version":1,"package":`+large+`}`); !problemIs(err, "PAYLOAD_TOO_LARGE") {
		t.Fatal("archive allowance leaked to knowledge requests", err)
	}
	if err := validateAction("preview", Action{ExpectedVersion: 1, Package: json.RawMessage(large)}); err != nil {
		t.Fatal("complete archive preview rejected", err)
	}
	if err := validateAction("preview", Action{ExpectedVersion: 1, Package: json.RawMessage(strings.Repeat("x", (16<<20)+1))}); !problemIs(err, "CONTEXT_INVALID") {
		t.Fatal("oversized archive accepted", err)
	}
	if err := request("/v1/context/memory/connection/preview", `{"expected_version":1,"session_namespace":"`+strings.Repeat("a", 64)+`"}`); !problemIs(err, "CONTEXT_INVALID") {
		t.Fatal("browser selected the native memory tenant", err)
	}
}
