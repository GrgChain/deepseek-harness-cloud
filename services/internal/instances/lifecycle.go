package instances

import (
	"errors"
	"math"
)

type Action string

const (
	Start     Action = "START"
	Stop      Action = "STOP"
	Delete    Action = "DELETE"
	Configure Action = "CONFIGURE"
)

var (
	ErrStateConflict        = errors.New("instance cannot accept this command")
	ErrVersionConflict      = errors.New("instance version changed")
	ErrStorageMissing       = errors.New("original storage is unavailable")
	ErrConfigurationInvalid = errors.New("model configuration is unavailable")
)

// Lifecycle is the persisted aggregate state. Planning has no external side
// effects; the application commits its result with the operation and Outbox.
type Lifecycle struct {
	Observed, Desired         string
	Version, AccessGeneration int64
	ActiveOperation           bool
}

// ResourceFacts come from authoritative, generation-bound reconciliation, never
// a browser-provided flag. An ERROR alone says nothing about external compute.
type ResourceFacts struct {
	Reconciled, ComputePresent, OriginalAssetReady, ModelUsable bool
}

func (state Lifecycle) Plan(action Action, expectedVersion int64, facts ResourceFacts) (Lifecycle, error) {
	if state.ActiveOperation || state.Version < 1 || state.Version == math.MaxInt64 || state.AccessGeneration < 1 || state.AccessGeneration == math.MaxInt64 {
		return Lifecycle{}, ErrStateConflict
	}
	if action == Configure && expectedVersion != state.Version {
		return Lifecycle{}, ErrVersionConflict
	}
	running, stopped := state.Observed == "RUNNING", state.Observed == "STOPPED"
	reconciled := state.Observed == "ERROR" && facts.Reconciled
	next := state
	switch action {
	case Start:
		if !stopped && !(reconciled && !facts.ComputePresent) {
			return Lifecycle{}, ErrStateConflict
		}
		if !facts.OriginalAssetReady {
			return Lifecycle{}, ErrStorageMissing
		}
		if !facts.ModelUsable {
			return Lifecycle{}, ErrConfigurationInvalid
		}
		next.Observed, next.Desired = "STARTING", "RUNNING"
	case Stop:
		if !running && !(reconciled && facts.ComputePresent) {
			return Lifecycle{}, ErrStateConflict
		}
		next.Observed, next.Desired = "STOPPING", "STOPPED"
		next.AccessGeneration++
	case Delete:
		if !running && !stopped && !reconciled {
			return Lifecycle{}, ErrStateConflict
		}
		next.Observed, next.Desired = "DELETING", "DELETED"
		next.AccessGeneration++
	case Configure:
		if !running && !stopped {
			return Lifecycle{}, ErrStateConflict
		}
		if !facts.ModelUsable {
			return Lifecycle{}, ErrConfigurationInvalid
		}
		if running {
			next.Observed = "STOPPING"
		}
		next.AccessGeneration++
	default:
		return Lifecycle{}, ErrStateConflict
	}
	next.Version++
	next.ActiveOperation = true
	return next, nil
}
