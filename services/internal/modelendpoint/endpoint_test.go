package modelendpoint

import "testing"

func TestCustomEndpointBounds(t *testing.T) {
	for _, base := range []string{"http://127.0.0.1:11434/v1", "http://localhost:12345", "http://192.168.1.5:18090/v1", "http://192.168.20.165:8080/v1", "http://localhost:8000/v1", "http://127.0.0.1:1024/v1", "https://models.example/v1"} {
		if !ValidBase(base) {
			t.Fatal("valid custom root rejected", base)
		}
	}
	for _, base := range []string{"http://models.example/v1", "http://169.254.169.254:11434/v1", "http://0.0.0.0:11434", "http://127.0.0.1:1023", "http://localhost:80/v1", "http://localhost:11434/../admin", "http://localhost:11434/v1?x=1", "https://key@models.example/v1", "http://localhost:11434/v1/", "http://localhost:11434/%2e%2e/admin"} {
		if ValidBase(base) {
			t.Fatal("unsafe custom root admitted", base)
		}
	}
	if ValidBases([]string{"http://localhost:11434/v1", "http://localhost:11434/v1"}) {
		t.Fatal("duplicate roots admitted")
	}
}
