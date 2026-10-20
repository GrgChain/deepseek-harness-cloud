package worker

import (
	"context"
	"testing"
	"time"
)

type usageHostFixture struct {
	probe func(context.Context, ExecutionPlan) (StorageUsage, error)
}

func (h usageHostFixture) Acquire(context.Context, string) (func(), error) { return func() {}, nil }
func (h usageHostFixture) StorageUsage(ctx context.Context, p ExecutionPlan) (StorageUsage, error) {
	return h.probe(ctx, p)
}
func TestUsageCollectorPreservesObservationsAndRejectsSupersededResults(t *testing.T) {
	f := newMonitorFixture(t)
	id, _ := f.instance(t)
	host := usageHostFixture{probe: func(ctx context.Context, p ExecutionPlan) (StorageUsage, error) {
		return StorageUsage{Used: 1 << 30, Available: 8 << 30}, nil
	}}
	collector := NewStorageUsageCollector(f.pool, host)
	collector.collect(f.ctx, id)
	var used, available int64
	var sampled time.Time
	var failed bool
	read := func() {
		t.Helper()
		if e := f.admin.QueryRow(f.ctx, `SELECT usage_used_bytes,usage_available_bytes,usage_sampled_at,usage_error FROM storage_assets WHERE instance_id=$1`, id).Scan(&used, &available, &sampled, &failed); e != nil {
			t.Fatal(e)
		}
	}
	read()
	if used != 1<<30 || available != 8<<30 || failed {
		t.Fatal("trusted usage not published", used, available, failed)
	}
	previous := sampled
	collector.host = usageHostFixture{probe: func(context.Context, ExecutionPlan) (StorageUsage, error) {
		return StorageUsage{}, context.DeadlineExceeded
	}}
	collector.collect(f.ctx, id)
	read()
	if !failed || !sampled.Equal(previous) || used != 1<<30 {
		t.Fatal("failed collection replaced last observation")
	}
	collector.host = usageHostFixture{probe: func(context.Context, ExecutionPlan) (StorageUsage, error) {
		f.exec(t, `UPDATE instances SET version=version+1,desired_state='STOPPED' WHERE id=$1`, id)
		return StorageUsage{Used: 2 << 30, Available: 7 << 30}, nil
	}}
	collector.collect(f.ctx, id)
	read()
	if !sampled.Equal(previous) || used != 1<<30 {
		t.Fatal("superseded usage was published")
	}
	var calls int
	collector.host = usageHostFixture{probe: func(context.Context, ExecutionPlan) (StorageUsage, error) { calls++; return StorageUsage{}, nil }}
	collector.collect(f.ctx, id)
	if calls != 0 {
		t.Fatal("stopped compute was probed")
	}
}
