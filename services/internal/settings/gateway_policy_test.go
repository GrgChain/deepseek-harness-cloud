package settings

import (
	"strings"
	"testing"
)

func TestGatewayMayUsePlatformDatabaseCatalog(t *testing.T) {
	p, err := ParsePolicy([]byte(`{"schema_version":1,"gateways":[{"endpoint":"https://models.enterprise.example:14000","revision":"v1","addresses":["10.50.0.10/32"],"models":[],"probe_retries_disabled":true,"probe_fallbacks_disabled":true}]}`))
	if err != nil || p == nil {
		t.Fatal("database-managed gateway rejected", err)
	}
	if p.AllowsConnection("platform-models", "anthropic-messages", p.Gateways[0].Endpoint, "unknown") {
		t.Fatal("empty static catalog approved an unknown alias")
	}
}

func TestGatewayPolicyApprovesExactOriginModelRevisionAndDestinations(t *testing.T) {
	raw := []byte(`{"schema_version":1,"endpoints":["https://api.deepseek.com/anthropic"],"models":["deepseek-flash"],"gateways":[{"endpoint":"https://llm.enterprise.example:14000","revision":"routes-2026-10-13","addresses":["10.50.0.10/32"],"models":[{"id":"enterprise-chat-v1","response_models":["provider-chat-2026"],"context_window":128000,"max_output_tokens":4096,"streaming":true,"tool_use":true}],"probe_retries_disabled":true,"probe_fallbacks_disabled":true}]}`)
	p, err := ParsePolicy(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !p.AllowsConnection("platform-models", "anthropic-messages", "https://llm.enterprise.example:14000", "enterprise-chat-v1") {
		t.Fatal("approved gateway rejected")
	}
	for _, endpoint := range []string{"https://llm.enterprise.example:14001", "https://llm.enterprise.example:14000/v1", "https://llm.enterprise.example:14000?x=1"} {
		if p.AllowsConnection("platform-models", "anthropic-messages", endpoint, "enterprise-chat-v1") {
			t.Fatal("origin restriction bypassed")
		}
	}
	if p.AllowsConnection("deepseek-official", "deepseek-messages", "https://llm.enterprise.example:14000", "enterprise-chat-v1") || p.AllowsConnection("platform-models", "anthropic-messages", "https://llm.enterprise.example:14000", "unknown") {
		t.Fatal("protocol or alias restriction bypassed")
	}
	manifest, ok := p.Manifest("https://llm.enterprise.example:14000", "enterprise-chat-v1")
	if !ok || manifest.Revision != "routes-2026-10-13" || manifest.Digest == "" {
		t.Fatal("routing approval was not frozen")
	}
	for _, address := range []string{"127.0.0.1/32", "169.254.169.254/32", "10.0.0.0/8", "0.0.0.0/0"} {
		changed := string(raw)
		changed = strings.ReplaceAll(changed, "10.50.0.10/32", address)
		if _, err := ParsePolicy([]byte(changed)); err == nil {
			t.Fatal("unsafe gateway address approved", address)
		}
	}
}
