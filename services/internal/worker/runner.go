package worker

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func retryReason(err error) string {
	switch {
	case errors.Is(err, ErrLostLease):
		return "LEASE_LOST"
	case errors.Is(err, ErrExecutionRevoked):
		return "EXECUTION_REVOKED"
	case errors.Is(err, ErrSuperseded):
		return "INTENT_SUPERSEDED"
	case errors.Is(err, ErrInvalidEvidence):
		return "INVALID_EVIDENCE"
	case errors.Is(err, ErrHostIdentity):
		return "HOST_IDENTITY_INVALID"
	case errors.Is(err, ErrHostCapacity):
		return "HOST_CAPACITY_UNAVAILABLE"
	case errors.Is(err, ErrStorageUnavailable):
		return "STORAGE_UNAVAILABLE"
	case errors.Is(err, ErrHostUnavailable):
		return "HOST_UNAVAILABLE"
	case errors.Is(err, ErrExternalPending):
		return "EXTERNAL_OPERATION_PENDING"
	case errors.Is(err, ErrBuildRejected):
		return "BUILD_REJECTED"
	case errors.Is(err, context.DeadlineExceeded):
		return "DEPENDENCY_TIMEOUT"
	case errors.Is(err, context.Canceled):
		return "CANCELED"
	default:
		return "DEPENDENCY_OR_DATABASE_ERROR"
	}
}

type WorkQueue interface {
	Claim(context.Context, string, int, bool) ([]Lease, error)
	Renew(context.Context, Lease) error
	Defer(context.Context, Lease, bool) error
}
type LeaseExecutor interface {
	Execute(context.Context, Lease) error
}
type runnerOptions struct{ Poll, Heartbeat, Grace time.Duration }
type Runner struct {
	queue    WorkQueue
	executor LeaseExecutor
	owner    string
	options  runnerOptions
}

func NewRunner(queue WorkQueue, executor LeaseExecutor) *Runner {
	return newRunner(queue, executor, "worker-"+webapi.ID(), runnerOptions{2 * time.Second, 15 * time.Second, 20 * time.Second})
}
func newRunner(queue WorkQueue, executor LeaseExecutor, owner string, options runnerOptions) *Runner {
	return &Runner{queue, executor, owner, options}
}
func (r *Runner) execute(ctx context.Context, lease Lease) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- r.executor.Execute(runCtx, lease) }()
	heartbeat := time.NewTicker(r.options.Heartbeat)
	defer heartbeat.Stop()
	lost := false
	for {
		select {
		case err := <-result:
			if err != nil && !lost && runCtx.Err() == nil && !errors.Is(err, ErrLostLease) {
				slog.Warn("Worker operation deferred after reconciliation", "operation_id", lease.ID, "instance_id", lease.InstanceID, "reason_code", retryReason(err))
				retry, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				_ = r.queue.Defer(retry, lease, true)
				cancel()
			}
			return
		case <-heartbeat.C:
			renew, cancelRenew := context.WithTimeout(runCtx, 3*time.Second)
			err := r.queue.Renew(renew, lease)
			cancelRenew()
			if err != nil {
				lost = true
				cancel()
			}
		case <-runCtx.Done():
			// Every executor call is bounded and observes cancellation. Host helper
			// cleanup uses up to ten seconds before releasing its instance guard.
			<-result
			return
		}
	}
}
func (r *Runner) Run(ctx context.Context) error {
	if r.queue == nil || r.executor == nil || !workerName.MatchString(r.owner) || r.options.Poll <= 0 || r.options.Heartbeat <= 0 || r.options.Grace <= 0 {
		return ErrInvalidEvidence
	}
	jobs, cancelJobs := context.WithCancel(context.Background())
	defer cancelJobs()
	done := make(chan bool, 3)
	normal, system := 0, 0
	var wg sync.WaitGroup
	ticker := time.NewTicker(r.options.Poll)
	defer ticker.Stop()
	lastWarning := time.Time{}
	launch := func(lease Lease, safety bool) {
		wg.Add(1)
		if safety {
			system++
		} else {
			normal++
		}
		go func() { defer wg.Done(); r.execute(jobs, lease); done <- safety }()
	}
	poll := func() {
		for _, lane := range []struct {
			safety bool
			slots  int
		}{{false, 2 - normal}, {true, 1 - system}} {
			if ctx.Err() != nil || lane.slots <= 0 {
				continue
			}
			claimCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			leases, err := r.queue.Claim(claimCtx, r.owner, lane.slots, lane.safety)
			cancel()
			if err != nil || len(leases) > lane.slots {
				if time.Since(lastWarning) >= 30*time.Second {
					slog.Warn("Worker durable queue temporarily unavailable")
					lastWarning = time.Now()
				}
				continue
			}
			for _, lease := range leases {
				if lane.safety != (lease.ActorKind == "SYSTEM") {
					return
				}
				launch(lease, lane.safety)
			}
		}
	}
	poll()
	for {
		select {
		case <-ctx.Done():
			finished := make(chan struct{})
			go func() { wg.Wait(); close(finished) }()
			select {
			case <-finished:
				return nil
			case <-time.After(r.options.Grace):
				cancelJobs()
			}
			select {
			case <-finished:
				return nil
			case <-time.After(10 * time.Second):
				return errors.New("worker shutdown deadline exceeded")
			}
		case safety := <-done:
			if safety {
				system--
			} else {
				normal--
			}
		case <-ticker.C:
			poll()
		}
	}
}
