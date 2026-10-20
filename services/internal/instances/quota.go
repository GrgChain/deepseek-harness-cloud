package instances

import "errors"

var ErrQuotaExceeded = errors.New("resource quota is unavailable or exceeded")

type Resources struct{ CPU, Memory, Storage int64 }
type QuotaPolicy struct{ Instances, UserInstances, CPU, Memory, Storage, Concurrent int64 }
type QuotaUsage struct{ Instances, UserInstances, Running, CPU, Memory, Storage, Concurrent int64 }

// PlanReservation requires the application to hold the organization quota row
// lock and aggregate HELD/USED reservations plus recovery holds in that transaction.
// Stopped and retained data remain in usage; a start reserves only compute.
func (p QuotaPolicy) PlanReservation(create bool, usage QuotaUsage, spec Resources) (map[string]int64, error) {
	if spec.CPU <= 0 || spec.Memory <= 0 || spec.Storage <= 0 {
		return nil, ErrQuotaExceeded
	}
	demands := map[string]int64{"RUNNING": 1, "CPU": spec.CPU, "MEMORY": spec.Memory, "CONCURRENT": 1}
	if create {
		demands["INSTANCE"], demands["STORAGE"] = 1, spec.Storage
	}
	budgets := []struct{ limit, used, amount int64 }{
		{p.Instances, usage.Instances, demands["INSTANCE"]},
		{p.UserInstances, usage.UserInstances, demands["INSTANCE"]},
		// Running has no separate policy: every instance slot may run. Keep
		// recovery running evidence bounded by the same organization total.
		{p.Instances, usage.Running, demands["RUNNING"]},
		{p.CPU, usage.CPU, demands["CPU"]},
		{p.Memory, usage.Memory, demands["MEMORY"]},
		{p.Storage, usage.Storage, demands["STORAGE"]},
		{p.Concurrent, usage.Concurrent, demands["CONCURRENT"]},
	}
	for _, budget := range budgets {
		// Subtract before adding so large persisted counts cannot wrap into credit.
		if budget.limit < 1 || budget.used < 0 || budget.used > budget.limit || budget.amount > budget.limit-budget.used {
			return nil, ErrQuotaExceeded
		}
	}
	return demands, nil
}
