package contextservice

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
