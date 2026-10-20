package worker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type monitorQueueFixture struct {
	mu        sync.Mutex
	next      int
	published atomic.Int32
}

func (q *monitorQueueFixture) Due(ctx context.Context, slots int) ([]string, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	ids := []string{}
	for q.next < 3 && len(ids) < slots {
		q.next++
		ids = append(ids, "fixture")
	}
	return ids, nil
}
func (q *monitorQueueFixture) Load(context.Context, string) (HealthPlan, error) {
	return HealthPlan{}, nil
}
func (q *monitorQueueFixture) Publish(context.Context, HealthPlan, HealthResult) (bool, error) {
	q.published.Add(1)
	return true, nil
}

type healthCheckerFixture struct {
	run func(context.Context, HealthPlan) (HealthResult, error)
}

func (f healthCheckerFixture) Check(ctx context.Context, p HealthPlan) (HealthResult, error) {
	return f.run(ctx, p)
}

type scannerFixture struct {
	mu      sync.Mutex
	calls   int
	cursors []string
}

func (s *scannerFixture) Scan(ctx context.Context, cursor string, limit int) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.cursors = append(s.cursors, cursor)
	return "next", errors.New("fixture scan failure")
}
func TestMonitorBoundsHealthWorkAndContinuesSafetyCursorAfterFailure(t *testing.T) {
	queue := &monitorQueueFixture{}
	scanner := &scannerFixture{}
	var active, max atomic.Int32
	started := make(chan struct{}, 3)
	checker := healthCheckerFixture{run: func(ctx context.Context, p HealthPlan) (HealthResult, error) {
		n := active.Add(1)
		for old := max.Load(); n > old; old = max.Load() {
			if max.CompareAndSwap(old, n) {
				break
			}
		}
		defer active.Add(-1)
		started <- struct{}{}
		<-ctx.Done()
		return HealthResult{}, ctx.Err()
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	monitor := newMonitor(queue, checker, scanner, monitorOptions{Poll: time.Millisecond, Safety: time.Millisecond})
	finished := make(chan error, 1)
	go func() { finished <- monitor.Run(ctx) }()
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("health jobs did not start")
		}
	}
	select {
	case <-started:
		t.Fatal("health concurrency exceeded two")
	case <-time.After(10 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("monitor shutdown did not cancel health")
	}
	if max.Load() > 2 || queue.published.Load() != 0 {
		t.Fatal("monitor exceeded slots or published a cancelled check")
	}
	scanner.mu.Lock()
	defer scanner.mu.Unlock()
	if len(scanner.cursors) < 2 || scanner.cursors[1] != "next" {
		t.Fatal("safety scan kept retrying the same cursor after a per-instance failure")
	}
}
