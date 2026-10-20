package settings

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type ModelSync struct {
	service    *Service
	controlKey string
	client     *http.Client
	cursor     string
}

func NewModelSync(service *Service, key []byte) *ModelSync {
	return &ModelSync{service: service, controlKey: string(key)}
}

type modelSyncLease struct {
	ID, Org, Owner, Connection, Action, RemoteID string
	Version, Token                               int64
}
type modelSyncPlan struct {
	Gateway                 Gateway
	Frozen                  personalManifest
	Alias, Credential       string
	ProviderKey, VirtualKey []byte
	CheckedAt               time.Time
}

func (p *modelSyncPlan) destroy() { clear(p.ProviderKey); clear(p.VirtualKey) }

func (a *ModelSync) prepare(ctx context.Context, l modelSyncLease) (modelSyncPlan, error) {
	s := a.service
	var plan modelSyncPlan
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return plan, err
	}
	defer tx.Rollback(context.Background())
	var active bool
	if err = tx.QueryRow(ctx, `SELECT public.hc_personal_model_authority($1,$2)`, l.Org, l.Owner).Scan(&active); err != nil {
		return plan, err
	}
	if !active && l.Action != "REVOKE" {
		return plan, errModelControl
	}
	var enabled bool
	var version int64
	if err = tx.QueryRow(ctx, `SELECT enabled,current_version FROM user_model_connections WHERE id=$1 AND org_id=$2 AND owner_id=$3 FOR UPDATE`, l.Connection, l.Org, l.Owner).Scan(&enabled, &version); err != nil {
		return plan, err
	}
	if l.Action != "REVOKE" && (!enabled || version != l.Version) {
		return plan, errModelControl
	}
	var valid bool
	if err = tx.QueryRow(ctx, `SELECT lease_token=$2 AND lease_expires_at>clock_timestamp() AND status='RUNNING' FROM model_sync_operations WHERE id=$1 FOR UPDATE`, l.ID, l.Token).Scan(&valid); err != nil || !valid {
		return plan, errModelControl
	}
	var raw []byte
	var endpoint, providerSecret string
	var credential *string
	var boundVersion int64
	var envelope security.Envelope
	err = tx.QueryRow(ctx, `SELECT v.manifest,v.gateway_endpoint,v.route_alias,v.runtime_credential_ref::text,s.id::text,s.bound_version,s.key_id,s.nonce,s.ciphertext FROM user_model_connection_versions v JOIN external_provider_secrets s ON s.id=v.provider_secret_id AND s.org_id=v.org_id AND s.owner_id=v.owner_id AND s.connection_id=v.connection_id WHERE v.connection_id=$1 AND v.version=$2`, l.Connection, l.Version).Scan(&raw, &endpoint, &plan.Alias, &credential, &providerSecret, &boundVersion, &envelope.KeyID, &envelope.Nonce, &envelope.Ciphertext)
	if err != nil {
		return plan, err
	}
	if webapi.StrictUnmarshal(raw, &plan.Frozen) != nil {
		return plan, errModelControl
	}
	var ok bool
	plan.Gateway, ok = s.policy.Gateway(endpoint)
	if !ok || plan.Gateway.Revision != plan.Frozen.GatewayRevision {
		return plan, errModelControl
	}
	if l.Action != "REVOKE" {
		var approved bool
		if err = tx.QueryRow(ctx, `SELECT e.enabled AND p.enabled FROM model_catalog_entries e JOIN model_providers p ON p.id=e.provider_id WHERE e.org_id=$1 AND e.id=$2 FOR SHARE OF e,p`, l.Org, plan.Frozen.Catalog.ID).Scan(&approved); err != nil || !approved {
			return plan, errModelControl
		}
		plan.ProviderKey, err = s.cipher.Decrypt(envelope, security.Context{Environment: s.environment, OrganizationID: l.Org, RecordID: fmt.Sprintf("%s/%s/%s/%d", l.Owner, l.Connection, providerSecret, boundVersion), Purpose: "provider-key"})
		if err != nil {
			return plan, err
		}
	}
	if credential == nil {
		if l.Action == "REVOKE" {
			return plan, tx.Commit(ctx)
		}
		random := make([]byte, 32)
		if _, err = rand.Read(random); err != nil {
			plan.destroy()
			return plan, err
		}
		plan.VirtualKey = []byte("sk-" + base64.RawURLEncoding.EncodeToString(random))
		clear(random)
		plan.Credential = webapi.ID()
		encoded, e := s.cipher.Encrypt(plan.VirtualKey, s.aad(l.Org, plan.Credential))
		if e != nil {
			plan.destroy()
			return plan, e
		}
		_, err = tx.Exec(ctx, `INSERT INTO secrets(id,org_id,key_id,nonce,ciphertext) VALUES($1,$2,$3,$4,$5)`, plan.Credential, l.Org, encoded.KeyID, encoded.Nonce, encoded.Ciphertext)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE user_model_connection_versions SET runtime_credential_ref=$3 WHERE connection_id=$1 AND version=$2`, l.Connection, l.Version, plan.Credential)
		}
	} else {
		plan.Credential = *credential
		err = tx.QueryRow(ctx, `SELECT key_id,nonce,ciphertext FROM secrets WHERE id=$1 AND org_id=$2`, plan.Credential, l.Org).Scan(&envelope.KeyID, &envelope.Nonce, &envelope.Ciphertext)
		if err == nil {
			plan.VirtualKey, err = s.cipher.Decrypt(envelope, s.aad(l.Org, plan.Credential))
		}
	}
	if err == nil && l.Action == "UPSERT" {
		err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&plan.CheckedAt)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		plan.destroy()
	}
	return plan, err
}

func (a *ModelSync) RunOnce(ctx context.Context) (bool, error) {
	s := a.service
	if s.maintenance || a.controlKey == "" {
		return false, nil
	}
	if err := a.finishProviderAccountDeletes(ctx); err != nil {
		return false, err
	}
	if err := a.compensate(ctx); err != nil {
		return false, err
	}
	l, claimed, err := a.claim(ctx)
	if err != nil || !claimed {
		return false, err
	}
	task, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	plan, err := a.prepare(task, l)
	defer plan.destroy()
	if err == nil {
		client := a.client
		if client == nil {
			client = plan.Gateway.HTTPClient()
		}
		control := modelControl{plan.Gateway, client, a.controlKey}
		if l.Action == "REVOKE" {
			err = control.revoke(task, l.RemoteID, string(plan.VirtualKey), plan.Alias)
		} else {
			err = control.ensureRoute(task, l.RemoteID, plan.Alias, plan.Frozen, string(plan.ProviderKey))
			if err == nil {
				err = control.ensureKey(task, string(plan.VirtualKey), plan.Alias)
			}
			if err == nil {
				err = control.verifyRoute(task, l.RemoteID, plan.Alias, plan.Frozen)
			}
			if err == nil {
				found, e := control.keyInfo(task, string(plan.VirtualKey), plan.Alias)
				if e != nil || !found {
					err = errModelControl
				}
			}
		}
	}
	if err != nil {
		reason := "MODEL_SYNC_UNVERIFIED"
		if errors.Is(err, errModelRouteDrift) {
			reason = "MODEL_ROUTE_DRIFT"
		}
		_, e := s.pool.Exec(ctx, `WITH changed AS (UPDATE model_sync_operations SET status='UNCERTAIN',reason_code=$3,lease_expires_at=NULL,retry_at=clock_timestamp()+interval '30 seconds',updated_at=clock_timestamp() WHERE id=$1 AND lease_token=$2 AND status='RUNNING' RETURNING connection_id,connection_version,action) UPDATE user_model_connection_versions v SET sync_status='UNCERTAIN' FROM changed o WHERE v.connection_id=o.connection_id AND v.version=o.connection_version AND o.action='UPSERT' AND v.sync_status IN ('PENDING_SYNC','SYNCING','UNCERTAIN')`, l.ID, l.Token, reason)
		if e != nil {
			return true, e
		}
		return true, errModelControl
	}
	return true, a.publish(ctx, l, plan)
}

func (a *ModelSync) publish(ctx context.Context, l modelSyncLease, p modelSyncPlan) error {
	s := a.service
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var authorized bool
	if err = tx.QueryRow(ctx, `SELECT public.hc_personal_model_authority($1,$2)`, l.Org, l.Owner).Scan(&authorized); err != nil {
		return err
	}
	var enabled bool
	var current int64
	if err = tx.QueryRow(ctx, `SELECT enabled,current_version FROM user_model_connections WHERE id=$1 FOR UPDATE`, l.Connection).Scan(&enabled, &current); err != nil {
		return err
	}
	var valid bool
	if err = tx.QueryRow(ctx, `SELECT lease_token=$2 AND lease_expires_at>clock_timestamp() AND status='RUNNING' FROM model_sync_operations WHERE id=$1 FOR UPDATE`, l.ID, l.Token).Scan(&valid); err != nil || !valid {
		return errModelControl
	}
	if l.Action == "REVOKE" {
		_, err = tx.Exec(ctx, `UPDATE user_model_connection_versions SET sync_status='REVOKED' WHERE connection_id=$1 AND version=$2`, l.Connection, l.Version)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE model_sync_operations SET status='FAILED',reason_code='CREDENTIAL_REVOKED',lease_token=lease_token+1,lease_expires_at=NULL WHERE connection_id=$1 AND connection_version=$2 AND action='UPSERT' AND status IN ('PENDING','RUNNING','UNCERTAIN')`, l.Connection, l.Version)
		}
	} else {
		if !authorized || !enabled || current != l.Version {
			return errModelControl
		}
		var approved bool
		if err = tx.QueryRow(ctx, `SELECT e.enabled AND provider.enabled FROM model_catalog_entries e JOIN model_providers provider ON provider.id=e.provider_id WHERE e.org_id=$1 AND e.id=$2 FOR SHARE OF e,provider`, l.Org, p.Frozen.Catalog.ID).Scan(&approved); err != nil || !approved {
			return errModelControl
		}
		manifest := p.Gateway.Manifest(GatewayModel{ID: p.Alias, ResponseModels: p.Frozen.Catalog.ResponseModels, ContextWindow: p.Frozen.Catalog.ContextWindow, MaxOutputTokens: p.Frozen.Catalog.MaxOutputTokens, Streaming: p.Frozen.Catalog.Streaming, ToolUse: p.Frozen.Catalog.ToolUse})
		raw, _ := json.Marshal(manifest)
		_, err = tx.Exec(ctx, `INSERT INTO model_configurations(org_id,id,version,provider,endpoint,protocol,model,credential_ref,status,manifest) VALUES($1,$2,$3,'platform-models',$4,'anthropic-messages',$5,$6,'UNVALIDATED',$7) ON CONFLICT(id,version) DO NOTHING`, l.Org, l.Connection, l.Version, p.Gateway.Endpoint, p.Alias, p.Credential, raw)
		if err == nil {
			_, err = tx.Exec(ctx, `INSERT INTO model_validations(config_id,config_version,status,reason_code) VALUES($1,$2,'UNKNOWN','VALIDATION_REQUIRED') ON CONFLICT DO NOTHING`, l.Connection, l.Version)
		}
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE user_model_connection_versions SET sync_status='SYNCED',model_configuration_id=connection_id,model_configuration_version=version WHERE connection_id=$1 AND version=$2`, l.Connection, l.Version)
		}
		if err == nil {
			_, err = tx.Exec(ctx, `INSERT INTO model_gateway_checks(connection_id,connection_version,org_id,owner_id,status,checked_at,expires_at,reason_code,next_check_at) VALUES($1,$2,$3,$4,'HEALTHY',$5,$5::timestamptz+interval '60 seconds','',$5::timestamptz+interval '30 seconds') ON CONFLICT(connection_id,connection_version) DO UPDATE SET status='HEALTHY',checked_at=EXCLUDED.checked_at,expires_at=EXCLUDED.expires_at,reason_code='',next_check_at=EXCLUDED.next_check_at,lease_token=model_gateway_checks.lease_token+1,lease_expires_at=NULL`, l.Connection, l.Version, l.Org, l.Owner, p.CheckedAt)
		}
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE model_sync_operations SET status='SUCCEEDED',reason_code='',lease_expires_at=NULL,updated_at=clock_timestamp() WHERE id=$1 AND lease_token=$2`, l.ID, l.Token)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events(id,org_id,owner_id,resource_id,action,outcome,trace_id) VALUES($1,$2,$3,$4,$5,'SUCCEEDED',$6)`, webapi.ID(), l.Org, l.Owner, l.Connection, "PERSONAL_MODEL_"+l.Action+"_SYNCED", webapi.ID())
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (a *ModelSync) Run(ctx context.Context) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		for range 10 {
			ran, err := a.RunOnce(ctx)
			if err != nil && ctx.Err() == nil {
				slog.Warn("Model synchronization temporarily unavailable")
			}
			if err != nil || !ran {
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
