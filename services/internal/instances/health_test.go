package instances

import (
	"testing"
	"time"
)

func TestHealthReadinessAndDiagnosticAccess(t *testing.T) {
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	checks := map[Dimension]Check{}
	for _, d := range Dimensions() {
		checks[d] = Check{Status: Healthy, CheckedAt: now.Add(-time.Second), ExpiresAt: now.Add(time.Minute)}
	}
	h := NewHealthSet(checks)
	if !h.Ready("RUNNING", now) || !h.CanEnter("RUNNING", true, now) {
		t.Fatal("fresh five-dimensional health must allow readiness and owner access")
	}
	checks[Model] = Check{Status: Unhealthy}
	h = NewHealthSet(checks)
	if h.Ready("RUNNING", now) || !h.CanEnter("RUNNING", true, now) {
		t.Fatal("model failure must block readiness but allow owner diagnosis")
	}
	if h.CanEnter("RUNNING", false, now) || h.CanEnter("STOPPED", true, now) {
		t.Fatal("non-owner and stopped instances must never allow entry")
	}
	for _, d := range []Dimension{Application, Storage, Configuration, Access} {
		t.Run(string(d), func(t *testing.T) {
			copy := NewHealthSet(checks)
			checks[d] = Check{Status: Healthy, CheckedAt: now.Add(-time.Minute), ExpiresAt: now}
			if NewHealthSet(checks).CanEnter("RUNNING", true, now) {
				t.Fatal("expired check allowed entry")
			}
			if !copy.CanEnter("RUNNING", true, now) {
				t.Fatal("health aggregate retained mutable caller map")
			}
			checks[d] = Check{Status: Healthy, CheckedAt: now.Add(-time.Second), ExpiresAt: now.Add(time.Minute)}
		})
	}
}

func TestUnknownAndFutureChecksFailClosed(t *testing.T) {
	now := time.Now()
	if NewHealthSet(nil).Ready("RUNNING", now) || NewHealthSet(nil).CanEnter("RUNNING", true, now) {
		t.Fatal("missing health must fail closed")
	}
	checks := map[Dimension]Check{}
	for _, d := range Dimensions() {
		checks[d] = Check{Status: Healthy, CheckedAt: now.Add(time.Second), ExpiresAt: now.Add(time.Minute)}
	}
	if NewHealthSet(checks).Ready("RUNNING", now) {
		t.Fatal("future timestamp accepted")
	}
}
