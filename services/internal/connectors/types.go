package connectors

import (
	"encoding/json"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
)

type Connection struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	EndpointID     string          `json:"endpoint_id"`
	URL            string          `json:"url"`
	CatalogID      string          `json:"catalog_id,omitempty"`
	Protocol       string          `json:"protocol_version"`
	Auth           string          `json:"auth_type"`
	Version        int64           `json:"version"`
	CatalogVersion int64           `json:"catalog_version"`
	Enabled        bool            `json:"enabled"`
	Status         string          `json:"status"`
	Server         json.RawMessage `json:"server_info"`
	Tools          []Tool          `json:"tools"`
	VerifiedAt     *time.Time      `json:"verified_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	envelope       security.Envelope
}

type Input struct {
	Name            string `json:"name"`
	Description     string `json:"description"`
	EndpointID      string `json:"endpoint_id"`
	URL             string `json:"url"`
	CatalogID       string `json:"catalog_id,omitempty"`
	Protocol        string `json:"protocol_version"`
	Auth            string `json:"auth_type"`
	Credential      string `json:"credential"`
	Enabled         bool   `json:"enabled"`
	ExpectedVersion int64  `json:"expected_version"`
	ConfirmedName   string `json:"confirmed_name"`
}
