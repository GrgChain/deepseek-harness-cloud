package access

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedModelsLeavesUnknownModulesAndResponsesIntact(t *testing.T) {
	original := []byte("window.__ModuleLoader__.load({\n\tid: \"@deepseek-ai/dsh-client-ui-settings-models\",\n// different upstream release")
	patched, changed := adaptManagedModels(original, "https://portal.harness.test", "instance")
	if changed || !bytes.Equal(original, patched) {
		t.Fatal("unknown upstream module was modified")
	}
	for _, tc := range []struct {
		method, path, encoding string
		status                 int
	}{
		{"GET", "/plugins/??@deepseek-ai/dsh-client-ui-settings-models/client.js", "", 200},
		{"GET", "/plugins/??@deepseek-ai/other/client.js", "", 200},
		{"GET", "/project/file.js", "", 200},
		{"POST", "/plugins/??@deepseek-ai/dsh-client-ui-settings-models/client.js", "", 200},
		{"GET", "/plugins/??@deepseek-ai/dsh-client-ui-settings-models/client.js", "gzip", 200},
		{"GET", "/plugins/??@deepseek-ai/dsh-client-ui-settings-models/client.js", "", 206},
	} {
		u, _ := url.Parse(tc.path)
		response := &http.Response{StatusCode: tc.status, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(original)), Request: &http.Request{Method: tc.method, URL: u}, ContentLength: int64(len(original))}
		response.Header.Set("Content-Encoding", tc.encoding)
		if err := managedModelsResponse(response, "https://portal.harness.test", "instance"); err != nil {
			t.Fatal(err)
		}
		actual, err := io.ReadAll(response.Body)
		if err != nil || !bytes.Equal(actual, original) {
			t.Fatal("unrelated asset or response changed")
		}
	}
}

func TestManagedModelsPinnedRealBundle(t *testing.T) {
	file := os.Getenv("HC_TEST_DSH_MODELS_BUNDLE_FILE")
	if file == "" {
		t.Skip("requires the pinned public DSH browser bundle")
	}
	original, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	patched, changed := adaptManagedModels(original, "https://portal.harness.test:42013", "00000000-0000-4000-8000-000000000001")
	if !changed {
		t.Fatal("pinned module was not adapted")
	}
	if !bytes.Contains(patched, []byte("state.managedCatalog = catalog.value")) || !bytes.Contains(patched, []byte("data-hc-managed-models-directory")) {
		t.Fatal("managed directory missing")
	}
	// Only the two known insertion points change; the pinned module and all
	// other modules remain intact, including the existing local settings path.
	start := bytes.Index(original, []byte(modelsModuleStart))
	if !bytes.HasPrefix(patched, original[:start]) || !bytes.HasSuffix(patched, original[start+managedModelsSize:]) {
		t.Fatal("another browser module changed")
	}
	if bytes.Count(patched, []byte("const [registered, declared] = await Promise.all([")) != bytes.Count(original, []byte("const [registered, declared] = await Promise.all([")) {
		t.Fatal("native local settings path changed")
	}
	again, changed := adaptManagedModels(patched, "https://portal.harness.test", "instance")
	if changed || !bytes.Equal(again, patched) {
		t.Fatal("adapter applied twice")
	}
	output := filepath.Join(t.TempDir(), "plugins.js")
	if err = os.WriteFile(output, patched, 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("node", "--check", output).CombinedOutput(); err != nil {
		t.Fatalf("invalid adapted JavaScript: %s", out)
	}
	command := exec.Command("node", "--test", "managed-models-read.test.cjs")
	command.Env = append(os.Environ(), "HC_PATCHED_MODELS_FILE="+output)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("managed catalog recovery failed: %s", out)
	}
	mutated := bytes.Clone(original)
	mutated[start+100] ^= 1
	if _, changed := adaptManagedModels(mutated, "https://portal.harness.test", "instance"); changed {
		t.Fatal("upstream content mismatch accepted")
	}
	u, _ := url.Parse("/plugins/??@deepseek-ai/dsh-client-ui-settings-models/client.js")
	response := &http.Response{StatusCode: 200, Header: http.Header{"Etag": []string{"old"}}, Body: io.NopCloser(bytes.NewReader(original)), Request: &http.Request{Method: "GET", URL: u}}
	if err = managedModelsResponse(response, "https://portal.harness.test:42013", "00000000-0000-4000-8000-000000000001"); err != nil {
		t.Fatal(err)
	}
	actual, _ := io.ReadAll(response.Body)
	expected, _ := adaptManagedDirectory(patched)
	expected, _ = adaptManagedModelStartup(expected)
	expected, _ = adaptManagedConnection(expected)
	if !bytes.Equal(actual, expected) || response.ContentLength != int64(len(actual)) || response.Header.Get("ETag") != "" || strings.TrimSpace(response.Header.Get("Content-Length")) == "" {
		t.Fatal("adapted response metadata invalid")
	}
}
