-- User and role governance. Runtime identities may invoke the audited command
-- function; they never receive unrestricted writes to users or role grants.
ALTER TABLE users DROP CONSTRAINT users_status_check;
ALTER TABLE users ADD CONSTRAINT users_status_check CHECK (status IN ('PENDING_IDENTITY','ACTIVE','DISABLED','ARCHIVED'));
ALTER TABLE users ADD COLUMN display_name text NOT NULL DEFAULT '' CHECK (length(display_name)<=100);
ALTER TABLE users ADD COLUMN email text NOT NULL DEFAULT '' CHECK (length(email)<=254);
ALTER TABLE users ADD COLUMN identity_source text NOT NULL DEFAULT 'oidc' CHECK (identity_source IN ('oidc','local'));
ALTER TABLE users ADD COLUMN version bigint NOT NULL DEFAULT 1 CHECK (version>0);
ALTER TABLE users ADD COLUMN created_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE users ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE users ADD COLUMN last_login_at timestamptz;
UPDATE users SET display_name=left(identity_subject,100);

CREATE TABLE permissions (
    id text PRIMARY KEY,
    assignable boolean NOT NULL,
    dependencies text[] NOT NULL DEFAULT '{}'
);
INSERT INTO permissions(id,assignable,dependencies) VALUES
('users:read',false,'{}'),('users:manage',false,'{users:read}'),
('roles:read',false,'{}'),('roles:manage',false,'{roles:read}'),
('instances:read',true,'{}'),('instances:create',true,'{instances:read}'),
('instances:start',true,'{instances:read}'),('instances:stop',true,'{instances:read}'),
('instances:delete',true,'{instances:read}'),('instances:configure',true,'{instances:read}'),
('instances:access',true,'{instances:read}'),('instances:retry',true,'{instances:read}'),
('configuration:read',true,'{}'),('model:write',true,'{configuration:read}'),
('model:validate',true,'{configuration:read}'),('model:revoke',true,'{configuration:read}'),
('defaults:write',true,'{configuration:read}'),('audit:read',true,'{}'),
('audit:organization',true,'{audit:read}');
CREATE TABLE roles (
    id uuid PRIMARY KEY,
    org_id uuid NOT NULL REFERENCES organizations(id),
    code text NOT NULL CHECK (code IN ('ADMIN','MEMBER') OR code ~ '^[a-z][a-z0-9_]{2,31}$'),
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 64),
    description text NOT NULL DEFAULT '' CHECK (length(description)<=500),
    builtin boolean NOT NULL DEFAULT false CHECK (builtin=(code IN ('ADMIN','MEMBER'))),
    version bigint NOT NULL DEFAULT 1 CHECK (version>0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(org_id,code), UNIQUE(org_id,id)
);
CREATE TABLE role_permissions (
    role_id uuid NOT NULL REFERENCES roles(id),
    permission_id text NOT NULL REFERENCES permissions(id),
    PRIMARY KEY(role_id,permission_id)
);
CREATE FUNCTION public.hc_seed_roles() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE admin_id uuid:=gen_random_uuid(); member_id uuid:=gen_random_uuid();
BEGIN
    INSERT INTO public.roles(id,org_id,code,name,builtin) VALUES
      (admin_id,NEW.id,'ADMIN','管理员',true),(member_id,NEW.id,'MEMBER','成员',true);
    INSERT INTO public.role_permissions SELECT admin_id,id FROM public.permissions;
    INSERT INTO public.role_permissions SELECT member_id,id FROM public.permissions
      WHERE id LIKE 'instances:%' OR id IN ('configuration:read','audit:read');
    RETURN NEW;
END;
$$;
REVOKE ALL ON FUNCTION public.hc_seed_roles() FROM PUBLIC;
CREATE TRIGGER organizations_seed_roles AFTER INSERT ON organizations
FOR EACH ROW EXECUTE FUNCTION public.hc_seed_roles();
INSERT INTO roles(id,org_id,code,name,builtin)
SELECT gen_random_uuid(),organizations.id,builtins.code,builtins.name,true FROM organizations
CROSS JOIN (VALUES('ADMIN','管理员'),('MEMBER','成员')) AS builtins(code,name);
INSERT INTO role_permissions SELECT r.id,p.id FROM roles r CROSS JOIN permissions p
WHERE r.code='ADMIN' OR p.id LIKE 'instances:%' OR p.id IN ('configuration:read','audit:read');
ALTER TABLE memberships DROP CONSTRAINT memberships_role_check;
ALTER TABLE memberships ADD CONSTRAINT memberships_role_fk FOREIGN KEY(org_id,role) REFERENCES roles(org_id,code);

CREATE FUNCTION public.hc_effective_permissions(requested_org uuid,requested_user uuid)
RETURNS text[] LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
    SELECT COALESCE(array_agg(rp.permission_id ORDER BY rp.permission_id),'{}'::text[])
    FROM public.memberships m JOIN public.users u ON u.id=m.user_id
    JOIN public.organizations o ON o.id=m.org_id JOIN public.roles r ON r.org_id=m.org_id AND r.code=m.role
    JOIN public.role_permissions rp ON rp.role_id=r.id
    WHERE m.org_id=requested_org AND m.user_id=requested_user
    AND m.status='ACTIVE' AND u.status='ACTIVE' AND o.status='ACTIVE';
$$;
CREATE FUNCTION public.hc_has_permission(requested_org uuid,requested_user uuid,requested_permission text)
RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
    SELECT requested_permission=ANY(public.hc_effective_permissions(requested_org,requested_user));
$$;
REVOKE ALL ON FUNCTION public.hc_effective_permissions(uuid,uuid),public.hc_has_permission(uuid,uuid,text) FROM PUBLIC;

CREATE TABLE iam_requests (
    org_id uuid NOT NULL REFERENCES organizations(id),
    actor_id uuid NOT NULL REFERENCES users(id),
    action text NOT NULL CHECK (action IN ('USER_CREATE','ROLE_CREATE')),
    key_sha256 bytea NOT NULL CHECK (octet_length(key_sha256)=32),
    request_sha256 bytea NOT NULL CHECK (octet_length(request_sha256)=32),
    target_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(org_id,actor_id,action,key_sha256)
);
CREATE TABLE identity_provisioning_operations (
    id uuid PRIMARY KEY,
    org_id uuid NOT NULL,
    user_id uuid NOT NULL UNIQUE,
    provider_account_id text NOT NULL,
    login_email text NOT NULL CHECK (length(login_email) BETWEEN 1 AND 254),
    status text NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','SUCCEEDED')),
    reason_code text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY(org_id,user_id) REFERENCES memberships(org_id,user_id)
);

-- Called only from governance commands after membership locks. Tickets are
-- bound to the revoked Portal session/generation, including unconsumed tickets.
CREATE FUNCTION public.hc_iam_revoke(requested_org uuid,affected uuid[]) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
    UPDATE public.memberships SET auth_generation=auth_generation+1 WHERE org_id=requested_org AND user_id=ANY(affected);
    UPDATE public.portal_sessions SET revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE org_id=requested_org AND user_id=ANY(affected);
    UPDATE public.access_sessions a SET revoked_at=COALESCE(a.revoked_at,clock_timestamp())
      FROM public.portal_sessions ps WHERE ps.id=a.portal_session_id AND ps.org_id=requested_org AND ps.user_id=ANY(affected);
END;
$$;
REVOKE ALL ON FUNCTION public.hc_iam_revoke(uuid,uuid[]) FROM PUBLIC;

CREATE FUNCTION public.hc_iam_change(
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
    IF actor_role<>'ADMIN' THEN RAISE EXCEPTION 'IAM_FORBIDDEN'; END IF;
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
REVOKE ALL ON FUNCTION public.hc_iam_change(uuid,uuid,uuid,bigint,text,uuid,bigint,jsonb,bytea,bytea,text) FROM PUBLIC;
