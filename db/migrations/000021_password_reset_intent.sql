-- A reset's encrypted bcrypt hash is immutable until confirmation. Replaying
-- the same hash makes uncertain provider writes recoverable without a password.
CREATE TABLE identity_password_resets (
    id uuid PRIMARY KEY,
    org_id uuid NOT NULL,
    user_id uuid NOT NULL,
    actor_id uuid NOT NULL REFERENCES users(id),
    status text NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','SUCCEEDED')),
    reason_code text NOT NULL DEFAULT '',
    key_id text,
    nonce bytea,
    ciphertext bytea,
    lease_token bigint NOT NULL DEFAULT 1 CHECK (lease_token>0),
    lease_expires_at timestamptz,
    attempts integer NOT NULL DEFAULT 1 CHECK (attempts>0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY(org_id,user_id) REFERENCES memberships(org_id,user_id),
    CHECK ((status='PENDING' AND key_id IS NOT NULL AND nonce IS NOT NULL AND ciphertext IS NOT NULL AND octet_length(nonce)=12 AND octet_length(ciphertext)=76)
        OR (status='SUCCEEDED' AND key_id IS NULL AND nonce IS NULL AND ciphertext IS NULL AND lease_expires_at IS NULL))
);
CREATE UNIQUE INDEX identity_password_reset_pending ON identity_password_resets(org_id,user_id) WHERE status='PENDING';
CREATE INDEX identity_password_reset_latest ON identity_password_resets(org_id,user_id,created_at DESC,id DESC);

-- Keep ordinary governance under its existing authorization/locking boundary.
-- The old one-step reset may not be called by a runtime database identity.
ALTER FUNCTION public.hc_iam_change(uuid,uuid,uuid,bigint,text,uuid,bigint,jsonb,bytea,bytea,text) RENAME TO hc_iam_profile_change;
DO $$ BEGIN
    IF to_regrole('hc_api') IS NOT NULL THEN
        REVOKE ALL ON FUNCTION public.hc_iam_profile_change(uuid,uuid,uuid,bigint,text,uuid,bigint,jsonb,bytea,bytea,text) FROM hc_api;
    END IF;
END $$;
CREATE FUNCTION public.hc_iam_change(
    requested_org uuid,actor uuid,actor_session uuid,actor_generation bigint,
    requested_action text,target uuid,expected_version bigint,payload jsonb,
    request_key bytea,request_hash bytea,request_trace text)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE actor_role text;
BEGIN
    IF requested_action='USER_PASSWORD_RESET' THEN RAISE EXCEPTION 'IAM_INVALID'; END IF;
    IF requested_action='USER_ARCHIVE' THEN
        PERFORM pg_advisory_xact_lock(hashtextextended('hc-iam:'||requested_org::text,0));
        PERFORM m.user_id FROM public.memberships m WHERE m.org_id=requested_org ORDER BY m.user_id FOR UPDATE;
        actor_role:=public.hc_access_lock_principal(requested_org,actor,actor_session,actor_generation);
        IF actor_role IS NULL THEN RAISE EXCEPTION 'IAM_UNAUTHENTICATED'; END IF;
        IF actor_role<>'ADMIN' THEN RAISE EXCEPTION 'IAM_FORBIDDEN'; END IF;
        IF EXISTS(SELECT 1 FROM public.identity_password_resets WHERE org_id=requested_org AND user_id=target AND status='PENDING')
            THEN RAISE EXCEPTION 'IAM_STATE_CONFLICT'; END IF;
    END IF;
    RETURN public.hc_iam_profile_change(requested_org,actor,actor_session,actor_generation,
        requested_action,target,expected_version,payload,request_key,request_hash,request_trace);
END;
$$;
REVOKE ALL ON FUNCTION public.hc_iam_change(uuid,uuid,uuid,bigint,text,uuid,bigint,jsonb,bytea,bytea,text) FROM PUBLIC;

CREATE FUNCTION public.hc_iam_password_reset(
    requested_org uuid,actor uuid,actor_session uuid,actor_generation bigint,
    requested_action text,target uuid,expected_version bigint,operation uuid,
    expected_token bigint,hash_key text,hash_nonce bytea,hash_cipher bytea,request_trace text)
RETURNS bigint LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE
    account public.users%ROWTYPE;
    pending public.identity_password_resets%ROWTYPE;
    actor_role text;
    event text;
    result bigint;
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended('hc-iam:'||requested_org::text,0));
    PERFORM m.user_id FROM public.memberships m WHERE m.org_id=requested_org ORDER BY m.user_id FOR UPDATE;
    IF requested_action IN ('BEGIN','RETRY') THEN
        actor_role:=public.hc_access_lock_principal(requested_org,actor,actor_session,actor_generation);
        IF actor_role IS NULL THEN RAISE EXCEPTION 'IAM_UNAUTHENTICATED'; END IF;
        IF actor_role<>'ADMIN' THEN RAISE EXCEPTION 'IAM_FORBIDDEN'; END IF;
    ELSE
        -- Confirmation belongs to the committed lease. The request's session
        -- can have been revoked by its own reset; current administrator status
        -- and the fenced operation are still mandatory.
        IF NOT EXISTS(SELECT 1 FROM public.memberships m JOIN public.users u ON u.id=m.user_id
            JOIN public.organizations o ON o.id=m.org_id WHERE m.org_id=requested_org AND m.user_id=actor
            AND m.role='ADMIN' AND m.status='ACTIVE' AND u.status='ACTIVE' AND o.status='ACTIVE')
            THEN RAISE EXCEPTION 'IAM_FORBIDDEN'; END IF;
    END IF;
    SELECT u.* INTO account FROM public.users u JOIN public.memberships m ON m.user_id=u.id
        WHERE m.org_id=requested_org AND u.id=target FOR UPDATE OF u;
    IF NOT FOUND THEN RAISE EXCEPTION 'IAM_NOT_FOUND'; END IF;
    IF account.identity_source<>'local' OR account.status NOT IN ('ACTIVE','DISABLED')
        OR NOT EXISTS(SELECT 1 FROM public.identity_provisioning_operations WHERE org_id=requested_org AND user_id=target AND status='SUCCEEDED')
        THEN RAISE EXCEPTION 'IAM_STATE_CONFLICT'; END IF;

    IF requested_action IN ('BEGIN','RETRY') AND account.version<>expected_version THEN RAISE EXCEPTION 'IAM_VERSION_CONFLICT'; END IF;
    IF requested_action='BEGIN' THEN
        IF EXISTS(SELECT 1 FROM public.identity_password_resets WHERE org_id=requested_org AND user_id=target AND status='PENDING')
            THEN RAISE EXCEPTION 'IAM_PASSWORD_RESET_PENDING'; END IF;
        IF operation IS NULL OR hash_key IS NULL OR hash_key !~ '^[A-Za-z0-9_-]{1,64}$'
            OR hash_nonce IS NULL OR octet_length(hash_nonce)<>12 OR hash_cipher IS NULL OR octet_length(hash_cipher)<>76
            THEN RAISE EXCEPTION 'IAM_INVALID'; END IF;
        INSERT INTO public.identity_password_resets(id,org_id,user_id,actor_id,key_id,nonce,ciphertext,lease_expires_at)
            VALUES(operation,requested_org,target,actor,hash_key,hash_nonce,hash_cipher,clock_timestamp()+interval '15 seconds');
        UPDATE public.users SET version=version+1,updated_at=clock_timestamp() WHERE id=target;
        PERFORM public.hc_iam_revoke(requested_org,ARRAY[target]);
        event:='USER_PASSWORD_RESET_REQUESTED'; result:=1;
    ELSE
        SELECT * INTO pending FROM public.identity_password_resets WHERE id=operation AND org_id=requested_org AND user_id=target FOR UPDATE;
        IF NOT FOUND OR pending.status<>'PENDING' THEN RAISE EXCEPTION 'IAM_STATE_CONFLICT'; END IF;
        IF requested_action='RETRY' THEN
            IF pending.lease_expires_at>clock_timestamp() THEN RAISE EXCEPTION 'IAM_STATE_CONFLICT'; END IF;
            UPDATE public.identity_password_resets SET actor_id=actor,lease_token=lease_token+1,
                lease_expires_at=clock_timestamp()+interval '15 seconds',attempts=LEAST(attempts+1,1000000),reason_code='',updated_at=clock_timestamp()
                WHERE id=operation RETURNING lease_token INTO result;
            event:='USER_PASSWORD_RESET_RETRIED';
        ELSE
            IF pending.actor_id<>actor OR pending.lease_token<>expected_token OR pending.lease_expires_at<=clock_timestamp()
                OR pending.lease_expires_at IS NULL THEN RAISE EXCEPTION 'IAM_STATE_CONFLICT'; END IF;
            IF requested_action='CONFIRM' THEN
                PERFORM public.hc_iam_revoke(requested_org,ARRAY[target]);
                UPDATE public.users SET version=version+1,updated_at=clock_timestamp() WHERE id=target;
                UPDATE public.identity_password_resets SET status='SUCCEEDED',reason_code='',key_id=NULL,nonce=NULL,ciphertext=NULL,
                    lease_expires_at=NULL,updated_at=clock_timestamp() WHERE id=operation;
                event:='USER_PASSWORD_RESET';
            ELSIF requested_action='UNCERTAIN' THEN
                UPDATE public.identity_password_resets SET reason_code='IDENTITY_UNAVAILABLE',updated_at=clock_timestamp() WHERE id=operation;
                event:='USER_PASSWORD_RESET_UNCERTAIN';
            ELSE RAISE EXCEPTION 'IAM_INVALID'; END IF;
            result:=pending.lease_token;
        END IF;
    END IF;
    INSERT INTO public.audit_events(id,org_id,actor_id,owner_id,resource_id,action,outcome,trace_id,details)
        VALUES(gen_random_uuid(),requested_org,actor,target,target,event,
            CASE WHEN requested_action='CONFIRM' THEN 'SUCCEEDED' ELSE 'ACCEPTED' END,request_trace,
            jsonb_build_object('schema_version',1,'operation_id',operation));
    RETURN result;
END;
$$;
REVOKE ALL ON FUNCTION public.hc_iam_password_reset(uuid,uuid,uuid,bigint,text,uuid,bigint,uuid,bigint,text,bytea,bytea,text) FROM PUBLIC;
