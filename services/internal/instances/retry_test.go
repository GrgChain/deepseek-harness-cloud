package instances

import "testing"

func TestRetryKeepsOriginalTargetAndRequiresReconciledLatestFailure(t *testing.T) {
	state := Lifecycle{Observed: "ERROR", Desired: "RUNNING", Version: 5, AccessGeneration: 4}
	base := RetryFacts{Latest: true, Failed: true, User: true, Reconciled: true, Diagnostic: true, OriginalAssetReady: true, ModelUsable: true}
	for _, action := range []string{"CREATE", "START", "CONFIGURE", "STOP", "DELETE"} {
		target := "RUNNING"
		if action == "STOP" {
			target = "STOPPED"
		}
		if action == "DELETE" {
			target = "DELETED"
		}
		next, err := state.PlanRetry(action, target, base)
		if err != nil || next.Desired != target || next.Version != 6 || next.AccessGeneration != 5 || !next.ActiveOperation {
			t.Fatalf("retry %s lost target/version: %+v %v", action, next, err)
		}
	}
	for _, mutate := range []func(*RetryFacts){func(f *RetryFacts) { f.Latest = false }, func(f *RetryFacts) { f.Failed = false }, func(f *RetryFacts) { f.User = false }, func(f *RetryFacts) { f.Reconciled = false }, func(f *RetryFacts) { f.Diagnostic = false }, func(f *RetryFacts) { f.ModelUsable = false }, func(f *RetryFacts) { f.OriginalAssetReady = false }} {
		bad := base
		mutate(&bad)
		if _, err := state.PlanRetry("CREATE", "RUNNING", bad); err == nil {
			t.Fatal("unsafe retry accepted")
		}
	}
	base.ModelUsable, base.OriginalAssetReady = false, false
	if _, err := state.PlanRetry("DELETE", "DELETED", base); err != nil {
		t.Fatal("cleanup retry requires model or missing volume", err)
	}
	state.ActiveOperation = true
	if _, err := state.PlanRetry("DELETE", "DELETED", base); err == nil {
		t.Fatal("second active operation accepted")
	}
}

func TestRetryReservationsOnlyReplaceReleasedCompute(t *testing.T) {
	policy := QuotaPolicy{10, 2, 2000, 4 << 30, 100 << 30, 2}
	spec := Resources{2000, 4 << 30, 10 << 30}
	usage := QuotaUsage{Instances: 1, UserInstances: 1, Storage: 10 << 30}
	for _, held := range []QuotaUsage{{Instances: 1, Storage: 10 << 30}, {Instances: 1, Storage: 10 << 30, Running: 1, CPU: 2000, Memory: 4 << 30}} {
		all := usage
		all.Running, all.CPU, all.Memory = held.Running, held.CPU, held.Memory
		demands, err := retryReservations(policy, all, held, spec, true)
		if err != nil || demands["INSTANCE"] != 0 || demands["STORAGE"] != 0 || demands["CPU"] != 2000-held.CPU || demands["RUNNING"] != 1-held.Running {
			t.Fatalf("duplicate reservation: %v %v", demands, err)
		}
	}
	usage.Running, usage.CPU, usage.Memory = 1, 2000, 4<<30
	if _, err := retryReservations(policy, usage, QuotaUsage{Instances: 1, Storage: 10 << 30}, spec, true); err == nil {
		t.Fatal("quota exceeded by retry")
	}
}
