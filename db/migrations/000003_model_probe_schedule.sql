ALTER TABLE model_validations ADD COLUMN attempted_at timestamptz;
UPDATE model_validations SET attempted_at=COALESCE(lease_expires_at-interval '45 seconds',checked_at);

-- Lock the configuration head without permitting the Worker to rebind it.
CREATE OR REPLACE FUNCTION public.hc_worker_lock_model(requested_org uuid)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog, public AS $$
DECLARE model_id uuid;
BEGIN
    SELECT id INTO model_id FROM public.model_configuration_heads WHERE org_id=requested_org FOR UPDATE;
    RETURN model_id;
END;
$$;
REVOKE ALL ON FUNCTION public.hc_worker_lock_model(uuid) FROM PUBLIC;
