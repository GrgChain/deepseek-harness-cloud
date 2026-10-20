package settings

import "testing"

func TestModelPolicyRejectsUnapprovedDestinations(t *testing.T) {
	p, err := ParsePolicy([]byte(`{"schema_version":1,"endpoints":["https://api.deepseek.com/anthropic"],"models":["deepseek-flash"]}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"http://api.deepseek.com/anthropic", "https://api.deepseek.com.evil/anthropic", "https://api.deepseek.com/anthropic?x=1", "https://u:pass@api.deepseek.com/anthropic", "https://127.0.0.1", "https://api.deepseek.com/anthropic/../admin"} {
		if p.Allows(endpoint, "deepseek-flash") {
			t.Fatalf("unapproved endpoint accepted: %s", endpoint)
		}
	}
	if !p.Allows("https://api.deepseek.com/anthropic", "deepseek-flash") || p.Allows("https://api.deepseek.com/anthropic", "unknown") {
		t.Fatal("allowlist mismatch")
	}
	for _, raw := range []string{`{"schema_version":1,"endpoints":["https://127.0.0.1"],"models":["x"]}`, `{"schema_version":1,"endpoints":["https://api.deepseek.com"],"models":["x"],"extra":true}`, `{"schema_version":1,"endpoints":[],"models":["x"]}`} {
		if _, err := ParsePolicy([]byte(raw)); err == nil {
			t.Fatal("invalid deployment policy accepted")
		}
	}
}
