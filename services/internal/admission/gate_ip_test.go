package admission

import (
	"testing"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/access"
)

func TestNewGateRejectsIPOnlyWithoutIPAccessConfig(t *testing.T) {
	rules, err := access.NewRules("https://portal.harness.test:42013", "harness.test")
	if err != nil {
		t.Fatal(err)
	}
	runtime := &access.Runtime{
		Rules: rules,
		Options: access.Options{
			Environment: "local",
			IPOnly:      true,
			IPAccess:    nil,
		},
	}
	gate, err := NewGate(nil, runtime, "")
	if err == nil || gate != nil {
		t.Fatalf("expected error for IPOnly without IPAccess, got gate=%v err=%v", gate, err)
	}
}
