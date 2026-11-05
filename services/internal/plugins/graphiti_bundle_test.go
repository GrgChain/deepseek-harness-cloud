package plugins

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	pluginassets "github.com/jonluo94/deepseek-harness-cloud/infrastructure/plugins"
)

func TestBundledGraphitiArchiveIsAnInstallableDSHBundle(t *testing.T) {
	filename := "harness-cloud-dsh-graphiti-memory-0.2.0.tgz"
	archive, err := os.ReadFile(filepath.Join("..", "..", "..", "apps", "public", "plugin-packages", filename))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(archive, pluginassets.GraphitiPlugin) {
		t.Fatal("downloaded Graphiti archive differs from the automatically installed bundle")
	}
	plugin, err := ResolveArchive(context.Background(), filename, archive)
	if err != nil {
		t.Fatal("shipped Graphiti archive was rejected by the offline plugin parser", err)
	}
	if plugin.PackageName != "@harness-cloud/dsh-graphiti-memory" || plugin.Version != "0.2.0" || plugin.Author != "Harness Cloud" || plugin.SHA256 == "" {
		t.Fatal("unexpected shipped Graphiti identity", plugin)
	}
}
