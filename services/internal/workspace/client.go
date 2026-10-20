// Package workspace embeds Coder-derived workspace, template and build
// orchestration in the platform Go backend, using the platform PostgreSQL.
package workspace

import "errors"

var (
	ErrNotFound    = errors.New("workspace resource not found")
	ErrConflict    = errors.New("workspace resource conflicts")
	ErrUnavailable = errors.New("workspace operation unavailable or incompatible")
	ErrUncertain   = errors.New("workspace write outcome is uncertain; reconciliation required")
)

const Version = "v2.37.4+1d24629"

type BuildInfo struct {
	Version   string `json:"version"`
	Telemetry bool   `json:"telemetry"`
	Engine    string `json:"engine,omitempty"`
}

type Role struct {
	Name string `json:"name"`
}
type User struct {
	ID              string   `json:"id"`
	Username        string   `json:"username"`
	LoginType       string   `json:"login_type"`
	Status          string   `json:"status"`
	OrganizationIDs []string `json:"organization_ids"`
	Roles           []Role   `json:"roles"`
}

// Dormant includes a managed account that has never logged in; it is not a
// suspension. Platform membership remains the authoritative user authorization.
func OwnerStatusUsable(status string) bool { return status == "active" || status == "dormant" }
