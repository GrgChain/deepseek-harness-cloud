ALTER TABLE mcp_connectors DROP CONSTRAINT mcp_connectors_auth_type_check;
ALTER TABLE mcp_connectors ADD CONSTRAINT mcp_connectors_auth_type_check CHECK(auth_type IN ('none','bearer','oauth2'));
CREATE TABLE mcp_oauth_transactions (
 id uuid PRIMARY KEY, org_id uuid NOT NULL, owner_id uuid NOT NULL, connector_id uuid NOT NULL,
 session_id uuid NOT NULL REFERENCES portal_sessions(id), connector_version bigint NOT NULL,
 state_sha256 text NOT NULL UNIQUE, expires_at timestamptz NOT NULL,
 key_id text NOT NULL, nonce bytea NOT NULL CHECK(octet_length(nonce)=12), ciphertext bytea NOT NULL,
 FOREIGN KEY(org_id,owner_id,connector_id) REFERENCES mcp_connectors(org_id,owner_id,id)
);
CREATE INDEX mcp_oauth_expiry ON mcp_oauth_transactions(expires_at);
