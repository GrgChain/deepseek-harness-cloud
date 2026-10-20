package worker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fixtureQueue struct {
	mu                                           sync.Mutex
	next                                         int
	active, maxNormal, maxSystem, normal, system int
	renewFail                                    bool
	deferred                                     atomic.Int32
}

func (q *fixtureQueue) Claim(ctx context.Context, owner string, slots int, system bool) ([]Lease, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.next >= 3 {
		return []Lease{}, nil
	}
	q.next++
	kind := "USER"
	if system {
		kind = "SYSTEM"
		q.system++
		if q.system > q.maxSystem {
			q.maxSystem = q.system
		}
	} else {
		q.normal++
		if q.normal > q.maxNormal {
			q.maxNormal = q.normal
		}
	}
	return []Lease{{ID: kind, Owner: owner, ActorKind: kind, Token: 1}}, nil
}
func (q *fixtureQueue) Renew(context.Context, Lease) error {
	if q.renewFail {
		return ErrLostLease
	}
	return nil
}
func (q *fixtureQueue) Defer(context.Context, Lease, bool) error { q.deferred.Add(1); return nil }

type fixtureExecutor struct {
	run func(context.Context, Lease) error
}

func (e fixtureExecutor) Execute(ctx context.Context, l Lease) error { return e.run(ctx, l) }
func TestRunnerClaimsOnlyAvailableSlotsAndKeepsASafetyLane(t *testing.T) {
	queue := &fixtureQueue{}
	started := make(chan string, 3)
	release := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	executor := fixtureExecutor{run: func(ctx context.Context, l Lease) error {
		started <- l.ActorKind
		select {
		case <-release:
		case <-ctx.Done():
		}
		queue.mu.Lock()
		if l.ActorKind == "SYSTEM" {
			queue.system--
		} else {
			queue.normal--
		}
		queue.mu.Unlock()
		return nil
	}}
	runner := newRunner(queue, executor, "fixture-worker", runnerOptions{Poll: time.Millisecond, Heartbeat: time.Hour, Grace: time.Second})
	finished := make(chan error, 1)
	go func() { finished <- runner.Run(ctx) }()
	normal, system := 0, 0
	for range 3 {
		select {
		case kind := <-started:
			if kind == "SYSTEM" {
				system++
			} else {
				normal++
			}
		case <-time.After(time.Second):
			t.Fatal("bounded worker lanes did not execute")
		}
	}
	if normal != 2 || system != 1 {
		t.Fatalf("normal and safety slots: %d %d", normal, system)
	}
	cancel()
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	if queue.maxNormal > 2 || queue.maxSystem > 1 {
		t.Fatal("worker exceeded execution slots")
	}
}
func TestRunnerCancelsExecutionWhenRenewalLosesTheLease(t *testing.T) {
	queue := &fixtureQueue{next: 2, renewFail: true}
	canceled := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	executor := fixtureExecutor{run: func(ctx context.Context, l Lease) error { <-ctx.Done(); close(canceled); return ctx.Err() }}
	runner := newRunner(queue, executor, "fixture-worker", runnerOptions{Poll: time.Millisecond, Heartbeat: time.Millisecond, Grace: 20 * time.Millisecond})
	finished := make(chan error, 1)
	go func() { finished <- runner.Run(ctx) }()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("lost lease did not cancel execution")
	}
	cancel()
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if queue.deferred.Load() != 0 {
		t.Fatal("lost owner wrote retry state")
	}
}
func TestRunnerDurablyDefersUnfinishedExecution(t *testing.T) {
	queue := &fixtureQueue{next: 2}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	executor := fixtureExecutor{run: func(context.Context, Lease) error { return errors.New("bounded external call failed") }}
	runner := newRunner(queue, executor, "fixture-worker", runnerOptions{Poll: time.Millisecond, Heartbeat: time.Hour, Grace: 20 * time.Millisecond})
	finished := make(chan error, 1)
	go func() { finished <- runner.Run(ctx) }()
	deadline := time.After(time.Second)
	for queue.deferred.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("unfinished execution lost durable retry")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}

func TestRunnerShutdownDrainsThenCancelsWithoutReleasingUnknownWork(t *testing.T) {
	queue := &fixtureQueue{next: 2}
	started := make(chan struct{})
	stopped := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	executor := fixtureExecutor{run: func(ctx context.Context, l Lease) error {
		close(started)
		<-ctx.Done()
		close(stopped)
		return ctx.Err()
	}}
	runner := newRunner(queue, executor, "fixture-worker", runnerOptions{Poll: time.Millisecond, Heartbeat: time.Hour, Grace: 20 * time.Millisecond})
	finished := make(chan error, 1)
	go func() { finished <- runner.Run(ctx) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("job did not start")
	}
	cancel()
	select {
	case <-stopped:
		t.Fatal("shutdown skipped its drain period")
	case <-time.After(2 * time.Millisecond):
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown failed to cancel bounded execution")
	}
	if queue.deferred.Load() != 0 {
		t.Fatal("shutdown rewrote an externally unconfirmed result")
	}
}
