package webapi

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStrictJSONNullableArchiveObject(t *testing.T) {
	archive := `{"entries":[{"graph":{"invalid_at":null,"properties":[null,{"weight":1.0,"id":9007199254740993}]}}]}`
	var input struct {
		Version int             `json:"version"`
		Package json.RawMessage `json:"package"`
	}
	body := []byte(`{"version":1,"package":` + archive + `}`)
	if err := StrictUnmarshalWithNullableObject(body, &input, "package"); err != nil || string(input.Package) != archive {
		t.Fatal("archive metadata was rejected or changed", err)
	}
	if err := StrictUnmarshal(body, &input); err == nil {
		t.Fatal("ordinary strict decoding accepted nullable metadata")
	}
	for _, body := range []string{
		`{"version":null,"package":{"graph":null}}`,
		`{"version":1,"package":null}`,
		`{"version":1,"package":[]}`,
		`{"version":1,"package":"archive"}`,
		`{"version":1,"package":{"graph":{"id":1,"id":2}}}`,
		`{"version":1,"package":{"graph":null},"package":{}}`,
		`{"version":1,"package":{"graph":null},"unknown":1}`,
		`{"version":1,"package":{"graph":null}} {}`,
		`{"version":1,"package":{"graph":` + strings.Repeat("[", 33) + "null" + strings.Repeat("]", 33) + `}}`,
	} {
		t.Run(body, func(t *testing.T) {
			if err := StrictUnmarshalWithNullableObject([]byte(body), &input, "package"); err == nil {
				t.Fatal("malformed request accepted")
			}
		})
	}
	var object map[string]any
	if err := StrictUnmarshal([]byte(`{"":{"graph":null}}`), &object); err == nil {
		t.Fatal("default decoder accepted nulls below an empty key")
	}
}

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
