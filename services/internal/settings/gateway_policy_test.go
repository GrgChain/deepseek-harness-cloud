package settings

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGatewayProbeCanWaitForModelWithinCallerBudget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(15 * time.Second):
			_, _ = io.WriteString(w, "synthetic model response")
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	client := (Gateway{Endpoint: server.URL}).HTTPClient()
	transport := client.Transport.(*http.Transport)
	transport.DialContext = (&net.Dialer{}).DialContext
	defer transport.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal("gateway ended the model request before the caller budget", err)
	}
	response.Body.Close()
	short, stop := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer stop()
	request, _ = http.NewRequestWithContext(short, http.MethodGet, server.URL, nil)
	if response, err := client.Do(request); err == nil {
		response.Body.Close()
		t.Fatal("gateway ignored the caller deadline")
	}
}

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
