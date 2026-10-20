package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/instances"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func TestAssetOperationsRejectUnboundRequestsBeforeDocker(t *testing.T) {
	h := &LocalHost{}
	if err := h.AssertAssetIdle(context.Background(), ExecutionPlan{}); err == nil {
		t.Fatal("invalid asset reached host")
	}
	if err := h.RestoreAsset(context.Background(), ExecutionPlan{InstanceID: webapi.ID()}, ExecutionPlan{}, "invalid", nil); err == nil {
		t.Fatal("unbound restore reached host")
	}
	if err := h.ReclaimAsset(context.Background(), ExecutionPlan{}); err == nil {
		t.Fatal("unbound reclaim reached host")
	}
}

func TestBrowseStorageRejectsNonFixedRootBeforeHostAccess(t *testing.T) {
	h := &LocalHost{}
	_, _, err := h.BrowseStorage(context.Background(), ExecutionPlan{}, instances.StorageBrowseInput{Action: "LIST", Root: "etc"})
	if !errors.Is(err, ErrHostIdentity) {
		t.Fatalf("unexpected browse result: %v", err)
	}
}
