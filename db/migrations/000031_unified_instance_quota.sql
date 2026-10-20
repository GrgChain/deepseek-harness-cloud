-- Instance count is the only configurable count limit. Running reservations
-- remain lifecycle evidence, bounded by the same organization instance limit.
-- Keep CPU/memory/storage limits and existing usage/reservations unchanged.
ALTER TABLE quota_policies DROP COLUMN max_running;
ALTER TABLE user_quota_policies DROP COLUMN max_running;

-- Invalidate editors opened before the quota contract changed.
UPDATE quota_policies SET version=version+1;
UPDATE user_quota_policies SET version=version+1,updated_at=clock_timestamp();
