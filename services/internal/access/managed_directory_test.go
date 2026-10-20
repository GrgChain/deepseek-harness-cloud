package access

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestManagedDirectoryUnknownModuleUnchanged(t *testing.T) {
	original := []byte("window.__ModuleLoader__.load({\n\tid: \"@deepseek-ai/dsh-client-ui-directory-picker-browse\",\n// unknown version")
	if result, changed := adaptManagedDirectory(original); changed || !bytes.Equal(result, original) {
		t.Fatal("unknown directory picker changed")
	}
}

func TestManagedDirectoryNavigationPolicy(t *testing.T) {
	if out, err := exec.Command("node", "--test", "managed-directory.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("directory navigation policy failed: %s", out)
	}
}

func TestManagedDirectoryPinnedModule(t *testing.T) {
	file := os.Getenv("HC_TEST_DSH_DIRECTORY_MODULE_FILE")
	if file == "" {
		t.Skip("requires the pinned public directory picker module")
	}
	original, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	patched, changed := adaptManagedDirectory(original)
	if !changed || !bytes.Contains(patched, []byte(`ctx.uiWorkspace.listDirectory(checked(path), signal)`)) {
		t.Fatal("persistent default directory missing")
	}
	// Only the picker service injection changes; all other native UI stays intact.
	old := "const injected = () => ({\n\t\t\t\tlistDirectory: (path, signal) => ctx.uiWorkspace.listDirectory(path, signal),\n\t\t\t\tcreateDirectory: (path, name) => ctx.uiWorkspace.createDirectory(path, name),\n\t\t\t\tt: ctx.locale.bind(LOCALE_NS)\n\t\t\t});"
	reversed := bytes.Replace(patched, []byte(managedDirectoryView), []byte(old), 1)
	if !bytes.Equal(reversed, original) {
		t.Fatal("unrelated directory behavior changed")
	}
	if _, changed := adaptManagedDirectory(patched); changed {
		t.Fatal("directory picker adapted twice")
	}
	mutated := bytes.Clone(original)
	mutated[100] ^= 1
	if _, changed := adaptManagedDirectory(mutated); changed {
		t.Fatal("modified upstream module accepted")
	}
	output := filepath.Join(t.TempDir(), "directory.js")
	if err := os.WriteFile(output, patched, 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("node", "--check", output).CombinedOutput(); err != nil {
		t.Fatalf("invalid directory JavaScript: %s", out)
	}
	u, _ := url.Parse("/plugins/??@deepseek-ai/dsh-client-ui-directory-picker-browse/client.js")
	response := &http.Response{StatusCode: 200, Header: http.Header{"Etag": []string{"old"}}, Body: io.NopCloser(bytes.NewReader(original)), Request: &http.Request{Method: "GET", URL: u}}
	if err := managedModelsResponse(response, "https://portal.harness.test", "instance"); err != nil {
		t.Fatal(err)
	}
	actual, _ := io.ReadAll(response.Body)
	if !bytes.Equal(actual, patched) || response.ContentLength != int64(len(patched)) || response.Header.Get("ETag") != "" {
		t.Fatal("directory response not adapted correctly")
	}
}
