package worker

import (
	"context"
	"encoding/json"
	"errors"
	"math"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/instances"
)

var ErrExternalPending = errors.New("external build outcome is not proven")

type BuildAttempt struct {
	SchemaVersion   int     `json:"schema_version"`
	BeforeID        string  `json:"before_build_id"`
	BeforeNumber    int     `json:"before_build_number"`
	TemplateVersion string  `json:"template_version"`
	Transition      string  `json:"transition"`
	Phase           string  `json:"phase"`
	BuildID         *string `json:"build_id,omitempty"`
	Completed       bool    `json:"completed"`
}

func validBuildPhase(action, desired, phase, transition string) bool {
	switch phase {
	case "CREATE_START":
		return action == "CREATE" && transition == "start"
	case "START":
		return action == "START" && transition == "start"
	case "STOP":
		return action == "STOP" && transition == "stop"
	case "DELETE":
		return action == "DELETE" && transition == "delete"
	case "CONFIGURE_STOP":
		return action == "CONFIGURE" && desired == "RUNNING" && transition == "stop"
	case "CONFIGURE_START":
		return action == "CONFIGURE" && desired == "RUNNING" && transition == "start"
	case "CLEANUP_STOP":
		return transition == "stop"
	}
	return false
}
func (attempt BuildAttempt) valid(action, desired string) bool {
	return attempt.SchemaVersion == 1 && uuidPattern.MatchString(attempt.BeforeID) && attempt.BeforeNumber > 0 && attempt.BeforeNumber < math.MaxInt32 && uuidPattern.MatchString(attempt.TemplateVersion) && validBuildPhase(action, desired, attempt.Phase, attempt.Transition) && (attempt.BuildID == nil || uuidPattern.MatchString(*attempt.BuildID)) && (!attempt.Completed || attempt.BuildID != nil)
}

// BeginBuild persists the last observed build before recording a workspace build. Only the
// first caller may issue a request. A recovered ambiguous attempt must find
// its matching successor, or remain UNCERTAIN for controlled reconciliation.
func (s *ExecutionStore) BeginBuild(ctx context.Context, lease Lease, before string, number int, transition, phase string) (BuildAttempt, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return BuildAttempt{}, false, err
	}
	defer tx.Rollback(context.Background())
	r, err := s.lockExecution(ctx, tx, lease, phase == "CLEANUP_STOP")
	if err != nil {
		return BuildAttempt{}, false, err
	}
	target := r.Snapshot
	if len(r.Intent.Target) > 0 {
		target, err = instances.DecodeSnapshot(r.Intent.Target)
		if err != nil {
			return BuildAttempt{}, false, err
		}
	}
	next := BuildAttempt{SchemaVersion: 1, BeforeID: before, BeforeNumber: number, TemplateVersion: target.TemplateVersion, Transition: transition, Phase: phase}
	if !next.valid(r.Action, r.Intent.Desired) {
		return BuildAttempt{}, false, ErrInvalidEvidence
	}
	if old := r.Intent.External; old != nil {
		if old.Phase == phase {
			return *old, false, tx.Commit(ctx)
		}
		if !old.Completed {
			return BuildAttempt{}, false, ErrExternalPending
		}
		if phase != "CLEANUP_STOP" && !(old.Phase == "CONFIGURE_STOP" && phase == "CONFIGURE_START") {
			return BuildAttempt{}, false, ErrInvalidEvidence
		}
		if old.BuildID == nil || *old.BuildID != before {
			return BuildAttempt{}, false, ErrInvalidEvidence
		}
	}
	r.Intent.External = &next
	if err = writeBuildIntent(ctx, tx, lease, r.Intent); err != nil {
		return BuildAttempt{}, false, err
	}
	return next, true, tx.Commit(ctx)
}
func writeBuildIntent(ctx context.Context, tx pgx.Tx, lease Lease, intent Intent) error {
	raw, err := json.Marshal(intent)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE operations SET intent=$4,updated_at=clock_timestamp() WHERE id=$1 AND lease_owner=$2 AND lease_token=$3 AND lease_expires_at>clock_timestamp() AND status IN ('RUNNING','UNCERTAIN')`, lease.ID, lease.Owner, lease.Token, raw)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrLostLease
	}
	return nil
}
func (s *ExecutionStore) ConfirmBuild(ctx context.Context, lease Lease, id string) error {
	if !uuidPattern.MatchString(id) {
		return ErrInvalidEvidence
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	r, err := s.lockExecution(ctx, tx, lease, true)
	if err != nil {
		return err
	}
	if r.Intent.External == nil || r.Intent.External.BuildID == nil || *r.Intent.External.BuildID != id {
		return ErrInvalidEvidence
	}
	r.Intent.External.Completed = true
	if err = writeBuildIntent(ctx, tx, lease, r.Intent); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
