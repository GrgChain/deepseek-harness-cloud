package connectors

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestBuiltinCatalog(t *testing.T) {
	catalog := builtinCatalog()
	if catalog.Version != 2 || len(catalog.Entries) != 0 {
		t.Fatal("built-in catalog should be empty")
	}
	u, err := url.Parse(catalog.SourceURL)
	if err != nil || u.Host == "" || u.User != nil || u.Scheme != "https" && u.Scheme != "http" {
		t.Fatal("invalid catalog source link")
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
		// Removing presets must not invalidate existing deployment policies.
		{"feishu-cli", "https://mcp.example.com/mcp", true},
		{"youdao-note", "https://mcp.example.com/mcp", true},
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

func TestDefaultEndpointsPreserveDeploymentOverrides(t *testing.T) {
	s := NewService(nil, nil, "test", false, "")
	p, err := s.loadPolicy()
	if err != nil || len(p.Endpoints) != 0 {
		t.Fatal("empty catalog must not inject built-in endpoints", err)
	}
	s.policy = func() (Policy, error) {
		return Policy{Endpoints: []Endpoint{{ID: "company-docs", CatalogID: "tencent-docs", URL: "https://company.example/mcp"}}}, nil
	}
	p, err = s.loadPolicy()
	if err != nil || len(p.Endpoints) != 1 || p.Endpoints[0].URL != "https://company.example/mcp" {
		t.Fatal("deployment endpoint replaced", err)
	}
}
