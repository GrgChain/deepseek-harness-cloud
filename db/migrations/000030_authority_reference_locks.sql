-- Preserve authority serialization without blocking foreign-key reference locks.
-- Otherwise quota audit insertion can deadlock against a member creating an instance.
CREATE OR REPLACE FUNCTION public.hc_access_lock_principal(requested_org uuid, requested_user uuid, requested_session uuid, requested_generation bigint)
RETURNS text LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog, public AS $$
DECLARE current_role text;
BEGIN
    PERFORM user_id FROM public.memberships WHERE org_id=requested_org AND user_id=requested_user FOR NO KEY UPDATE;
    PERFORM id FROM public.portal_sessions WHERE id=requested_session AND org_id=requested_org AND user_id=requested_user FOR NO KEY UPDATE;
    SELECT m.role INTO current_role FROM public.memberships m
    JOIN public.users u ON u.id=m.user_id JOIN public.organizations o ON o.id=m.org_id
    JOIN public.portal_sessions ps ON ps.org_id=m.org_id AND ps.user_id=m.user_id
    WHERE m.org_id=requested_org AND m.user_id=requested_user AND ps.id=requested_session
    AND m.status='ACTIVE' AND u.status='ACTIVE' AND o.status='ACTIVE'
    AND m.auth_generation=requested_generation AND ps.auth_generation=m.auth_generation
    AND ps.revoked_at IS NULL AND ps.expires_at>clock_timestamp() AND ps.last_seen_at>clock_timestamp()-interval '30 minutes';
    RETURN current_role;
END;
$$;
REVOKE ALL ON FUNCTION public.hc_access_lock_principal(uuid,uuid,uuid,bigint) FROM PUBLIC;

