-- Worker compensation uses canonical authority and queues remote removal.
-- Callers cannot use this function to revoke a healthy, active owner's model.
CREATE FUNCTION public.hc_reconcile_personal_model(requested_connection uuid) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE h public.user_model_connections%ROWTYPE; active boolean; affected bigint[];
BEGIN
 SELECT * INTO h FROM public.user_model_connections WHERE id=requested_connection;
 IF NOT FOUND THEN RETURN false; END IF;
 SELECT o.status='ACTIVE' AND u.status='ACTIVE' AND m.status='ACTIVE' INTO active
 FROM public.memberships m JOIN public.users u ON u.id=m.user_id JOIN public.organizations o ON o.id=m.org_id
 WHERE m.org_id=h.org_id AND m.user_id=h.owner_id FOR UPDATE OF m;
 PERFORM id FROM public.model_configuration_heads WHERE org_id=h.org_id FOR UPDATE;
 SELECT * INTO h FROM public.user_model_connections WHERE id=requested_connection FOR UPDATE;
 SELECT array_agg(v.version ORDER BY v.version) INTO affected
 FROM public.user_model_connection_versions v JOIN public.model_catalog_entries e ON e.id=v.catalog_entry_id AND e.org_id=v.org_id
 JOIN public.model_providers p ON p.id=e.provider_id
 WHERE v.connection_id=h.id AND v.sync_status IN ('PENDING_SYNC','SYNCING','UNCERTAIN','SYNCED','FAILED')
 AND (NOT COALESCE(active,false) OR NOT e.enabled OR NOT p.enabled);
 IF affected IS NULL THEN RETURN false; END IF;
 UPDATE public.user_model_connections SET enabled=false,updated_at=clock_timestamp() WHERE id=h.id AND current_version=ANY(affected);
 UPDATE public.external_provider_secrets SET revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE connection_id=h.id AND bound_version=ANY(affected);
 UPDATE public.secrets SET revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE id IN (
   SELECT runtime_credential_ref FROM public.user_model_connection_versions WHERE connection_id=h.id AND version=ANY(affected));
 UPDATE public.model_configurations SET status='REVOKED',revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE id=h.id AND version=ANY(affected);
 UPDATE public.model_validations SET status='UNHEALTHY',reason_code='AUTH_REVOKED',lease_expires_at=NULL,lease_token=lease_token+1 WHERE config_id=h.id AND config_version=ANY(affected);
 UPDATE public.instances SET desired_state=CASE WHEN desired_state='DELETED' THEN desired_state ELSE 'STOPPED' END,
   access_generation=access_generation+1,version=version+1,updated_at=clock_timestamp()
   WHERE model_config_id=h.id AND model_config_version=ANY(affected) AND deleted_at IS NULL;
 UPDATE public.access_routes SET enabled=false WHERE instance_id IN (SELECT id FROM public.instances WHERE model_config_id=h.id AND model_config_version=ANY(affected));
 UPDATE public.access_sessions SET revoked_at=clock_timestamp() WHERE revoked_at IS NULL AND instance_id IN (
   SELECT id FROM public.instances WHERE model_config_id=h.id AND model_config_version=ANY(affected));
 UPDATE public.model_sync_operations SET status='FAILED',reason_code='AUTH_REVOKED',lease_token=lease_token+1
   WHERE connection_id=h.id AND connection_version=ANY(affected) AND action='UPSERT' AND status IN ('PENDING','UNCERTAIN');
 UPDATE public.user_model_connection_versions SET sync_status='REVOKING' WHERE connection_id=h.id AND version=ANY(affected);
 INSERT INTO public.model_sync_operations(id,org_id,owner_id,connection_id,connection_version,action,remote_model_id)
   SELECT gen_random_uuid(),org_id,owner_id,connection_id,connection_version,'REVOKE',remote_model_id FROM public.model_sync_operations
   WHERE connection_id=h.id AND connection_version=ANY(affected) AND action='UPSERT' ON CONFLICT(connection_id,connection_version,action) DO NOTHING;
 INSERT INTO public.audit_events(id,org_id,owner_id,resource_id,action,outcome,trace_id)
   VALUES(gen_random_uuid(),h.org_id,h.owner_id,h.id,'PERSONAL_MODEL_AUTH_REVOKED','SUCCEEDED',gen_random_uuid()::text);
 RETURN true;
END;
$$;
REVOKE ALL ON FUNCTION public.hc_reconcile_personal_model(uuid) FROM PUBLIC;

CREATE OR REPLACE FUNCTION public.hc_personal_model_authority(requested_org uuid, requested_owner uuid)
RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE active boolean;
BEGIN
 SELECT o.status='ACTIVE' AND u.status='ACTIVE' AND m.status='ACTIVE'
 INTO active FROM public.memberships m JOIN public.users u ON u.id=m.user_id JOIN public.organizations o ON o.id=m.org_id
 WHERE m.org_id=requested_org AND m.user_id=requested_owner FOR UPDATE OF m;
 IF NOT FOUND THEN RETURN false; END IF;
 PERFORM id FROM public.model_configuration_heads WHERE org_id=requested_org FOR UPDATE;
 RETURN active AND public.hc_has_permission(requested_org,requested_owner,'personal-model:write');
END;
$$;
