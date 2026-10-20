package access

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestModelStartupUnknownModuleIsUnchanged(t *testing.T) {
	original := []byte("window.__ModuleLoader__.load({\n\tid: \"@deepseek-ai/dsh-client-ui-model-selection\",\n// new release")
	patched, changed := adaptManagedModelStartup(original)
	if changed || !bytes.Equal(original, patched) {
		t.Fatal("unknown client changed")
	}
}

func TestModelStartupPinnedModule(t *testing.T) {
	file := os.Getenv("HC_TEST_DSH_MODEL_SELECTION_FILE")
	if file == "" {
		t.Skip("requires pinned public model selector module")
	}
	original, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	bundle := append([]byte("/* preceding module */\n"), original...)
	bundle = append(bundle, []byte("\n/* following module */")...)
	patched, changed := adaptManagedModelStartup(bundle)
	if !changed {
		t.Fatal("pinned module not adapted")
	}
	if bytes.Contains(patched, []byte("this.catalog.load().catch(() => {});\n\t\t\t\tctx.on")) || !bytes.Contains(patched, []byte("this.readManagedCatalog(generation)")) || !bytes.Contains(patched, []byte("if (available) directory.load().catch(() => {});")) || !bytes.HasPrefix(patched, []byte("/* preceding module */\n")) || !bytes.HasSuffix(patched, []byte("\n/* following module */")) {
		t.Fatal("startup adaptation missing or escaped module")
	}
	again, changed := adaptManagedModelStartup(patched)
	if changed || !bytes.Equal(patched, again) {
		t.Fatal("applied twice")
	}
	altered := bytes.Replace(bundle, []byte("ModelCatalogDirectory"), []byte("OtherCatalogDirectory"), 1)
	if _, changed := adaptManagedModelStartup(altered); changed {
		t.Fatal("changed mismatched module")
	}
	output := filepath.Join(t.TempDir(), "model-selection.js")
	if err = os.WriteFile(output, patched, 0600); err != nil {
		t.Fatal(err)
	}
	if result, err := exec.Command("node", "--check", output).CombinedOutput(); err != nil {
		t.Fatalf("invalid JavaScript: %s", result)
	}
	command := exec.Command("node", "--test", "managed-model-startup.test.cjs")
	command.Env = append(os.Environ(), "HC_PATCHED_MODEL_SELECTION_FILE="+output)
	if result, err := command.CombinedOutput(); err != nil {
		t.Fatalf("model startup lifecycle failed: %s", result)
	}
}
