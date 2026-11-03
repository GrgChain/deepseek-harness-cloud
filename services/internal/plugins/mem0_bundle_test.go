package plugins

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestBundledMem0ArchiveIsAnInstallableDSHBundle(t *testing.T) {
	filename := "harness-cloud-dsh-mem0-memory-0.1.0.tgz"
	archive, err := os.ReadFile(filepath.Join("..", "..", "..", "apps", "public", "plugin-packages", filename))
	if err != nil {
		t.Fatal(err)
	}
	plugin, err := ResolveArchive(context.Background(), filename, archive)
	if err != nil {
		t.Fatal("shipped Mem0 archive was rejected by the offline plugin parser", err)
	}
	if plugin.PackageName != "@harness-cloud/dsh-mem0-memory" || plugin.Version != "0.1.0" || plugin.Author != "Harness Cloud" || plugin.SHA256 == "" {
		t.Fatal("unexpected shipped Mem0 identity", plugin)
	}
}
