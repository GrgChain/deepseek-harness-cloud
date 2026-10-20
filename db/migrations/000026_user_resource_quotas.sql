-- Limits are usage caps, not reserved host capacity. Missing rows inherit the
-- organization policy. All quota mutations serialize on quota_policies.
CREATE TABLE user_quota_policies (
    org_id uuid NOT NULL REFERENCES quota_policies(org_id),
    owner_id uuid NOT NULL,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    max_instances bigint NOT NULL CHECK (max_instances >= 0),
    max_running bigint NOT NULL CHECK (max_running >= 0),
    cpu_millicores bigint NOT NULL CHECK (cpu_millicores >= 0),
    memory_bytes bigint NOT NULL CHECK (memory_bytes >= 0),
    storage_bytes bigint NOT NULL CHECK (storage_bytes >= 0),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, owner_id),
    FOREIGN KEY (org_id, owner_id) REFERENCES memberships(org_id, user_id)
);
