package plugins

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func metadataEditFixture() EditInput {
	return EditInput{
		Name: "个人插件", Summary: "个人简介", Description: "详细说明\n第二行", Author: "个人发布方",
		Version: "1.2.3-rc.1+build.2", Category: "开发工具", Keywords: []string{"工具", "DSH"},
		Repository: "https://github.com/example/plugin", Documentation: "https://example.com/docs#usage",
		Compatibility: "DSH >=0.2.1", CheckedAt: "2026-10-27", SourceNote: "包资料及项目文档", ExpectedRevision: 1,
	}
}

func requireMetadataProblem(t *testing.T, err error, status int, code string) {
	t.Helper()
	var problem *webapi.Problem
	if !errors.As(err, &problem) || problem.Status != status || problem.Code != code {
		t.Fatalf("expected %d %s, got %v", status, code, err)
	}
}

func TestPluginEditInputValidation(t *testing.T) {
	input := metadataEditFixture()
	input.Name, input.Author = " 个人插件 \t", " 个人发布方 "
	if err := input.validate(); err != nil || input.Name != "个人插件" || input.Author != "个人发布方" {
		t.Fatal("complete metadata was rejected or not normalized", err)
	}
	input = metadataEditFixture()
	input.Summary, input.Description, input.Compatibility, input.SourceNote = "", "", "", ""
	input.Repository, input.Documentation, input.Keywords = "", "", []string{}
	if err := input.validate(); err != nil {
		t.Fatal("clearing optional metadata was rejected", err)
	}
	for _, category := range []string{"会话与记忆", "开发工具", "集成扩展"} {
		input = metadataEditFixture()
		input.Category = category
		if err := input.validate(); err != nil {
			t.Fatalf("valid category %s rejected: %v", category, err)
		}
	}
	for _, tc := range []struct {
		name   string
		change func(*EditInput)
	}{
		{"empty name", func(v *EditInput) { v.Name = " \t" }},
		{"empty author", func(v *EditInput) { v.Author = " \n" }},
		{"empty category", func(v *EditInput) { v.Category = "" }},
		{"unknown category", func(v *EditInput) { v.Category = "other" }},
		{"nil keywords", func(v *EditInput) { v.Keywords = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := metadataEditFixture()
			tc.change(&input)
			requireMetadataProblem(t, input.validate(), 422, "PLUGIN_INVALID")
		})
	}
}

func TestPluginMetadataDateValidation(t *testing.T) {
	for _, date := range []string{"2024-02-29", "2000-02-29", "0001-01-01", "9999-12-31"} {
		input := metadataEditFixture()
		input.CheckedAt = date
		if err := input.validate(); err != nil {
			t.Errorf("valid calendar date %s rejected: %v", date, err)
		}
	}
	for _, date := range []string{"", "0000-01-01", "2025-02-29", "2100-02-29", "2026-04-31", "2026-13-01", "2026-00-01", "2026-01-00", "2026-1-02", "2026-01-2", " 2026-01-02", "2026-01-02 ", "2026-01-02T00:00:00Z"} {
		t.Run(date, func(t *testing.T) {
			input := metadataEditFixture()
			input.CheckedAt = date
			requireMetadataProblem(t, input.validate(), 422, "PLUGIN_INVALID")
		})
	}
}

func TestPluginMetadataURLValidation(t *testing.T) {
	for _, field := range []struct {
		name  string
		value func(*EditInput) *string
	}{
		{"repository", func(v *EditInput) *string { return &v.Repository }},
		{"documentation", func(v *EditInput) *string { return &v.Documentation }},
	} {
		t.Run(field.name, func(t *testing.T) {
			for _, value := range []string{"", "https://example.com", "https://example.com:8443/docs?version=1#usage", "https://example.com/使用说明", "https://example.com/" + strings.Repeat("a", 2048-len("https://example.com/"))} {
				input := metadataEditFixture()
				*field.value(&input) = value
				if err := input.validate(); err != nil {
					t.Errorf("valid metadata URL rejected: %v", err)
				}
			}
			for _, value := range []string{"http://example.com", "//example.com/docs", "/docs", "javascript:alert(1)", "data:text/plain,hello", "https:example.com", "https://", "https://user:pass@example.com", "https://user@example.com", "https://example.com:invalid", " https://example.com", "https://example.com ", "https://example.com/\n", "https://example.com/" + strings.Repeat("a", 2049-len("https://example.com/")), "https://example.com/" + strings.Repeat("界", 700)} {
				input := metadataEditFixture()
				*field.value(&input) = value
				requireMetadataProblem(t, input.validate(), 422, "PLUGIN_INVALID")
			}
		})
	}
}

func TestPluginMetadataTextAndKeywordLimits(t *testing.T) {
	for _, field := range []struct {
		name      string
		value     func(*EditInput) *string
		limit     int
		multiline bool
	}{
		{"name", func(v *EditInput) *string { return &v.Name }, 214, false},
		{"author", func(v *EditInput) *string { return &v.Author }, 200, false},
		{"summary", func(v *EditInput) *string { return &v.Summary }, 1000, true},
		{"description", func(v *EditInput) *string { return &v.Description }, 4000, true},
		{"compatibility", func(v *EditInput) *string { return &v.Compatibility }, 2000, true},
		{"source note", func(v *EditInput) *string { return &v.SourceNote }, 4000, true},
	} {
		t.Run(field.name, func(t *testing.T) {
			input := metadataEditFixture()
			*field.value(&input) = strings.Repeat("字", field.limit)
			if err := input.validate(); err != nil {
				t.Fatal("exact character limit rejected", err)
			}
			for _, value := range []string{strings.Repeat("字", field.limit+1), "before\x00after", "before\x7fafter", string([]byte{'x', 0xff})} {
				input = metadataEditFixture()
				*field.value(&input) = value
				requireMetadataProblem(t, input.validate(), 422, "PLUGIN_INVALID")
			}
			input = metadataEditFixture()
			*field.value(&input) = "first\nsecond\tthird\rfinal"
			if field.multiline {
				if err := input.validate(); err != nil {
					t.Fatal("multiline display text was rejected", err)
				}
			} else {
				requireMetadataProblem(t, input.validate(), 422, "PLUGIN_INVALID")
			}
		})
	}
	input := metadataEditFixture()
	input.Keywords = make([]string, 20)
	for i := range input.Keywords {
		input.Keywords[i] = strings.Repeat("字", 60)
	}
	if err := input.validate(); err != nil {
		t.Fatal("exact keyword count and length rejected", err)
	}
	for _, keywords := range [][]string{nil, {""}, {" \t "}, {strings.Repeat("字", 61)}, {"first\nsecond"}, {"before\x00after"}, {string([]byte{0xff})}, append(append([]string{}, input.Keywords...), "extra")} {
		input = metadataEditFixture()
		input.Keywords = keywords
		requireMetadataProblem(t, input.validate(), 422, "PLUGIN_INVALID")
	}
}

func TestPluginEditRevisionAndVersionValidation(t *testing.T) {
	for _, revision := range []int64{1, 2, math.MaxInt64 - 1} {
		input := metadataEditFixture()
		input.ExpectedRevision = revision
		if err := input.validate(); err != nil {
			t.Errorf("valid revision %d rejected: %v", revision, err)
		}
	}
	for _, revision := range []int64{-1, 0, math.MaxInt64} {
		input := metadataEditFixture()
		input.ExpectedRevision = revision
		requireMetadataProblem(t, input.validate(), 422, "PLUGIN_INVALID")
	}
	for _, version := range []string{"0.0.0", "1.2.3", "1.2.3-rc.0", "1.2.3-0.alpha+build.01", "1.2.3-" + strings.Repeat("a", 122)} {
		input := metadataEditFixture()
		input.Version = version
		if err := input.validate(); err != nil {
			t.Errorf("fixed semver %s rejected: %v", version, err)
		}
	}
	for _, version := range []string{"", "latest", "^1.2.3", ">=1.2.3", "1.2", "v1.2.3", "01.2.3", "1.02.3", "1.2.03", "1.2.3-01", "1.2.3-rc.01", "1.2.3-", "1.2.3+", "1.2.3-rc..1", "1.2.3-预览", " 1.2.3", "1.2.3 ", "1.2.3-" + strings.Repeat("a", 123)} {
		input := metadataEditFixture()
		input.Version = version
		requireMetadataProblem(t, input.validate(), 422, "PLUGIN_INVALID")
	}
}

func TestPluginEditInputDecodeRequiresCompleteStrictObject(t *testing.T) {
	want := metadataEditFixture()
	valid, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	decode := func(body []byte, contentType string) (EditInput, error) {
		t.Helper()
		r := httptest.NewRequest(http.MethodPatch, "/v1/plugins/example", strings.NewReader(string(body)))
		r.Header.Set("Content-Type", contentType)
		return decodeEditInput(httptest.NewRecorder(), r)
	}
	for _, contentType := range []string{"application/json", "application/json; charset=utf-8"} {
		got, err := decode(valid, contentType)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("complete edit input changed during decode", got, err)
		}
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(valid, &fields); err != nil || len(fields) != 13 {
		t.Fatal("test fixture must contain every edit field", err)
	}
	for name, value := range fields {
		for _, kind := range []string{"missing", "null", "wrong case", "wrong type", "duplicate"} {
			t.Run(name+"/"+kind, func(t *testing.T) {
				mutated := make(map[string]json.RawMessage, len(fields))
				for key, data := range fields {
					mutated[key] = data
				}
				switch kind {
				case "missing":
					delete(mutated, name)
				case "null":
					mutated[name] = json.RawMessage("null")
				case "wrong case":
					delete(mutated, name)
					mutated[strings.ToUpper(name)] = value
				case "wrong type":
					mutated[name] = json.RawMessage("42")
					if name == "expected_revision" {
						mutated[name] = json.RawMessage(`"1"`)
					}
				}
				body, err := json.Marshal(mutated)
				if err != nil {
					t.Fatal(err)
				}
				if kind == "duplicate" {
					key, _ := json.Marshal(name)
					body = []byte(strings.TrimSuffix(string(body), "}") + "," + string(key) + ":" + string(value) + "}")
				}
				_, err = decode(body, "application/json")
				requireMetadataProblem(t, err, 422, "PLUGIN_INVALID")
			})
		}
	}
	for _, name := range []string{"unknown", "id", "owner_id", "org_id", "package_name", "default_id", "revision", "source", "custom", "addition_method", "filename", "sha256", "archive_key_id", "archive_nonce", "archive_ciphertext", "content_base64"} {
		t.Run("forbidden/"+name, func(t *testing.T) {
			body := []byte(strings.TrimSuffix(string(valid), "}") + `,"` + name + `":"forged"}`)
			_, err := decode(body, "application/json")
			requireMetadataProblem(t, err, 422, "PLUGIN_INVALID")
		})
	}
	for _, body := range []string{"", "null", "[]", `"text"`, "42", "{}", string(valid) + "{}", string(valid) + "false", strings.Replace(string(valid), `"expected_revision":1`, `"expected_revision":1.5`, 1), strings.Replace(string(valid), `"expected_revision":1`, `"expected_revision":9223372036854775808`, 1), strings.Replace(string(valid), `"keywords":["工具","DSH"]`, `"keywords":["工具",null]`, 1)} {
		_, err := decode([]byte(body), "application/json")
		requireMetadataProblem(t, err, 422, "PLUGIN_INVALID")
	}
	for _, contentType := range []string{"", "text/plain", "application/problem+json", "application/json; broken"} {
		_, err := decode(valid, contentType)
		requireMetadataProblem(t, err, 422, "PLUGIN_INVALID")
	}
}

func TestPluginEditInputRequestSizeBound(t *testing.T) {
	valid, err := json.Marshal(metadataEditFixture())
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{64 << 10, (64 << 10) + 1} {
		body := string(valid) + strings.Repeat(" ", size-len(valid))
		r := httptest.NewRequest(http.MethodPatch, "/v1/plugins/example", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		_, err := decodeEditInput(httptest.NewRecorder(), r)
		if size == 64<<10 {
			if err != nil {
				t.Fatal("exact 64 KiB request was rejected", err)
			}
		} else {
			requireMetadataProblem(t, err, 413, "PLUGIN_TOO_LARGE")
		}
	}
}
