package plugins

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func TestPluginHandlerRejectsUntrustedWriteFields(t *testing.T) {
	h := NewHandler(nil, func(context.Context, string, int) error { return nil })
	p := identity.Principal{Permissions: []string{"instances:read", "plugins:manage"}}
	for _, tc := range []struct{ path, body string }{
		{"/v1/plugins:preview", `{"package_name":"test-plugin","owner_id":"other"}`},
		{"/v1/plugins:preview", `{"package_name":"test-plugin","package_name":"other-plugin"}`},
		{"/v1/plugins:preview", `{"package_name":null}`},
		{"/v1/plugins:preview", `{"package_name":42}`},
		{"/v1/plugins", `{"package_name":"test-plugin","version":"1.0.0","category":"开发工具","name":"forged"}`},
		{"/v1/plugins", `{"package_name":"test-plugin","version":"1.0.0","category":"开发工具","source":"official"}`},
		{"/v1/plugins", `{"package_name":"test-plugin","version":1,"category":"开发工具"}`},
		{"/v1/plugins:preview", `{"package_name":"test-plugin","filename":"","content_base64":""}`},
		{"/v1/plugins:preview", `{"package_name":"test-plugin","version":"1.0.0"}`},
		{"/v1/plugins:preview", `{"package_name":"test-plugin","category":"开发工具"}`},
		{"/v1/plugins:preview", `{"filename":"test.tgz"}`},
		{"/v1/plugins:preview", `{"content_base64":"YQ=="}`},
		{"/v1/plugins:preview", `{"filename":"test.tgz","content_base64":"!"}`},
		{"/v1/plugins:preview", `{"filename":"test.tgz","content_base64":""}`},
		{"/v1/plugins:preview", `{"filename":"test.tgz","content_base64":null}`},
		{"/v1/plugins:preview", `{"filename":"test.tgz","filename":"other.tgz","content_base64":"YQ=="}`},
		{"/v1/plugins:preview", `{"filename":"test.tgz","content_base64":"YQ==","version":"1.0.0"}`},
		{"/v1/plugins:preview", `{"filename":"test.tgz","content_base64":"YQ==","category":"开发工具"}`},
		{"/v1/plugins", `{"filename":"test.tgz","content_base64":"YQ=="}`},
		{"/v1/plugins", `{"package_name":"test-plugin","category":"开发工具"}`},
		{"/v1/plugins", `{"package_name":"","filename":"test.tgz","content_base64":"YQ==","category":"开发工具"}`},
		{"/v1/plugins", `{"filename":"test.tgz","content_base64":"YQ==","category":"开发工具","sha256":"forged"}`},
	} {
		r := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeAuthenticated(w, r, p)
		if w.Code != 422 {
			t.Errorf("%s %s: got %d", tc.path, tc.body, w.Code)
		}
	}
}

func TestPluginInputArchiveTransportBounds(t *testing.T) {
	for _, size := range []int{80 << 10, maxArchiveBytes, maxArchiveBytes + 1} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			data := []byte(strings.Repeat("x", size))
			body, err := json.Marshal(map[string]string{"filename": "test.tgz", "content_base64": base64.StdEncoding.EncodeToString(data)})
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/v1/plugins:preview", strings.NewReader(string(body)))
			r.Header.Set("Content-Type", "application/json")
			_, decoded, err := decodeInput(w, r, true)
			if size > maxArchiveBytes {
				if err == nil {
					t.Fatal("oversized original archive was accepted")
				}
				webapi.WriteError(w, r, err)
				if w.Code != 413 || !strings.Contains(w.Body.String(), "PLUGIN_TOO_LARGE") {
					t.Fatal("oversized archive error differs", w.Code, w.Body.String())
				}
				return
			}
			if err != nil || len(decoded) != size || string(decoded) != string(data) {
				t.Fatal("valid archive transport was truncated or rejected", len(decoded), err)
			}
		})
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/plugins:preview", strings.NewReader(strings.Repeat(" ", (8<<20)+1)))
	r.Header.Set("Content-Type", "application/json")
	_, _, err := decodeInput(w, r, true)
	if err == nil {
		t.Fatal("oversized JSON body was accepted")
	}
	webapi.WriteError(w, r, err)
	if w.Code != 413 || !strings.Contains(w.Body.String(), "PLUGIN_TOO_LARGE") {
		t.Fatal("JSON body limit error differs", w.Code, w.Body.String())
	}
}

func TestPluginHandlerSeparatesReadAndManagePermission(t *testing.T) {
	h := NewHandler(nil, nil)
	for _, path := range []string{"/v1/plugins", "/v1/plugins:preview"} {
		w := httptest.NewRecorder()
		h.ServeAuthenticated(w, httptest.NewRequest(http.MethodPost, path, nil), identity.Principal{Permissions: []string{"instances:read"}})
		if w.Code != 403 {
			t.Fatalf("read permission allowed plugin write: %d", w.Code)
		}
	}
	w := httptest.NewRecorder()
	h.ServeAuthenticated(w, httptest.NewRequest(http.MethodGet, "/v1/plugins", nil), identity.Principal{})
	if w.Code != 403 {
		t.Fatal("unprivileged list was accepted", w.Code)
	}
}
