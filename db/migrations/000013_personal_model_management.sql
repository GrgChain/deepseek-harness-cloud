-- Platform-owned provider/catalog metadata and private BYOK connections.
-- LiteLLM is a synchronized execution target, never the user-facing manager.
CREATE TABLE model_providers (
    id text PRIMARY KEY CHECK (id ~ '^[a-z][a-z0-9_-]{1,31}$'),
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 64),
    litellm_prefix text NOT NULL CHECK (litellm_prefix ~ '^[a-z][a-z0-9_]*$'),
    api_bases jsonb NOT NULL CHECK (jsonb_typeof(api_bases)='array' AND jsonb_array_length(api_bases) BETWEEN 1 AND 16),
    default_model text NOT NULL CHECK (length(default_model) BETWEEN 1 AND 128),
    parameters jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(parameters)='object'),
    enabled boolean NOT NULL DEFAULT true,
    version bigint NOT NULL DEFAULT 1 CHECK (version>0)
);
INSERT INTO model_providers(id,name,litellm_prefix,api_bases,default_model,parameters) VALUES
('openai','OpenAI','openai','["https://api.openai.com/v1"]','gpt-4.1','{}'),
('anthropic','Anthropic Claude','anthropic','["https://api.anthropic.com"]','claude-sonnet-4-5-20250929','{}'),
('gemini','Google Gemini','gemini','["https://generativelanguage.googleapis.com"]','gemini-2.5-flash','{"reasoning_effort":"none"}'),
('qwen','通义千问','dashscope','["https://dashscope.aliyuncs.com/compatible-mode/v1","https://dashscope-intl.aliyuncs.com/compatible-mode/v1"]','qwen-plus','{"extra_body":{"enable_thinking":false}}'),
('deepseek','DeepSeek','deepseek','["https://api.deepseek.com"]','deepseek-chat','{}'),
('moonshot','Kimi / Moonshot AI','moonshot','["https://api.moonshot.ai/v1","https://api.moonshot.cn/v1"]','kimi-k2.5','{"extra_body":{"thinking":{"type":"disabled"}}}'),
('zai','Z.AI / GLM','zai','["https://api.z.ai/api/paas/v4"]','glm-4.7','{"extra_body":{"thinking":{"type":"disabled"}}}');

CREATE TABLE model_catalog_entries (
    id uuid PRIMARY KEY,
    org_id uuid NOT NULL REFERENCES organizations(id),
    provider_id text NOT NULL REFERENCES model_providers(id),
    model_id text NOT NULL CHECK (model_id ~ '^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$'),
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    api_base text NOT NULL CHECK (api_base LIKE 'https://%' AND length(api_base)<=2048),
    response_models jsonb NOT NULL CHECK (jsonb_typeof(response_models)='array' AND jsonb_array_length(response_models) BETWEEN 1 AND 16),
    context_window bigint NOT NULL CHECK (context_window BETWEEN 1024 AND 10000000),
    max_output_tokens bigint NOT NULL CHECK (max_output_tokens BETWEEN 128 AND 1000000 AND max_output_tokens<=context_window),
    streaming boolean NOT NULL,
    tool_use boolean NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    version bigint NOT NULL DEFAULT 1 CHECK (version>0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(org_id,id), UNIQUE(org_id,provider_id,model_id,api_base)
);
CREATE FUNCTION public.hc_seed_model_catalog() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
    INSERT INTO public.model_catalog_entries(id,org_id,provider_id,model_id,name,api_base,response_models,context_window,max_output_tokens,streaming,tool_use)
    SELECT gen_random_uuid(),NEW.id,id,default_model,name,api_bases->>0,jsonb_build_array(default_model),32768,4096,true,true
    FROM public.model_providers WHERE enabled;
    RETURN NEW;
END;
$$;
REVOKE ALL ON FUNCTION public.hc_seed_model_catalog() FROM PUBLIC;
CREATE TRIGGER organizations_seed_model_catalog AFTER INSERT ON organizations FOR EACH ROW EXECUTE FUNCTION public.hc_seed_model_catalog();
INSERT INTO model_catalog_entries(id,org_id,provider_id,model_id,name,api_base,response_models,context_window,max_output_tokens,streaming,tool_use)
SELECT gen_random_uuid(),o.id,p.id,p.default_model,p.name,p.api_bases->>0,jsonb_build_array(p.default_model),32768,4096,true,true
FROM organizations o CROSS JOIN model_providers p WHERE p.enabled;

-- Keep the existing single organization head and its APIs intact; private
-- connections share the version/validation/runtime reference through a registry.
CREATE TABLE model_connection_registry (
    id uuid PRIMARY KEY,
    org_id uuid NOT NULL REFERENCES organizations(id),
    owner_id uuid,
    UNIQUE(org_id,id), UNIQUE(org_id,owner_id,id),
    FOREIGN KEY(org_id,owner_id) REFERENCES memberships(org_id,user_id)
);
INSERT INTO model_connection_registry(id,org_id) SELECT id,org_id FROM model_configuration_heads;
ALTER TABLE model_configurations DROP CONSTRAINT model_configurations_org_id_id_fkey;
ALTER TABLE model_configurations ADD CONSTRAINT model_configurations_registry_fk FOREIGN KEY(org_id,id) REFERENCES model_connection_registry(org_id,id);
CREATE FUNCTION public.hc_register_organization_model() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
    INSERT INTO public.model_connection_registry(id,org_id) VALUES(NEW.id,NEW.org_id);
    RETURN NEW;
END;
$$;
REVOKE ALL ON FUNCTION public.hc_register_organization_model() FROM PUBLIC;
CREATE TRIGGER model_heads_register BEFORE INSERT ON model_configuration_heads FOR EACH ROW EXECUTE FUNCTION public.hc_register_organization_model();

CREATE TABLE user_model_connections (
    id uuid PRIMARY KEY,
    org_id uuid NOT NULL,
    owner_id uuid NOT NULL,
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    current_version bigint NOT NULL DEFAULT 0 CHECK (current_version>=0),
    enabled boolean NOT NULL DEFAULT true,
    archived_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(org_id,owner_id,id),
    FOREIGN KEY(org_id,owner_id,id) REFERENCES model_connection_registry(org_id,owner_id,id),
    CHECK (archived_at IS NULL OR NOT enabled)
);
CREATE INDEX user_model_connections_owner ON user_model_connections(org_id,owner_id,created_at DESC,id DESC);
CREATE TABLE external_provider_secrets (
    id uuid PRIMARY KEY,
    org_id uuid NOT NULL,
    owner_id uuid NOT NULL,
    connection_id uuid NOT NULL,
    bound_version bigint NOT NULL CHECK (bound_version>0),
    key_id text NOT NULL,
    nonce bytea NOT NULL CHECK (octet_length(nonce)=12),
    ciphertext bytea NOT NULL CHECK (octet_length(ciphertext) BETWEEN 17 AND 4112),
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz,
    UNIQUE(org_id,owner_id,connection_id,id),
    FOREIGN KEY(org_id,owner_id,connection_id) REFERENCES user_model_connections(org_id,owner_id,id)
);
CREATE TABLE user_model_connection_versions (
    connection_id uuid NOT NULL,
    org_id uuid NOT NULL,
    owner_id uuid NOT NULL,
    version bigint NOT NULL CHECK (version>0),
    catalog_entry_id uuid NOT NULL,
    catalog_version bigint NOT NULL CHECK (catalog_version>0),
    provider_secret_id uuid NOT NULL,
    gateway_endpoint text NOT NULL CHECK (gateway_endpoint LIKE 'https://%' AND length(gateway_endpoint)<=2048),
    route_alias text NOT NULL UNIQUE CHECK (route_alias ~ '^hc-[a-f0-9]{32}-v[1-9][0-9]*$'),
    manifest jsonb NOT NULL CHECK (jsonb_typeof(manifest)='object'),
    sync_status text NOT NULL DEFAULT 'PENDING_SYNC' CHECK (sync_status IN ('PENDING_SYNC','SYNCING','UNCERTAIN','SYNCED','FAILED','REVOKING','REVOKED')),
    model_configuration_id uuid,
    model_configuration_version bigint,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(connection_id,version), UNIQUE(org_id,owner_id,connection_id,version),
    FOREIGN KEY(org_id,owner_id,connection_id) REFERENCES user_model_connections(org_id,owner_id,id),
    FOREIGN KEY(org_id,catalog_entry_id) REFERENCES model_catalog_entries(org_id,id),
    FOREIGN KEY(org_id,owner_id,connection_id,provider_secret_id) REFERENCES external_provider_secrets(org_id,owner_id,connection_id,id),
    FOREIGN KEY(org_id,model_configuration_id,model_configuration_version) REFERENCES model_configurations(org_id,id,version),
    CHECK ((model_configuration_id IS NULL)=(model_configuration_version IS NULL)),
    CHECK (model_configuration_id IS NULL OR (model_configuration_id=connection_id AND model_configuration_version=version)),
    CHECK (sync_status<>'SYNCED' OR model_configuration_id IS NOT NULL)
);
CREATE TABLE model_sync_operations (
    id uuid PRIMARY KEY,
    org_id uuid NOT NULL,
    owner_id uuid NOT NULL,
    connection_id uuid NOT NULL,
    connection_version bigint NOT NULL,
    action text NOT NULL CHECK (action IN ('UPSERT','REVOKE')),
    status text NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','RUNNING','UNCERTAIN','SUCCEEDED','FAILED')),
    remote_model_id text NOT NULL CHECK (length(remote_model_id) BETWEEN 1 AND 128),
    lease_token bigint NOT NULL DEFAULT 0 CHECK (lease_token>=0),
    lease_owner text,
    lease_expires_at timestamptz,
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 100),
    reason_code text NOT NULL DEFAULT '' CHECK (reason_code ~ '^[A-Z0-9_]{0,128}$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY(org_id,owner_id,connection_id,connection_version) REFERENCES user_model_connection_versions(org_id,owner_id,connection_id,version),
    UNIQUE(connection_id,connection_version,action)
);
CREATE INDEX model_sync_operations_pending ON model_sync_operations(created_at,id) WHERE status IN ('PENDING','RUNNING','UNCERTAIN');
CREATE TABLE user_model_defaults (
    org_id uuid NOT NULL,
    owner_id uuid NOT NULL,
    connection_id uuid NOT NULL,
    connection_version bigint NOT NULL,
    version bigint NOT NULL DEFAULT 1 CHECK (version>0),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(org_id,owner_id),
    FOREIGN KEY(org_id,owner_id,connection_id,connection_version) REFERENCES user_model_connection_versions(org_id,owner_id,connection_id,version)
);

INSERT INTO permissions(id,assignable,dependencies) VALUES
('personal-model:read',true,'{}'),
('personal-model:write',true,'{personal-model:read}'),
('personal-model:validate',true,'{personal-model:read}'),
('personal-model:revoke',true,'{personal-model:read}'),
('models:catalog:manage',false,'{configuration:read}');
INSERT INTO role_permissions(role_id,permission_id)
SELECT r.id,p.id FROM roles r CROSS JOIN permissions p
WHERE (r.code IN ('ADMIN','MEMBER') AND p.id LIKE 'personal-model:%') OR (r.code='ADMIN' AND p.id='models:catalog:manage');
CREATE OR REPLACE FUNCTION public.hc_seed_roles() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE admin_id uuid:=gen_random_uuid(); member_id uuid:=gen_random_uuid();
BEGIN
    INSERT INTO public.roles(id,org_id,code,name,builtin) VALUES
      (admin_id,NEW.id,'ADMIN','管理员',true),(member_id,NEW.id,'MEMBER','成员',true);
    INSERT INTO public.role_permissions SELECT admin_id,id FROM public.permissions;
    INSERT INTO public.role_permissions SELECT member_id,id FROM public.permissions
      WHERE id LIKE 'instances:%' OR id LIKE 'personal-model:%' OR id IN ('configuration:read','audit:read');
    RETURN NEW;
END;
$$;
UPDATE roles SET version=version+1,updated_at=clock_timestamp() WHERE code IN ('ADMIN','MEMBER');
UPDATE memberships SET auth_generation=auth_generation+1 WHERE role IN ('ADMIN','MEMBER');
UPDATE portal_sessions SET revoked_at=clock_timestamp() WHERE revoked_at IS NULL;
UPDATE access_sessions SET revoked_at=clock_timestamp() WHERE revoked_at IS NULL;
