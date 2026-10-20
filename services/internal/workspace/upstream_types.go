// Copyright (C) Coder Technologies, Inc.
// SPDX-License-Identifier: AGPL-3.0-only
// Adapted from codersdk/{workspaces,workspacebuilds,templates,templateversions,provisionerdaemons}.go
// in Coder v2.37.4 on 2026-10-13: IDs use strings and unsupported fields are omitted.
package workspace

import "time"

type Job struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	ErrorCode string `json:"error_code"`
}
type Build struct {
	ID                string    `json:"id"`
	WorkspaceID       string    `json:"workspace_id"`
	TemplateVersionID string    `json:"template_version_id"`
	BuildNumber       int       `json:"build_number"`
	Transition        string    `json:"transition"`
	Status            string    `json:"status"`
	Job               Job       `json:"job"`
	OwnerID           string    `json:"workspace_owner_id"`
	InitiatorID       string    `json:"initiator_id"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}
type Workspace struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	OwnerID          string `json:"owner_id"`
	OrganizationID   string `json:"organization_id"`
	TemplateID       string `json:"template_id"`
	LatestBuild      Build  `json:"latest_build"`
	AutomaticUpdates string `json:"automatic_updates"`
	TTLMillis        *int64 `json:"ttl_ms"`
}

type Parameter struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
type CreateWorkspaceRequest struct {
	Name                string      `json:"name"`
	TemplateVersionID   string      `json:"template_version_id"`
	RichParameterValues []Parameter `json:"rich_parameter_values,omitempty"`
	AutomaticUpdates    string      `json:"automatic_updates,omitempty"`
	TTLMillis           int64       `json:"ttl_ms"`
}

type Template struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organization_id"`
	Name           string `json:"name"`
	ActiveVersion  string `json:"active_version_id"`
	Deleted        bool   `json:"deleted"`
}
type TemplateVersion struct {
	ID             string `json:"id"`
	TemplateID     string `json:"template_id"`
	OrganizationID string `json:"organization_id"`
	Name           string `json:"name"`
	Archived       bool   `json:"archived"`
	ExternalAgent  bool   `json:"has_external_agent"`
	Job            struct {
		Job
		FileID         string `json:"file_id"`
		OrganizationID string `json:"organization_id"`
		InitiatorID    string `json:"initiator_id"`
	} `json:"job"`
}
type Variable struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	Sensitive bool   `json:"sensitive"`
}
