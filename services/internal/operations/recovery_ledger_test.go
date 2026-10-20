package operations

import (
	"math"
	"testing"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func TestRecoveryQuotaTotalsRejectOverflowWithoutChangingReceiver(t *testing.T) {
	q := InventoryQuota{Storage: math.MaxInt64}
	if q.add(InventoryQuota{Storage: 1}) || q.Storage != math.MaxInt64 {
		t.Fatal("overflow turned a commitment into quota credit")
	}
}

func TestRecoveryRetentionChargesOwnersWithoutFabricatingInstances(t *testing.T) {
	org, owner, workspaceOwner, id, asset := webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID()
	resources := []ResourceObservation{
		{Kind: "ASSET", ExternalID: asset, InstanceID: id, AssetID: asset, OrganizationID: org, OwnerID: owner, Capacity: 10 << 30, IdentityValid: true},
		{Kind: "VOLUME", ExternalID: "hc-" + asset, InstanceID: id, AssetID: asset, OrganizationID: org, OwnerID: owner, Capacity: 10 << 30, IdentityValid: true},
		{Kind: "WORKSPACE", ExternalID: webapi.ID(), InstanceID: id, WorkspaceOwner: workspaceOwner, TemplateVersion: webapi.ID(), Status: "stopped", IdentityValid: true},
	}
	owners := map[string]string{workspaceOwner: owner}
	changes, report, err := PlanRecoveryRetention(org, nil, resources, nil, owners)
	if err != nil || len(changes) != 2 || report.Conservative.Instances != 1 || report.Conservative.Storage != 10<<30 {
		t.Fatal("orphan disk/volume must count once and belong to the mapped owner", err, changes)
	}
	for _, change := range changes {
		if change.OwnerID != owner || change.InstanceID != id || change.Before != 0 {
			t.Fatal("hold lost original identity", change)
		}
	}
	holds := []RecoveryQuotaHold{{OwnerID: owner, InstanceID: id, Resource: "INSTANCE", Amount: 1}, {OwnerID: owner, InstanceID: id, Resource: "STORAGE", Amount: 10 << 30}}
	changes, report, err = PlanRecoveryRetention(org, nil, resources, holds, owners)
	if err != nil || len(changes) != 0 || report.Ledger.Storage != 10<<30 || report.Conservative.Storage != 10<<30 {
		t.Fatal("replay doubled a retained orphan", err)
	}
	// Disappearance is not permission to release charged resources.
	changes, report, err = PlanRecoveryRetention(org, nil, nil, holds, owners)
	if err != nil || len(changes) != 0 || report.Conservative.Storage != 10<<30 || report.Ledger.Instances != 1 {
		t.Fatal("missing resource released a recovery hold", err)
	}
	second := resources[0]
	second.AssetID = webapi.ID()
	second.ExternalID = second.AssetID
	changes, report, err = PlanRecoveryRetention(org, nil, append(resources, second), holds, owners)
	if err != nil || len(changes) != 1 || changes[0].Resource != "STORAGE" || changes[0].Before != 10<<30 || changes[0].After != 20<<30 || report.Conservative.Storage != 20<<30 {
		t.Fatal("distinct disk not charged additively", err, changes)
	}
	for _, name := range []string{"unknown owner", "conflicting owner", "unverified", "wrong organization", "invalid instance", "foreign hold"} {
		t.Run(name, func(t *testing.T) {
			r := append([]ResourceObservation(nil), resources...)
			h := append([]RecoveryQuotaHold(nil), holds...)
			mapping := map[string]string{workspaceOwner: owner}
			switch name {
			case "unknown owner":
				delete(mapping, workspaceOwner)
			case "conflicting owner":
				mapping[workspaceOwner] = webapi.ID()
			case "unverified":
				r[0].IdentityValid = false
			case "wrong organization":
				r[0].OrganizationID = webapi.ID()
			case "invalid instance":
				r[0].InstanceID = ""
			case "foreign hold":
				h[0].OwnerID = webapi.ID()
			}
			if _, _, err := PlanRecoveryRetention(org, nil, r, h, mapping); err == nil {
				t.Fatal("unowned retention accepted")
			}
		})
	}
}

func TestRecoveryRetentionAddsOnlyDeficitAndPreservesExistingLedger(t *testing.T) {
	org, owner, workspaceOwner, id, asset, workspace, version := webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID(), webapi.ID()
	expected := []InventoryInstance{{ID: id, OrganizationID: org, OwnerID: owner, WorkspaceOwner: workspaceOwner, AssetID: asset, WorkspaceID: workspace, TemplateVersion: version, State: "STOPPED", StorageStatus: "READY", Storage: 10 << 30, Reserved: InventoryQuota{Instances: 1, Storage: 5 << 30}}}
	resources := []ResourceObservation{{Kind: "ASSET", ExternalID: asset, InstanceID: id, AssetID: asset, OrganizationID: org, OwnerID: owner, Capacity: 10 << 30, IdentityValid: true}, {Kind: "VOLUME", ExternalID: "hc-" + asset, InstanceID: id, AssetID: asset, OrganizationID: org, OwnerID: owner, Capacity: 10 << 30, IdentityValid: true}, {Kind: "WORKSPACE", ExternalID: workspace, InstanceID: id, WorkspaceOwner: workspaceOwner, TemplateVersion: version, Status: "stopped", IdentityValid: true}}
	changes, report, err := PlanRecoveryRetention(org, expected, resources, nil, map[string]string{workspaceOwner: owner})
	if err != nil || len(changes) != 1 || changes[0].After != 5<<30 || report.Conservative.Storage != 10<<30 {
		t.Fatal("deficit did not preserve the old ledger", err, changes)
	}
	if expected[0].Reserved.Storage != 5<<30 {
		t.Fatal("planner mutated input ledger")
	}
	resources = append(resources, ResourceObservation{Kind: "CONTAINER", ExternalID: "aaaaaaaaaaaaaaaa", InstanceID: id, AssetID: asset, WorkspaceOwner: workspaceOwner, WorkspaceID: workspace, CPU: 4000, Memory: 8 << 30, Status: "running", IdentityValid: true})
	changes, report, err = PlanRecoveryRetention(org, expected, resources, nil, map[string]string{workspaceOwner: owner})
	if err != nil || report.Conservative.Running != 1 || report.Conservative.CPU != 4000 || report.Conservative.Memory != 8<<30 || len(changes) != 4 {
		t.Fatal("retained live compute escaped conservative charge", err)
	}
}
