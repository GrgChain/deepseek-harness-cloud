-- Keep a stable create receipt without persisting the submitted provider key.
ALTER TABLE user_model_connections ADD COLUMN create_key_sha256 bytea NOT NULL CHECK (octet_length(create_key_sha256)=32);
ALTER TABLE user_model_connections ADD COLUMN create_request_sha256 bytea NOT NULL CHECK (octet_length(create_request_sha256)=32);
ALTER TABLE user_model_connections ADD CONSTRAINT user_model_create_key UNIQUE(org_id,owner_id,create_key_sha256);
