package worker

import "testing"

func TestStorageDiagnosticNeverLogsRawUpstreamText(t *testing.T) {
	if storageFailurePhase("harness-cloud: storage operation failed [DEVICE_ATTACHMENT]; original data retained\n") != "DEVICE_ATTACHMENT" {
		t.Fatal("controlled phase missing")
	}
	for _, raw := range []string{"secret-key", "harness-cloud: storage operation failed [secret-key]; original data retained", "extra private text\nharness-cloud: storage operation failed [MOUNT]; original data retained", "harness-cloud: storage operation failed [MOUNT]; original data retained\nprivate upstream diagnostic"} {
		if storageFailurePhase(raw) != "" {
			t.Fatal("raw diagnostic accepted")
		}
	}
}
