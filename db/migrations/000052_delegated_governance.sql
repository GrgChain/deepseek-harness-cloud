-- Complete the explicitly assignable governance permissions introduced in migration 35.
-- Replace only authorization decisions; retain serialized membership locks,
-- version/idempotency checks, audit, protected built-ins, self/last-admin guards,
-- session revocation and password-reset leases. Existing function ACLs are retained.

CREATE OR REPLACE FUNCTION public.hc_iam_profile_change(
    requested_org uuid,actor uuid,actor_session uuid,actor_generation bigint,
    requested_action text,target uuid,expected_version bigint,payload jsonb,
    request_key bytea,request_hash bytea,request_trace text)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE
    actor_role text; old_role public.roles%ROWTYPE; old_user public.users%ROWTYPE;
    selected_role public.roles%ROWTYPE; previous_code text;
    selected_permissions text[]; affected uuid[]; receipt public.iam_requests%ROWTYPE;
    changes jsonb:='{}'; next_status text;
BEGIN
    -- One governance writer per organization. All membership locks precede the
    -- actor's session and role rows, so two administrators cannot lock each other
    -- in opposite orders. Ordinary commands lock only their own membership.
    PERFORM pg_advisory_xact_lock(hashtextextended('hc-iam:'||requested_org::text,0));
    PERFORM m.user_id FROM public.memberships m WHERE m.org_id=requested_org ORDER BY m.user_id FOR UPDATE;
    actor_role:=public.hc_access_lock_principal(requested_org,actor,actor_session,actor_generation);
    IF actor_role IS NULL THEN RAISE EXCEPTION 'IAM_UNAUTHENTICATED'; END IF;
    -- Management privileges are explicitly delegable; role names are not grants.
    IF requested_action IN ('ROLE_CREATE','ROLE_UPDATE','ROLE_DELETE') THEN
        IF NOT public.hc_has_permission(requested_org,actor,'roles:manage') THEN RAISE EXCEPTION 'IAM_FORBIDDEN'; END IF;
    ELSIF requested_action IN ('USER_CREATE','USER_UPDATE','USER_ROLE','USER_DISABLE','USER_ENABLE','USER_ARCHIVE','USER_IDENTITY_CONFIRM','USER_IDENTITY_FAILURE') THEN
        IF NOT public.hc_has_permission(requested_org,actor,'users:manage') THEN RAISE EXCEPTION 'IAM_FORBIDDEN'; END IF;
    ELSE RAISE EXCEPTION 'IAM_INVALID';
    END IF;
    IF requested_action IN ('USER_CREATE','ROLE_CREATE') THEN
        IF octet_length(request_key)<>32 OR octet_length(request_hash)<>32 OR request_key IS NULL OR request_hash IS NULL THEN
            RAISE EXCEPTION 'IAM_INVALID';
        END IF;
        SELECT * INTO receipt FROM public.iam_requests
          WHERE org_id=requested_org AND actor_id=actor AND action=requested_action AND key_sha256=request_key;
        IF FOUND THEN
            IF receipt.request_sha256<>request_hash THEN RAISE EXCEPTION 'IAM_IDEMPOTENCY_CONFLICT'; END IF;
            RETURN receipt.target_id;
        END IF;
    END IF;
    IF requested_action IN ('ROLE_CREATE','ROLE_UPDATE','ROLE_DELETE') THEN
        IF requested_action<>'ROLE_CREATE' THEN
            SELECT * INTO old_role FROM public.roles WHERE id=target AND org_id=requested_org FOR UPDATE;
            IF NOT FOUND THEN RAISE EXCEPTION 'IAM_NOT_FOUND'; END IF;
            IF old_role.builtin THEN RAISE EXCEPTION 'IAM_BUILTIN_ROLE'; END IF;
            IF old_role.version<>expected_version THEN RAISE EXCEPTION 'IAM_VERSION_CONFLICT'; END IF;
        ELSIF expected_version<>0 THEN RAISE EXCEPTION 'IAM_INVALID';
        END IF;
        IF requested_action='ROLE_DELETE' THEN
            IF EXISTS(SELECT 1 FROM public.memberships WHERE org_id=requested_org AND role=old_role.code) THEN
                RAISE EXCEPTION 'IAM_ROLE_IN_USE';
            END IF;
            DELETE FROM public.role_permissions WHERE role_id=target;
            DELETE FROM public.roles WHERE id=target;
            changes:=jsonb_build_object('role_code',old_role.code);
        ELSE
            IF jsonb_typeof(payload->'permissions')<>'array' OR payload->>'code' !~ '^[a-z][a-z0-9_]{2,31}$'
              OR payload->>'code' IN ('admin','member') THEN RAISE EXCEPTION 'IAM_INVALID'; END IF;
            SELECT COALESCE(array_agg(value ORDER BY value),'{}'::text[]) INTO selected_permissions
              FROM jsonb_array_elements_text(payload->'permissions');
            IF cardinality(selected_permissions)<>(SELECT count(DISTINCT x) FROM unnest(selected_permissions) x)
              OR EXISTS(SELECT 1 FROM unnest(selected_permissions) x LEFT JOIN public.permissions p ON p.id=x WHERE p.id IS NULL OR NOT p.assignable)
              OR EXISTS(SELECT 1 FROM public.permissions p WHERE p.id=ANY(selected_permissions) AND NOT p.dependencies<@selected_permissions)
              THEN RAISE EXCEPTION 'IAM_INVALID_PERMISSIONS'; END IF;
            IF requested_action='ROLE_CREATE' THEN
                INSERT INTO public.roles(id,org_id,code,name,description)
                  VALUES(target,requested_org,payload->>'code',payload->>'name',COALESCE(payload->>'description',''));
            ELSE
                IF old_role.code<>payload->>'code' THEN RAISE EXCEPTION 'IAM_IMMUTABLE_ROLE_CODE'; END IF;
                SELECT COALESCE(array_agg(user_id ORDER BY user_id),'{}'::uuid[]) INTO affected
                  FROM public.memberships WHERE org_id=requested_org AND role=old_role.code;
                changes:=jsonb_build_object('before_permissions',
                  (SELECT COALESCE(jsonb_agg(permission_id ORDER BY permission_id),'[]') FROM public.role_permissions WHERE role_id=target));
                UPDATE public.roles SET name=payload->>'name',description=COALESCE(payload->>'description',''),
                  version=version+1,updated_at=clock_timestamp() WHERE id=target;
                DELETE FROM public.role_permissions WHERE role_id=target;
                PERFORM public.hc_iam_revoke(requested_org,affected);
            END IF;
            INSERT INTO public.role_permissions SELECT target,unnest(selected_permissions);
            changes:=changes||jsonb_build_object('role_code',payload->>'code','permissions',selected_permissions);
        END IF;
    ELSIF requested_action='USER_CREATE' THEN
        IF expected_version<>0 OR payload->>'status' NOT IN ('ACTIVE','PENDING_IDENTITY')
          OR payload->>'identity_source' NOT IN ('oidc','local') THEN RAISE EXCEPTION 'IAM_INVALID'; END IF;
        SELECT * INTO selected_role FROM public.roles WHERE id=(payload->>'role_id')::uuid AND org_id=requested_org;
        IF NOT FOUND THEN RAISE EXCEPTION 'IAM_INVALID_ROLE'; END IF;
        INSERT INTO public.users(id,identity_issuer,identity_subject,workspace_owner_id,status,display_name,email,identity_source)
          VALUES(target,payload->>'issuer',payload->>'subject',(payload->>'workspace_owner_id')::uuid,
            payload->>'status',payload->>'display_name',payload->>'email',payload->>'identity_source');
        INSERT INTO public.memberships(org_id,user_id,role,status)
          VALUES(requested_org,target,selected_role.code,CASE WHEN payload->>'status'='ACTIVE' THEN 'ACTIVE' ELSE 'DISABLED' END);
        IF payload->>'identity_source'='local' THEN
            INSERT INTO public.identity_provisioning_operations(id,org_id,user_id,provider_account_id,login_email)
              VALUES((payload->>'operation_id')::uuid,requested_org,target,payload->>'provider_account_id',payload->>'email');
        END IF;
        changes:=jsonb_build_object('role_code',selected_role.code,'status',payload->>'status','identity_source',payload->>'identity_source');
    ELSIF requested_action IN ('USER_UPDATE','USER_ROLE','USER_DISABLE','USER_ENABLE','USER_ARCHIVE','USER_IDENTITY_CONFIRM','USER_IDENTITY_FAILURE','USER_PASSWORD_RESET') THEN
        SELECT u.* INTO old_user FROM public.users u JOIN public.memberships m ON m.user_id=u.id
          WHERE u.id=target AND m.org_id=requested_org FOR UPDATE OF u;
        IF NOT FOUND THEN RAISE EXCEPTION 'IAM_NOT_FOUND'; END IF;
        IF requested_action='USER_IDENTITY_CONFIRM' AND old_user.status='ACTIVE'
          AND EXISTS(SELECT 1 FROM public.identity_provisioning_operations WHERE user_id=target AND status='SUCCEEDED') THEN RETURN target; END IF;
        IF old_user.version<>expected_version THEN RAISE EXCEPTION 'IAM_VERSION_CONFLICT'; END IF;
        SELECT role INTO previous_code FROM public.memberships WHERE org_id=requested_org AND user_id=target;
        IF requested_action IN ('USER_ROLE','USER_DISABLE','USER_ARCHIVE') AND target=actor THEN RAISE EXCEPTION 'IAM_SELF_CHANGE'; END IF;
        IF requested_action='USER_UPDATE' THEN
            UPDATE public.users SET display_name=payload->>'display_name',email=payload->>'email',version=version+1,updated_at=clock_timestamp() WHERE id=target;
        ELSIF requested_action='USER_ROLE' THEN
            SELECT * INTO selected_role FROM public.roles WHERE id=(payload->>'role_id')::uuid AND org_id=requested_org;
            IF NOT FOUND THEN RAISE EXCEPTION 'IAM_INVALID_ROLE'; END IF;
            UPDATE public.memberships SET role=selected_role.code WHERE org_id=requested_org AND user_id=target;
            UPDATE public.users SET version=version+1,updated_at=clock_timestamp() WHERE id=target;
            PERFORM public.hc_iam_revoke(requested_org,ARRAY[target]);
            changes:=jsonb_build_object('before_role',previous_code,'role_code',selected_role.code);
        ELSIF requested_action IN ('USER_DISABLE','USER_ENABLE','USER_ARCHIVE') THEN
            next_status:=CASE requested_action WHEN 'USER_DISABLE' THEN 'DISABLED' WHEN 'USER_ENABLE' THEN 'ACTIVE' ELSE 'ARCHIVED' END;
            IF old_user.status NOT IN ('ACTIVE','DISABLED') THEN RAISE EXCEPTION 'IAM_STATE_CONFLICT'; END IF;
            IF requested_action='USER_ENABLE' AND old_user.status<>'DISABLED' THEN RAISE EXCEPTION 'IAM_STATE_CONFLICT'; END IF;
            IF requested_action='USER_ARCHIVE' AND (
              EXISTS(SELECT 1 FROM public.instances i WHERE i.org_id=requested_org AND i.owner_id=target AND i.deleted_at IS NULL)
              OR EXISTS(SELECT 1 FROM public.instances i JOIN public.operations op ON op.instance_id=i.id WHERE i.org_id=requested_org AND i.owner_id=target AND op.status IN ('ACCEPTED','RUNNING','UNCERTAIN'))
              OR EXISTS(SELECT 1 FROM public.storage_assets WHERE org_id=requested_org AND owner_id=target AND status<>'DELETED')
              OR EXISTS(SELECT 1 FROM public.recovery_quota_holds WHERE org_id=requested_org AND owner_id=target)
            ) THEN RAISE EXCEPTION 'IAM_USER_HAS_ASSETS'; END IF;
            UPDATE public.users SET status=next_status,version=version+1,updated_at=clock_timestamp() WHERE id=target;
            UPDATE public.memberships SET status=CASE WHEN next_status='ACTIVE' THEN 'ACTIVE' ELSE 'DISABLED' END WHERE org_id=requested_org AND user_id=target;
            PERFORM public.hc_iam_revoke(requested_org,ARRAY[target]);
            changes:=jsonb_build_object('before_status',old_user.status,'status',next_status);
        ELSIF requested_action IN ('USER_IDENTITY_CONFIRM','USER_IDENTITY_FAILURE') THEN
            IF old_user.status<>'PENDING_IDENTITY' THEN RAISE EXCEPTION 'IAM_STATE_CONFLICT'; END IF;
            IF requested_action='USER_IDENTITY_CONFIRM' THEN
                UPDATE public.users SET status='ACTIVE',version=version+1,updated_at=clock_timestamp() WHERE id=target;
                UPDATE public.memberships SET status='ACTIVE' WHERE org_id=requested_org AND user_id=target;
                UPDATE public.identity_provisioning_operations SET status='SUCCEEDED',reason_code='',updated_at=clock_timestamp() WHERE org_id=requested_org AND user_id=target;
            ELSE
                UPDATE public.identity_provisioning_operations SET reason_code='IDENTITY_UNAVAILABLE',updated_at=clock_timestamp() WHERE org_id=requested_org AND user_id=target;
            END IF;
        ELSE
            IF old_user.identity_source<>'local' OR old_user.status NOT IN ('ACTIVE','DISABLED') THEN RAISE EXCEPTION 'IAM_STATE_CONFLICT'; END IF;
            PERFORM public.hc_iam_revoke(requested_org,ARRAY[target]);
            UPDATE public.users SET version=version+1,updated_at=clock_timestamp() WHERE id=target;
        END IF;
        IF previous_code='ADMIN' AND old_user.status='ACTIVE' AND NOT EXISTS(
          SELECT 1 FROM public.memberships m JOIN public.users u ON u.id=m.user_id
          WHERE m.org_id=requested_org AND m.role='ADMIN' AND m.status='ACTIVE' AND u.status='ACTIVE'
        ) THEN RAISE EXCEPTION 'IAM_LAST_ADMIN'; END IF;
    ELSE RAISE EXCEPTION 'IAM_INVALID';
    END IF;
    IF requested_action IN ('USER_CREATE','ROLE_CREATE') THEN
        INSERT INTO public.iam_requests(org_id,actor_id,action,key_sha256,request_sha256,target_id)
          VALUES(requested_org,actor,requested_action,request_key,request_hash,target);
    END IF;
    INSERT INTO public.audit_events(id,org_id,actor_id,owner_id,resource_id,action,outcome,trace_id,details)
      VALUES(gen_random_uuid(),requested_org,actor,actor,target,requested_action,'SUCCEEDED',request_trace,changes||jsonb_build_object('schema_version',1));
    RETURN target;
END;
$$;

CREATE OR REPLACE FUNCTION public.hc_iam_change(
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
        IF NOT public.hc_has_permission(requested_org,actor,'users:manage') THEN RAISE EXCEPTION 'IAM_FORBIDDEN'; END IF;
        IF EXISTS(SELECT 1 FROM public.identity_password_resets WHERE org_id=requested_org AND user_id=target AND status='PENDING')
            THEN RAISE EXCEPTION 'IAM_STATE_CONFLICT'; END IF;
    END IF;
    RETURN public.hc_iam_profile_change(requested_org,actor,actor_session,actor_generation,
        requested_action,target,expected_version,payload,request_key,request_hash,request_trace);
END;
$$;
REVOKE ALL ON FUNCTION public.hc_iam_change(uuid,uuid,uuid,bigint,text,uuid,bigint,jsonb,bytea,bytea,text) FROM PUBLIC;

CREATE OR REPLACE FUNCTION public.hc_iam_password_reset(
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
        IF NOT public.hc_has_permission(requested_org,actor,'users:manage') THEN RAISE EXCEPTION 'IAM_FORBIDDEN'; END IF;
    ELSE
        -- Confirmation belongs to the committed lease. The request's session
        -- can have been revoked by its own reset; current users:manage permission
        -- and the fenced operation are still mandatory.
        IF NOT public.hc_has_permission(requested_org,actor,'users:manage') THEN RAISE EXCEPTION 'IAM_FORBIDDEN'; END IF;
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
