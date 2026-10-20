CREATE OR REPLACE FUNCTION public.hc_model_available(model_org uuid, model_owner uuid, config_id uuid, config_version bigint) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT EXISTS(SELECT 1 FROM public.model_connection_registry r WHERE r.org_id=model_org AND r.id=config_id
 AND (r.owner_id IS NULL OR (r.owner_id=model_owner AND EXISTS(
   SELECT 1 FROM public.user_model_connections h JOIN public.user_model_connection_versions v ON v.connection_id=h.id AND v.org_id=h.org_id AND v.owner_id=h.owner_id
   JOIN public.model_catalog_entries e ON e.id=v.catalog_entry_id AND e.org_id=v.org_id JOIN public.model_providers p ON p.id=e.provider_id
   WHERE h.id=r.id AND h.enabled AND h.archived_at IS NULL AND v.version=config_version AND v.sync_status='SYNCED'
   AND e.enabled AND p.enabled AND v.model_configuration_id=config_id AND v.model_configuration_version=config_version))));
$$;
