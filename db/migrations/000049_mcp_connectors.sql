INSERT INTO permissions(id,assignable,dependencies) VALUES
 ('connectors:read',true,'{}'),
 ('connectors:manage',true,'{connectors:read}'),
 ('connectors:invoke',true,'{connectors:read}');
WITH granted AS (
 INSERT INTO role_permissions(role_id,permission_id)
 SELECT r.id,p.id FROM roles r CROSS JOIN permissions p
 WHERE r.builtin AND r.code IN ('ADMIN','MEMBER') AND p.id LIKE 'connectors:%'
 ON CONFLICT DO NOTHING RETURNING role_id
), changed AS (
 UPDATE roles r SET version=version+1,updated_at=clock_timestamp()
 WHERE EXISTS(SELECT 1 FROM granted g WHERE g.role_id=r.id) RETURNING r.id,r.org_id
)
INSERT INTO audit_events(id,org_id,resource_id,action,outcome,trace_id,details)
 SELECT gen_random_uuid(),org_id,id,'CONNECTOR_DEFAULT_PERMISSIONS_GRANTED','SUCCEEDED',gen_random_uuid()::text,jsonb_build_object('migration',49) FROM changed;

CREATE OR REPLACE FUNCTION public.hc_seed_roles() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE admin_id uuid:=gen_random_uuid(); member_id uuid:=gen_random_uuid();
BEGIN
 INSERT INTO public.roles(id,org_id,code,name,builtin) VALUES
 (admin_id,NEW.id,'ADMIN','管理员',true),(member_id,NEW.id,'MEMBER','成员',true);
 INSERT INTO public.role_permissions SELECT admin_id,id FROM public.permissions;
 INSERT INTO public.role_permissions SELECT member_id,id FROM public.permissions
 WHERE id LIKE 'instances:%' OR id LIKE 'personal-model:%' OR id LIKE 'skills:%'
 OR id LIKE 'memory:%' OR id LIKE 'knowledge:%' OR id LIKE 'connectors:%'
 OR id IN ('configuration:read','audit:read');
 RETURN NEW;
END;
$$;

CREATE TABLE mcp_connectors (
 id uuid PRIMARY KEY, org_id uuid NOT NULL REFERENCES organizations(id), owner_id uuid NOT NULL REFERENCES users(id),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 100),
 description text NOT NULL DEFAULT '' CHECK(length(description)<=1000),
 endpoint_id text NOT NULL, protocol_version text NOT NULL CHECK(protocol_version IN ('2026-07-28','2025-11-25')),
 auth_type text NOT NULL CHECK(auth_type IN ('none','bearer')),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0), catalog_version bigint NOT NULL DEFAULT 0,
 enabled boolean NOT NULL DEFAULT true, status text NOT NULL DEFAULT 'UNVERIFIED' CHECK(status IN ('UNVERIFIED','READY','FAILED','AUTH_REQUIRED')),
 server_info jsonb NOT NULL DEFAULT '{}', tools jsonb NOT NULL DEFAULT '[]',
 key_id text NOT NULL, nonce bytea NOT NULL CHECK(octet_length(nonce)=12), ciphertext bytea NOT NULL,
 verified_at timestamptz, deleted_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(), updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(org_id,owner_id,id)
);
CREATE UNIQUE INDEX mcp_connector_name ON mcp_connectors(org_id,owner_id,name) WHERE deleted_at IS NULL;
CREATE INDEX mcp_connector_owner ON mcp_connectors(org_id,owner_id,updated_at DESC,id) WHERE deleted_at IS NULL;
CREATE TABLE mcp_calls (
 id uuid PRIMARY KEY, org_id uuid NOT NULL, owner_id uuid NOT NULL, connector_id uuid NOT NULL,
 connector_version bigint NOT NULL, catalog_version bigint NOT NULL, tool_name text NOT NULL,
 request_sha256 text NOT NULL, idempotency_key text NOT NULL,
 status text NOT NULL CHECK(status IN ('RUNNING','SUCCEEDED','TOOL_ERROR','REJECTED','UNKNOWN')),
 reason text NOT NULL DEFAULT '', timeout_seconds integer NOT NULL CHECK(timeout_seconds BETWEEN 1 AND 120),
 cancel_requested boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(), completed_at timestamptz,
 UNIQUE(org_id,owner_id,idempotency_key),
 FOREIGN KEY(org_id,owner_id,connector_id) REFERENCES mcp_connectors(org_id,owner_id,id)
);
CREATE INDEX mcp_calls_owner ON mcp_calls(org_id,owner_id,created_at DESC,id);
