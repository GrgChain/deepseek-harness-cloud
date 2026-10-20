package instances

import "time"

type Dimension string
type Status string

const (
	Application   Dimension = "application"
	Storage       Dimension = "storage"
	Configuration Dimension = "configuration"
	Access        Dimension = "access"
	Model         Dimension = "model"
	Healthy       Status    = "HEALTHY"
	Unhealthy     Status    = "UNHEALTHY"
)

func Dimensions() []Dimension { return []Dimension{Application, Storage, Configuration, Access, Model} }

type Check struct {
	Status    Status
	CheckedAt time.Time
	ExpiresAt time.Time
}

func (c Check) fresh(now time.Time) bool {
	return c.Status == Healthy && !c.CheckedAt.IsZero() && !c.CheckedAt.After(now) && c.ExpiresAt.After(now) && c.ExpiresAt.After(c.CheckedAt)
}

type HealthSet struct{ checks map[Dimension]Check }

func NewHealthSet(checks map[Dimension]Check) HealthSet {
	copy := make(map[Dimension]Check, len(checks))
	for dimension, check := range checks {
		copy[dimension] = check
	}
	return HealthSet{checks: copy}
}

func (h HealthSet) Ready(state string, now time.Time) bool {
	return h.CanEnter(state, true, now) && h.checks[Model].fresh(now)
}

// Authorization must be resolved from current authoritative state before this call.
func (h HealthSet) CanEnter(state string, authorizedOwner bool, now time.Time) bool {
	if state != "RUNNING" || !authorizedOwner {
		return false
	}
	for _, d := range []Dimension{Application, Storage, Configuration, Access} {
		if !h.checks[d].fresh(now) {
			return false
		}
	}
	return true
}
