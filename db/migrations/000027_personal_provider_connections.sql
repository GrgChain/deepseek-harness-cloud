CREATE TABLE personal_provider_connections (
 id uuid PRIMARY KEY,
 org_id uuid NOT NULL,
 owner_id uuid NOT NULL,
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 100),
 provider_id text NOT NULL REFERENCES model_providers(id),
 api_base text NOT NULL CHECK(length(api_base) BETWEEN 1 AND 2048),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 status text NOT NULL DEFAULT 'ACTIVE' CHECK(status IN ('ACTIVE','DELETING','DELETED')),
 create_key_sha256 bytea CHECK(create_key_sha256 IS NULL OR octet_length(create_key_sha256)=32),
 create_request_sha256 bytea CHECK(create_request_sha256 IS NULL OR octet_length(create_request_sha256)=32),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(org_id,owner_id,id),
 UNIQUE(org_id,owner_id,create_key_sha256),
 FOREIGN KEY(org_id,owner_id) REFERENCES memberships(org_id,user_id)
);
ALTER TABLE user_model_connections ADD COLUMN provider_connection_id uuid;
ALTER TABLE user_model_connections ADD COLUMN provider_retired boolean NOT NULL DEFAULT false;
ALTER TABLE user_model_connections ADD CONSTRAINT personal_provider_owner_fk FOREIGN KEY(org_id,owner_id,provider_connection_id) REFERENCES personal_provider_connections(org_id,owner_id,id);
CREATE INDEX personal_provider_models ON user_model_connections(provider_connection_id) WHERE archived_at IS NULL;
-- Existing connections retain their own credentials and immutable runtime IDs.
-- They are never merged merely because names or endpoint roots happen to match.
INSERT INTO personal_provider_connections(id,org_id,owner_id,name,provider_id,api_base)
SELECT h.id,h.org_id,h.owner_id,h.name,v.manifest->'catalog'->>'provider',v.manifest->'catalog'->>'api_base'
FROM user_model_connections h JOIN user_model_connection_versions v ON v.connection_id=h.id AND v.version=h.current_version
WHERE h.archived_at IS NULL;
UPDATE user_model_connections h SET provider_connection_id=h.id WHERE EXISTS(SELECT 1 FROM personal_provider_connections p WHERE p.id=h.id);
-- API cannot SELECT the provider credential ciphertext directly. This bounded
-- function returns only one active credential of the caller's own connection,
-- after rechecking the real Portal session and current database permission.
CREATE FUNCTION public.hc_personal_provider_credential(requested_org uuid, requested_owner uuid, requested_session uuid, requested_generation bigint, requested_provider uuid)
RETURNS TABLE(connection_id uuid, secret_id uuid, bound_version bigint,key_id text,nonce bytea,ciphertext bytea)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 IF public.hc_access_lock_principal(requested_org,requested_owner,requested_session,requested_generation) IS NULL
 OR NOT public.hc_has_permission(requested_org,requested_owner,'personal-model:write') THEN RETURN; END IF;
 RETURN QUERY SELECT h.id,s.id,s.bound_version,s.key_id,s.nonce,s.ciphertext
 FROM public.personal_provider_connections p JOIN public.user_model_connections h ON h.provider_connection_id=p.id AND h.org_id=p.org_id AND h.owner_id=p.owner_id
 JOIN public.user_model_connection_versions v ON v.connection_id=h.id AND v.version=h.current_version
 JOIN public.external_provider_secrets s ON s.id=v.provider_secret_id AND s.connection_id=h.id AND s.org_id=h.org_id AND s.owner_id=h.owner_id
 WHERE p.id=requested_provider AND p.org_id=requested_org AND p.owner_id=requested_owner AND p.status='ACTIVE'
 AND h.enabled AND NOT h.provider_retired AND h.archived_at IS NULL AND s.revoked_at IS NULL
 ORDER BY h.created_at,h.id LIMIT 1;
END;
$$;
REVOKE ALL ON FUNCTION public.hc_personal_provider_credential(uuid,uuid,uuid,bigint,uuid) FROM PUBLIC;
