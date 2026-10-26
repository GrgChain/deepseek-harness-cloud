package instances

import (
	"errors"
	"testing"
)

func TestLifecycleProtectsOriginalAssetAndRevokesAccess(t *testing.T) {
	stopped := Lifecycle{Observed: "STOPPED", Desired: "STOPPED", Version: 7, AccessGeneration: 3}
	facts := ResourceFacts{ModelUsable: true}
	if _, err := stopped.Plan(Start, 0, facts); !errors.Is(err, ErrStorageMissing) {
		t.Fatalf("missing original asset: %v", err)
	}
	facts.OriginalAssetReady = true
	next, err := stopped.Plan(Start, 0, facts)
	if err != nil || next.Observed != "STARTING" || next.Desired != "RUNNING" || next.Version != 8 {
		t.Fatalf("start: %+v %v", next, err)
	}
	if stopped.Version != 7 {
		t.Fatal("planning mutated persisted state")
	}
	running := Lifecycle{Observed: "RUNNING", Desired: "RUNNING", Version: 8, AccessGeneration: 3}
	// A revoked model must never prevent safety cleanup.
	next, err = running.Plan(Stop, 0, ResourceFacts{})
	if err != nil || next.Observed != "STOPPING" || next.Desired != "STOPPED" || next.AccessGeneration != 4 {
		t.Fatalf("stop: %+v %v", next, err)
	}
	next, err = running.Plan(Configure, 8, facts)
	if err != nil || next.Observed != "STOPPING" || next.Desired != "RUNNING" || next.AccessGeneration != 4 {
		t.Fatalf("configure restart: %+v %v", next, err)
	}
	if _, err := running.Plan(Configure, 7, facts); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale configure: %v", err)
	}
	if _, err := running.Plan(Configure, 0, facts); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("missing expected version: %v", err)
	}
}

func TestLifecycleErrorRecoveryUsesReconciledResourceFacts(t *testing.T) {
	broken := Lifecycle{Observed: "ERROR", Desired: "RUNNING", Version: 1, AccessGeneration: 1}
	for _, action := range []Action{Start, Stop, Configure} {
		if _, err := broken.Plan(action, 1, ResourceFacts{OriginalAssetReady: true, ModelUsable: true}); !errors.Is(err, ErrStateConflict) {
			t.Fatalf("unknown resources accepted %s: %v", action, err)
		}
	}
	absent := ResourceFacts{Reconciled: true, OriginalAssetReady: true, ModelUsable: true}
	if _, err := broken.Plan(Start, 0, absent); err != nil {
		t.Fatal(err)
	}
	if _, err := broken.Plan(Stop, 0, absent); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("absent compute stop: %v", err)
	}
	present := ResourceFacts{Reconciled: true, ComputePresent: true}
	if _, err := broken.Plan(Start, 0, present); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("present compute start: %v", err)
	}
	if _, err := broken.Plan(Stop, 0, present); err != nil {
		t.Fatal(err)
	}
	if _, err := broken.Plan(Delete, 0, present); err != nil {
		t.Fatal(err)
	}
}

func TestLifecycleNeverDuplicatesActiveOrCompletedCommands(t *testing.T) {
	facts := ResourceFacts{OriginalAssetReady: true, ModelUsable: true}
	for _, observed := range []string{"CREATING", "STARTING", "STOPPING", "DELETING", "DELETED", "invalid"} {
		state := Lifecycle{Observed: observed, Desired: "RUNNING", Version: 1, AccessGeneration: 1}
		for _, action := range []Action{Start, Stop, Delete, Configure} {
			if _, err := state.Plan(action, 1, facts); err == nil {
				t.Fatalf("accepted %s from %s", action, observed)
			}
		}
	}
	for _, state := range []Lifecycle{{Observed: "RUNNING", Desired: "RUNNING", Version: 1, AccessGeneration: 1}, {Observed: "STOPPED", Desired: "STOPPED", Version: 1, AccessGeneration: 1}} {
		duplicate := Start
		if state.Observed == "STOPPED" {
			duplicate = Stop
		}
		if _, err := state.Plan(duplicate, 0, facts); !errors.Is(err, ErrStateConflict) {
			t.Fatal(err)
		}
		state.ActiveOperation = true
		for _, action := range []Action{Start, Stop, Delete, Configure} {
			if _, err := state.Plan(action, 1, facts); !errors.Is(err, ErrStateConflict) {
				t.Fatalf("accepted active %s: %v", action, err)
			}
		}
	}
}

func TestLifecycleDeletesErrorWithoutAssumingComputeIsAbsent(t *testing.T) {
	for _, facts := range []ResourceFacts{{}, {Reconciled: true}, {Reconciled: true, ComputePresent: true}} {
		broken := Lifecycle{Observed: "ERROR", Desired: "RUNNING", Version: 2, AccessGeneration: 3}
		next, err := broken.Plan(Delete, 0, facts)
		if err != nil || next.Observed != "DELETING" || next.Desired != "DELETED" || next.Version != 3 || next.AccessGeneration != 4 || !next.ActiveOperation {
			t.Fatalf("error cleanup: %+v %v", next, err)
		}
		broken.ActiveOperation = true
		if _, err = broken.Plan(Delete, 0, facts); !errors.Is(err, ErrStateConflict) {
			t.Fatalf("accepted conflicting cleanup: %v", err)
		}
	}
}
