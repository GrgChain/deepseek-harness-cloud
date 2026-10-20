package operations

import (
	"errors"
	"sort"
)

var ErrRecoveryLedger = errors.New("recovery retention requires reviewed stable inventory, verified ownership and an isolated quarantined database")

type RecoveryQuotaHold struct {
	OwnerID    string `json:"owner_id"`
	InstanceID string `json:"instance_id"`
	Resource   string `json:"resource_type"`
	Amount     int64  `json:"amount"`
}
type RecoveryQuotaChange struct {
	OwnerID    string `json:"owner_id"`
	InstanceID string `json:"instance_id"`
	Resource   string `json:"resource_type"`
	Before     int64  `json:"before"`
	After      int64  `json:"after"`
}
type RecoveryRetentionReport struct {
	RunID            string                `json:"run_id,omitempty"`
	InventoryRunID   string                `json:"inventory_run_id"`
	SnapshotSHA256   string                `json:"snapshot_sha256"`
	Applied          bool                  `json:"applied"`
	Quarantined      bool                  `json:"quarantined"`
	PromotionAllowed bool                  `json:"promotion_allowed"`
	Before           InventoryQuota        `json:"before"`
	After            InventoryQuota        `json:"after"`
	Changes          []RecoveryQuotaChange `json:"changes"`
}

var recoveryResources = []string{"INSTANCE", "RUNNING", "CPU", "MEMORY", "STORAGE", "CONCURRENT"}

func (q *InventoryQuota) resource(name string) *int64 {
	switch name {
	case "INSTANCE":
		return &q.Instances
	case "RUNNING":
		return &q.Running
	case "CPU":
		return &q.CPU
	case "MEMORY":
		return &q.Memory
	case "STORAGE":
		return &q.Storage
	case "CONCURRENT":
		return &q.Concurrent
	}
	return nil
}
func (q *InventoryQuota) setAdded(name string, amount int64) bool {
	field := q.resource(name)
	if field == nil {
		return false
	}
	*field += amount
	return true
}
func quotaForResource(name string, amount int64) InventoryQuota {
	var q InventoryQuota
	q.setAdded(name, amount)
	return q
}

// PlanRecoveryRetention charges only a verified owner's deficit. It never
// releases an old reservation/hold and never turns an external orphan into an
// authorized platform instance. Workspace -> platform ownership comes from the DB.
func PlanRecoveryRetention(org string, expected []InventoryInstance, observed []ResourceObservation, holds []RecoveryQuotaHold, workspaceOwners map[string]string) ([]RecoveryQuotaChange, InventoryReport, error) {
	var empty InventoryReport
	if !uuid.MatchString(org) || len(holds) > 6000 || len(workspaceOwners) > 1000 {
		return nil, empty, ErrRecoveryLedger
	}
	validOwners := map[string]bool{}
	for workspaceOwner, owner := range workspaceOwners {
		if !uuid.MatchString(workspaceOwner) || !uuid.MatchString(owner) {
			return nil, empty, ErrRecoveryLedger
		}
		validOwners[owner] = true
	}
	owners := map[string]string{}
	bind := func(id, owner string) bool {
		if !uuid.MatchString(id) || !validOwners[owner] || (owners[id] != "" && owners[id] != owner) {
			return false
		}
		owners[id] = owner
		return true
	}
	base := map[string]InventoryQuota{}
	for _, e := range expected {
		if e.OrganizationID != org || workspaceOwners[e.WorkspaceOwner] != e.OwnerID || !bind(e.ID, e.OwnerID) {
			return nil, empty, ErrRecoveryLedger
		}
		base[e.ID] = e.Reserved
	}
	previous := map[string]InventoryQuota{}
	seenHolds := map[string]bool{}
	for _, h := range holds {
		key := h.InstanceID + ":" + h.Resource
		if !bind(h.InstanceID, h.OwnerID) || seenHolds[key] || h.Amount < 1 || h.Amount > 1<<50 {
			return nil, empty, ErrRecoveryLedger
		}
		seenHolds[key] = true
		q := previous[h.InstanceID]
		if !q.setAdded(h.Resource, h.Amount) {
			return nil, empty, ErrRecoveryLedger
		}
		previous[h.InstanceID] = q
	}
	for _, r := range observed {
		if !r.IdentityValid || !uuid.MatchString(r.InstanceID) {
			return nil, empty, ErrRecoveryLedger
		}
		if r.Kind == "ASSET" || r.Kind == "VOLUME" {
			if r.OrganizationID != org || !uuid.MatchString(r.AssetID) || !bind(r.InstanceID, r.OwnerID) || (r.Kind == "ASSET" && r.ExternalID != r.AssetID) || (r.Kind == "VOLUME" && r.ExternalID != "hc-"+r.AssetID) {
				return nil, empty, ErrRecoveryLedger
			}
		}
		if r.Kind == "WORKSPACE" || r.Kind == "CONTAINER" {
			if !bind(r.InstanceID, workspaceOwners[r.WorkspaceOwner]) {
				return nil, empty, ErrRecoveryLedger
			}
		}
	}
	for _, r := range observed {
		if owners[r.InstanceID] == "" {
			return nil, empty, ErrRecoveryLedger
		}
	}
	report, required, err := compareInventory(expected, observed, holds)
	if err != nil {
		return nil, empty, err
	}
	for _, f := range report.Findings {
		if f.Finding == "UNVERIFIED" || f.Finding == "IDENTITY_MISMATCH" || f.Finding == "UNRESOLVED_STATE" {
			return nil, report, ErrRecoveryLedger
		}
	}
	changes := []RecoveryQuotaChange{}
	for id, quota := range required {
		original, current := base[id], previous[id]
		for _, resource := range recoveryResources {
			before := *current.resource(resource)
			after := max(before, *quota.resource(resource)-*original.resource(resource))
			if after > 1<<50 {
				return nil, report, ErrRecoveryLedger
			}
			if after > before {
				changes = append(changes, RecoveryQuotaChange{owners[id], id, resource, before, after})
			}
		}
	}
	sort.Slice(changes, func(i, j int) bool {
		return changes[i].InstanceID+":"+changes[i].Resource < changes[j].InstanceID+":"+changes[j].Resource
	})
	return changes, report, nil
}
