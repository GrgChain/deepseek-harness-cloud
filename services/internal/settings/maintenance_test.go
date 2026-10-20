package settings

import (
	"context"
	"testing"
)

func TestMaintenanceDoesNotAcquireOrChargeAutomaticProbe(t *testing.T) {
	// A nil pool/probe makes any dependency call fail this assertion immediately.
	a := NewAutoValidator(&Service{maintenance: true})
	claimed, err := a.RunOnce(context.Background())
	if claimed || err != nil {
		t.Fatal("maintenance started an automatic paid probe")
	}
}
