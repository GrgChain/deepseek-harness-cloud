-- Read-only remote verification is independent of synchronization and paid
-- inference evidence. An upgrade never fabricates a successful remote check.
CREATE TABLE model_gateway_checks (
    connection_id uuid NOT NULL,
    connection_version bigint NOT NULL,
    org_id uuid NOT NULL,
    owner_id uuid NOT NULL,
    status text NOT NULL DEFAULT 'UNKNOWN' CHECK (status IN ('UNKNOWN','HEALTHY','UNHEALTHY')),
    checked_at timestamptz,
    expires_at timestamptz,
    reason_code text NOT NULL DEFAULT 'MODEL_GATEWAY_UNVERIFIED' CHECK (reason_code ~ '^[A-Z0-9_]{0,128}$'),
    attempted_at timestamptz,
    next_check_at timestamptz NOT NULL DEFAULT now(),
    lease_token bigint NOT NULL DEFAULT 0 CHECK (lease_token>=0),
    lease_expires_at timestamptz,
    PRIMARY KEY(connection_id,connection_version),
    FOREIGN KEY(org_id,owner_id,connection_id,connection_version)
        REFERENCES user_model_connection_versions(org_id,owner_id,connection_id,version) ON DELETE CASCADE,
    CHECK (status<>'HEALTHY' OR (checked_at IS NOT NULL AND expires_at IS NOT NULL AND expires_at>checked_at AND expires_at<=checked_at+interval '60 seconds' AND reason_code=''))
);
CREATE INDEX model_gateway_checks_due ON model_gateway_checks(next_check_at,connection_id,connection_version);
INSERT INTO model_gateway_checks(connection_id,connection_version,org_id,owner_id)
SELECT connection_id,version,org_id,owner_id FROM user_model_connection_versions WHERE sync_status='SYNCED';

CREATE OR REPLACE FUNCTION public.hc_model_available(model_org uuid, model_owner uuid, config_id uuid, config_version bigint) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT EXISTS(SELECT 1 FROM public.model_connection_registry r WHERE r.org_id=model_org AND r.id=config_id
 AND (r.owner_id IS NULL OR (r.owner_id=model_owner AND EXISTS(
   SELECT 1 FROM public.user_model_connections h JOIN public.user_model_connection_versions v ON v.connection_id=h.id AND v.org_id=h.org_id AND v.owner_id=h.owner_id
   JOIN public.model_catalog_entries e ON e.id=v.catalog_entry_id AND e.org_id=v.org_id JOIN public.model_providers p ON p.id=e.provider_id
   JOIN public.model_gateway_checks g ON g.connection_id=v.connection_id AND g.connection_version=v.version AND g.org_id=v.org_id AND g.owner_id=v.owner_id
   JOIN public.memberships member ON member.org_id=h.org_id AND member.user_id=h.owner_id
   JOIN public.users u ON u.id=member.user_id JOIN public.organizations o ON o.id=member.org_id
   WHERE h.id=r.id AND h.enabled AND h.archived_at IS NULL AND v.version=config_version AND v.sync_status='SYNCED'
   AND e.enabled AND p.enabled AND v.model_configuration_id=config_id AND v.model_configuration_version=config_version
   AND member.status='ACTIVE' AND u.status='ACTIVE' AND o.status='ACTIVE' AND public.hc_has_permission(h.org_id,h.owner_id,'personal-model:write')
   AND g.status='HEALTHY' AND g.checked_at<=clock_timestamp() AND g.expires_at>clock_timestamp()))));
$$;
REVOKE ALL ON model_gateway_checks FROM PUBLIC;
