-- Owner-confirmed permanent release is distinct from automatic retention cleanup.
INSERT INTO permissions(id,assignable,dependencies) VALUES('instances:release-storage',true,'{instances:read}');
INSERT INTO role_permissions(role_id,permission_id) SELECT id,'instances:release-storage' FROM roles WHERE builtin;
CREATE TABLE storage_release_requests (
 id uuid PRIMARY KEY,
 org_id uuid NOT NULL,
 owner_id uuid NOT NULL,
 instance_id uuid NOT NULL,
 asset_id uuid NOT NULL UNIQUE,
 confirmed_name text NOT NULL,
 status text NOT NULL CHECK(status IN ('ACCEPTED','RUNNING','UNCERTAIN','SUCCEEDED')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 retry_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 completed_at timestamptz,
 FOREIGN KEY(org_id,owner_id,instance_id,asset_id) REFERENCES storage_assets(org_id,owner_id,instance_id,id),
 CHECK((status='SUCCEEDED')=(completed_at IS NOT NULL))
);
CREATE INDEX storage_release_pending ON storage_release_requests(retry_at,id) WHERE status<>'SUCCEEDED';
