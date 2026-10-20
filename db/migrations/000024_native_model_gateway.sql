-- The platform Go model gateway replaces the external model proxy. Older
-- migration checksums remain unchanged; active metadata uses platform names.
ALTER TABLE model_providers RENAME COLUMN litellm_prefix TO adapter_prefix;
ALTER TABLE model_configurations DROP CONSTRAINT model_connection_type;
UPDATE model_configurations SET provider='platform-models' WHERE provider='litellm';
ALTER TABLE model_configurations ADD CONSTRAINT model_connection_type CHECK (
 (provider='deepseek-official' AND protocol='deepseek-messages' AND manifest='{}'::jsonb) OR
 (provider='platform-models' AND protocol='anthropic-messages' AND jsonb_typeof(manifest)='object'
  AND manifest ?& ARRAY['id','response_models','context_window','max_output_tokens','streaming','tool_use','revision','digest']
  AND manifest->>'id'=model AND manifest->>'digest' ~ '^sha256:[a-f0-9]{64}$'));

CREATE TABLE model_gateway_routes (
 id uuid PRIMARY KEY,
 org_id uuid NOT NULL,
 owner_id uuid NOT NULL,
 connection_id uuid NOT NULL,
 connection_version bigint NOT NULL,
 alias text NOT NULL UNIQUE CHECK(alias ~ '^hc-[a-f0-9]{32}-v[1-9][0-9]*$'),
 parameters jsonb NOT NULL CHECK(jsonb_typeof(parameters)='object' AND NOT parameters ? 'api_key'),
 enabled boolean NOT NULL DEFAULT true,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(connection_id,connection_version),
 FOREIGN KEY(org_id,owner_id,connection_id,connection_version) REFERENCES user_model_connection_versions(org_id,owner_id,connection_id,version)
);
CREATE TABLE model_gateway_keys (
 key_hash bytea PRIMARY KEY CHECK(octet_length(key_hash)=32),
 route_id uuid NOT NULL UNIQUE REFERENCES model_gateway_routes(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 revoked_at timestamptz,
 window_started timestamptz NOT NULL DEFAULT now(),
 requests integer NOT NULL DEFAULT 0 CHECK(requests BETWEEN 0 AND 60)
);
REVOKE ALL ON model_gateway_routes,model_gateway_keys FROM PUBLIC;

-- Existing personal versions must be registered with the native boundary and
-- capability tested again. Old inference evidence cannot certify this adapter.
UPDATE model_validations SET status='UNKNOWN',reason_code='VALIDATION_REQUIRED',expires_at=NULL,lease_token=lease_token+1,lease_expires_at=NULL
WHERE EXISTS(SELECT 1 FROM user_model_connection_versions v WHERE v.connection_id=config_id AND v.version=config_version AND v.sync_status='SYNCED');
UPDATE model_sync_operations SET status='PENDING',reason_code='',retry_at=clock_timestamp(),lease_token=lease_token+1,lease_expires_at=NULL
WHERE action='UPSERT' AND EXISTS(SELECT 1 FROM user_model_connection_versions v WHERE v.connection_id=model_sync_operations.connection_id AND v.version=connection_version AND v.sync_status='SYNCED');
UPDATE user_model_connection_versions SET sync_status='PENDING_SYNC' WHERE sync_status='SYNCED';
UPDATE model_gateway_checks SET status='UNKNOWN',reason_code='MODEL_GATEWAY_UNVERIFIED',expires_at=NULL,lease_token=lease_token+1,lease_expires_at=NULL,next_check_at=clock_timestamp();
