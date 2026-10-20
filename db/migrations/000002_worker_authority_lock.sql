-- Worker may lock authorization and the model head in the established order,
-- but must not gain UPDATE privileges on identity or configuration ownership.
CREATE OR REPLACE FUNCTION public.hc_worker_lock_authority(requested_org uuid, requested_owner uuid)
RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE authorized boolean;
BEGIN
    SELECT m.status='ACTIVE' AND u.status='ACTIVE' AND o.status='ACTIVE'
    INTO authorized
    FROM public.memberships m
    JOIN public.users u ON u.id=m.user_id
    JOIN public.organizations o ON o.id=m.org_id
    WHERE m.org_id=requested_org AND m.user_id=requested_owner
    FOR UPDATE OF m;
    IF NOT FOUND THEN RAISE EXCEPTION 'worker owner not registered'; END IF;
    PERFORM id FROM public.model_configuration_heads WHERE org_id=requested_org FOR UPDATE;
    IF NOT FOUND THEN RAISE EXCEPTION 'worker configuration head not registered'; END IF;
    RETURN authorized;
END;
$$;
REVOKE ALL ON FUNCTION public.hc_worker_lock_authority(uuid,uuid) FROM PUBLIC;
