package connectors

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuiltinCatalog(t *testing.T) {
	catalog := builtinCatalog()
	want := strings.Fields("tencent-docs tencent-ima tencent-meeting tencent-news wechat-reading tencent-lexiang tencent-weiyun qq-mail qq-music fliggy baidu-map ctrip-wendao meituan-travel didi yuandian qcc tencent-ardot youdao-note notion openalex dida365 feishu-cli wecom-cli dingtalk-cli agently-cli obsidian-cli yuque-mcp weknora dify")
	if catalog.Version != 1 || len(catalog.Entries) != len(want) {
		t.Fatal("incomplete built-in catalog")
	}
	seen := map[string]bool{}
	categories := map[string]bool{"office": true, "knowledge": true, "productivity": true, "media": true, "travel": true, "professional": true, "self_hosted": true}
	modes := map[string]bool{"remote": true, "gateway": true, "internal": true, "stdio": true}
	for _, entry := range catalog.Entries {
		if seen[entry.ID] || !endpointID.MatchString(entry.ID) || entry.Name == "" || entry.Description == "" || entry.SetupHint == "" || len(entry.Requirements) == 0 || !categories[entry.Category] || !modes[entry.Mode] {
			t.Fatal("invalid catalog entry", entry.ID)
		}
		for _, link := range []string{entry.DocURL, entry.GuideURL, catalog.SourceURL} {
			u, err := url.Parse(link)
			if err != nil || u.Host == "" || u.User != nil || u.Scheme != "https" && u.Scheme != "http" {
				t.Fatal("invalid catalog link", entry.ID)
			}
		}
		seen[entry.ID] = true
	}
	for _, id := range want {
		if !seen[id] {
			t.Fatal("missing catalog entry", id)
		}
	}
	// Callers cannot mutate the metadata served by a later request.
	catalog.Entries[0].Requirements[0] = "changed"
	if builtinCatalog().Entries[0].Requirements[0] == "changed" {
		t.Fatal("catalog shared mutable state")
	}
}

func TestCatalogBindingsDoNotApproveNetworkTargets(t *testing.T) {
	if policy, err := LoadPolicy(""); err != nil || len(policy.Endpoints) != 0 {
		t.Fatal("catalog must not approve external services", err)
	}
	file := filepath.Join(t.TempDir(), "endpoints.json")
	for _, tc := range []struct {
		catalog, target string
		valid           bool
	}{
		{"", "https://mcp.example.com/mcp", true},
		{"notion", "https://mcp.example.com/mcp", true},
		{"not-a-connector", "https://mcp.example.com/mcp", false},
		{"notion", "http://127.0.0.1/mcp", false},
		{"notion", "https://mcp.example.com/mcp?token=secret", false},
	} {
		data, err := json.Marshal(Policy{Endpoints: []Endpoint{{ID: "approved", Name: "Approved", URL: tc.target, CatalogID: tc.catalog}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, data, 0600); err != nil {
			t.Fatal(err)
		}
		policy, err := LoadPolicy(file)
		if (err == nil) != tc.valid {
			t.Fatalf("catalog=%q target=%q accepted=%v", tc.catalog, tc.target, err == nil)
		}
		if err == nil && policy.Endpoints[0].CatalogID != tc.catalog {
			t.Fatal("binding lost")
		}
	}
}
