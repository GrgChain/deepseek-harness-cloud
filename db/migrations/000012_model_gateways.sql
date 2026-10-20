-- External model connections remain immutable versions of the organization head.
ALTER TABLE model_configurations DROP CONSTRAINT model_configurations_provider_check;
ALTER TABLE model_configurations DROP CONSTRAINT model_configurations_protocol_check;
ALTER TABLE model_configurations ADD COLUMN manifest jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE model_configurations ADD CONSTRAINT model_connection_type CHECK (
 (provider='deepseek-official' AND protocol='deepseek-messages' AND manifest='{}'::jsonb) OR
 (provider='litellm' AND protocol='anthropic-messages' AND jsonb_typeof(manifest)='object'
  AND manifest ?& ARRAY['id','revision','digest','context_window','max_output_tokens','streaming','tool_use','response_models']
  AND manifest->>'id'=model AND manifest->>'digest' ~ '^sha256:[a-f0-9]{64}$'));
ALTER TABLE model_validations ADD COLUMN evidence jsonb NOT NULL DEFAULT '{}'::jsonb
 CHECK (jsonb_typeof(evidence)='object');
CREATE TABLE model_directories (
 config_id uuid NOT NULL,
 config_version bigint NOT NULL,
 checked_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL,
 status text NOT NULL CHECK(status IN ('HEALTHY','UNHEALTHY')),
 reason_code text NOT NULL DEFAULT '',
 items jsonb NOT NULL DEFAULT '[]'::jsonb CHECK(jsonb_typeof(items)='array' AND jsonb_array_length(items)<=100),
 PRIMARY KEY(config_id,config_version),
 FOREIGN KEY(config_id,config_version) REFERENCES model_configurations(id,version),
 CHECK(expires_at>checked_at)
);
