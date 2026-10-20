package webapi

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStrictJSONRejectsAmbiguousPayloads(t *testing.T) {
	for _, body := range []string{`{"version":1,"version":2}`, `{"unknown":1}`, `{"version":null}`, `{} {}`, `null`, `[]`} {
		t.Run(body, func(t *testing.T) {
			var input struct {
				Version int `json:"version"`
			}
			r := httptest.NewRequest("PUT", "/", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			if err := Decode(httptest.NewRecorder(), r, &input); err == nil {
				t.Fatal("ambiguous JSON accepted")
			}
		})
	}
	var input struct {
		Version int `json:"version"`
	}
	r := httptest.NewRequest("PUT", "/", strings.NewReader(`{"version":1}`))
	r.Header.Set("Content-Type", "application/json")
	if err := Decode(httptest.NewRecorder(), r, &input); err != nil || input.Version != 1 {
		t.Fatal("valid JSON rejected")
	}
}
