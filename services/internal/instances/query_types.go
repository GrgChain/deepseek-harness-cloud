package instances

import (
	"errors"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type ResourceSpec struct {
	ID      string `json:"id"`
	CPU     int64  `json:"cpu_millicores"`
	Memory  int64  `json:"memory_bytes"`
	Storage int64  `json:"storage_bytes"`
}

type Snapshot struct {
	DefaultsVersion int64        `json:"defaults_version"`
	TemplateID      string       `json:"template_id"`
	TemplateVersion string       `json:"template_version"`
	ImageDigest     string       `json:"image_digest"`
	Spec            ResourceSpec `json:"spec"`
	ModelID         string       `json:"model_configuration_id"`
	ModelVersion    int64        `json:"model_configuration_version"`
	Provider        string       `json:"provider"`
	Protocol        string       `json:"protocol"`
	Model           string       `json:"model"`
	ApprovalDigest  string       `json:"model_approval_digest,omitempty"`
}

func DecodeSnapshot(data []byte) (Snapshot, error) {
	var stored struct {
		SchemaVersion int `json:"schema_version"`
		Snapshot
	}
	if webapi.StrictUnmarshal(data, &stored) != nil {
		return Snapshot{}, errors.New("persisted configuration snapshot is invalid")
	}
	connectionValid := (stored.Provider == "deepseek-official" && stored.Protocol == "deepseek-messages" && stored.ApprovalDigest == "") || (stored.Provider == "platform-models" && stored.Protocol == "anthropic-messages" && digestPattern.MatchString(stored.ApprovalDigest))
	if stored.SchemaVersion != 1 || stored.DefaultsVersion < 1 || stored.ModelVersion < 1 || !idPattern.MatchString(stored.TemplateID) || !idPattern.MatchString(stored.TemplateVersion) || !idPattern.MatchString(stored.ModelID) || !idPattern.MatchString(stored.Spec.ID) || !digestPattern.MatchString(stored.ImageDigest) || stored.Spec.CPU <= 0 || stored.Spec.Memory <= 0 || stored.Spec.Storage <= 0 || !connectionValid || stored.Model == "" || len(stored.Model) > 128 {
		return Snapshot{}, errors.New("persisted configuration snapshot is invalid")
	}
	return stored.Snapshot, nil
}

type HealthView struct {
	Status    string     `json:"status"`
	CheckedAt *time.Time `json:"checked_at"`
	ExpiresAt *time.Time `json:"expires_at"`
	Reason    string     `json:"reason_code"`
}

func (h HealthView) normalize(now time.Time) HealthView {
	if h.CheckedAt != nil {
		value := h.CheckedAt.UTC()
		h.CheckedAt = &value
	}
	if h.ExpiresAt != nil {
		value := h.ExpiresAt.UTC()
		h.ExpiresAt = &value
	}
	currentFact := h.Status == "UNHEALTHY" && h.CheckedAt == nil && h.ExpiresAt == nil && (h.Reason == "CREDENTIAL_REVOKED" || h.Reason == "ACCESS_CLOSED" || h.Reason == "STORAGE_MISSING")
	if !currentFact && (h.Status == "HEALTHY" || h.Status == "UNHEALTHY" || h.Status == "CHECKING") && (h.CheckedAt == nil || h.ExpiresAt == nil || h.CheckedAt.After(now) || !h.ExpiresAt.After(now) || !h.ExpiresAt.After(*h.CheckedAt)) {
		h.Status = "UNKNOWN"
		h.Reason = "HEALTH_EXPIRED"
	}
	if h.Status == "" {
		h.Status = "UNKNOWN"
		h.Reason = "HEALTH_REQUIRED"
	}
	return h
}

type StorageView struct {
	AssetID        string     `json:"asset_id"`
	Status         string     `json:"status"`
	Capacity       int64      `json:"capacity_bytes"`
	RetentionUntil *time.Time `json:"retention_until"`
}

type InstanceView struct {
	ID                string                   `json:"id"`
	OrganizationID    string                   `json:"org_id"`
	OwnerID           string                   `json:"owner_id"`
	Name              string                   `json:"name"`
	Backend           string                   `json:"backend_type"`
	WorkspaceID       *string                  `json:"workspace_id"`
	AssetID           *string                  `json:"storage_asset_id"`
	Storage           *StorageView             `json:"storage"`
	Desired           string                   `json:"desired_state"`
	Observed          string                   `json:"observed_state"`
	Version           int64                    `json:"version"`
	TemplateVersion   string                   `json:"template_version"`
	ModelID           string                   `json:"model_configuration_id"`
	ModelVersion      int64                    `json:"model_configuration_version"`
	ModelDisplayName  string                   `json:"model_display_name"`
	ModelProviderName string                   `json:"model_provider_name"`
	Snapshot          Snapshot                 `json:"config_snapshot"`
	Health            map[Dimension]HealthView `json:"health"`
	Ready             bool                     `json:"ready"`
	CanEnter          bool                     `json:"can_enter"`
	AllowedActions    []string                 `json:"allowed_actions"`
	ActiveOperationID *string                  `json:"active_operation_id"`
	LatestOperationID *string                  `json:"latest_operation_id"`
	AccessHost        *string                  `json:"access_host"`
	CreatedAt         time.Time                `json:"created_at"`
	UpdatedAt         time.Time                `json:"updated_at"`
	DeletedAt         *time.Time               `json:"deleted_at"`
	RetentionUntil    *time.Time               `json:"retention_until"`
}

type ListInput struct {
	Limit                int
	Page                 int
	Cursor, State, Query string
	IncludeDeleted       bool
}

type InstancePage struct {
	Items      []InstanceView `json:"items"`
	Total      int64          `json:"total"`
	NextCursor *string        `json:"next_cursor"`
}
