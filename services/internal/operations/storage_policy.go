package operations

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/runtimeapproval"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/worker"
)

var ErrStoragePolicy = errors.New("asset maintenance requires the bound original asset, stopped compute, conservative quota and a permitted target")

type AssetState struct {
	ID, InstanceID, OrganizationID, OwnerID, FilesystemID, Status, Observed, Desired string
	Capacity                                                                         int64
	ActiveOperation, StorageReserved, ModelUsable, LegalHold                         bool
	RetentionUntil, DeletedAt                                                        time.Time
	Intent                                                                           json.RawMessage
	Plan                                                                             worker.ExecutionPlan
}

func (a AssetState) original() bool {
	return uuid.MatchString(a.ID) && uuid.MatchString(a.InstanceID) && uuid.MatchString(a.OrganizationID) && uuid.MatchString(a.OwnerID) && a.FilesystemID == a.ID && runtimeapproval.SupportedStorage(a.Capacity) && a.StorageReserved && !a.ActiveOperation
}
func CheckBackup(a AssetState) error {
	if !a.original() || !((a.Status == "READY" && a.Observed == "STOPPED" && a.Desired == "STOPPED") || (a.Status == "RETAINED" && a.Observed == "DELETED" && a.Desired == "DELETED")) {
		return ErrStoragePolicy
	}
	return nil
}
func CheckStorageRestore(source, target AssetState) error {
	if CheckBackup(source) != nil || !target.original() || source.ID == target.ID || source.Capacity != target.Capacity || source.InstanceID == target.InstanceID || source.OwnerID != target.OwnerID || source.OrganizationID != target.OrganizationID || (target.Status != "READY" && target.Status != "RESTORING") || target.Observed != "STOPPED" || target.Desired != "STOPPED" || !target.ModelUsable || target.LegalHold {
		return ErrStoragePolicy
	}
	return nil
}
func CheckReclaim(a AssetState, now time.Time) error {
	if !a.original() || (a.Status != "RETAINED" && a.Status != "RECLAIM_PENDING") || a.Observed != "DELETED" || a.Desired != "DELETED" || a.LegalHold || a.RetentionUntil.IsZero() || a.RetentionUntil.After(now) || a.DeletedAt.IsZero() || a.DeletedAt.Add(7*24*time.Hour).After(now) {
		return ErrStoragePolicy
	}
	return nil
}
