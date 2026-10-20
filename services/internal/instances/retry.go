package instances

import "math"

func retryDiagnostic(code string) bool {
	switch code {
	case "MODEL_UNAVAILABLE", "WORKSPACE_UNAVAILABLE", "STORAGE_MISSING", "CONFIGURATION_INVALID", "DEPENDENCY_UNAVAILABLE", "INTERNAL_ERROR":
		return true
	}
	return false
}

type RetryFacts struct{ Latest, Failed, User, Reconciled, Diagnostic, OriginalAssetReady, ModelUsable bool }

func (s Lifecycle) PlanRetry(action, target string, f RetryFacts) (Lifecycle, error) {
	if !f.Latest || !f.Failed || !f.User || !f.Reconciled || !f.Diagnostic || s.ActiveOperation || (s.Observed != "ERROR" && s.Observed != "STOPPED") || s.Version < 1 || s.Version == math.MaxInt64 || s.AccessGeneration < 1 || s.AccessGeneration == math.MaxInt64 {
		return Lifecycle{}, ErrStateConflict
	}
	next := s
	switch action {
	case "CREATE", "START", "CONFIGURE":
		if !f.OriginalAssetReady {
			return Lifecycle{}, ErrStorageMissing
		}
		if !f.ModelUsable {
			return Lifecycle{}, ErrConfigurationInvalid
		}
		if target != "RUNNING" && !(action == "CONFIGURE" && target == "STOPPED") {
			return Lifecycle{}, ErrStateConflict
		}
		if target == "RUNNING" {
			next.Observed = "STARTING"
			if action == "CREATE" {
				next.Observed = "CREATING"
			}
		} else {
			next.Observed = "STOPPED"
		}
	case "STOP":
		if target != "STOPPED" {
			return Lifecycle{}, ErrStateConflict
		}
		next.Observed = "STOPPING"
	case "DELETE":
		if target != "DELETED" {
			return Lifecycle{}, ErrStateConflict
		}
		next.Observed = "DELETING"
	default:
		return Lifecycle{}, ErrStateConflict
	}
	next.Desired = target
	next.Version++
	next.AccessGeneration++
	next.ActiveOperation = true
	return next, nil
}

func retryReservations(p QuotaPolicy, usage, own QuotaUsage, spec Resources, running bool) (map[string]int64, error) {
	if own.Instances != 1 || own.Storage != spec.Storage {
		return nil, ErrQuotaExceeded
	}
	if !running {
		return map[string]int64{}, nil
	}
	if (own.Running != 0 && own.Running != 1) || (own.CPU != 0 && own.CPU != spec.CPU) || (own.Memory != 0 && own.Memory != spec.Memory) || own.Concurrent < 0 || own.Concurrent > 1 || usage.Running < own.Running || usage.CPU < own.CPU || usage.Memory < own.Memory || usage.Concurrent < own.Concurrent {
		return nil, ErrQuotaExceeded
	}
	usage.Running -= own.Running
	usage.CPU -= own.CPU
	usage.Memory -= own.Memory
	usage.Concurrent -= own.Concurrent
	demands, err := p.PlanReservation(false, usage, spec)
	if err != nil {
		return nil, err
	}
	demands["RUNNING"] -= own.Running
	demands["CPU"] -= own.CPU
	demands["MEMORY"] -= own.Memory
	demands["CONCURRENT"] -= own.Concurrent
	return demands, nil
}
