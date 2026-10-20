package instances

import (
	"errors"
	"math"
	"testing"
)

func TestInstanceLimitAllowsEveryInstanceToRun(t *testing.T) {
	policy := QuotaPolicy{Instances: 2, UserInstances: 2, CPU: 2000, Memory: 4 << 30, Storage: 20 << 30, Concurrent: 2}
	spec := Resources{CPU: 1000, Memory: 2 << 30, Storage: 10 << 30}
	usage := QuotaUsage{}
	for range 2 {
		demands, err := policy.PlanReservation(true, usage, spec)
		if err != nil {
			t.Fatalf("an available instance slot must also allow running: %v", err)
		}
		usage.Instances += demands["INSTANCE"]
		usage.UserInstances += demands["INSTANCE"]
		usage.Running += demands["RUNNING"]
		usage.CPU += demands["CPU"]
		usage.Memory += demands["MEMORY"]
		usage.Storage += demands["STORAGE"]
	}
	if _, err := policy.PlanReservation(true, usage, spec); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("a third instance must still exceed quota: %v", err)
	}
	// Stop one instance, retaining its instance and disk reservations, then
	// start it again while the other instance remains running.
	usage.Running--
	usage.CPU -= spec.CPU
	usage.Memory -= spec.Memory
	if _, err := policy.PlanReservation(false, usage, spec); err != nil {
		t.Fatalf("all existing instance slots must be startable: %v", err)
	}
}

func TestQuotaReservesCreateAndStartWithoutDoubleCountingRetainedData(t *testing.T) {
	policy := QuotaPolicy{Instances: 10, UserInstances: 2, CPU: 10000, Memory: 20 << 30, Storage: 100 << 30, Concurrent: 2}
	spec := Resources{CPU: 2000, Memory: 4 << 30, Storage: 10 << 30}
	usage := QuotaUsage{Instances: 1, UserInstances: 1, Storage: 10 << 30}
	create, err := policy.PlanReservation(true, usage, spec)
	if err != nil || create["INSTANCE"] != 1 || create["STORAGE"] != 10<<30 || create["CPU"] != 2000 || len(create) != 6 {
		t.Fatalf("create: %v %v", create, err)
	}
	// Starting a stopped instance does not reserve its retained instance/data twice.
	usage.UserInstances = 2
	usage.Instances = 10
	usage.Storage = 100 << 30
	start, err := policy.PlanReservation(false, usage, spec)
	if err != nil || start["INSTANCE"] != 0 || start["STORAGE"] != 0 || len(start) != 4 {
		t.Fatalf("start: %v %v", start, err)
	}
}

func TestQuotaRejectsEverySaturatedBudgetAndCorruptCounts(t *testing.T) {
	p := QuotaPolicy{Instances: 10, UserInstances: 2, CPU: 10000, Memory: 20 << 30, Storage: 100 << 30, Concurrent: 2}
	spec := Resources{CPU: 2000, Memory: 4 << 30, Storage: 10 << 30}
	cases := []QuotaUsage{
		{Instances: 10}, {UserInstances: 2}, {CPU: 9000}, {Memory: 19 << 30}, {Storage: 95 << 30}, {Concurrent: 2},
		{Instances: -1}, {UserInstances: -1}, {Running: -1}, {CPU: -1}, {Memory: -1}, {Storage: -1}, {Concurrent: -1},
		{CPU: math.MaxInt64},
	}
	for _, usage := range cases {
		if _, err := p.PlanReservation(true, usage, spec); !errors.Is(err, ErrQuotaExceeded) {
			t.Fatalf("quota accepted %+v: %v", usage, err)
		}
	}
	for _, invalid := range []Resources{{CPU: 0, Memory: 4 << 30, Storage: 10 << 30}, {CPU: 2000, Memory: 0, Storage: 10 << 30}, {CPU: 2000, Memory: 4 << 30, Storage: -1}} {
		if _, err := p.PlanReservation(true, QuotaUsage{}, invalid); err == nil {
			t.Fatalf("invalid resource accepted %+v", invalid)
		}
	}
}
