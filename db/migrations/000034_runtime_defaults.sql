-- Runtime defaults can be initialized before any model account exists.
ALTER TABLE organization_defaults ALTER COLUMN model_config_id DROP NOT NULL;
ALTER TABLE organization_defaults ALTER COLUMN model_config_version DROP NOT NULL;
ALTER TABLE organization_defaults ADD CONSTRAINT organization_defaults_model_pair
    CHECK ((model_config_id IS NULL) = (model_config_version IS NULL));
ALTER TABLE organization_defaults ADD COLUMN retention_days integer NOT NULL DEFAULT 7
    CHECK (retention_days BETWEEN 1 AND 365);

CREATE OR REPLACE FUNCTION public.hc_check_organization_model_owner() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,public AS $$
BEGIN
 IF NEW.model_config_id IS NULL AND NEW.model_config_version IS NULL THEN RETURN NEW; END IF;
 IF NOT EXISTS(SELECT 1 FROM public.model_connection_registry WHERE org_id=NEW.org_id AND id=NEW.model_config_id AND owner_id IS NULL) THEN
   RAISE EXCEPTION 'organization defaults require an organization model' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;
