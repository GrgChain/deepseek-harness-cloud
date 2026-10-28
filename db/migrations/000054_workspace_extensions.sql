CREATE TABLE workspace_extensions (
 instance_id uuid PRIMARY KEY,
 org_id uuid NOT NULL,
 owner_id uuid NOT NULL,
 auth_generation bigint NOT NULL CHECK(auth_generation>0),
 configuration jsonb NOT NULL CHECK(jsonb_typeof(configuration)='object'),
 applied_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(org_id,owner_id,instance_id) REFERENCES instances(org_id,owner_id,id)
);
REVOKE ALL ON workspace_extensions FROM PUBLIC;

-- Runtime authority has a workspace lifetime, never a forged Portal session.
CREATE FUNCTION public.hc_extensions_lock_authority(requested_org uuid,requested_owner uuid,requested_instance uuid,requested_generation bigint)
RETURNS text LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE selected_role text;
BEGIN
 SELECT m.role INTO selected_role FROM public.memberships m
 JOIN public.users u ON u.id=m.user_id JOIN public.organizations o ON o.id=m.org_id
 WHERE m.org_id=requested_org AND m.user_id=requested_owner AND m.status='ACTIVE'
 AND u.status='ACTIVE' AND o.status='ACTIVE' AND m.auth_generation=requested_generation FOR UPDATE OF m;
 IF NOT FOUND THEN RETURN NULL; END IF;
 PERFORM i.id FROM public.instances i JOIN public.workspace_extensions e ON e.instance_id=i.id
 WHERE i.id=requested_instance AND i.org_id=requested_org AND i.owner_id=requested_owner
 AND e.auth_generation=requested_generation AND i.deleted_at IS NULL
 AND i.desired_state='RUNNING' AND i.observed_state='RUNNING' FOR UPDATE OF i;
 IF NOT FOUND THEN RETURN NULL; END IF;
 RETURN selected_role;
END;
$$;
REVOKE ALL ON FUNCTION public.hc_extensions_lock_authority(uuid,uuid,uuid,bigint) FROM PUBLIC;
