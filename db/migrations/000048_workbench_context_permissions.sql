-- Personal memory and knowledge management are workbench features. Built-in
-- administrators and members receive only these owner-scoped capabilities;
-- custom roles keep their explicitly assigned permissions.
WITH granted AS (
 INSERT INTO role_permissions(role_id,permission_id)
 SELECT r.id,p.id FROM roles r CROSS JOIN permissions p
 WHERE r.builtin AND r.code IN ('ADMIN','MEMBER')
 AND p.id IN ('memory:read','memory:manage','knowledge:read','knowledge:manage')
 ON CONFLICT DO NOTHING RETURNING role_id
), changed AS (
 UPDATE roles r SET version=version+1,updated_at=clock_timestamp()
 WHERE EXISTS(SELECT 1 FROM granted g WHERE g.role_id=r.id)
 RETURNING r.id,r.org_id
)
INSERT INTO audit_events(id,org_id,resource_id,action,outcome,trace_id,details)
 SELECT gen_random_uuid(),org_id,id,'CONTEXT_DEFAULT_PERMISSIONS_GRANTED','SUCCEEDED',
 gen_random_uuid()::text,jsonb_build_object('migration',48) FROM changed;

CREATE OR REPLACE FUNCTION public.hc_seed_roles() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE admin_id uuid:=gen_random_uuid(); member_id uuid:=gen_random_uuid();
BEGIN
 INSERT INTO public.roles(id,org_id,code,name,builtin) VALUES
 (admin_id,NEW.id,'ADMIN','管理员',true),(member_id,NEW.id,'MEMBER','成员',true);
 INSERT INTO public.role_permissions SELECT admin_id,id FROM public.permissions;
 INSERT INTO public.role_permissions SELECT member_id,id FROM public.permissions
 WHERE id LIKE 'instances:%' OR id LIKE 'personal-model:%' OR id LIKE 'skills:%'
 OR id LIKE 'memory:%' OR id LIKE 'knowledge:%'
 OR id IN ('configuration:read','audit:read');
 RETURN NEW;
END;
$$;
