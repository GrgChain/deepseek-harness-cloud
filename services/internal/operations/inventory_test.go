package operations

import (
	"strings"
	"testing"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func TestRecoveryInventoryNeverReleasesMissingOrOrphanResources(t *testing.T) {
	org, owner, id, asset, ws, workspaceOwner, version := webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID()
	expected := []InventoryInstance{{ID: id, AssetID: asset, OrganizationID: org, OwnerID: owner, WorkspaceOwner: workspaceOwner, WorkspaceID: ws, TemplateVersion: version, State: "STOPPED", StorageStatus: "READY", Storage: 10 << 30, Reserved: InventoryQuota{Instances: 1, Storage: 10 << 30}}}
	observed := []ResourceObservation{
		{Kind: "ASSET", ExternalID: asset, InstanceID: id, AssetID: asset, OrganizationID: org, OwnerID: owner, Capacity: 10 << 30, IdentityValid: true},
		{Kind: "VOLUME", ExternalID: "hc-" + asset, InstanceID: id, AssetID: asset, OrganizationID: org, OwnerID: owner, Capacity: 10 << 30, IdentityValid: true},
		{Kind: "WORKSPACE", ExternalID: ws, InstanceID: id, WorkspaceOwner: workspaceOwner, TemplateVersion: version, Status: "stopped", IdentityValid: true},
	}
	report, err := CompareInventory(expected, observed)
	if err != nil || report.Blockers != 0 || report.Conservative.Storage != 10<<30 || report.PromotionAllowed {
		t.Fatal("matching disk and volume were not deduplicated", err, report)
	}
	orphan, orphanAsset := webapi.ID(), webapi.ID()
	observed = append(observed, ResourceObservation{Kind: "ASSET", ExternalID: orphanAsset, InstanceID: orphan, AssetID: orphanAsset, OrganizationID: org, OwnerID: owner, Capacity: 10 << 30, IdentityValid: true})
	report, err = CompareInventory(expected, observed)
	if err != nil || report.Blockers == 0 || report.Conservative.Storage != 20<<30 || report.Conservative.Instances < 2 {
		t.Fatal("old snapshot orphan was not held conservatively", err, report)
	}
	report, err = CompareInventory(expected, nil)
	if err != nil || report.Blockers == 0 || report.Conservative.Storage != 10<<30 || report.Conservative.Instances != 1 {
		t.Fatal("missing external resources released the old ledger", err, report)
	}
	observed[0].OwnerID = webapi.ID()
	observed = append(observed, ResourceObservation{Kind: "CONTAINER", ExternalID: strings.Repeat("a", 64), InstanceID: id, AssetID: asset, WorkspaceOwner: workspaceOwner, WorkspaceID: ws, Status: "running", CPU: 4000, Memory: 8 << 30, IdentityValid: false})
	report, err = CompareInventory(expected, observed)
	if err != nil || report.Blockers < 2 || report.Conservative.Running < 1 || report.Conservative.CPU < 4000 || report.Conservative.Memory < 8<<30 {
		t.Fatal("foreign owner or uncertain running compute escaped quarantine", err, report)
	}
}

func TestRecoveryInventoryCountsDistinctDisksAndDuplicateCompute(t *testing.T) {
	id, org, owner := webapi.ID(), webapi.ID(), webapi.ID()
	resources := []ResourceObservation{}
	for range 2 {
		asset := webapi.ID()
		resources = append(resources, ResourceObservation{Kind: "ASSET", ExternalID: asset, InstanceID: id, AssetID: asset, OrganizationID: org, OwnerID: owner, Capacity: 10 << 30, IdentityValid: true}, ResourceObservation{Kind: "VOLUME", ExternalID: "hc-" + asset, InstanceID: id, AssetID: asset, OrganizationID: org, OwnerID: owner, Capacity: 10 << 30, IdentityValid: true})
	}
	for _, idChar := range []string{"a", "b"} {
		resources = append(resources, ResourceObservation{Kind: "CONTAINER", ExternalID: strings.Repeat(idChar, 64), InstanceID: id, Status: "running", CPU: 2000, Memory: 4 << 30})
	}
	r, err := CompareInventory(nil, resources)
	if err != nil || r.Conservative.Storage != 20<<30 || r.Conservative.Instances != 1 || r.Conservative.Running != 2 || r.Conservative.CPU != 4000 || r.Conservative.Memory != 8<<30 {
		t.Fatal("multiple resources collapsed into one reservation", err, r.Conservative)
	}
}

func TestRecoveryInventoryRejectsDuplicateAndOversizedCensus(t *testing.T) {
	r := ResourceObservation{Kind: "ASSET", ExternalID: webapi.ID(), Capacity: 10 << 30}
	if _, err := CompareInventory(nil, []ResourceObservation{r, r}); err == nil {
		t.Fatal("duplicate external identity accepted")
	}
	if _, err := CompareInventory(nil, make([]ResourceObservation, 5001)); err == nil {
		t.Fatal("unbounded census accepted")
	}
}

func TestDeletedRetainedDiskDoesNotRecreateAnInstanceQuotaCommitment(t *testing.T) {
	id, asset, org, owner := webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID()
	expected := []InventoryInstance{{ID: id, AssetID: asset, OrganizationID: org, OwnerID: owner, State: "DELETED", StorageStatus: "RETAINED", Storage: 10 << 30, Reserved: InventoryQuota{Storage: 10 << 30}}}
	observed := []ResourceObservation{{Kind: "ASSET", ExternalID: asset, InstanceID: id, AssetID: asset, OrganizationID: org, OwnerID: owner, Capacity: 10 << 30, IdentityValid: true}, {Kind: "VOLUME", ExternalID: "hc-" + asset, InstanceID: id, AssetID: asset, OrganizationID: org, OwnerID: owner, Capacity: 10 << 30, IdentityValid: true}}
	r, err := CompareInventory(expected, observed)
	if err != nil || r.Blockers != 0 || r.Ledger != r.Conservative || r.Conservative.Instances != 0 || r.Conservative.Storage != 10<<30 {
		t.Fatal("a legitimately retained deleted disk reacquired instance quota", err, r.Conservative)
	}
	observed = append(observed, ResourceObservation{Kind: "WORKSPACE", ExternalID: webapi.ID(), InstanceID: id, WorkspaceOwner: owner, TemplateVersion: webapi.ID(), Status: "stopped", IdentityValid: true})
	r, err = CompareInventory(expected, observed)
	if err != nil || r.Blockers == 0 || r.Conservative.Instances != 1 {
		t.Fatal("unexpected surviving workspace lost its instance commitment", err, r.Conservative)
	}
}
