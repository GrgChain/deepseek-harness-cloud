ALTER TABLE personal_provider_connections RENAME TO personal_provider_accounts;
ALTER TABLE user_model_connections RENAME COLUMN provider_connection_id TO provider_account_id;
ALTER TABLE user_model_connections RENAME CONSTRAINT personal_provider_owner_fk TO personal_provider_account_owner_fk;
ALTER INDEX personal_provider_models RENAME TO personal_provider_account_models;
DROP FUNCTION public.hc_personal_provider_credential(uuid,uuid,uuid,bigint,uuid);
CREATE FUNCTION public.hc_personal_provider_account_credential(requested_org uuid, requested_owner uuid, requested_session uuid, requested_generation bigint, requested_provider uuid)
RETURNS TABLE(connection_id uuid, secret_id uuid, bound_version bigint,key_id text,nonce bytea,ciphertext bytea)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 IF public.hc_access_lock_principal(requested_org,requested_owner,requested_session,requested_generation) IS NULL
 OR NOT public.hc_has_permission(requested_org,requested_owner,'personal-model:write') THEN RETURN; END IF;
 RETURN QUERY SELECT h.id,s.id,s.bound_version,s.key_id,s.nonce,s.ciphertext
 FROM public.personal_provider_accounts p JOIN public.user_model_connections h ON h.provider_account_id=p.id AND h.org_id=p.org_id AND h.owner_id=p.owner_id
 JOIN public.user_model_connection_versions v ON v.connection_id=h.id AND v.version=h.current_version
 JOIN public.external_provider_secrets s ON s.id=v.provider_secret_id AND s.connection_id=h.id AND s.org_id=h.org_id AND s.owner_id=h.owner_id
 WHERE p.id=requested_provider AND p.org_id=requested_org AND p.owner_id=requested_owner AND p.status='ACTIVE'
 AND h.enabled AND NOT h.provider_retired AND h.archived_at IS NULL AND s.revoked_at IS NULL
 ORDER BY h.created_at,h.id LIMIT 1;
END;
$$;
REVOKE ALL ON FUNCTION public.hc_personal_provider_account_credential(uuid,uuid,uuid,bigint,uuid) FROM PUBLIC;
