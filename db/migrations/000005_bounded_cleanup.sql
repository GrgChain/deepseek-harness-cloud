-- Trusted bounded deletion only; runtime roles receive EXECUTE, never DELETE.
CREATE OR REPLACE FUNCTION public.hc_cleanup_expired(batch_size integer) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE remaining integer := batch_size; n integer; result jsonb := '{}'::jsonb;
BEGIN
 IF batch_size IS NULL OR batch_size<1 OR batch_size>500 THEN RAISE EXCEPTION 'invalid cleanup bound'; END IF;
 IF NOT pg_try_advisory_xact_lock(731804525) THEN RETURN result; END IF;
 WITH doomed AS (SELECT id FROM public.access_tickets WHERE expires_at<=clock_timestamp() OR consumed_at IS NOT NULL ORDER BY expires_at,id LIMIT remaining FOR UPDATE SKIP LOCKED)
 DELETE FROM public.access_tickets WHERE id IN (SELECT id FROM doomed);
 GET DIAGNOSTICS n=ROW_COUNT; remaining:=remaining-n; result:=result||jsonb_build_object('Tickets',n);
 WITH doomed AS (SELECT c.id FROM public.access_challenges c WHERE (c.expires_at<=clock_timestamp() OR c.consumed_at IS NOT NULL) AND NOT EXISTS(SELECT 1 FROM public.access_tickets t WHERE t.challenge_id=c.id) ORDER BY c.expires_at,c.id LIMIT remaining FOR UPDATE OF c SKIP LOCKED)
 DELETE FROM public.access_challenges WHERE id IN (SELECT id FROM doomed);
 GET DIAGNOSTICS n=ROW_COUNT; remaining:=remaining-n; result:=result||jsonb_build_object('Challenges',n);
 WITH doomed AS (SELECT id FROM public.access_sessions WHERE expires_at<=clock_timestamp() OR revoked_at IS NOT NULL ORDER BY expires_at,id LIMIT remaining FOR UPDATE SKIP LOCKED)
 DELETE FROM public.access_sessions WHERE id IN (SELECT id FROM doomed);
 GET DIAGNOSTICS n=ROW_COUNT; remaining:=remaining-n; result:=result||jsonb_build_object('Sessions',n);
 WITH doomed AS (SELECT instance_id FROM public.native_sessions WHERE expires_at<=clock_timestamp() ORDER BY expires_at,instance_id LIMIT remaining FOR UPDATE SKIP LOCKED)
 DELETE FROM public.native_sessions WHERE instance_id IN (SELECT instance_id FROM doomed);
 GET DIAGNOSTICS n=ROW_COUNT; remaining:=remaining-n; result:=result||jsonb_build_object('Native',n);
 WITH doomed AS (SELECT p.id FROM public.portal_sessions p WHERE (p.expires_at<=clock_timestamp() OR p.revoked_at IS NOT NULL OR p.last_seen_at<=clock_timestamp()-interval '30 minutes') AND NOT EXISTS(SELECT 1 FROM public.access_tickets t WHERE t.portal_session_id=p.id) AND NOT EXISTS(SELECT 1 FROM public.access_sessions s WHERE s.portal_session_id=p.id) ORDER BY p.expires_at,p.id LIMIT remaining FOR UPDATE OF p SKIP LOCKED)
 DELETE FROM public.portal_sessions WHERE id IN (SELECT id FROM doomed);
 GET DIAGNOSTICS n=ROW_COUNT; remaining:=remaining-n; result:=result||jsonb_build_object('Portal',n);
 WITH doomed AS (SELECT id FROM public.oidc_transactions WHERE expires_at<=clock_timestamp() OR consumed_at IS NOT NULL ORDER BY expires_at,id LIMIT remaining FOR UPDATE SKIP LOCKED)
 DELETE FROM public.oidc_transactions WHERE id IN (SELECT id FROM doomed);
 GET DIAGNOSTICS n=ROW_COUNT; remaining:=remaining-n; result:=result||jsonb_build_object('OIDC',n);
 WITH doomed AS (SELECT k.org_id,k.actor_id,k.action,k.target_id,k.key FROM public.idempotency_records k JOIN public.operations o ON o.id=k.operation_id WHERE k.expires_at<=clock_timestamp() AND o.status IN ('SUCCEEDED','FAILED') AND o.updated_at<=clock_timestamp()-interval '7 days' ORDER BY k.expires_at,k.org_id,k.actor_id,k.action,k.target_id,k.key LIMIT remaining FOR UPDATE OF k SKIP LOCKED)
 DELETE FROM public.idempotency_records WHERE (org_id,actor_id,action,target_id,key) IN (SELECT org_id,actor_id,action,target_id,key FROM doomed);
 GET DIAGNOSTICS n=ROW_COUNT; result:=result||jsonb_build_object('Idempotency',n);
 RETURN result;
END $$;
REVOKE ALL ON FUNCTION public.hc_cleanup_expired(integer) FROM PUBLIC;
