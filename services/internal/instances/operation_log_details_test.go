package instances

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOperationLogDetailsExposeTypedMetadataWithoutCredentialsOrRawBodies(t *testing.T) {
	raw := []byte(`{"operation_id":"00000000-0000-4000-8000-000000000001","reason_code":"MODEL_AUTH_REJECTED","version":2,"permissions":["instances:read"],"before":{"max_instances":1,"api_key":"private-secret"},"after":{"max_instances":2,"memory_bytes":4294967296,"headers":{"Authorization":"private-secret"}},"api_key":"private-secret","request_body":"private-secret","report":{"credentials":"private-secret"}}`)
	details := safeOperationLogDetails(raw)
	encoded, _ := json.Marshal(details)
	if strings.Contains(string(encoded), "private-secret") || len(details) != 6 {
		t.Fatalf("unsafe or missing operation log metadata: %s", encoded)
	}
	if details["reason_code"] != "MODEL_AUTH_REJECTED" || len(details["before"].(map[string]any)) != 1 {
		t.Fatal("typed change values were not retained")
	}
	for _, raw := range []string{`null`, `[]`, `not-json`, `{"version":-1,"operation_id":"not-an-id","before":{"max_instances":"private-secret"},"reason_code":"Authorization: private-secret"}`} {
		if encoded, _ := json.Marshal(safeOperationLogDetails([]byte(raw))); string(encoded) != "{}" {
			t.Fatalf("malformed metadata exposed: %s", encoded)
		}
	}
}
