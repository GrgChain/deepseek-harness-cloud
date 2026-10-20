-- Real quota commitments for verified resources omitted by an older restore.
-- They do not fabricate instances/operations or authorize external resources.
CREATE TABLE recovery_quota_holds (
    org_id uuid NOT NULL,
    owner_id uuid NOT NULL,
    instance_id uuid NOT NULL,
    resource_type text NOT NULL CHECK (resource_type IN ('INSTANCE','RUNNING','CPU','MEMORY','STORAGE','CONCURRENT')),
    amount bigint NOT NULL CHECK (amount > 0 AND amount <= 1125899906842624),
    inventory_run_id uuid NOT NULL,
    inventory_sha256 text NOT NULL CHECK (inventory_sha256 ~ '^[a-f0-9]{64}$'),
    reason_code text NOT NULL CHECK (reason_code ~ '^[A-Z][A-Z0-9_]{0,63}$'),
    operator_ref text NOT NULL CHECK (operator_ref ~ '^[A-Za-z0-9_.:@/-]{1,128}$'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (org_id,instance_id,resource_type),
    FOREIGN KEY (org_id,owner_id) REFERENCES memberships(org_id,user_id)
);
REVOKE ALL ON recovery_quota_holds FROM PUBLIC;

-- One read model used by both preflight and locked command acceptance. Normal
-- services can read totals but cannot change an orphan hold or release credit.
CREATE VIEW quota_commitments AS
SELECT org_id,owner_id,instance_id,resource_type,amount FROM quota_reservations WHERE status IN ('HELD','USED')
UNION ALL
SELECT org_id,owner_id,instance_id,resource_type,amount FROM recovery_quota_holds;
REVOKE ALL ON quota_commitments FROM PUBLIC;
