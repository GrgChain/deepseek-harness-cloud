ALTER TABLE user_model_defaults ALTER COLUMN connection_id DROP NOT NULL;
ALTER TABLE user_model_defaults ALTER COLUMN connection_version DROP NOT NULL;
ALTER TABLE user_model_defaults ADD CONSTRAINT personal_default_binding_pair CHECK ((connection_id IS NULL)=(connection_version IS NULL));
