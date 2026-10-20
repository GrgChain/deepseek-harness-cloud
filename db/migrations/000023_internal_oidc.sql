-- Embedded first-party OpenID Provider. Password hashes never leave the API role.
CREATE TABLE identity_accounts (
    id uuid PRIMARY KEY,
    org_id uuid NOT NULL REFERENCES organizations(id),
    user_id uuid UNIQUE REFERENCES users(id),
    login_email text NOT NULL CHECK(length(login_email) BETWEEN 3 AND 254 AND login_email=lower(login_email)),
    display_name text NOT NULL CHECK(length(display_name) BETWEEN 1 AND 100),
    password_hash bytea NOT NULL CHECK(octet_length(password_hash)=60),
    credential_version bigint NOT NULL DEFAULT 1 CHECK(credential_version>0),
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(org_id,login_email)
);
CREATE TABLE identity_authorizations (
    id uuid PRIMARY KEY,
    browser_sha256 bytea NOT NULL CHECK(octet_length(browser_sha256)=32),
    csrf_sha256 bytea NOT NULL CHECK(octet_length(csrf_sha256)=32),
    client_id text NOT NULL,
    redirect_uri text NOT NULL,
    state text NOT NULL,
    nonce text NOT NULL,
    challenge text NOT NULL CHECK(length(challenge)=43),
    scope text NOT NULL,
    attempts integer NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 10),
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz
);
CREATE INDEX identity_authorizations_expiry ON identity_authorizations(expires_at,id);
CREATE TABLE identity_codes (
    id uuid PRIMARY KEY,
    code_sha256 bytea NOT NULL UNIQUE CHECK(octet_length(code_sha256)=32),
    account_id uuid NOT NULL REFERENCES identity_accounts(id),
    client_id text NOT NULL,
    redirect_uri text NOT NULL,
    nonce text NOT NULL,
    challenge text NOT NULL CHECK(length(challenge)=43),
    scope text NOT NULL,
    auth_generation bigint NOT NULL,
    credential_version bigint NOT NULL,
    authenticated_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz
);
CREATE INDEX identity_codes_expiry ON identity_codes(expires_at,id);
CREATE TABLE identity_access_tokens (
    id uuid PRIMARY KEY,
    token_sha256 bytea NOT NULL UNIQUE CHECK(octet_length(token_sha256)=32),
    code_id uuid NOT NULL REFERENCES identity_codes(id),
    account_id uuid NOT NULL REFERENCES identity_accounts(id),
    scope text NOT NULL,
    auth_generation bigint NOT NULL,
    credential_version bigint NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz
);
CREATE INDEX identity_access_tokens_expiry ON identity_access_tokens(expires_at,id);
CREATE INDEX identity_access_tokens_code ON identity_access_tokens(code_id);

-- Preserve the shared 500-row cleanup budget, including protocol state.
ALTER FUNCTION public.hc_cleanup_expired(integer) RENAME TO hc_cleanup_platform_expired;
REVOKE ALL ON FUNCTION public.hc_cleanup_platform_expired(integer) FROM PUBLIC;
DO $$ BEGIN
    IF to_regrole('hc_worker') IS NOT NULL THEN
        REVOKE ALL ON FUNCTION public.hc_cleanup_platform_expired(integer) FROM hc_worker;
    END IF;
END $$;
CREATE FUNCTION public.hc_cleanup_expired(batch_size integer) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE remaining integer:=batch_size; n integer; result jsonb:='{}'::jsonb;
BEGIN
    IF batch_size IS NULL OR batch_size<1 OR batch_size>500 THEN RAISE EXCEPTION 'invalid cleanup bound'; END IF;
    IF NOT pg_try_advisory_xact_lock(731804525) THEN RETURN result; END IF;
    WITH doomed AS (SELECT t.id FROM public.identity_access_tokens t LEFT JOIN public.identity_codes c ON c.id=t.code_id
        WHERE t.expires_at<=clock_timestamp() OR t.revoked_at IS NOT NULL OR c.expires_at<=clock_timestamp()-interval '5 minutes'
        ORDER BY t.expires_at,t.id LIMIT remaining FOR UPDATE OF t SKIP LOCKED)
    DELETE FROM public.identity_access_tokens WHERE id IN(SELECT id FROM doomed);
    GET DIAGNOSTICS n=ROW_COUNT; remaining:=remaining-n; result:=result||jsonb_build_object('IdentityTokens',n);
    WITH doomed AS (SELECT c.id FROM public.identity_codes c WHERE c.expires_at<=clock_timestamp()-interval '5 minutes'
        AND NOT EXISTS(SELECT 1 FROM public.identity_access_tokens t WHERE t.code_id=c.id)
        ORDER BY c.expires_at,c.id LIMIT remaining FOR UPDATE OF c SKIP LOCKED)
    DELETE FROM public.identity_codes WHERE id IN(SELECT id FROM doomed);
    GET DIAGNOSTICS n=ROW_COUNT; remaining:=remaining-n; result:=result||jsonb_build_object('IdentityCodes',n);
    WITH doomed AS (SELECT id FROM public.identity_authorizations WHERE expires_at<=clock_timestamp() OR consumed_at IS NOT NULL
        ORDER BY expires_at,id LIMIT remaining FOR UPDATE SKIP LOCKED)
    DELETE FROM public.identity_authorizations WHERE id IN(SELECT id FROM doomed);
    GET DIAGNOSTICS n=ROW_COUNT; remaining:=remaining-n; result:=result||jsonb_build_object('IdentityAuthorizations',n);
    IF remaining>0 THEN result:=result||public.hc_cleanup_platform_expired(remaining); END IF;
    RETURN result;
END $$;
REVOKE ALL ON FUNCTION public.hc_cleanup_expired(integer) FROM PUBLIC;

-- Runtime API can lock current identity authority without gaining UPDATE on
-- organization/user governance columns. The issuer/account pair stays immutable.
CREATE FUNCTION public.hc_identity_lock_authority(requested_org uuid,account uuid,issuer text,expected_version bigint,expected_generation bigint)
RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE a public.identity_accounts%ROWTYPE; active_org text; current_generation bigint;
BEGIN
    SELECT * INTO a FROM public.identity_accounts WHERE id=account AND org_id=requested_org FOR SHARE;
    IF NOT FOUND OR NOT a.enabled OR a.credential_version<>expected_version THEN RETURN false; END IF;
    SELECT status INTO active_org FROM public.organizations WHERE id=requested_org FOR SHARE;
    IF active_org IS DISTINCT FROM 'ACTIVE' THEN RETURN false; END IF;
    IF a.user_id IS NULL THEN RETURN expected_generation=1; END IF;
    SELECT m.auth_generation INTO current_generation FROM public.memberships m JOIN public.users u ON u.id=m.user_id
      WHERE m.org_id=requested_org AND m.user_id=a.user_id AND m.status='ACTIVE' AND u.status='ACTIVE'
      AND u.identity_source='local' AND u.identity_issuer=issuer AND u.identity_subject=a.id::text FOR SHARE OF m,u;
    IF NOT FOUND OR current_generation<>expected_generation THEN RETURN false; END IF;
    RETURN NOT EXISTS(SELECT 1 FROM public.identity_password_resets r WHERE r.org_id=requested_org AND r.user_id=a.user_id AND r.status='PENDING');
END $$;
REVOKE ALL ON FUNCTION public.hc_identity_lock_authority(uuid,uuid,text,bigint,bigint) FROM PUBLIC;
