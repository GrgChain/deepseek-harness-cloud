CREATE FUNCTION public.hc_identity_lock_provisioning(requested_org uuid,target_user uuid)
RETURNS TABLE(provider_account_id text,login_email text)
LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
    SELECT op.provider_account_id,op.login_email
      FROM public.identity_provisioning_operations op JOIN public.users u ON u.id=op.user_id
     WHERE op.org_id=requested_org AND op.user_id=target_user AND op.status='PENDING'
       AND u.identity_source='local' AND u.status='PENDING_IDENTITY'
     FOR UPDATE OF op
$$;
REVOKE ALL ON FUNCTION public.hc_identity_lock_provisioning(uuid,uuid) FROM PUBLIC;
DO $$ BEGIN
    IF to_regrole('hc_api') IS NOT NULL THEN
        GRANT EXECUTE ON FUNCTION public.hc_identity_lock_provisioning(uuid,uuid) TO hc_api;
    END IF;
END $$;
