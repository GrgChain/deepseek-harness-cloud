package operations

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"sort"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/worker"
)

var ErrInventory = errors.New("recovery inventory incomplete, changing or outside isolated quarantine; serving resources remain unchanged")

type ResourceObservation = worker.ResourceObservation

type InventoryQuota struct {
	Instances  int64 `json:"instances"`
	Running    int64 `json:"running"`
	CPU        int64 `json:"cpu_millicores"`
	Memory     int64 `json:"memory_bytes"`
	Storage    int64 `json:"storage_bytes"`
	Concurrent int64 `json:"concurrent"`
}
type InventoryInstance struct {
	ID, AssetID, OrganizationID, OwnerID, WorkspaceOwner, WorkspaceID, TemplateVersion, State, StorageStatus string
	Storage                                                                                                  int64
	Reserved                                                                                                 InventoryQuota
	ActiveOperation                                                                                          bool
}
type InventoryFinding struct {
	ResourceObservation
	Finding string `json:"finding"`
}
type InventoryReport struct {
	RunID            string             `json:"run_id,omitempty"`
	Applied          bool               `json:"applied"`
	Quarantined      bool               `json:"quarantined"`
	PromotionAllowed bool               `json:"promotion_allowed"`
	SnapshotSHA256   string             `json:"snapshot_sha256"`
	Blockers         int                `json:"blockers"`
	Ledger           InventoryQuota     `json:"ledger"`
	Conservative     InventoryQuota     `json:"conservative"`
	Findings         []InventoryFinding `json:"findings"`
}

// CompareInventory takes the maximum of old ledger commitments and observed
// physical resources, per identity. Absence can never release an old hold.
func CompareInventory(expected []InventoryInstance, observed []ResourceObservation) (InventoryReport, error) {
	return compareInventoryReport(expected, observed, nil)
}

func compareInventoryReport(expected []InventoryInstance, observed []ResourceObservation, holds []RecoveryQuotaHold) (InventoryReport, error) {
	report, _, err := compareInventory(expected, observed, holds)
	return report, err
}

func compareInventory(expected []InventoryInstance, observed []ResourceObservation, holds []RecoveryQuotaHold) (InventoryReport, map[string]InventoryQuota, error) {
	report := InventoryReport{Quarantined: true, Findings: []InventoryFinding{}}
	if len(expected) > 1000 || len(observed) > 5000 || len(holds) > 6000 {
		return report, nil, ErrInventory
	}
	known := map[string]InventoryInstance{}
	quota := map[string]InventoryQuota{}
	for _, h := range holds {
		if !uuid.MatchString(h.InstanceID) || !uuid.MatchString(h.OwnerID) || h.Amount < 1 || h.Amount > 1<<50 {
			return report, nil, ErrInventory
		}
		q := quota[h.InstanceID]
		if !q.setAdded(h.Resource, h.Amount) {
			return report, nil, ErrInventory
		}
		quota[h.InstanceID] = q
		if !report.Ledger.add(quotaForResource(h.Resource, h.Amount)) {
			return report, nil, ErrInventory
		}
	}
	physicalStorage := map[string]struct {
		instance string
		bytes    int64
	}{}
	physicalCompute := map[string]InventoryQuota{}
	for _, e := range expected {
		if !uuid.MatchString(e.ID) || known[e.ID].ID != "" {
			return report, nil, ErrInventory
		}
		known[e.ID] = e
		q := e.Reserved
		for _, name := range recoveryResources {
			if *q.resource(name) < 0 || *q.resource(name) > 1<<50 {
				return report, nil, ErrInventory
			}
		}
		if !report.Ledger.add(q) || !q.add(quota[e.ID]) {
			return report, nil, ErrInventory
		}
		if e.State != "DELETED" {
			q.Instances = max(q.Instances, 1)
		}
		if e.StorageStatus != "DELETED" {
			q.Storage = max(q.Storage, e.Storage)
		}
		quota[e.ID] = q
		if e.ActiveOperation || (e.State != "STOPPED" && e.State != "DELETED") || (e.StorageStatus != "READY" && e.StorageStatus != "RETAINED" && e.StorageStatus != "DELETED") {
			report.Findings = append(report.Findings, InventoryFinding{ResourceObservation: ResourceObservation{Kind: "DATABASE", ExternalID: e.ID, InstanceID: e.ID}, Finding: "UNRESOLVED_STATE"})
		}
	}
	seen := map[string]bool{}
	present := map[string]bool{}
	for _, r := range observed {
		if r.Kind != "ASSET" && r.Kind != "VOLUME" && r.Kind != "WORKSPACE" && r.Kind != "CONTAINER" && r.Kind != "NETWORK" {
			return report, nil, ErrInventory
		}
		if len(r.ExternalID) == 0 || len(r.ExternalID) > 80 || r.Capacity < 0 || r.Capacity > 1<<50 || r.CPU < 0 || r.CPU > 1<<40 || r.Memory < 0 || r.Memory > 1<<50 {
			return report, nil, ErrInventory
		}
		key := r.Kind + ":" + r.ExternalID
		if seen[key] {
			return report, nil, ErrInventory
		}
		seen[key] = true
		e, exists := known[r.InstanceID]
		finding := "MATCHED"
		if !r.IdentityValid {
			finding = "UNVERIFIED"
		} else if !exists {
			finding = "ORPHAN"
		} else {
			valid := true
			switch r.Kind {
			case "ASSET", "VOLUME":
				valid = r.AssetID == e.AssetID && r.OrganizationID == e.OrganizationID && r.OwnerID == e.OwnerID && r.Capacity == e.Storage && e.StorageStatus != "DELETED"
			case "WORKSPACE":
				valid = r.ExternalID == e.WorkspaceID && r.WorkspaceOwner == e.WorkspaceOwner && r.TemplateVersion == e.TemplateVersion && e.State != "DELETED"
			case "CONTAINER":
				valid = r.AssetID == e.AssetID && r.WorkspaceOwner == e.WorkspaceOwner && r.WorkspaceID == e.WorkspaceID
			case "NETWORK":
				valid = false // A fully stopped instance has no network.
			}
			if !valid {
				finding = "IDENTITY_MISMATCH"
			}
		}
		if (r.Kind == "CONTAINER") || (r.Kind == "WORKSPACE" && r.Status != "stopped" && r.Status != "deleted") {
			if finding == "MATCHED" {
				finding = "ACTIVE_COMPUTE"
			}
		}
		present[r.Kind+":"+r.InstanceID] = finding == "MATCHED"
		qkey := r.InstanceID
		if !uuid.MatchString(qkey) {
			qkey = key
		}
		q := quota[qkey]
		// A verified retained disk for a known soft-deleted instance consumes
		// storage only. A surviving workspace/compute or an unknown disk still
		// needs a conservative instance commitment.
		if !exists || e.State != "DELETED" || (r.Kind != "ASSET" && r.Kind != "VOLUME") {
			q.Instances = max(q.Instances, 1)
		}
		if r.Kind == "ASSET" || r.Kind == "VOLUME" {
			storageKey := r.OrganizationID + ":" + r.OwnerID + ":" + r.InstanceID + ":" + r.AssetID
			// Invalid disk/volume pairing cannot be deduplicated safely.
			if finding == "UNVERIFIED" || finding == "IDENTITY_MISMATCH" {
				storageKey = key
			}
			value := physicalStorage[storageKey]
			value.instance = qkey
			capacity := r.Capacity
			if !r.IdentityValid {
				capacity = max(capacity, 100<<30)
			}
			value.bytes = max(value.bytes, capacity)
			physicalStorage[storageKey] = value
		}
		if r.Kind == "CONTAINER" || (r.Kind == "WORKSPACE" && r.Status != "stopped" && r.Status != "deleted") {
			if r.Kind == "CONTAINER" {
				v := physicalCompute[qkey]
				v.Running++
				v.CPU += max(r.CPU, 2000)
				v.Memory += max(r.Memory, 4<<30)
				physicalCompute[qkey] = v
			} else {
				q.Running = max(q.Running, 1)
				q.CPU = max(q.CPU, 2000)
				q.Memory = max(q.Memory, 4<<30)
			}
		}
		quota[qkey] = q
		report.Findings = append(report.Findings, InventoryFinding{r, finding})
	}
	for _, e := range expected {
		for _, kind := range []string{"ASSET", "VOLUME", "WORKSPACE"} {
			if (kind == "WORKSPACE" && e.State == "DELETED") || (kind != "WORKSPACE" && e.StorageStatus == "DELETED") {
				continue
			}
			if !present[kind+":"+e.ID] {
				report.Findings = append(report.Findings, InventoryFinding{ResourceObservation: ResourceObservation{Kind: kind, ExternalID: e.ID, InstanceID: e.ID}, Finding: "MISSING_OR_MISMATCHED"})
			}
		}
	}
	storageTotals := map[string]int64{}
	for _, v := range physicalStorage {
		storageTotals[v.instance] += v.bytes
	}
	for id, q := range quota {
		q.Storage = max(q.Storage, storageTotals[id])
		v := physicalCompute[id]
		q.Running = max(q.Running, v.Running)
		q.CPU = max(q.CPU, v.CPU)
		q.Memory = max(q.Memory, v.Memory)
		quota[id] = q
		if !report.Conservative.add(q) {
			return report, nil, ErrInventory
		}
	}
	sort.Slice(report.Findings, func(i, j int) bool {
		a, b := report.Findings[i], report.Findings[j]
		return a.Kind+":"+a.ExternalID+":"+a.Finding < b.Kind+":"+b.ExternalID+":"+b.Finding
	})
	for _, f := range report.Findings {
		if f.Finding != "MATCHED" {
			report.Blockers++
		}
	}
	raw, _ := json.Marshal(struct {
		Expected []InventoryInstance
		Findings []InventoryFinding
		Holds    []RecoveryQuotaHold
	}{expected, report.Findings, holds})
	hash := sha256.Sum256(raw)
	report.SnapshotSHA256 = hex.EncodeToString(hash[:])
	return report, quota, nil
}
func (q *InventoryQuota) add(v InventoryQuota) bool {
	for _, name := range recoveryResources {
		a, b := q.resource(name), v.resource(name)
		if *b < 0 || *a < 0 || *b > math.MaxInt64-*a {
			return false
		}
	}
	for _, name := range recoveryResources {
		*q.resource(name) += *v.resource(name)
	}
	return true
}
