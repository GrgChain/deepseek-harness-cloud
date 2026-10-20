CREATE FUNCTION public.hc_pending_identity_login_email()
RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE provisioning_status text;
BEGIN
    IF NEW.status <> 'PENDING_IDENTITY' OR NEW.email IS NOT DISTINCT FROM OLD.email THEN
        RETURN NEW;
    END IF;

    SELECT status INTO provisioning_status
      FROM public.identity_provisioning_operations
      WHERE user_id=NEW.id
      FOR UPDATE;
    IF NOT FOUND OR provisioning_status <> 'PENDING' THEN
        RAISE EXCEPTION 'IAM_IDENTITY_NOT_PENDING';
    END IF;
    IF EXISTS(SELECT 1 FROM public.identity_accounts WHERE user_id=NEW.id) THEN
        RAISE EXCEPTION 'IAM_IDENTITY_ALREADY_SUPPLIED';
    END IF;

    UPDATE public.identity_provisioning_operations
      SET login_email=lower(NEW.email),reason_code='',updated_at=clock_timestamp()
      WHERE user_id=NEW.id AND status='PENDING';
    RETURN NEW;
END;
$$;
REVOKE ALL ON FUNCTION public.hc_pending_identity_login_email() FROM PUBLIC;

CREATE TRIGGER users_pending_identity_login_email
BEFORE UPDATE OF email ON public.users
FOR EACH ROW EXECUTE FUNCTION public.hc_pending_identity_login_email();
