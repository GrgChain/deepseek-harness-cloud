-- Persist the virtual credential before the first remote side effect so a
-- lost response never requires issuing a second unknown credential.
ALTER TABLE user_model_connection_versions ADD COLUMN runtime_credential_ref uuid;
ALTER TABLE user_model_connection_versions ADD CONSTRAINT personal_runtime_credential_fk
 FOREIGN KEY(org_id,runtime_credential_ref) REFERENCES secrets(org_id,id);
ALTER TABLE model_sync_operations ADD COLUMN retry_at timestamptz NOT NULL DEFAULT now();

CREATE FUNCTION public.hc_model_available(model_org uuid, model_owner uuid, config_id uuid, config_version bigint) RETURNS boolean
LANGUAGE sql STABLE SET search_path=pg_catalog,public AS $$
 SELECT EXISTS(SELECT 1 FROM public.model_connection_registry r WHERE r.org_id=model_org AND r.id=config_id
 AND (r.owner_id IS NULL OR (r.owner_id=model_owner AND EXISTS(
   SELECT 1 FROM public.user_model_connections h JOIN public.user_model_connection_versions v
   ON v.connection_id=h.id AND v.org_id=h.org_id AND v.owner_id=h.owner_id
   WHERE h.id=r.id AND h.enabled AND h.archived_at IS NULL AND v.version=config_version AND v.sync_status='SYNCED'
   AND v.model_configuration_id=config_id AND v.model_configuration_version=config_version))));
$$;
REVOKE ALL ON FUNCTION public.hc_model_available(uuid,uuid,uuid,bigint) FROM PUBLIC;

CREATE FUNCTION public.hc_check_instance_model_owner() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,public AS $$
BEGIN
 IF TG_OP='UPDATE' AND NEW.org_id=OLD.org_id AND NEW.owner_id=OLD.owner_id AND NEW.model_config_id=OLD.model_config_id AND NEW.model_config_version=OLD.model_config_version THEN RETURN NEW; END IF;
 IF NOT public.hc_model_available(NEW.org_id,NEW.owner_id,NEW.model_config_id,NEW.model_config_version) THEN
   RAISE EXCEPTION 'model connection is not available to instance owner' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER instances_model_owner BEFORE INSERT OR UPDATE OF model_config_id,model_config_version,owner_id,org_id ON instances
 FOR EACH ROW EXECUTE FUNCTION public.hc_check_instance_model_owner();

CREATE FUNCTION public.hc_check_organization_model_owner() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,public AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM public.model_connection_registry WHERE org_id=NEW.org_id AND id=NEW.model_config_id AND owner_id IS NULL) THEN
   RAISE EXCEPTION 'organization defaults require an organization model' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER defaults_model_owner BEFORE INSERT OR UPDATE OF model_config_id,org_id ON organization_defaults
 FOR EACH ROW EXECUTE FUNCTION public.hc_check_organization_model_owner();
