package contextservice

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestEndpointBoundary(t *testing.T) {
	for _, address := range []string{"file:///etc/passwd", "http://user:secret@localhost", "https://localhost/?key=secret", "https://localhost/#fragment", "http://127.0.0.1/../internal"} {
		if _, err := endpointURL(Endpoint{URL: address, AllowPrivateHTTP: true}); err == nil {
			t.Fatalf("accepted %q", address)
		}
	}
	for _, address := range []string{"http://169.254.169.254", "http://1.1.1.1", "http://0.0.0.0"} {
		if _, err := endpointClient(context.Background(), Endpoint{URL: address, AllowPrivateHTTP: true}); err == nil {
			t.Fatalf("accepted plain HTTP %q", address)
		}
	}
	reached := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
	defer destination.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 302) }))
	defer redirect.Close()
	c, err := endpointClient(context.Background(), Endpoint{URL: redirect.URL, AllowPrivateHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	response, err := c.Get(redirect.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 302 || reached {
		t.Fatal("followed redirect")
	}
}
func TestPrivatePolicy(t *testing.T) {
	p := filepath.Join(t.TempDir(), "endpoints.json")
	good := `{"endpoints":[{"id":"local","name":"Local","backend":"mem0","url":"http://127.0.0.1:18000/mem0","allow_private_http":true,"data_use":"local"}]}`
	if err := os.WriteFile(p, []byte(good), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPolicy(p); err == nil {
		t.Fatal("publicly readable policy")
	}
	if err := os.Chmod(p, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPolicy(p); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPolicy(""); err != nil {
		t.Fatal("optional external services made platform unavailable")
	}
}
