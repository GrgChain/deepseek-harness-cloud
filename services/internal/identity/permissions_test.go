package identity

import (
	"encoding/json"
	"testing"
)

func TestCurrentUserExposesAuthorizationVersion(t *testing.T) {
	p := Principal{UserID: "00000000-0000-4000-8000-000000000001", Role: "MEMBER", Generation: 7}
	data, err := json.Marshal(p.CurrentUser())
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err = json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	if body["auth_generation"] != float64(7) {
		t.Fatal("current-user response must expose its authoritative authorization version")
	}
	if _, ok := body["permissions"].([]any); !ok {
		t.Fatal("current-user response must expose an explicit effective permission list, including an empty list")
	}
}
