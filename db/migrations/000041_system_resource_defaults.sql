-- Apply larger limits to newly initialized organizations; preserve saved quotas.
ALTER TABLE quota_policies
    ALTER COLUMN max_instances SET DEFAULT 20,
    ALTER COLUMN cpu_millicores SET DEFAULT 40000,
    ALTER COLUMN memory_bytes SET DEFAULT 85899345920,
    ALTER COLUMN storage_bytes SET DEFAULT 214748364800;
