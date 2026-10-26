-- Context content remains in independently deployed memory / knowledge services.
-- Connections and grants are platform metadata, not vector or graph storage.
INSERT INTO permissions(id,assignable,dependencies) VALUES
 ('memory:read',true,'{}'),('memory:manage',true,'{memory:read}'),
 ('knowledge:read',true,'{}'),('knowledge:manage',true,'{knowledge:read}');

-- Deliberately do not grant new capabilities to existing roles. Administrators
-- explicitly assign them through the existing, audited role workflow.

CREATE TABLE context_connections (
 id uuid PRIMARY KEY,
 org_id uuid NOT NULL REFERENCES organizations(id),
 owner_id uuid NOT NULL REFERENCES users(id),
 kind text NOT NULL CHECK(kind IN ('memory','knowledge')),
 backend text NOT NULL CHECK(backend IN ('openviking','mem0','graphiti','milvus','weaviate','qdrant')),
 endpoint_id text NOT NULL CHECK(length(endpoint_id) BETWEEN 1 AND 64),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 100),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 enabled boolean NOT NULL DEFAULT true,
 status text NOT NULL DEFAULT 'UNVERIFIED' CHECK(status IN ('UNVERIFIED','READY','FAILED')),
 capabilities jsonb NOT NULL DEFAULT '{}'::jsonb CHECK(jsonb_typeof(capabilities)='object'),
 key_id text NOT NULL, nonce bytea NOT NULL CHECK(octet_length(nonce)=12), ciphertext bytea NOT NULL,
 verified_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(org_id,owner_id,id),
 UNIQUE(org_id,owner_id,kind,name),
 CHECK((kind='memory' AND backend IN ('openviking','mem0','graphiti')) OR (kind='knowledge' AND backend IN ('milvus','weaviate','qdrant')))
);
CREATE INDEX context_connections_owner ON context_connections(org_id,owner_id,kind,updated_at DESC,id);

CREATE TABLE context_bindings (
 org_id uuid NOT NULL,
 owner_id uuid NOT NULL,
 instance_id uuid NOT NULL,
 connection_id uuid NOT NULL,
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 enabled boolean NOT NULL DEFAULT true,
 scopes jsonb NOT NULL DEFAULT '[]'::jsonb CHECK(jsonb_typeof(scopes)='array'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(instance_id,connection_id),
 FOREIGN KEY(org_id,owner_id,connection_id) REFERENCES context_connections(org_id,owner_id,id) ON DELETE CASCADE,
 FOREIGN KEY(org_id,owner_id,instance_id) REFERENCES instances(org_id,owner_id,id)
);

-- A durable operation intent precedes remote writes. UNKNOWN is reconciled with
-- the original key; callers must never infer rollback from a network timeout.
CREATE TABLE context_operations (
 id uuid PRIMARY KEY,
 org_id uuid NOT NULL,
 owner_id uuid NOT NULL,
 connection_id uuid NOT NULL,
 connection_version bigint NOT NULL CHECK(connection_version>0),
 action text NOT NULL CHECK(length(action) BETWEEN 1 AND 64),
 idempotency_sha256 bytea NOT NULL CHECK(octet_length(idempotency_sha256)=32),
 request_sha256 bytea NOT NULL CHECK(octet_length(request_sha256)=32),
 status text NOT NULL CHECK(status IN ('PENDING','SUCCEEDED','FAILED','UNKNOWN')),
 key_id text NOT NULL, nonce bytea NOT NULL CHECK(octet_length(nonce)=12), ciphertext bytea NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(connection_id,idempotency_sha256),
 FOREIGN KEY(org_id,owner_id,connection_id) REFERENCES context_connections(org_id,owner_id,id)
);

CREATE TABLE context_previews (
 id uuid PRIMARY KEY,
 org_id uuid NOT NULL,
 owner_id uuid NOT NULL,
 connection_id uuid NOT NULL,
 connection_version bigint NOT NULL CHECK(connection_version>0),
 purpose text NOT NULL CHECK(purpose IN ('memory-write','memory-import','memory-migration','document-import')),
 key_id text NOT NULL, nonce bytea NOT NULL CHECK(octet_length(nonce)=12), ciphertext bytea NOT NULL,
 expires_at timestamptz NOT NULL DEFAULT clock_timestamp()+interval '5 minutes',
 consumed_at timestamptz,
 FOREIGN KEY(org_id,owner_id,connection_id) REFERENCES context_connections(org_id,owner_id,id) ON DELETE CASCADE
);
CREATE INDEX context_preview_expiry ON context_previews(expires_at);
