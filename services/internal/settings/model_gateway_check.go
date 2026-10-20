package settings

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

// Include retained versions used by defaults or instances: updating a
// connection must not stop verification of an existing instance's snapshot.
const gatewayCheckJoins = ` FROM model_gateway_checks g
 JOIN user_model_connection_versions v ON v.connection_id=g.connection_id AND v.version=g.connection_version AND v.org_id=g.org_id AND v.owner_id=g.owner_id
 JOIN user_model_connections h ON h.id=v.connection_id AND h.org_id=v.org_id AND h.owner_id=v.owner_id
 JOIN model_configurations c ON c.id=v.model_configuration_id AND c.version=v.model_configuration_version AND c.org_id=v.org_id
 JOIN secrets sec ON sec.id=c.credential_ref AND sec.org_id=c.org_id
 JOIN external_provider_secrets ps ON ps.id=v.provider_secret_id AND ps.org_id=v.org_id AND ps.owner_id=v.owner_id AND ps.connection_id=v.connection_id
 JOIN model_catalog_entries e ON e.id=v.catalog_entry_id AND e.org_id=v.org_id
 JOIN model_providers p ON p.id=e.provider_id
 JOIN model_sync_operations op ON op.connection_id=v.connection_id AND op.connection_version=v.version AND op.action='UPSERT' AND op.status='SUCCEEDED'`
const gatewayCheckEligible = `h.enabled AND h.archived_at IS NULL AND v.sync_status='SYNCED' AND c.revoked_at IS NULL AND c.status<>'REVOKED' AND sec.revoked_at IS NULL AND ps.revoked_at IS NULL AND e.enabled AND p.enabled
 AND EXISTS(SELECT 1 FROM memberships member JOIN users u ON u.id=member.user_id JOIN organizations o ON o.id=member.org_id WHERE member.org_id=g.org_id AND member.user_id=g.owner_id AND member.status='ACTIVE' AND u.status='ACTIVE' AND o.status='ACTIVE')
 AND public.hc_has_permission(g.org_id,g.owner_id,'personal-model:write')
 AND (h.current_version=v.version OR ` + usedModelVersion + `)`

type gatewayCheckLease struct {
	Org, Owner, Connection string
	Version, Token         int64
	Started                time.Time
}

func (a *ModelSync) claimCheck(ctx context.Context) (gatewayCheckLease, bool, error) {
	var l gatewayCheckLease
	err := a.service.pool.QueryRow(ctx, `WITH candidate AS (
 SELECT g.connection_id,g.connection_version`+gatewayCheckJoins+` WHERE `+gatewayCheckEligible+`
 AND g.next_check_at<=clock_timestamp() AND (g.lease_expires_at IS NULL OR g.lease_expires_at<=clock_timestamp())
 ORDER BY g.next_check_at,g.connection_id,g.connection_version FOR UPDATE OF g SKIP LOCKED LIMIT 1)
 UPDATE model_gateway_checks g SET lease_token=lease_token+1,attempted_at=clock_timestamp(),lease_expires_at=clock_timestamp()+interval '30 seconds'
 FROM candidate candidate WHERE g.connection_id=candidate.connection_id AND g.connection_version=candidate.connection_version
 RETURNING g.org_id::text,g.owner_id::text,g.connection_id::text,g.connection_version,g.lease_token,g.attempted_at`).Scan(&l.Org, &l.Owner, &l.Connection, &l.Version, &l.Token, &l.Started)
	if errors.Is(err, pgx.ErrNoRows) {
		return l, false, nil
	}
	return l, err == nil, err
}

func (a *ModelSync) prepareCheck(ctx context.Context, l gatewayCheckLease) (modelSyncPlan, string, error) {
	var plan modelSyncPlan
	var remote string
	tx, err := a.service.pool.Begin(ctx)
	if err != nil {
		return plan, remote, err
	}
	defer tx.Rollback(context.Background())
	if err = a.lockCheckAuthority(ctx, tx, l); err != nil {
		return plan, remote, err
	}
	var raw []byte
	var endpoint string
	var envelope security.Envelope
	err = tx.QueryRow(ctx, `SELECT v.manifest,v.gateway_endpoint,v.route_alias,c.credential_ref::text,sec.key_id,sec.nonce,sec.ciphertext,op.remote_model_id`+gatewayCheckJoins+`
 WHERE g.connection_id=$1 AND g.connection_version=$2 AND g.lease_token=$3 AND g.lease_expires_at>clock_timestamp() AND `+gatewayCheckEligible+` FOR SHARE OF e,p`, l.Connection, l.Version, l.Token).Scan(&raw, &endpoint, &plan.Alias, &plan.Credential, &envelope.KeyID, &envelope.Nonce, &envelope.Ciphertext, &remote)
	if err != nil || webapi.StrictUnmarshal(raw, &plan.Frozen) != nil {
		return plan, remote, errModelControl
	}
	var ok bool
	plan.Gateway, ok = a.service.policy.Gateway(endpoint)
	if !ok || plan.Gateway.Revision != plan.Frozen.GatewayRevision {
		return plan, remote, errModelControl
	}
	// Read only the platform-issued virtual credential, never the provider key.
	plan.VirtualKey, err = a.service.cipher.Decrypt(envelope, a.service.aad(l.Org, plan.Credential))
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		plan.destroy()
	}
	return plan, remote, err
}

func (a *ModelSync) lockCheckAuthority(ctx context.Context, tx pgx.Tx, l gatewayCheckLease) error {
	var active bool
	if err := tx.QueryRow(ctx, `SELECT public.hc_personal_model_authority($1,$2)`, l.Org, l.Owner).Scan(&active); err != nil {
		return err
	}
	// The same membership -> model head -> connection order as revoke/update.
	var id string
	return tx.QueryRow(ctx, `SELECT id::text FROM user_model_connections WHERE id=$1 AND org_id=$2 AND owner_id=$3 FOR UPDATE`, l.Connection, l.Org, l.Owner).Scan(&id)
}

func (a *ModelSync) publishCheck(ctx context.Context, l gatewayCheckLease, reason string) error {
	s := a.service
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err = a.lockCheckAuthority(ctx, tx, l); err != nil {
		return err
	}
	var previous, previousReason string
	err = tx.QueryRow(ctx, `SELECT status,reason_code FROM model_gateway_checks WHERE connection_id=$1 AND connection_version=$2 AND lease_token=$3 AND lease_expires_at>clock_timestamp() FOR UPDATE`, l.Connection, l.Version, l.Token).Scan(&previous, &previousReason)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // A late or superseded response has no publication authority.
	}
	if err != nil {
		return err
	}
	var eligible bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1`+gatewayCheckJoins+` WHERE g.connection_id=$1 AND g.connection_version=$2 AND `+gatewayCheckEligible+`)`, l.Connection, l.Version).Scan(&eligible); err != nil {
		return err
	}
	status := "HEALTHY"
	if !eligible {
		status, reason = "UNKNOWN", "CREDENTIAL_REVOKED"
	} else if reason != "" {
		status = "UNHEALTHY"
	}
	_, err = tx.Exec(ctx, `UPDATE model_gateway_checks SET status=$4,reason_code=$5,checked_at=$6,expires_at=$6::timestamptz+interval '60 seconds',next_check_at=clock_timestamp()+interval '30 seconds',lease_expires_at=NULL WHERE connection_id=$1 AND connection_version=$2 AND lease_token=$3`, l.Connection, l.Version, l.Token, status, reason, l.Started)
	if err != nil {
		return err
	}
	if previous != status || previousReason != reason {
		_, err = tx.Exec(ctx, `INSERT INTO audit_events(id,org_id,owner_id,resource_id,action,outcome,trace_id,details) VALUES($1,$2,$3,$4,'PERSONAL_MODEL_GATEWAY_CHECK',$5,$6,jsonb_build_object('schema_version',1,'configuration_version',$7::bigint,'reason_code',$8::text))`, webapi.ID(), l.Org, l.Owner, l.Connection, map[bool]string{true: "SUCCEEDED", false: "FAILED"}[status == "HEALTHY"], webapi.ID(), l.Version, reason)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// CheckOnce reads only remote model/key metadata. It never repairs a missing
// route, issues a new credential, or invokes the external model.
func (a *ModelSync) CheckOnce(ctx context.Context) (bool, error) {
	if a.service.maintenance || a.controlKey == "" {
		return false, nil
	}
	acquire, cancel := context.WithTimeout(ctx, 5*time.Second)
	l, claimed, err := a.claimCheck(acquire)
	cancel()
	if err != nil || !claimed {
		return false, err
	}
	task, cancel := context.WithTimeout(ctx, 15*time.Second)
	plan, remote, err := a.prepareCheck(task, l)
	defer plan.destroy()
	if err == nil {
		client := a.client
		if client == nil {
			client = plan.Gateway.HTTPClient()
		}
		control := modelControl{plan.Gateway, client, a.controlKey}
		err = control.verifyRoute(task, remote, plan.Alias, plan.Frozen)
		if err == nil {
			found, keyErr := control.keyInfo(task, string(plan.VirtualKey), plan.Alias)
			if keyErr != nil || !found {
				err = errModelControl
			}
		}
	}
	cancel()
	reason := ""
	if err != nil {
		reason = "MODEL_GATEWAY_UNVERIFIED"
		if errors.Is(err, errModelRouteDrift) {
			reason = "MODEL_ROUTE_DRIFT"
		}
	}
	finish, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if publishErr := a.publishCheck(finish, l, reason); publishErr != nil {
		return true, publishErr
	}
	if err != nil {
		return true, errModelControl
	}
	return true, nil
}

// Independent of synchronization so a slow or failed UPSERT cannot keep old
// synchronized routes healthy indefinitely. Due order prevents starvation.
func (a *ModelSync) RunChecks(ctx context.Context) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		for range 10 {
			ran, err := a.CheckOnce(ctx)
			if err != nil && ctx.Err() == nil {
				slog.Warn("Model gateway verification temporarily unavailable")
			}
			if !ran || ctx.Err() != nil {
				break
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
