CREATE FUNCTION public.hc_assign_default_user_quota()
RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
    IF NEW.action <> 'USER_CREATE' THEN
        RETURN NEW;
    END IF;

    INSERT INTO public.user_quota_policies(org_id,owner_id,max_instances,cpu_millicores,memory_bytes,storage_bytes)
    SELECT NEW.org_id,NEW.target_id,allocation.instances,
      CASE WHEN allocation.instances > allocation.cpu_limit / allocation.spec_cpu
        THEN allocation.cpu_limit ELSE allocation.spec_cpu * allocation.instances END,
      CASE WHEN allocation.instances > allocation.memory_limit / allocation.spec_memory
        THEN allocation.memory_limit ELSE allocation.spec_memory * allocation.instances END,
      CASE WHEN allocation.instances > allocation.storage_limit / allocation.spec_storage
        THEN allocation.storage_limit ELSE allocation.spec_storage * allocation.instances END
    FROM (
      SELECT LEAST(q.max_user_instances::bigint,q.max_instances::bigint) AS instances,
        q.cpu_millicores AS cpu_limit,q.memory_bytes AS memory_limit,q.storage_bytes AS storage_limit,
        s.cpu_millicores AS spec_cpu,s.memory_bytes AS spec_memory,s.storage_bytes AS spec_storage
      FROM public.quota_policies q
      JOIN public.organization_defaults d ON d.org_id=q.org_id
      JOIN public.approved_specs s ON s.id=d.spec_id
      WHERE q.org_id=NEW.org_id
    ) allocation
    ON CONFLICT(org_id,owner_id) DO NOTHING;

    RETURN NEW;
END;
$$;
REVOKE ALL ON FUNCTION public.hc_assign_default_user_quota() FROM PUBLIC;
CREATE TRIGGER iam_requests_assign_default_user_quota
AFTER INSERT ON public.iam_requests
FOR EACH ROW EXECUTE FUNCTION public.hc_assign_default_user_quota();
