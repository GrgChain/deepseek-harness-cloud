package devlocal

import (
	"context"
	"github.com/jackc/pgx/v5"
)

// GrantAPI is for isolated local/test databases. The migration identity owns DDL;
// application identities cannot approve runtime images or modify role mappings.
func GrantAPI(ctx context.Context, tx pgx.Tx) error {
	if err := grantWorkspaceRead(ctx, tx, "hc_api"); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `GRANT SELECT ON organizations,users,memberships TO hc_api;
GRANT SELECT,INSERT,UPDATE ON oidc_transactions,portal_sessions TO hc_api;
GRANT SELECT,INSERT,UPDATE ON identity_accounts,identity_authorizations,identity_codes,identity_access_tokens TO hc_api;
GRANT EXECUTE ON FUNCTION public.hc_identity_lock_authority(uuid,uuid,text,bigint,bigint) TO hc_api;
GRANT EXECUTE ON FUNCTION public.hc_identity_lock_provisioning(uuid,uuid) TO hc_api;
GRANT UPDATE(auth_generation) ON memberships TO hc_api;
GRANT UPDATE ON access_sessions TO hc_api;
GRANT SELECT(id,portal_session_id,revoked_at,instance_id) ON access_sessions TO hc_api;
GRANT INSERT ON audit_events TO hc_api;
GRANT SELECT,INSERT,UPDATE ON model_directories TO hc_api;
GRANT SELECT ON model_providers,model_catalog_entries TO hc_api;
GRANT UPDATE(version) ON model_providers TO hc_api;
GRANT INSERT,UPDATE ON model_catalog_entries TO hc_api;
GRANT SELECT,INSERT ON model_connection_registry,user_model_connections,user_model_connection_versions,model_sync_operations TO hc_api;
GRANT SELECT ON model_gateway_checks TO hc_api;
GRANT SELECT,INSERT,UPDATE ON personal_provider_accounts TO hc_api;
GRANT EXECUTE ON FUNCTION public.hc_personal_provider_account_credential(uuid,uuid,uuid,bigint,uuid) TO hc_api;
GRANT UPDATE ON user_model_connections,user_model_connection_versions,model_sync_operations TO hc_api;
GRANT SELECT,INSERT,UPDATE,DELETE ON user_model_defaults TO hc_api;
GRANT UPDATE(revoked_at) ON external_provider_secrets TO hc_api;
GRANT EXECUTE ON FUNCTION public.hc_model_available(uuid,uuid,uuid,bigint) TO hc_api;
GRANT INSERT ON external_provider_secrets TO hc_api;
GRANT SELECT(id,org_id,owner_id,connection_id,bound_version,revoked_at) ON external_provider_secrets TO hc_api;
GRANT SELECT,INSERT,UPDATE ON secrets,model_configuration_heads,model_configurations,model_validations,probe_budgets,organization_defaults TO hc_api;
GRANT EXECUTE ON FUNCTION public.hc_worker_lock_model(uuid) TO hc_api;
GRANT EXECUTE ON FUNCTION public.hc_access_lock_principal(uuid,uuid,uuid,bigint),public.hc_access_lock_instance(uuid,uuid,uuid) TO hc_api;
GRANT SELECT ON access_challenges TO hc_api;
GRANT UPDATE(consumed_at) ON access_challenges TO hc_api;
GRANT SELECT,INSERT ON access_tickets TO hc_api;
GRANT SELECT ON approved_templates,approved_specs,quota_policies,instances,operations,outbox_events TO hc_api;
GRANT SELECT ON storage_assets,instance_health,audit_events TO hc_api;
GRANT SELECT,INSERT ON storage_release_requests TO hc_api;
GRANT SELECT,INSERT,DELETE ON storage_browse_requests TO hc_api;
GRANT SELECT,INSERT,UPDATE,DELETE ON personal_skills,skill_import_previews,skill_replacements TO hc_api;
GRANT SELECT,INSERT ON workspace_skill_snapshots TO hc_api;
GRANT SELECT,INSERT ON workspace_extensions TO hc_api;
GRANT SELECT,INSERT,UPDATE,DELETE ON context_connections,context_bindings,context_previews TO hc_api;
GRANT SELECT,INSERT,UPDATE,DELETE ON context_operations TO hc_api;
GRANT SELECT,INSERT,UPDATE ON mcp_connectors,mcp_calls TO hc_api;
GRANT SELECT,INSERT,DELETE ON mcp_oauth_transactions TO hc_api;
GRANT UPDATE(status,maintenance_intent,owner_removed_at) ON storage_assets TO hc_api;
GRANT SELECT(id,org_id,owner_id,instance_id,asset_id,capacity_bytes,template_version,image_digest,status,created_at,completed_at,verified_at,expires_at,backup_sha256,set_id) ON storage_backups TO hc_api;
GRANT SELECT,INSERT,DELETE ON idempotency_records TO hc_api;
GRANT SELECT,INSERT ON quota_reservations TO hc_api;
GRANT SELECT ON quota_commitments TO hc_api;
GRANT SELECT,INSERT ON user_quota_policies TO hc_api;
GRANT UPDATE(version,max_instances,cpu_millicores,memory_bytes,storage_bytes,updated_at) ON user_quota_policies TO hc_api;
GRANT UPDATE(version,max_instances,max_user_instances,cpu_millicores,memory_bytes,storage_bytes) ON quota_policies TO hc_api;
GRANT INSERT ON instances,storage_assets TO hc_api;
GRANT UPDATE(storage_asset_id) ON instances TO hc_api;
GRANT UPDATE(access_generation,version,desired_state,observed_state,updated_at) ON instances TO hc_api;
GRANT SELECT ON access_routes TO hc_api;
GRANT UPDATE(enabled) ON access_routes TO hc_api;
GRANT INSERT ON operations,outbox_events TO hc_api;
GRANT SELECT ON roles,role_permissions,permissions,identity_provisioning_operations TO hc_api;
GRANT SELECT ON identity_password_resets TO hc_api;
GRANT EXECUTE ON FUNCTION public.hc_iam_password_reset(uuid,uuid,uuid,bigint,text,uuid,bigint,uuid,bigint,text,bytea,bytea,text) TO hc_api;
GRANT UPDATE(last_login_at) ON users TO hc_api;
GRANT EXECUTE ON FUNCTION public.hc_effective_permissions(uuid,uuid),public.hc_has_permission(uuid,uuid,text) TO hc_api;
GRANT EXECUTE ON FUNCTION public.hc_iam_change(uuid,uuid,uuid,bigint,text,uuid,bigint,jsonb,bytea,bytea,text) TO hc_api;`)
	return err
}

// GrantWorker grants scheduling and resource publication plus controlled
// authority locking. It cannot write identities, approve templates or run DDL.
func GrantWorker(ctx context.Context, tx pgx.Tx) error {
	if err := grantWorkspaceRead(ctx, tx, "hc_worker"); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `GRANT SELECT ON workspace_template_versions,workspace_templates,workspace_builds TO hc_worker;
GRANT INSERT,UPDATE ON workspace_workspaces,workspace_builds TO hc_worker;
GRANT SELECT(status) ON users TO hc_worker;
GRANT SELECT(org_id,user_id,status,auth_generation) ON memberships TO hc_worker;
GRANT EXECUTE ON FUNCTION public.hc_has_permission(uuid,uuid,text) TO hc_worker;`); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `GRANT EXECUTE ON FUNCTION public.hc_cleanup_expired(integer) TO hc_worker;
GRANT SELECT,INSERT ON operations,outbox_events TO hc_worker;
GRANT UPDATE(lease_owner,lease_token,lease_expires_at,status,stage,updated_at) ON operations TO hc_worker;
GRANT UPDATE(status,retry_at,attempts) ON outbox_events TO hc_worker;
GRANT EXECUTE ON FUNCTION public.hc_worker_lock_authority(uuid,uuid) TO hc_worker;
GRANT EXECUTE ON FUNCTION public.hc_worker_lock_model(uuid) TO hc_worker;
GRANT SELECT ON organizations,model_configuration_heads,organization_defaults TO hc_worker;
GRANT SELECT ON model_providers,model_catalog_entries,model_connection_registry,user_model_connections,user_model_connection_versions,model_sync_operations,user_model_defaults TO hc_worker;
GRANT UPDATE ON model_sync_operations,user_model_connection_versions TO hc_worker;
GRANT SELECT,INSERT,UPDATE ON model_gateway_checks TO hc_worker;
GRANT UPDATE(current_version,archived_at) ON user_model_connections TO hc_worker;
GRANT SELECT,UPDATE ON personal_provider_accounts TO hc_worker;
GRANT UPDATE(version) ON model_providers,model_catalog_entries TO hc_worker;
GRANT SELECT ON external_provider_secrets TO hc_worker;
GRANT INSERT ON secrets,model_configurations,model_validations TO hc_worker;
GRANT EXECUTE ON FUNCTION public.hc_model_available(uuid,uuid,uuid,bigint),public.hc_personal_model_authority(uuid,uuid),public.hc_reconcile_personal_model(uuid) TO hc_worker;
GRANT SELECT(id,version,runtime_manifest,enabled) ON approved_templates TO hc_worker;
GRANT UPDATE(status) ON model_configurations TO hc_worker;
GRANT UPDATE(status,checked_at,expires_at,reason_code,lease_token,lease_expires_at,attempted_at,evidence) ON model_validations TO hc_worker;
GRANT SELECT,INSERT ON probe_budgets TO hc_worker;
GRANT UPDATE(attempts) ON probe_budgets TO hc_worker;
GRANT SELECT ON instances,storage_assets,quota_policies,quota_reservations,model_configurations,model_validations,workspace_skill_snapshots TO hc_worker;
GRANT SELECT,UPDATE(applied_at,applied_generation) ON workspace_extensions TO hc_worker;
GRANT EXECUTE ON FUNCTION public.hc_extensions_lock_authority(uuid,uuid,uuid,bigint) TO hc_worker;
GRANT SELECT,UPDATE ON context_connections,context_bindings,mcp_connectors TO hc_worker;
GRANT SELECT ON context_operations TO hc_worker;
GRANT SELECT,INSERT,UPDATE ON mcp_calls,mcp_oauth_transactions TO hc_worker;
GRANT SELECT(id,asset_id,status) ON storage_backups TO hc_worker;
GRANT SELECT(id,org_id,revoked_at,key_id,nonce,ciphertext) ON secrets TO hc_worker;
GRANT SELECT(id,workspace_owner_id,status) ON users TO hc_worker;
GRANT UPDATE(version) ON quota_policies TO hc_worker;
GRANT UPDATE(desired_state,access_generation,observed_state,config_snapshot,model_config_id,model_config_version,version,updated_at,deleted_at,workspace_id) ON instances TO hc_worker;
GRANT SELECT,UPDATE ON storage_release_requests TO hc_worker;
GRANT SELECT,UPDATE,DELETE ON storage_browse_requests TO hc_worker;
GRANT SELECT ON recovery_quota_holds TO hc_worker;
GRANT UPDATE(status,maintenance_intent,init_version,retention_until,filesystem_id,usage_used_bytes,usage_available_bytes,usage_sampled_at,usage_attempted_at,usage_error) ON storage_assets TO hc_worker;
GRANT UPDATE(status,updated_at) ON quota_reservations TO hc_worker;
GRANT UPDATE(intent,build_id,error_code,error_message,reconciled_at) ON operations TO hc_worker;
GRANT SELECT,INSERT,UPDATE ON access_routes,instance_health TO hc_worker;
GRANT SELECT,INSERT,UPDATE,DELETE ON native_sessions TO hc_worker;
GRANT SELECT(instance_id,revoked_at),UPDATE(revoked_at) ON access_sessions TO hc_worker;
GRANT SELECT(operation_id),UPDATE(expires_at) ON idempotency_records TO hc_worker;
GRANT INSERT ON audit_events TO hc_worker;`)
	return err
}

// GrantGateway provides current authorization and native-session access. It
// cannot read model ciphertext or mutate identities, instances, routes or quota.
func GrantGateway(ctx context.Context, tx pgx.Tx) error {
	if err := grantWorkspaceRead(ctx, tx, "hc_gateway"); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `GRANT SELECT ON organizations,memberships,model_configurations,instances,storage_assets,instance_health,access_routes,native_sessions TO hc_gateway;
GRANT EXECUTE ON FUNCTION public.hc_model_available(uuid,uuid,uuid,bigint) TO hc_gateway;
GRANT SELECT(id,instance_id,status,type,build_id) ON operations TO hc_gateway;
GRANT SELECT(id,workspace_owner_id,status) ON users TO hc_gateway;
GRANT SELECT(id,org_id,revoked_at) ON secrets TO hc_gateway;
GRANT SELECT(id,org_id,user_id,auth_generation,expires_at,last_seen_at,revoked_at) ON portal_sessions TO hc_gateway;
GRANT UPDATE(last_seen_at) ON portal_sessions TO hc_gateway;
GRANT SELECT,INSERT,UPDATE ON access_challenges,access_tickets,access_sessions TO hc_gateway;
GRANT EXECUTE ON FUNCTION public.hc_worker_lock_model(uuid),public.hc_access_lock_principal(uuid,uuid,uuid,bigint),public.hc_access_lock_instance(uuid,uuid,uuid) TO hc_gateway;
GRANT INSERT ON audit_events TO hc_gateway;
GRANT EXECUTE ON FUNCTION public.hc_has_permission(uuid,uuid,text) TO hc_gateway;`)
	return err
}

func grantWorkspaceRead(ctx context.Context, tx pgx.Tx, role string) error {
	// Only these fixed role names may reach this local grant helper.
	if role != "hc_api" && role != "hc_worker" && role != "hc_gateway" {
		return pgx.ErrNoRows
	}
	_, err := tx.Exec(ctx, `GRANT SELECT ON workspace_authority,workspace_workspaces TO `+role+`;
GRANT SELECT(id,workspace_id,template_version_id,build_number,transition,initiator_id,job_id,job_status,error_code,parameters,created_at,started_at,completed_at) ON workspace_builds TO `+role+`;`)
	return err
}

// GrantModelGateway has no identity writes or workspace/control-plane access.
func GrantModelGateway(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `GRANT USAGE ON SCHEMA public TO hc_model_gateway;
GRANT SELECT ON hc_schema_migrations,organizations,model_providers,model_catalog_entries,user_model_connections,user_model_connection_versions,model_configurations,model_sync_operations,external_provider_secrets,personal_provider_accounts,model_validations,model_gateway_checks TO hc_model_gateway;
GRANT SELECT(id,status) ON users TO hc_model_gateway;
GRANT SELECT(org_id,user_id,status) ON memberships TO hc_model_gateway;
GRANT SELECT(id,org_id,key_id,nonce,ciphertext,revoked_at) ON secrets TO hc_model_gateway;
GRANT SELECT,INSERT,UPDATE ON model_gateway_routes,model_gateway_keys TO hc_model_gateway;
GRANT EXECUTE ON FUNCTION public.hc_has_permission(uuid,uuid,text) TO hc_model_gateway;`)
	return err
}
