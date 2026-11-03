package worker

import (
	"context"
	"errors"
	"math"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/instances"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
)

var (
	ErrSuperseded       = errors.New("instance execution intent was superseded")
	ErrExecutionRevoked = errors.New("instance execution authority or credential was revoked")
	ErrInvalidEvidence  = errors.New("runtime completion evidence is missing or invalid")
)

var uuidPattern = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`)

var hostPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,252}$`)

type ExecutionStore struct {
	pool        *pgxpool.Pool
	cipher      *security.SecretCipher
	environment string
}

func NewExecutionStore(pool *pgxpool.Pool, cipher *security.SecretCipher, environment string) *ExecutionStore {
	return &ExecutionStore{pool, cipher, environment}
}

type executionRecord struct {
	Org, Owner, Name, Action, ActorKind, Trace, Desired string
	ActorID                                             *string
	Snapshot                                            instances.Snapshot
	Encoded                                             []byte
	Intent                                              Intent
	Version                                             int64
	AssetID, WorkspaceID                                *string
	AssetStatus                                         string
	AssetCapacity                                       int64
	Authorized                                          bool
}

// lockExecution observes immutable ownership first, then takes the documented
// membership -> model head -> quota -> instance -> operation -> outbox locks.
// No network work or credential material is involved in these transactions.
func (s *ExecutionStore) lockExecution(ctx context.Context, tx pgx.Tx, lease Lease, allowSuperseded bool) (executionRecord, error) {
	var r executionRecord
	if !uuidPattern.MatchString(lease.ID) || !uuidPattern.MatchString(lease.InstanceID) || !workerName.MatchString(lease.Owner) || lease.Token < 1 {
		return r, ErrLostLease
	}
	err := tx.QueryRow(ctx, `SELECT i.org_id::text,i.owner_id::text FROM instances i JOIN operations o ON o.instance_id=i.id WHERE o.id=$1 AND i.id=$2`, lease.ID, lease.InstanceID).Scan(&r.Org, &r.Owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrLostLease
	}
	if err != nil {
		return r, err
	}
	err = tx.QueryRow(ctx, `SELECT public.hc_worker_lock_authority($1,$2)`, r.Org, r.Owner).Scan(&r.Authorized)
	if err != nil {
		return r, err
	}
	var ignored string
	if err = tx.QueryRow(ctx, `SELECT org_id::text FROM quota_policies WHERE org_id=$1 FOR UPDATE`, r.Org).Scan(&ignored); err != nil {
		return r, err
	}
	err = tx.QueryRow(ctx, `SELECT name,version,desired_state,config_snapshot,storage_asset_id::text,workspace_id::text FROM instances WHERE id=$1 AND org_id=$2 AND owner_id=$3 FOR UPDATE`, lease.InstanceID, r.Org, r.Owner).Scan(&r.Name, &r.Version, &r.Desired, &r.Encoded, &r.AssetID, &r.WorkspaceID)
	if err != nil {
		return r, err
	}
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT type,actor_kind,actor_id::text,intent,trace_id FROM operations WHERE id=$1 AND instance_id=$2 AND lease_owner=$3 AND lease_token=$4 AND lease_expires_at>clock_timestamp() AND status IN ('RUNNING','UNCERTAIN') FOR UPDATE`, lease.ID, lease.InstanceID, lease.Owner, lease.Token).Scan(&r.Action, &r.ActorKind, &r.ActorID, &raw, &r.Trace)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrLostLease
	}
	if err != nil {
		return r, err
	}
	if r.Intent, err = parseIntent(raw, r.Action, r.ActorKind); err != nil {
		return r, err
	}
	if r.Authorized && r.ActorKind == "USER" && (r.Action == "CREATE" || r.Action == "START" || r.Action == "CONFIGURE") {
		err = tx.QueryRow(ctx, `SELECT public.hc_has_permission($1,$2,$3) AND ($4<>'RUNNING' OR public.hc_has_permission($1,$2,'instances:start'))`, r.Org, r.Owner, "instances:"+strings.ToLower(r.Action), r.Intent.Desired).Scan(&r.Authorized)
		if err != nil {
			return r, err
		}
	}
	// A SYSTEM STOP releases only compute. It must preserve an already accepted
	// DELETE target so security cleanup cannot silently undo the user's intent.
	desiredMatches := r.Desired == r.Intent.Desired || (r.ActorKind == "SYSTEM" && r.Action == "STOP" && r.Intent.Desired == "STOPPED" && r.Desired == "DELETED")
	if r.Version == math.MaxInt64 || r.Version < r.Intent.ExpectedVersion || (!allowSuperseded && (r.Version != r.Intent.ExpectedVersion || !desiredMatches)) {
		return r, ErrSuperseded
	}
	if r.Snapshot, err = instances.DecodeSnapshot(r.Encoded); err != nil {
		return r, err
	}
	if len(r.Intent.Target) > 0 {
		target, err := instances.DecodeSnapshot(r.Intent.Target)
		if err != nil {
			return r, err
		}
		if err = s.validateTarget(ctx, tx, r.Action, r.Snapshot, target); err != nil {
			return r, err
		}
	}
	expectedTemplate := r.Snapshot.TemplateVersion
	if len(r.Intent.Target) > 0 {
		target, _ := instances.DecodeSnapshot(r.Intent.Target)
		expectedTemplate = target.TemplateVersion
	}
	if r.Intent.External != nil && r.Intent.External.TemplateVersion != expectedTemplate {
		return r, ErrInvalidEvidence
	}
	if r.Action == "DELETE" && *r.Intent.ConfirmedName != r.Name {
		return r, ErrInvalidEvidence
	}
	if r.AssetID != nil {
		err = tx.QueryRow(ctx, `SELECT status,capacity_bytes FROM storage_assets WHERE id=$1 AND instance_id=$2 AND org_id=$3 AND owner_id=$4`, *r.AssetID, lease.InstanceID, r.Org, r.Owner).Scan(&r.AssetStatus, &r.AssetCapacity)
		if err != nil {
			return r, err
		}
	}
	err = tx.QueryRow(ctx, `SELECT id::text FROM outbox_events WHERE operation_id=$1 AND status='PENDING' FOR UPDATE`, lease.ID).Scan(&ignored)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrLostLease
	}
	return r, err
}
