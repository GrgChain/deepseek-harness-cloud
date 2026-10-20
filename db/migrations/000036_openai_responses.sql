ALTER TABLE model_catalog_entries DROP CONSTRAINT model_catalog_api_base_check;
ALTER TABLE model_catalog_entries ADD CONSTRAINT model_catalog_api_base_check
 CHECK(length(api_base)<=2048 AND (api_base LIKE 'https://%' OR (provider_id IN ('custom-openai','custom-openai-responses','custom-anthropic') AND api_base LIKE 'http://%')));

INSERT INTO model_providers(id,name,adapter_prefix,api_bases,default_model) VALUES
('custom-openai-responses','自定义 / 本地 · OpenAI Responses','custom_openai_responses','["http://localhost:11434/v1"]','user-defined');

CREATE OR REPLACE FUNCTION public.hc_seed_model_catalog() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 INSERT INTO public.model_catalog_entries(id,org_id,provider_id,model_id,name,api_base,response_models,context_window,max_output_tokens,streaming,tool_use)
 SELECT gen_random_uuid(),NEW.id,id,default_model,name,api_bases->>0,jsonb_build_array(default_model),32768,4096,true,true
 FROM public.model_providers WHERE enabled AND id NOT IN ('custom-openai','custom-openai-responses','custom-anthropic');
 RETURN NEW;
END;
$$;
