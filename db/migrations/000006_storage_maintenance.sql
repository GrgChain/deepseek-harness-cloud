ALTER TABLE public.storage_assets DROP CONSTRAINT storage_assets_status_check;
ALTER TABLE public.storage_assets ADD CONSTRAINT storage_assets_status_check CHECK(status IN ('PREPARING','READY','RETAINED','RESTORING','RECLAIM_PENDING','DELETED'));
ALTER TABLE public.storage_assets ADD COLUMN maintenance_intent jsonb CHECK(maintenance_intent IS NULL OR (jsonb_typeof(maintenance_intent)='object' AND maintenance_intent->>'schema_version'='1'));
