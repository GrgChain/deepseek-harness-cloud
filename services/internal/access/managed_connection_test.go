package access

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestManagedConnectionUnknownModule(t *testing.T) {
	original := []byte("window.__ModuleLoader__.load({\n\tid: \"@deepseek-ai/dsh-client-ui-settings-general\", unknown")
	if result, changed := adaptManagedConnection(original); changed || !bytes.Equal(result, original) {
		t.Fatal("unknown module changed")
	}
}
func TestManagedConnectionPinnedLifecycle(t *testing.T) {
	file := os.Getenv("HC_TEST_DSH_SETTINGS_GENERAL_FILE")
	if file == "" {
		t.Skip("pinned browser shell fixture required")
	}
	original, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	bundle := append([]byte("/*before*/\n"), original...)
	bundle = append(bundle, []byte("\n/*after*/")...)
	patched, changed := adaptManagedConnection(bundle)
	if !changed {
		t.Fatal("reviewed shell module not adapted")
	}
	if !bytes.HasPrefix(patched, []byte("/*before*/\n")) || !bytes.HasSuffix(patched, []byte("\n/*after*/")) {
		t.Fatal("modified surrounding bundle")
	}
	if again, changed := adaptManagedConnection(patched); changed || !bytes.Equal(again, patched) {
		t.Fatal("adapted twice")
	}
	altered := bytes.Replace(bundle, []byte("SettingsRoot"), []byte("OtherSettingsRoot"), 1)
	if _, changed := adaptManagedConnection(altered); changed {
		t.Fatal("changed mismatched module")
	}
	output := filepath.Join(t.TempDir(), "shell.js")
	if err := os.WriteFile(output, patched, 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("node", "--check", output).CombinedOutput(); err != nil {
		t.Fatalf("invalid shell JavaScript: %s", out)
	}
	command := exec.Command("node", "--test", "managed-connection.test.cjs")
	command.Env = append(os.Environ(), "HC_PATCHED_SETTINGS_GENERAL_FILE="+output)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("connection lifecycle failed: %s", out)
	}
}
