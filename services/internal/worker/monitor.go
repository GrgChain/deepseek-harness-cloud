package worker

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

type HealthQueue interface {
	Due(context.Context, int) ([]string, error)
	Load(context.Context, string) (HealthPlan, error)
	Publish(context.Context, HealthPlan, HealthResult) (bool, error)
}
type SafetyScanner interface {
	Scan(context.Context, string, int) (string, error)
}
type monitorOptions struct{ Poll, Safety time.Duration }
type Monitor struct {
	store   HealthQueue
	checker HealthChecker
	safety  SafetyScanner
	options monitorOptions
}

func NewMonitor(store HealthQueue, checker HealthChecker, safety SafetyScanner) *Monitor {
	return newMonitor(store, checker, safety, monitorOptions{2 * time.Second, 10 * time.Second})
}
func newMonitor(store HealthQueue, checker HealthChecker, safety SafetyScanner, options monitorOptions) *Monitor {
	return &Monitor{store, checker, safety, options}
}
func (m *Monitor) probe(ctx context.Context, id string) {
	probe, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	plan, err := m.store.Load(probe, id)
	if err != nil {
		return
	}
	defer plan.Destroy()
	result, err := m.checker.Check(probe, plan)
	defer result.Destroy()
	if err != nil || ctx.Err() != nil {
		return
	}
	if probe.Err() != nil {
		result.Destroy()
		result = initialHealth()
		for d, v := range result.Checks {
			v.Reason = "HEALTH_TIMEOUT"
			result.Checks[d] = v
		}
	}
	finish, cancelFinish := context.WithTimeout(ctx, 3*time.Second)
	defer cancelFinish()
	_, _ = m.store.Publish(finish, plan, result)
}
func (m *Monitor) scan(ctx context.Context) {
	ticker := time.NewTicker(m.options.Safety)
	defer ticker.Stop()
	cursor := ""
	lastWarning := time.Time{}
	for {
		scan, cancel := context.WithTimeout(ctx, 5*time.Second)
		next, err := m.safety.Scan(scan, cursor, 10)
		cancel()
		cursor = next
		if err != nil && ctx.Err() == nil && time.Since(lastWarning) >= 30*time.Second {
			slog.Warn("Worker safety scan temporarily unavailable")
			lastWarning = time.Now()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (m *Monitor) Run(ctx context.Context) error {
	if m.store == nil || m.checker == nil || m.safety == nil || m.options.Poll <= 0 || m.options.Safety <= 0 {
		return ErrInvalidEvidence
	}
	var jobs sync.WaitGroup
	jobs.Add(1)
	go func() { defer jobs.Done(); m.scan(ctx) }()
	done := make(chan struct{}, 2)
	active := 0
	ticker := time.NewTicker(m.options.Poll)
	defer ticker.Stop()
	poll := func() {
		if ctx.Err() != nil || active >= 2 {
			return
		}
		query, cancel := context.WithTimeout(ctx, 2*time.Second)
		ids, err := m.store.Due(query, 2-active)
		cancel()
		if err != nil || len(ids) > 2-active {
			return
		}
		for _, id := range ids {
			active++
			jobs.Add(1)
			go func() { defer jobs.Done(); m.probe(ctx, id); done <- struct{}{} }()
		}
	}
	poll()
	for {
		select {
		case <-ctx.Done():
			jobs.Wait()
			return nil
		case <-done:
			active--
		case <-ticker.C:
			poll()
		}
	}
}
