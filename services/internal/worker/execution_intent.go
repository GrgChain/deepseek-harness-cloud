package worker

import (
	"encoding/json"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/instances"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type Intent struct {
	SchemaVersion      int             `json:"schema_version"`
	ExpectedVersion    int64           `json:"expected_instance_version"`
	Desired            string          `json:"target_desired_state"`
	Reason             string          `json:"reason"`
	RequestID          string          `json:"request_id"`
	AcceptedGeneration *int64          `json:"accepted_auth_generation,omitempty"`
	Target             json.RawMessage `json:"target_snapshot,omitempty"`
	ConfirmedName      *string         `json:"confirmed_name,omitempty"`
	External           *BuildAttempt   `json:"external_build,omitempty"`
}

func parseIntent(data []byte, action, actorKind string) (Intent, error) {
	var v Intent
	if webapi.StrictUnmarshal(data, &v) != nil || v.SchemaVersion != 1 || v.ExpectedVersion < 1 || v.RequestID == "" || len(v.RequestID) > 128 {
		return v, ErrInvalidEvidence
	}
	if actorKind == "USER" {
		if v.Reason != "USER_REQUEST" || v.AcceptedGeneration == nil || *v.AcceptedGeneration < 1 {
			return v, ErrInvalidEvidence
		}
	} else if actorKind != "SYSTEM" || action != "STOP" || (v.Reason != "AUTH_REVOKED" && v.Reason != "CREDENTIAL_REVOKED") || v.AcceptedGeneration != nil {
		return v, ErrInvalidEvidence
	}
	switch action {
	case "CREATE", "START":
		if v.Desired != "RUNNING" || len(v.Target) == 0 || v.ConfirmedName != nil {
			return v, ErrInvalidEvidence
		}
	case "CONFIGURE":
		if (v.Desired != "RUNNING" && v.Desired != "STOPPED") || len(v.Target) == 0 || v.ConfirmedName != nil {
			return v, ErrInvalidEvidence
		}
	case "STOP":
		if v.Desired != "STOPPED" || len(v.Target) > 0 || v.ConfirmedName != nil {
			return v, ErrInvalidEvidence
		}
	case "DELETE":
		if v.Desired != "DELETED" || len(v.Target) > 0 || v.ConfirmedName == nil {
			return v, ErrInvalidEvidence
		}
	default:
		return v, ErrInvalidEvidence
	}
	if v.External != nil && !v.External.valid(action, v.Desired) {
		return v, ErrInvalidEvidence
	}
	if len(v.Target) > 0 {
		if _, err := instances.DecodeSnapshot(v.Target); err != nil {
			return v, ErrInvalidEvidence
		}
	}
	return v, nil
}
