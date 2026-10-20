// Copyright (C) Coder Technologies, Inc.
// SPDX-License-Identifier: AGPL-3.0-only
// Extracted from Coder v2.37.4; adapted for Harness Cloud on 2026-10-13.
// See third_party/coder/README.md for provenance and modifications.

package workspace

type WorkspaceTransition string

const (
	WorkspaceTransitionStart  WorkspaceTransition = "start"
	WorkspaceTransitionStop   WorkspaceTransition = "stop"
	WorkspaceTransitionDelete WorkspaceTransition = "delete"
)

type WorkspaceStatus string

const (
	WorkspaceStatusPending   WorkspaceStatus = "pending"
	WorkspaceStatusStarting  WorkspaceStatus = "starting"
	WorkspaceStatusRunning   WorkspaceStatus = "running"
	WorkspaceStatusStopping  WorkspaceStatus = "stopping"
	WorkspaceStatusStopped   WorkspaceStatus = "stopped"
	WorkspaceStatusFailed    WorkspaceStatus = "failed"
	WorkspaceStatusCanceling WorkspaceStatus = "canceling"
	WorkspaceStatusCanceled  WorkspaceStatus = "canceled"
	WorkspaceStatusDeleting  WorkspaceStatus = "deleting"
	WorkspaceStatusDeleted   WorkspaceStatus = "deleted"
)

type ProvisionerJobStatus string

const (
	ProvisionerJobPending   ProvisionerJobStatus = "pending"
	ProvisionerJobRunning   ProvisionerJobStatus = "running"
	ProvisionerJobSucceeded ProvisionerJobStatus = "succeeded"
	ProvisionerJobCanceling ProvisionerJobStatus = "canceling"
	ProvisionerJobCanceled  ProvisionerJobStatus = "canceled"
	ProvisionerJobFailed    ProvisionerJobStatus = "failed"
)

func ConvertWorkspaceStatus(jobStatus ProvisionerJobStatus, transition WorkspaceTransition) WorkspaceStatus {
	switch jobStatus {
	case ProvisionerJobPending:
		return WorkspaceStatusPending
	case ProvisionerJobRunning:
		switch transition {
		case WorkspaceTransitionStart:
			return WorkspaceStatusStarting
		case WorkspaceTransitionStop:
			return WorkspaceStatusStopping
		case WorkspaceTransitionDelete:
			return WorkspaceStatusDeleting
		}
	case ProvisionerJobSucceeded:
		switch transition {
		case WorkspaceTransitionStart:
			return WorkspaceStatusRunning
		case WorkspaceTransitionStop:
			return WorkspaceStatusStopped
		case WorkspaceTransitionDelete:
			return WorkspaceStatusDeleted
		}
	case ProvisionerJobCanceling:
		return WorkspaceStatusCanceling
	case ProvisionerJobCanceled:
		return WorkspaceStatusCanceled
	case ProvisionerJobFailed:
		return WorkspaceStatusFailed
	}

	// return error status since we should never get here
	return WorkspaceStatusFailed
}
