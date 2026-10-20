package instances

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestStoredSnapshotRejectsSecretsAndUnknownVersions(t *testing.T) {
	valid := `{"schema_version":1,"defaults_version":1,"template_id":"00000000-0000-4000-8000-000000000001","template_version":"00000000-0000-4000-8000-000000000002","image_digest":"sha256:` + strings.Repeat("a", 64) + `","spec":{"id":"00000000-0000-4000-8000-000000000003","cpu_millicores":2000,"memory_bytes":4294967296,"storage_bytes":10737418240},"model_configuration_id":"00000000-0000-4000-8000-000000000004","model_configuration_version":1,"provider":"deepseek-official","protocol":"deepseek-messages","model":"test-model"}`
	result, err := DecodeSnapshot([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	output, _ := json.Marshal(result)
	if strings.Contains(string(output), "schema_version") {
		t.Fatal("internal persistence version crossed API boundary")
	}
	for _, invalid := range []string{strings.Replace(valid, `"schema_version":1`, `"schema_version":2`, 1), strings.TrimSuffix(valid, "}") + `,"api_key":"synthetic-secret"}`, strings.Replace(valid, `"storage_bytes":10737418240`, `"storage_bytes":0`, 1)} {
		if _, err := DecodeSnapshot([]byte(invalid)); err == nil {
			t.Fatal("invalid persisted snapshot accepted")
		}
	}
}
func TestQueryHealthNeverExtendsEvidenceFreshness(t *testing.T) {
	now := time.Now().UTC()
	start := now.Add(-time.Minute)
	expiry := now
	result := (HealthView{Status: "HEALTHY", CheckedAt: &start, ExpiresAt: &expiry}).normalize(now)
	if result.Status != "UNKNOWN" {
		t.Fatal("expired health was exposed as healthy")
	}
	if (HealthView{}).normalize(now).Status != "UNKNOWN" {
		t.Fatal("missing health became healthy")
	}
	future := now.Add(time.Second)
	expiry = now.Add(time.Minute)
	if (HealthView{Status: "HEALTHY", CheckedAt: &future, ExpiresAt: &expiry}).normalize(now).Status != "UNKNOWN" {
		t.Fatal("future health was accepted")
	}
	if operationError("arbitrary-private-upstream-message", "trace").Code != "INTERNAL_ERROR" {
		t.Fatal("unapproved upstream error crossed boundary")
	}
}

func TestUnhealthyObservationsExpireButCurrentFactsRemainBlocked(t *testing.T) {
	now := time.Now().UTC()
	checked, expires := now.Add(-2*time.Minute), now.Add(-time.Minute)
	for _, status := range []string{"HEALTHY", "UNHEALTHY", "CHECKING"} {
		result := (HealthView{Status: status, CheckedAt: &checked, ExpiresAt: &expires, Reason: "HEALTH_TIMEOUT"}).normalize(now)
		if result.Status != "UNKNOWN" || result.Reason != "HEALTH_EXPIRED" {
			t.Fatalf("expired %s observation remained current: %+v", status, result)
		}
	}
	for _, reason := range []string{"CREDENTIAL_REVOKED", "ACCESS_CLOSED", "STORAGE_MISSING"} {
		result := (HealthView{Status: "UNHEALTHY", Reason: reason}).normalize(now)
		if result.Status != "UNHEALTHY" || result.Reason != reason {
			t.Fatalf("current database fact lost its blocking status: %+v", result)
		}
	}
}

func TestModelStartupFailureExplainsRecoveryWithoutReconnecting(t *testing.T) {
	problem := operationError("MODEL_UNAVAILABLE", "startup-trace")
	if problem.Code != "MODEL_UNAVAILABLE" || !strings.Contains(problem.Message, "无需在工作空间重新连接") || !retryDiagnostic(problem.Code) {
		t.Fatal("model startup failure lost its recovery guidance")
	}
}
