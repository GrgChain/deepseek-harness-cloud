-- Rename platform contracts without changing IDs, FK targets or original data.
ALTER TABLE users RENAME COLUMN coder_user_id TO workspace_owner_id;
ALTER TABLE users RENAME CONSTRAINT users_coder_user_id_key TO users_workspace_owner_id_key;
ALTER TABLE instances RENAME COLUMN coder_workspace_name TO workspace_name;
ALTER TABLE instances RENAME COLUMN coder_workspace_id TO workspace_id;
ALTER TABLE instances RENAME CONSTRAINT instances_coder_workspace_name_key TO instances_workspace_name_key;
ALTER TABLE instances RENAME CONSTRAINT instances_coder_workspace_id_key TO instances_workspace_id_key;

DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM approved_templates WHERE runtime_manifest ? 'coder_version' AND runtime_manifest ? 'workspace_source_version') THEN
  RAISE EXCEPTION 'ambiguous workspace source version';
 END IF;
END $$;
UPDATE approved_templates
 SET runtime_manifest=(runtime_manifest-'coder_version') || jsonb_build_object('workspace_source_version',runtime_manifest->'coder_version')
 WHERE runtime_manifest ? 'coder_version';
UPDATE operations SET error_code='WORKSPACE_UNAVAILABLE' WHERE error_code='CODER_UPSTREAM_UNAVAILABLE';
UPDATE instance_health SET reason_code='WORKSPACE_UNAVAILABLE' WHERE reason_code='CODER_UPSTREAM_UNAVAILABLE';
