package worker

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestReadOnlyHostInventoryOfRealOriginalAssets(t *testing.T) {
	path := os.Getenv("HC_WORKER_CONFIG_FILE")
	if path == "" {
		t.Skip("explicit trusted host required for read-only inventory")
	}
	o, err := LoadOptions(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewLocalHost(o.Host)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	first, err := h.Inventory(ctx)
	if err != nil {
		t.Fatal("read-only census failed", err)
	}
	second, err := h.Inventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	if string(a) != string(b) {
		t.Fatal("host changed during controlled inventory")
	}
	for _, r := range first {
		if strings.Contains(r.ExternalID, "hc-local") {
			t.Fatal("control plane included in DSH census")
		}
		if r.Kind == "ASSET" && r.IdentityValid && (r.Capacity != 10<<30 || r.AssetID != r.ExternalID) {
			t.Fatal("verified disk has wrong identity")
		}
	}
}
