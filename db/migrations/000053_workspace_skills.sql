-- Immutable, encrypted copies of the skills selected when a workspace is created.
CREATE TABLE workspace_skill_snapshots (
 instance_id uuid PRIMARY KEY REFERENCES instances(id) ON DELETE CASCADE,
 org_id uuid NOT NULL REFERENCES organizations(id),
 owner_id uuid NOT NULL REFERENCES users(id),
 key_id text NOT NULL,
 nonce bytea NOT NULL CHECK (octet_length(nonce)=12),
 ciphertext bytea NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
REVOKE ALL ON workspace_skill_snapshots FROM PUBLIC;
