-- Public IP origins need distinct ports for workspace/Portal origin isolation.
-- Allocations survive process restarts and are released only after deletion.
CREATE TABLE ip_workspace_ports (
    instance_id uuid PRIMARY KEY REFERENCES instances(id),
    slot integer NOT NULL UNIQUE CHECK (slot >= 0 AND slot < 64512)
);
REVOKE ALL ON ip_workspace_ports FROM PUBLIC;
DO $$ BEGIN
    IF to_regrole('hc_api') IS NOT NULL THEN
        GRANT SELECT, INSERT, DELETE ON ip_workspace_ports TO hc_api;
    END IF;
    IF to_regrole('hc_gateway') IS NOT NULL THEN
        GRANT SELECT ON ip_workspace_ports TO hc_gateway;
    END IF;
END $$;
