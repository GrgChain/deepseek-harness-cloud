package access

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func TestAccessConfigRequiresPrivateReferencesAndSameHTTPSDeployment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.json")
	good := Options{SchemaVersion: 1, OrganizationID: webapi.ID(), WorkspaceOrganizationID: webapi.ID(), ControlUserID: webapi.ID(), WorkspaceEngine: "embedded", Environment: "local", DataKeyID: "local-v1", DataKeyFile: "/private/key", PortalOrigin: "https://portal.harness.localhost:42013", Domain: "harness.localhost"}
	write := func(o Options) {
		raw, _ := json.Marshal(o)
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(good)
	if _, err := LoadOptions(path); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Options){func(o *Options) { o.DataKeyFile = "/private/../key" }, func(o *Options) { o.OrganizationID = "" }, func(o *Options) { o.PortalOrigin = "https://portal.foreign.test:42013" }, func(o *Options) { o.WorkspaceEngine = "http://192.168.1.1:17080" }, func(o *Options) { o.SchemaVersion = 2 }} {
		o := good
		change(&o)
		write(o)
		if _, err := LoadOptions(path); err == nil {
			t.Fatal("unsafe Gateway config accepted")
		}
	}

	for _, legacy := range []string{"coder_endpoint", "coder_token_file", "coder_organization_id", "allow_local_coder_http"} {
		raw, _ := json.Marshal(good)
		raw = append(raw[:len(raw)-1], []byte(`,"`+legacy+`":null}`)...)
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadOptions(path); err == nil {
			t.Fatal("legacy configuration alias accepted", legacy)
		}
	}
	write(good)
	os.Chmod(path, 0644)
	if _, err := LoadOptions(path); err == nil {
		t.Fatal("public config accepted")
	}
	os.Chmod(path, 0600)
	raw, _ := json.Marshal(good)
	raw = append(raw[:len(raw)-1], []byte(`,"schema_version":1}`)...)
	os.WriteFile(path, raw, 0600)
	if _, err := LoadOptions(path); err == nil {
		t.Fatal("duplicate deployment field accepted")
	}
}
