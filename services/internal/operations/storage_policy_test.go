package operations

import (
	"testing"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func TestStorageMaintenancePoliciesPreserveOwnerAssetAndRetention(t *testing.T) {
	now := time.Now().UTC()
	source := AssetState{ID: webapi.ID(), InstanceID: webapi.ID(), OrganizationID: webapi.ID(), OwnerID: webapi.ID(), FilesystemID: "", Capacity: 10 << 30, Status: "RETAINED", Observed: "DELETED", Desired: "DELETED", StorageReserved: true, RetentionUntil: now.Add(-time.Hour), DeletedAt: now.Add(-8 * 24 * time.Hour)}
	source.FilesystemID = source.ID
	target := source
	target.ID = webapi.ID()
	target.FilesystemID = target.ID
	target.InstanceID = webapi.ID()
	target.Status = "READY"
	target.Observed = "STOPPED"
	target.Desired = "STOPPED"
	target.ModelUsable = true
	if err := CheckBackup(source); err != nil {
		t.Fatal(err)
	}
	if err := CheckStorageRestore(source, target); err != nil {
		t.Fatal(err)
	}
	if err := CheckReclaim(source, now); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*AssetState){func(s *AssetState) { s.OwnerID = webapi.ID() }, func(s *AssetState) { s.Capacity = 20 << 30 }, func(s *AssetState) { s.ID = source.ID }, func(s *AssetState) { s.InstanceID = source.InstanceID }, func(s *AssetState) { s.Observed = "RUNNING" }, func(s *AssetState) { s.ActiveOperation = true }, func(s *AssetState) { s.StorageReserved = false }, func(s *AssetState) { s.ModelUsable = false }, func(s *AssetState) { s.LegalHold = true }} {
		changed := target
		change(&changed)
		if CheckStorageRestore(source, changed) == nil {
			t.Fatal("invalid restore target accepted")
		}
	}
	for _, gib := range []int64{1, 20, 100} {
		sized := source
		sized.Capacity = gib << 30
		if CheckBackup(sized) != nil || CheckReclaim(sized, now) != nil {
			t.Fatal("supported storage maintenance rejected", gib)
		}
	}
	for _, change := range []func(*AssetState){func(s *AssetState) { s.LegalHold = true }, func(s *AssetState) { s.RetentionUntil = now.Add(time.Hour) }, func(s *AssetState) { s.DeletedAt = now.Add(-6 * 24 * time.Hour) }, func(s *AssetState) { s.DeletedAt = time.Time{} }, func(s *AssetState) { s.ActiveOperation = true }, func(s *AssetState) { s.Status = "READY" }, func(s *AssetState) { s.Observed = "STOPPED" }} {
		changed := source
		change(&changed)
		if CheckReclaim(changed, now) == nil {
			t.Fatal("protected retained asset accepted for reclamation")
		}
	}
}
