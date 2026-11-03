INSERT INTO permissions(id,assignable,dependencies) VALUES
 ('plugins:manage',true,'{instances:read}');

WITH granted AS (
 INSERT INTO role_permissions(role_id,permission_id)
 SELECT id,'plugins:manage' FROM roles WHERE builtin AND code IN ('ADMIN','MEMBER')
 ON CONFLICT DO NOTHING RETURNING role_id
), changed AS (
 UPDATE roles r SET version=version+1,updated_at=clock_timestamp()
 WHERE EXISTS(SELECT 1 FROM granted g WHERE g.role_id=r.id)
 RETURNING r.id,r.org_id
)
INSERT INTO audit_events(id,org_id,resource_id,action,outcome,trace_id,details)
 SELECT gen_random_uuid(),org_id,id,'PLUGIN_DEFAULT_PERMISSION_GRANTED','SUCCEEDED',
 gen_random_uuid()::text,jsonb_build_object('migration',58) FROM changed;

CREATE TABLE personal_plugins (
 id uuid PRIMARY KEY,
 org_id uuid NOT NULL REFERENCES organizations(id),
 owner_id uuid NOT NULL REFERENCES users(id),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 214),
 package_name text NOT NULL CHECK(length(package_name) BETWEEN 1 AND 214),
 package_version text NOT NULL CHECK(length(package_version) BETWEEN 1 AND 128),
 author text NOT NULL,
 category text NOT NULL CHECK(category IN ('会话与记忆','开发工具','集成扩展')),
 summary text NOT NULL,
 description text NOT NULL,
 keywords text[] NOT NULL DEFAULT '{}',
 repository text NOT NULL DEFAULT '',
 documentation text NOT NULL DEFAULT '',
 compatibility text NOT NULL,
 checked_at date NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(org_id,owner_id,package_name)
);
CREATE INDEX personal_plugins_owner ON personal_plugins(org_id,owner_id,created_at DESC,id);
REVOKE ALL ON personal_plugins FROM PUBLIC;

CREATE OR REPLACE FUNCTION public.hc_seed_roles() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE admin_id uuid:=gen_random_uuid(); member_id uuid:=gen_random_uuid();
BEGIN
 INSERT INTO public.roles(id,org_id,code,name,builtin) VALUES
 (admin_id,NEW.id,'ADMIN','管理员',true),(member_id,NEW.id,'MEMBER','成员',true);
 INSERT INTO public.role_permissions SELECT admin_id,id FROM public.permissions;
 INSERT INTO public.role_permissions SELECT member_id,id FROM public.permissions
 WHERE id LIKE 'instances:%' OR id LIKE 'personal-model:%' OR id LIKE 'skills:%'
 OR id LIKE 'memory:%' OR id LIKE 'knowledge:%' OR id LIKE 'connectors:%'
 OR id IN ('configuration:read','audit:read','plugins:manage');
 RETURN NEW;
END;
$$;
