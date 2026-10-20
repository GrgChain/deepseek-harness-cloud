-- Personal endpoint/model selections are isolated from the organization catalog.
ALTER TABLE model_catalog_entries ADD COLUMN owner_id uuid;
ALTER TABLE model_catalog_entries ADD CONSTRAINT model_catalog_owner_fk FOREIGN KEY(org_id,owner_id) REFERENCES memberships(org_id,user_id);
ALTER TABLE model_catalog_entries DROP CONSTRAINT model_catalog_entries_org_id_provider_id_model_id_api_base_key;
CREATE UNIQUE INDEX model_catalog_shared_key ON model_catalog_entries(org_id,provider_id,model_id,api_base) WHERE owner_id IS NULL;
CREATE INDEX model_catalog_personal_key ON model_catalog_entries(org_id,owner_id,provider_id,model_id,api_base) WHERE owner_id IS NOT NULL;
ALTER TABLE model_catalog_entries DROP CONSTRAINT model_catalog_entries_api_base_check;
ALTER TABLE model_catalog_entries ADD CONSTRAINT model_catalog_api_base_check CHECK(length(api_base)<=2048 AND (api_base LIKE 'https://%' OR (provider_id IN ('custom-openai','custom-anthropic') AND api_base LIKE 'http://%')));
INSERT INTO model_providers(id,name,adapter_prefix,api_bases,default_model) VALUES
('custom-openai','自定义 / 本地 · OpenAI','custom_openai','["http://localhost:11434/v1"]','user-defined'),
('custom-anthropic','自定义 / 本地 · Anthropic','custom_anthropic','["http://localhost:11434"]','user-defined');
-- Current compatible defaults; per-user discovery reads the live official API.
UPDATE model_providers SET default_model='deepseek-flash',parameters='{"extra_body":{"thinking":{"type":"disabled"}}}',version=version+1 WHERE id='deepseek';
UPDATE model_providers SET default_model='kimi-k2.6',version=version+1 WHERE id='moonshot';
CREATE OR REPLACE FUNCTION public.hc_seed_model_catalog() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 INSERT INTO public.model_catalog_entries(id,org_id,provider_id,model_id,name,api_base,response_models,context_window,max_output_tokens,streaming,tool_use)
 SELECT gen_random_uuid(),NEW.id,id,default_model,name,api_bases->>0,jsonb_build_array(default_model),32768,4096,true,true
 FROM public.model_providers WHERE enabled AND id NOT IN ('custom-openai','custom-anthropic');
 RETURN NEW;
END;
$$;
INSERT INTO model_catalog_entries(id,org_id,provider_id,model_id,name,api_base,response_models,context_window,max_output_tokens,streaming,tool_use)
SELECT gen_random_uuid(),o.id,p.id,p.default_model,p.name,p.api_bases->>0,jsonb_build_array(p.default_model),32768,4096,true,true
FROM organizations o CROSS JOIN model_providers p WHERE p.id IN ('deepseek','moonshot')
ON CONFLICT(org_id,provider_id,model_id,api_base) WHERE owner_id IS NULL DO NOTHING;
-- Retired defaults without frozen references no longer appear in the picker.
UPDATE model_catalog_entries e SET enabled=false,version=version+1,updated_at=clock_timestamp()
WHERE ((provider_id='deepseek' AND model_id IN ('deepseek-chat','deepseek-reasoner')) OR (provider_id='moonshot' AND model_id='kimi-k2.5'))
AND NOT EXISTS(SELECT 1 FROM user_model_connection_versions v WHERE v.catalog_entry_id=e.id);
