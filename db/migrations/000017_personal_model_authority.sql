CREATE FUNCTION public.hc_personal_model_authority(requested_org uuid, requested_owner uuid)
RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE active boolean;
BEGIN
 SELECT o.status='ACTIVE' AND u.status='ACTIVE' AND m.status='ACTIVE'
 INTO active FROM public.memberships m JOIN public.users u ON u.id=m.user_id JOIN public.organizations o ON o.id=m.org_id
 WHERE m.org_id=requested_org AND m.user_id=requested_owner FOR UPDATE OF m;
 IF NOT FOUND THEN RETURN false; END IF;
 PERFORM id FROM public.model_configuration_heads WHERE org_id=requested_org FOR UPDATE;
 RETURN active;
END;
$$;
REVOKE ALL ON FUNCTION public.hc_personal_model_authority(uuid,uuid) FROM PUBLIC;
