CREATE TABLE storage_browse_requests (
 id uuid PRIMARY KEY,
 org_id uuid NOT NULL,
 owner_id uuid NOT NULL,
 instance_id uuid NOT NULL,
 asset_id uuid NOT NULL,
 action text NOT NULL CHECK(action IN ('LIST','PREVIEW','DOWNLOAD')),
 root text NOT NULL CHECK(root IN ('workspace','dsh')),
 path text NOT NULL CHECK(length(path)<=4096),
 after_name text NOT NULL DEFAULT '' CHECK(length(after_name)<=255),
 result_public_key bytea,
 status text NOT NULL CHECK(status IN ('QUEUED','RUNNING','SUCCEEDED','FAILED')),
 listing jsonb,
 content bytea,
 file_name text,
 error_code text,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 expires_at timestamptz NOT NULL,
 FOREIGN KEY(org_id,owner_id,instance_id,asset_id) REFERENCES storage_assets(org_id,owner_id,instance_id,id),
 CHECK(action='LIST' OR (path<>'' AND after_name='')),
 CHECK((action='LIST' AND result_public_key IS NULL) OR (action<>'LIST' AND result_public_key IS NOT NULL AND octet_length(result_public_key)=32)),
 CHECK(status<>'SUCCEEDED' OR (action='LIST' AND listing IS NOT NULL) OR (action<>'LIST' AND content IS NOT NULL AND file_name IS NOT NULL)),
 CHECK(status<>'FAILED' OR error_code IS NOT NULL)
);
CREATE INDEX storage_browse_queue ON storage_browse_requests(created_at,id) WHERE status='QUEUED';
CREATE INDEX storage_browse_expiry ON storage_browse_requests(expires_at);
CREATE UNIQUE INDEX storage_browse_one_active ON storage_browse_requests(asset_id) WHERE status IN ('QUEUED','RUNNING');
