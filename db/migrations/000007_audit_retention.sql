-- Legal hold is independent of the purgeable audit rows. Service roles have
-- no privileges here; only the trusted maintenance account may change it.
CREATE TABLE audit_retention_holds (
    org_id uuid PRIMARY KEY REFERENCES organizations(id),
    active boolean NOT NULL,
    reason_code text NOT NULL CHECK (reason_code ~ '^[A-Z][A-Z0-9_]{0,63}$'),
    operator_ref text NOT NULL CHECK (operator_ref ~ '^[A-Za-z0-9_.:@/-]{1,128}$'),
    changed_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
REVOKE ALL ON audit_retention_holds FROM PUBLIC;
