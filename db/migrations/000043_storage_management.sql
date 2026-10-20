ALTER TABLE storage_assets
  ADD COLUMN usage_used_bytes bigint CHECK (usage_used_bytes >= 0),
  ADD COLUMN usage_available_bytes bigint CHECK (usage_available_bytes >= 0),
  ADD COLUMN usage_sampled_at timestamptz,
  ADD COLUMN usage_attempted_at timestamptz,
  ADD COLUMN usage_error boolean NOT NULL DEFAULT false,
  ADD CONSTRAINT storage_usage_observation CHECK (
    (usage_used_bytes IS NULL AND usage_available_bytes IS NULL AND usage_sampled_at IS NULL)
    OR (usage_used_bytes IS NOT NULL AND usage_available_bytes IS NOT NULL AND usage_sampled_at IS NOT NULL
        AND usage_used_bytes + usage_available_bytes <= capacity_bytes));

CREATE INDEX storage_assets_owner_list ON storage_assets(org_id,owner_id,id);
CREATE TABLE storage_backups (
  id uuid PRIMARY KEY,
  org_id uuid NOT NULL,
  owner_id uuid NOT NULL,
  instance_id uuid NOT NULL,
  asset_id uuid NOT NULL,
  capacity_bytes bigint NOT NULL CHECK(capacity_bytes>0),
  template_version uuid NOT NULL,
  image_digest text NOT NULL,
  status text NOT NULL CHECK(status IN ('RUNNING','SUCCEEDED','FAILED','UNCERTAIN','PRUNE_PENDING','PRUNED')),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  completed_at timestamptz,
  verified_at timestamptz,
  expires_at timestamptz,
  backup_sha256 text CHECK(backup_sha256 ~ '^[a-f0-9]{64}$'),
  set_id uuid,
  archive_path text NOT NULL,
  FOREIGN KEY(org_id,owner_id,instance_id,asset_id) REFERENCES storage_assets(org_id,owner_id,instance_id,id),
  CHECK(status<>'SUCCEEDED' OR (completed_at IS NOT NULL AND verified_at IS NOT NULL AND backup_sha256 IS NOT NULL))
);
CREATE INDEX storage_backups_asset_history ON storage_backups(org_id,owner_id,asset_id,created_at DESC,id DESC);
CREATE UNIQUE INDEX storage_backups_active ON storage_backups(asset_id) WHERE status IN ('RUNNING','UNCERTAIN');
