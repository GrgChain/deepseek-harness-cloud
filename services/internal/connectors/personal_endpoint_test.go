package connectors

import "testing"

func TestPersonalEndpointPolicy(t *testing.T) {
	s := NewService(nil, nil, "test", false, "")
	for _, address := range []string{
		"http://public.example/mcp", "https://127.0.0.1/mcp", "https://10.0.0.1/mcp",
		"https://[::1]/mcp", "https://169.254.169.254/mcp", "https://100.64.0.1/mcp",
		"https://user:password@public.example/mcp", "https://public.example/mcp?key=secret",
		"https://public.example/mcp#secret", "file:///tmp/mcp", "https://public.example/../mcp",
	} {
		if _, err := s.personalEndpoint(address, ""); !problemIs(err, "CONNECTOR_INVALID") {
			t.Errorf("unsafe personal address accepted: %s", address)
		}
	}
	if endpoint, err := s.personalEndpoint("https://public.example/mcp", "notion"); err != nil || endpoint.AllowPrivate || endpoint.AllowHTTP {
		t.Fatal("public HTTPS requires platform configuration", err)
	}
	s.policy = func() (Policy, error) {
		return Policy{Endpoints: []Endpoint{{ID: "local", URL: "http://127.0.0.1:18781/mcp", AllowPrivate: true, AllowHTTP: true}}}, nil
	}
	if _, err := s.personalEndpoint("http://127.0.0.1:18781/mcp", ""); err != nil {
		t.Fatal("exact local exception rejected", err)
	}
	for _, address := range []string{"http://127.0.0.1:18782/mcp", "http://127.0.0.1:18781/other"} {
		if _, err := s.personalEndpoint(address, ""); !problemIs(err, "CONNECTOR_INVALID") {
			t.Fatal("local exception applies to different target", err)
		}
	}
}
