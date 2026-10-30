package contextservice

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
)

func documentRequest(t *testing.T, filename string, content []byte) *http.Request {
	t.Helper()
	var data bytes.Buffer
	form := multipart.NewWriter(&data)
	part, err := form.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err = form.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/context/knowledge/parse", &data)
	r.Header.Set("Content-Type", form.FormDataContentType())
	return r
}

func TestDoclingLive(t *testing.T) {
	endpoint := os.Getenv("HC_TEST_DOCLING_URL")
	if endpoint == "" {
		t.Skip("requires a running Docling service")
	}
	t.Setenv("HC_DOCLING_URL", endpoint)
	h := NewHandler(nil, func(context.Context, string, int) error { return nil })
	recorder := httptest.NewRecorder()
	h.ServeAuthenticated(recorder, documentRequest(t, "sample.md", []byte("# 项目计划\n\n测试文档")), identity.Principal{Permissions: []string{"knowledge:manage"}})
	if recorder.Code != 200 || !strings.Contains(recorder.Body.String(), "项目计划") {
		t.Fatalf("live Docling parse failed: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestDocumentParsing(t *testing.T) {
	status, markdown := "success", "# 项目\n内容"
	upstreamCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		if r.URL.Path != "/v1/convert/file" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if err := r.ParseMultipartForm(maxDocumentBytes); err != nil {
			t.Fatal(err)
		}
		file, header, err := r.FormFile("files")
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		data, _ := io.ReadAll(file)
		if header.Filename != "test.docx" || string(data) != "sample" {
			t.Errorf("unexpected input: %s %q", header.Filename, data)
		}
		if r.FormValue("ocr_engine") != "rapidocr" || r.FormValue("ocr_lang") != "ch" {
			t.Error("Chinese OCR was not configured")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": status, "document": map[string]string{"md_content": markdown}})
	}))
	defer server.Close()
	t.Setenv("HC_DOCLING_URL", server.URL)
	h := NewHandler(nil, func(context.Context, string, int) error { return nil })
	principal := identity.Principal{Permissions: []string{"knowledge:manage"}}
	recorder := httptest.NewRecorder()
	h.ServeAuthenticated(recorder, documentRequest(t, "test.docx", []byte("sample")), principal)
	if recorder.Code != 200 || !strings.Contains(recorder.Body.String(), "项目") {
		t.Fatalf("parse failed: %d %s", recorder.Code, recorder.Body.String())
	}
	status = "partial_success"
	recorder = httptest.NewRecorder()
	h.ServeAuthenticated(recorder, documentRequest(t, "test.docx", []byte("sample")), principal)
	if recorder.Code != 422 {
		t.Fatalf("partial conversion accepted: %d", recorder.Code)
	}
	status = "success"
	markdown = strings.Repeat("x", (256<<10)+1)
	recorder = httptest.NewRecorder()
	h.ServeAuthenticated(recorder, documentRequest(t, "test.docx", []byte("sample")), principal)
	if recorder.Code != 422 {
		t.Fatalf("oversized output accepted: %d", recorder.Code)
	}
	for _, tc := range []struct {
		name      string
		content   []byte
		principal identity.Principal
		want      int
	}{
		{"test.docx", []byte("sample"), identity.Principal{Permissions: []string{"knowledge:read"}}, 403},
		{"test.exe", []byte("sample"), principal, 422},
		{"test.docx", nil, principal, 422},
		{"test.docx", bytes.Repeat([]byte("x"), maxDocumentBytes+1), principal, 413},
	} {
		t.Run(fmt.Sprintf("%s_%d_%d", tc.name, len(tc.content), tc.want), func(t *testing.T) {
			before := upstreamCalls
			recorder := httptest.NewRecorder()
			h.ServeAuthenticated(recorder, documentRequest(t, tc.name, tc.content), tc.principal)
			if recorder.Code != tc.want || upstreamCalls != before {
				t.Fatalf("got %d, upstream calls %d", recorder.Code, upstreamCalls-before)
			}
		})
	}
	t.Setenv("HC_DOCLING_URL", "http://127.0.0.1:1")
	recorder = httptest.NewRecorder()
	h.ServeAuthenticated(recorder, documentRequest(t, "test.docx", []byte("sample")), principal)
	if recorder.Code != 503 || !strings.Contains(recorder.Body.String(), "文档解析服务暂不可用") {
		t.Fatalf("unavailable parser: %d %s", recorder.Code, recorder.Body.String())
	}
}
