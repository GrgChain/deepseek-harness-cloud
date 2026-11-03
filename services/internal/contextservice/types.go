package contextservice

import (
	"encoding/json"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
)

type Connection struct {
	ID                     string           `json:"id"`
	Kind                   string           `json:"kind"`
	Backend                string           `json:"backend"`
	EndpointID             string           `json:"endpoint_id"`
	ServiceURL             string           `json:"service_url,omitempty"`
	Embedding              *EmbeddingConfig `json:"embedding,omitempty"`
	EmbeddingKeyConfigured bool             `json:"embedding_key_configured,omitempty"`
	Name                   string           `json:"name"`
	Version                int64            `json:"version"`
	Enabled                bool             `json:"enabled"`
	Status                 string           `json:"status"`
	Capabilities           json.RawMessage  `json:"capabilities"`
	VerifiedAt             *time.Time       `json:"verified_at"`
	UpdatedAt              time.Time        `json:"updated_at"`
	Integration            string           `json:"integration"`
	envelope               security.Envelope
}

type Input struct {
	Name            string           `json:"name"`
	EndpointID      string           `json:"endpoint_id"`
	ServiceURL      string           `json:"service_url"`
	Embedding       *EmbeddingConfig `json:"embedding"`
	Credential      string           `json:"credential"`
	Enabled         *bool            `json:"enabled"`
	ExpectedVersion int64            `json:"expected_version"`
	ConfirmedName   string           `json:"confirmed_name"`
}

// Only these fields cross the external boundary. Scope and namespace are always
// resolved by the platform; clients cannot submit filters or external tenant IDs.
type Action struct {
	ExpectedVersion int64           `json:"expected_version"`
	IdempotencyKey  string          `json:"idempotency_key"`
	Scope           string          `json:"scope"`
	Query           string          `json:"query"`
	SearchMode      string          `json:"search_mode,omitempty"`
	MinSimilarity   *float64        `json:"min_similarity,omitempty"`
	MemorySource    string          `json:"memory_source,omitempty"`
	Page            int             `json:"page,omitempty"`
	PageSize        int             `json:"page_size,omitempty"`
	Limit           int             `json:"limit"`
	RecordID        string          `json:"record_id"`
	RecordVersion   int64           `json:"record_version"`
	Content         string          `json:"content"`
	Title           string          `json:"title"`
	Type            string          `json:"type"`
	Source          string          `json:"source"`
	Token           string          `json:"token"`
	Mode            string          `json:"mode"`
	Package         json.RawMessage `json:"package,omitempty"`
	Confirmed       bool            `json:"confirmed"`
	AcceptLoss      bool            `json:"accept_loss"`
	SessionRequest  *SessionRequest `json:"session_request,omitempty"`
}

type Binding struct {
	InstanceID   string `json:"instance_id"`
	ConnectionID string `json:"connection_id"`
	Version      int64  `json:"version"`
	Enabled      bool   `json:"enabled"`
	Integration  string `json:"integration"`
}
