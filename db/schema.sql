-- Initial V0.1 schema. Immutable after first application.

CREATE TABLE organizations (
    id uuid PRIMARY KEY,
    singleton boolean NOT NULL DEFAULT true UNIQUE CHECK (singleton),
    name text NOT NULL,
    status text NOT NULL CHECK (status IN ('ACTIVE', 'DISABLED'))
);
CREATE TABLE users (
    id uuid PRIMARY KEY,
    identity_issuer text NOT NULL,
    identity_subject text NOT NULL,
    coder_user_id uuid NOT NULL UNIQUE,
    status text NOT NULL CHECK (status IN ('ACTIVE', 'DISABLED')),
    UNIQUE (identity_issuer, identity_subject)
);
CREATE TABLE memberships (
    org_id uuid NOT NULL REFERENCES organizations(id),
    user_id uuid NOT NULL REFERENCES users(id),
    role text NOT NULL CHECK (role IN ('ADMIN', 'MEMBER')),
    status text NOT NULL CHECK (status IN ('ACTIVE', 'DISABLED')),
    auth_generation bigint NOT NULL DEFAULT 1 CHECK (auth_generation > 0),
    PRIMARY KEY (org_id, user_id)
);
CREATE TABLE portal_sessions (
    id uuid PRIMARY KEY,
    secret_sha256 bytea NOT NULL UNIQUE CHECK (octet_length(secret_sha256) = 32),
    org_id uuid NOT NULL,
    user_id uuid NOT NULL,
    auth_generation bigint NOT NULL,
    csrf_sha256 bytea NOT NULL CHECK (octet_length(csrf_sha256) = 32),
    csrf_cipher bytea NOT NULL,
    csrf_key_id text NOT NULL,
    csrf_nonce bytea NOT NULL CHECK (octet_length(csrf_nonce) = 12),
    created_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    FOREIGN KEY (org_id, user_id) REFERENCES memberships(org_id, user_id),
    CHECK (expires_at > created_at)
);
CREATE TABLE oidc_transactions (
    id uuid PRIMARY KEY,
    state_sha256 bytea NOT NULL UNIQUE CHECK (octet_length(state_sha256) = 32),
    browser_sha256 bytea NOT NULL CHECK (octet_length(browser_sha256) = 32),
    nonce_sha256 bytea NOT NULL CHECK (octet_length(nonce_sha256) = 32),
    pkce_cipher bytea NOT NULL,
    pkce_nonce bytea NOT NULL CHECK (octet_length(pkce_nonce) = 12),
    key_id text NOT NULL,
    return_path text NOT NULL CHECK (return_path LIKE '/app/%'),
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz
);
CREATE TABLE secrets (
    id uuid PRIMARY KEY,
    org_id uuid NOT NULL REFERENCES organizations(id),
    key_id text NOT NULL,
    nonce bytea NOT NULL CHECK (octet_length(nonce) = 12),
    ciphertext bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz,
    UNIQUE (org_id, id)
);
CREATE TABLE model_configuration_heads (
    id uuid PRIMARY KEY,
    org_id uuid NOT NULL UNIQUE REFERENCES organizations(id),
    current_version bigint NOT NULL DEFAULT 0 CHECK (current_version >= 0),
    UNIQUE (org_id, id)
);
CREATE TABLE model_configurations (
    org_id uuid NOT NULL,
    id uuid NOT NULL,
    version bigint NOT NULL CHECK (version > 0),
    provider text NOT NULL CHECK (provider = 'deepseek-official'),
    endpoint text NOT NULL,
    protocol text NOT NULL CHECK (protocol = 'deepseek-messages'),
    model text NOT NULL,
    credential_ref uuid NOT NULL,
    status text NOT NULL CHECK (status IN ('UNVALIDATED', 'VALID', 'INVALID', 'REVOKED')),
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz,
    PRIMARY KEY (id, version),
    UNIQUE (org_id, id, version),
    FOREIGN KEY (org_id, id) REFERENCES model_configuration_heads(org_id, id),
    FOREIGN KEY (org_id, credential_ref) REFERENCES secrets(org_id, id)
);
CREATE TABLE model_validations (
    config_id uuid NOT NULL,
    config_version bigint NOT NULL,
    status text NOT NULL CHECK (status IN ('UNKNOWN', 'CHECKING', 'HEALTHY', 'UNHEALTHY')),
    checked_at timestamptz,
    expires_at timestamptz,
    reason_code text NOT NULL DEFAULT '',
    lease_token bigint NOT NULL DEFAULT 0 CHECK (lease_token >= 0),
    lease_expires_at timestamptz,
    PRIMARY KEY (config_id, config_version),
    FOREIGN KEY (config_id, config_version) REFERENCES model_configurations(id, version),
    CHECK (expires_at IS NULL OR (checked_at IS NOT NULL AND expires_at > checked_at))
);
CREATE TABLE probe_budgets (
    org_id uuid NOT NULL REFERENCES organizations(id),
    day date NOT NULL,
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0 AND attempts <= 1000),
    PRIMARY KEY (org_id, day)
);
CREATE TABLE approved_templates (
    id uuid NOT NULL,
    version uuid NOT NULL,
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 128),
    image_digest text NOT NULL CHECK (image_digest ~ '^sha256:[a-f0-9]{64}$'),
    runtime_manifest jsonb NOT NULL CHECK (jsonb_typeof(runtime_manifest) = 'object'),
    enabled boolean NOT NULL DEFAULT false,
    PRIMARY KEY (id, version)
);
CREATE UNIQUE INDEX approved_templates_single_enabled ON approved_templates ((true)) WHERE enabled;
CREATE TABLE approved_specs (
    id uuid PRIMARY KEY,
    cpu_millicores bigint NOT NULL CHECK (cpu_millicores > 0),
    memory_bytes bigint NOT NULL CHECK (memory_bytes > 0),
    storage_bytes bigint NOT NULL CHECK (storage_bytes > 0),
    enabled boolean NOT NULL DEFAULT false
);
CREATE UNIQUE INDEX approved_specs_single_enabled ON approved_specs ((true)) WHERE enabled;
CREATE TABLE organization_defaults (
    org_id uuid PRIMARY KEY REFERENCES organizations(id),
    version bigint NOT NULL CHECK (version > 0),
    template_id uuid NOT NULL,
    template_version uuid NOT NULL,
    spec_id uuid NOT NULL REFERENCES approved_specs(id),
    model_config_id uuid NOT NULL,
    model_config_version bigint NOT NULL,
    FOREIGN KEY (template_id, template_version) REFERENCES approved_templates(id, version),
    FOREIGN KEY (org_id, model_config_id, model_config_version)
        REFERENCES model_configurations(org_id, id, version)
);
CREATE TABLE instances (
    id uuid PRIMARY KEY,
    org_id uuid NOT NULL,
    owner_id uuid NOT NULL,
    name text NOT NULL CHECK (name ~ '^[a-z][a-z0-9-]{2,31}$'),
    external_create_key uuid NOT NULL UNIQUE,
    coder_workspace_name text NOT NULL UNIQUE,
    coder_workspace_id uuid UNIQUE,
    backend_type text NOT NULL DEFAULT 'docker' CHECK (backend_type = 'docker'),
    storage_asset_id uuid UNIQUE,
    desired_state text NOT NULL CHECK (desired_state IN ('RUNNING', 'STOPPED', 'DELETED')),
    observed_state text NOT NULL CHECK (observed_state IN
        ('CREATING', 'STARTING', 'RUNNING', 'STOPPING', 'STOPPED', 'ERROR', 'DELETING', 'DELETED')),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    access_generation bigint NOT NULL DEFAULT 1 CHECK (access_generation > 0),
    model_config_id uuid NOT NULL,
    model_config_version bigint NOT NULL,
    config_snapshot jsonb NOT NULL CHECK (jsonb_typeof(config_snapshot) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    FOREIGN KEY (org_id, owner_id) REFERENCES memberships(org_id, user_id),
    FOREIGN KEY (org_id, model_config_id, model_config_version)
        REFERENCES model_configurations(org_id, id, version),
    UNIQUE (org_id, owner_id, id),
    CHECK ((observed_state = 'DELETED') = (deleted_at IS NOT NULL))
);
CREATE UNIQUE INDEX instances_active_name ON instances(org_id, name) WHERE deleted_at IS NULL;
CREATE INDEX instances_owner_page ON instances(org_id, owner_id, created_at DESC, id DESC);
CREATE TABLE storage_assets (
    id uuid PRIMARY KEY,
    org_id uuid NOT NULL,
    owner_id uuid NOT NULL,
    instance_id uuid NOT NULL UNIQUE,
    resource_name text NOT NULL UNIQUE,
    filesystem_id text,
    capacity_bytes bigint NOT NULL CHECK (capacity_bytes > 0),
    init_version integer NOT NULL DEFAULT 0 CHECK (init_version >= 0),
    status text NOT NULL CHECK (status IN ('PREPARING', 'READY', 'RETAINED', 'RECLAIM_PENDING', 'DELETED')),
    retention_until timestamptz,
    legal_hold boolean NOT NULL DEFAULT false,
    FOREIGN KEY (org_id, owner_id, instance_id) REFERENCES instances(org_id, owner_id, id),
    UNIQUE (org_id, owner_id, instance_id, id)
);
ALTER TABLE instances ADD CONSTRAINT instances_original_asset
    FOREIGN KEY (org_id, owner_id, id, storage_asset_id)
    REFERENCES storage_assets(org_id, owner_id, instance_id, id);
CREATE TABLE instance_health (
    instance_id uuid NOT NULL REFERENCES instances(id),
    dimension text NOT NULL CHECK (dimension IN ('application', 'storage', 'configuration', 'access', 'model')),
    status text NOT NULL CHECK (status IN ('UNKNOWN', 'CHECKING', 'HEALTHY', 'UNHEALTHY')),
    checked_at timestamptz,
    expires_at timestamptz,
    reason_code text NOT NULL DEFAULT '',
    PRIMARY KEY (instance_id, dimension),
    CHECK (expires_at IS NULL OR (checked_at IS NOT NULL AND expires_at > checked_at))
);
CREATE TABLE operations (
    id uuid PRIMARY KEY,
    instance_id uuid NOT NULL REFERENCES instances(id),
    actor_id uuid REFERENCES users(id),
    actor_kind text NOT NULL DEFAULT 'USER' CHECK (actor_kind IN ('USER', 'SYSTEM')),
    type text NOT NULL CHECK (type IN ('CREATE', 'START', 'STOP', 'DELETE', 'CONFIGURE')),
    status text NOT NULL CHECK (status IN ('ACCEPTED', 'RUNNING', 'UNCERTAIN', 'SUCCEEDED', 'FAILED')),
    stage text NOT NULL CHECK (stage IN
        ('QUEUED', 'PREPARING_STORAGE', 'CREATING_WORKSPACE', 'BUILDING', 'CONFIGURING',
         'CHECKING', 'REVOKING_ACCESS', 'STOPPING', 'DELETING', 'RECONCILING', 'COMPLETE')),
    retry_of uuid REFERENCES operations(id),
    intent jsonb NOT NULL CHECK (jsonb_typeof(intent) = 'object'),
    lease_owner text,
    lease_token bigint NOT NULL DEFAULT 0 CHECK (lease_token >= 0),
    lease_expires_at timestamptz,
    build_id uuid,
    trace_id text NOT NULL,
    error_code text,
    error_message text,
    reconciled_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((actor_kind = 'USER') = (actor_id IS NOT NULL)),
    CHECK (actor_kind <> 'SYSTEM' OR type = 'STOP'),
    CHECK ((lease_owner IS NULL) = (lease_expires_at IS NULL))
);
CREATE UNIQUE INDEX operations_single_active ON operations(instance_id)
    WHERE status IN ('ACCEPTED', 'RUNNING', 'UNCERTAIN');
CREATE INDEX operations_reconcile ON operations(status, lease_expires_at);
CREATE TABLE idempotency_records (
    org_id uuid NOT NULL REFERENCES organizations(id),
    actor_id uuid NOT NULL REFERENCES users(id),
    action text NOT NULL,
    target_id uuid NOT NULL,
    key text NOT NULL CHECK (key ~ '^[A-Za-z0-9_-]{16,128}$'),
    request_sha256 bytea NOT NULL CHECK (octet_length(request_sha256) = 32),
    operation_id uuid NOT NULL REFERENCES operations(id),
    response jsonb NOT NULL CHECK (jsonb_typeof(response) = 'object'),
    expires_at timestamptz,
    PRIMARY KEY (org_id, actor_id, action, target_id, key)
);
CREATE TABLE outbox_events (
    id uuid PRIMARY KEY,
    operation_id uuid NOT NULL UNIQUE REFERENCES operations(id),
    status text NOT NULL CHECK (status IN ('PENDING', 'DONE')),
    retry_at timestamptz NOT NULL DEFAULT now(),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX outbox_pending ON outbox_events(retry_at, id) WHERE status = 'PENDING';
CREATE TABLE quota_policies (
    org_id uuid PRIMARY KEY REFERENCES organizations(id),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    max_instances integer NOT NULL DEFAULT 20 CHECK (max_instances > 0),
    max_user_instances integer NOT NULL DEFAULT 2 CHECK (max_user_instances > 0),
    cpu_millicores bigint NOT NULL DEFAULT 40000 CHECK (cpu_millicores > 0),
    memory_bytes bigint NOT NULL DEFAULT 85899345920 CHECK (memory_bytes > 0),
    storage_bytes bigint NOT NULL DEFAULT 214748364800 CHECK (storage_bytes > 0),
    max_concurrent_creates integer NOT NULL DEFAULT 2 CHECK (max_concurrent_creates > 0)
);
-- Limits are usage caps, not reserved host capacity. Missing rows inherit the
-- organization policy. All quota mutations serialize on quota_policies.
CREATE TABLE user_quota_policies (
    org_id uuid NOT NULL REFERENCES quota_policies(org_id),
    owner_id uuid NOT NULL,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    max_instances bigint NOT NULL CHECK (max_instances >= 0),
    cpu_millicores bigint NOT NULL CHECK (cpu_millicores >= 0),
    memory_bytes bigint NOT NULL CHECK (memory_bytes >= 0),
    storage_bytes bigint NOT NULL CHECK (storage_bytes >= 0),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, owner_id),
    FOREIGN KEY (org_id, owner_id) REFERENCES memberships(org_id, user_id)
);

CREATE TABLE quota_reservations (
    id uuid PRIMARY KEY,
    org_id uuid NOT NULL,
    owner_id uuid NOT NULL,
    instance_id uuid NOT NULL,
    operation_id uuid NOT NULL REFERENCES operations(id),
    resource_type text NOT NULL CHECK (resource_type IN ('INSTANCE', 'RUNNING', 'CPU', 'MEMORY', 'STORAGE', 'CONCURRENT')),
    amount bigint NOT NULL CHECK (amount > 0),
    status text NOT NULL CHECK (status IN ('HELD', 'USED', 'RELEASED')),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (org_id, owner_id, instance_id) REFERENCES instances(org_id, owner_id, id),
    UNIQUE (operation_id, resource_type)
);
CREATE INDEX quota_active ON quota_reservations(org_id, resource_type, owner_id)
    WHERE status IN ('HELD', 'USED');
CREATE TABLE access_routes (
    instance_id uuid PRIMARY KEY REFERENCES instances(id),
    host text NOT NULL UNIQUE,
    upstream_ip inet NOT NULL,
    upstream_port integer NOT NULL DEFAULT 3080 CHECK (upstream_port = 3080),
    runtime_generation uuid NOT NULL,
    enabled boolean NOT NULL DEFAULT false
);
CREATE TABLE access_challenges (
    id uuid PRIMARY KEY,
    instance_id uuid NOT NULL REFERENCES instances(id),
    host text NOT NULL,
    secret_sha256 bytea NOT NULL CHECK (octet_length(secret_sha256) = 32),
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz
);
CREATE TABLE access_tickets (
    id uuid PRIMARY KEY,
    ticket_sha256 bytea NOT NULL UNIQUE CHECK (octet_length(ticket_sha256) = 32),
    challenge_id uuid NOT NULL UNIQUE REFERENCES access_challenges(id),
    portal_session_id uuid NOT NULL REFERENCES portal_sessions(id),
    instance_id uuid NOT NULL REFERENCES instances(id),
    host text NOT NULL,
    auth_generation bigint NOT NULL,
    access_generation bigint NOT NULL,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz
);
CREATE TABLE access_sessions (
    id uuid PRIMARY KEY,
    secret_sha256 bytea NOT NULL UNIQUE CHECK (octet_length(secret_sha256) = 32),
    portal_session_id uuid NOT NULL REFERENCES portal_sessions(id),
    instance_id uuid NOT NULL REFERENCES instances(id),
    host text NOT NULL,
    auth_generation bigint NOT NULL,
    access_generation bigint NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz
);
CREATE TABLE native_sessions (
    instance_id uuid PRIMARY KEY REFERENCES instances(id),
    runtime_generation uuid NOT NULL,
    key_id text NOT NULL,
    nonce bytea NOT NULL CHECK (octet_length(nonce) = 12),
    cookie_ciphertext bytea NOT NULL,
    expires_at timestamptz NOT NULL
);
CREATE TABLE audit_events (
    id uuid PRIMARY KEY,
    org_id uuid NOT NULL REFERENCES organizations(id),
    actor_id uuid REFERENCES users(id),
    owner_id uuid,
    resource_id uuid,
    action text NOT NULL,
    outcome text NOT NULL CHECK (outcome IN ('ACCEPTED', 'SUCCEEDED', 'FAILED', 'DENIED')),
    trace_id text NOT NULL,
    timestamp timestamptz NOT NULL DEFAULT now(),
    details jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(details) = 'object'),
    FOREIGN KEY (org_id, owner_id) REFERENCES memberships(org_id, user_id)
);
CREATE INDEX audit_page ON audit_events(org_id, timestamp DESC, id DESC);
CREATE INDEX audit_owner_page ON audit_events(org_id, owner_id, timestamp DESC, id DESC);
CREATE INDEX audit_actor_page ON audit_events(org_id, actor_id, timestamp DESC, id DESC);
CREATE INDEX portal_sessions_expiry ON portal_sessions(expires_at);
CREATE INDEX oidc_transactions_expiry ON oidc_transactions(expires_at);
CREATE INDEX oidc_transactions_browser ON oidc_transactions(browser_sha256) WHERE consumed_at IS NULL;
CREATE INDEX access_challenges_expiry ON access_challenges(expires_at);
CREATE INDEX access_tickets_expiry ON access_tickets(expires_at);
CREATE INDEX access_sessions_expiry ON access_sessions(expires_at);
CREATE INDEX idempotency_expiry ON idempotency_records(expires_at) WHERE expires_at IS NOT NULL;
CREATE FUNCTION reject_audit_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'audit events are append-only';
END;
$$;
CREATE TRIGGER audit_append_only BEFORE UPDATE OR DELETE ON audit_events
    FOR EACH ROW EXECUTE FUNCTION reject_audit_mutation();

-- Worker may lock authorization and the model head in the established order,
-- but must not gain UPDATE privileges on identity or configuration ownership.
CREATE OR REPLACE FUNCTION public.hc_worker_lock_authority(requested_org uuid, requested_owner uuid)
RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE authorized boolean;
BEGIN
    SELECT m.status='ACTIVE' AND u.status='ACTIVE' AND o.status='ACTIVE'
    INTO authorized
    FROM public.memberships m
    JOIN public.users u ON u.id=m.user_id
    JOIN public.organizations o ON o.id=m.org_id
    WHERE m.org_id=requested_org AND m.user_id=requested_owner
    FOR UPDATE OF m;
    IF NOT FOUND THEN RAISE EXCEPTION 'worker owner not registered'; END IF;
    PERFORM id FROM public.model_configuration_heads WHERE org_id=requested_org FOR UPDATE;
    IF NOT FOUND THEN RAISE EXCEPTION 'worker configuration head not registered'; END IF;
    RETURN authorized;
END;
$$;
REVOKE ALL ON FUNCTION public.hc_worker_lock_authority(uuid,uuid) FROM PUBLIC;

ALTER TABLE model_validations ADD COLUMN attempted_at timestamptz;
UPDATE model_validations SET attempted_at=COALESCE(lease_expires_at-interval '45 seconds',checked_at);

-- Lock the configuration head without permitting the Worker to rebind it.
CREATE OR REPLACE FUNCTION public.hc_worker_lock_model(requested_org uuid)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog, public AS $$
DECLARE model_id uuid;
BEGIN
    SELECT id INTO model_id FROM public.model_configuration_heads WHERE org_id=requested_org FOR UPDATE;
    RETURN model_id;
END;
$$;
REVOKE ALL ON FUNCTION public.hc_worker_lock_model(uuid) FROM PUBLIC;

-- Gateway locks current authority without permission to change identities.
CREATE OR REPLACE FUNCTION public.hc_access_lock_principal(requested_org uuid, requested_user uuid, requested_session uuid, requested_generation bigint)
RETURNS text LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog, public AS $$
DECLARE current_role text;
BEGIN
    PERFORM user_id FROM public.memberships WHERE org_id=requested_org AND user_id=requested_user FOR UPDATE;
    PERFORM id FROM public.portal_sessions WHERE id=requested_session AND org_id=requested_org AND user_id=requested_user FOR UPDATE;
    SELECT m.role INTO current_role FROM public.memberships m
    JOIN public.users u ON u.id=m.user_id JOIN public.organizations o ON o.id=m.org_id
    JOIN public.portal_sessions ps ON ps.org_id=m.org_id AND ps.user_id=m.user_id
    WHERE m.org_id=requested_org AND m.user_id=requested_user AND ps.id=requested_session
    AND m.status='ACTIVE' AND u.status='ACTIVE' AND o.status='ACTIVE'
    AND m.auth_generation=requested_generation AND ps.auth_generation=m.auth_generation
    AND ps.revoked_at IS NULL AND ps.expires_at>clock_timestamp() AND ps.last_seen_at>clock_timestamp()-interval '30 minutes';
    RETURN current_role;
END;
$$;
REVOKE ALL ON FUNCTION public.hc_access_lock_principal(uuid,uuid,uuid,bigint) FROM PUBLIC;

CREATE OR REPLACE FUNCTION public.hc_access_lock_instance(requested_org uuid, requested_user uuid, requested_instance uuid)
RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog, public AS $$
BEGIN
    PERFORM id FROM public.instances WHERE id=requested_instance AND org_id=requested_org AND owner_id=requested_user FOR UPDATE;
    RETURN FOUND;
END;
$$;
REVOKE ALL ON FUNCTION public.hc_access_lock_instance(uuid,uuid,uuid) FROM PUBLIC;

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

ALTER TABLE public.storage_assets DROP CONSTRAINT storage_assets_status_check;
ALTER TABLE public.storage_assets ADD CONSTRAINT storage_assets_status_check CHECK(status IN ('PREPARING','READY','RETAINED','RESTORING','RECLAIM_PENDING','DELETED'));
ALTER TABLE public.storage_assets ADD COLUMN maintenance_intent jsonb CHECK(maintenance_intent IS NULL OR (jsonb_typeof(maintenance_intent)='object' AND maintenance_intent->>'schema_version'='1'));

-- Legal hold is independent of the purgeable audit rows. Service roles have
-- no privileges here; only the trusted maintenance account may change it.
CREATE TABLE audit_retention_holds (
    org_id uuid PRIMARY KEY REFERENCES organizations(id),
    active boolean NOT NULL,
    reason_code text NOT NULL CHECK (reason_code ~ '^[A-Z][A-Z0-9_]{0,63}$'),
    operator_ref text NOT NULL CHECK (operator_ref ~ '^[A-Za-z0-9_.:@/-]{1,128}$'),
    changed_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
REVOKE ALL ON audit_retention_holds FROM PUBLIC;

-- Real quota commitments for verified resources omitted by an older restore.
-- They do not fabricate instances/operations or authorize external resources.
CREATE TABLE recovery_quota_holds (
    org_id uuid NOT NULL,
    owner_id uuid NOT NULL,
    instance_id uuid NOT NULL,
    resource_type text NOT NULL CHECK (resource_type IN ('INSTANCE','RUNNING','CPU','MEMORY','STORAGE','CONCURRENT')),
    amount bigint NOT NULL CHECK (amount > 0 AND amount <= 1125899906842624),
    inventory_run_id uuid NOT NULL,
    inventory_sha256 text NOT NULL CHECK (inventory_sha256 ~ '^[a-f0-9]{64}$'),
    reason_code text NOT NULL CHECK (reason_code ~ '^[A-Z][A-Z0-9_]{0,63}$'),
    operator_ref text NOT NULL CHECK (operator_ref ~ '^[A-Za-z0-9_.:@/-]{1,128}$'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (org_id,instance_id,resource_type),
    FOREIGN KEY (org_id,owner_id) REFERENCES memberships(org_id,user_id)
);
REVOKE ALL ON recovery_quota_holds FROM PUBLIC;

-- One read model used by both preflight and locked command acceptance. Normal
-- services can read totals but cannot change an orphan hold or release credit.
CREATE VIEW quota_commitments AS
SELECT org_id,owner_id,instance_id,resource_type,amount FROM quota_reservations WHERE status IN ('HELD','USED')
UNION ALL
SELECT org_id,owner_id,instance_id,resource_type,amount FROM recovery_quota_holds;
REVOKE ALL ON quota_commitments FROM PUBLIC;

-- Coder v2.37.4 workspace/template/build persistence adapted to platform PG.
-- Modified 2026-10-13. SPDX-License-Identifier: AGPL-3.0-only
-- Upstream queries: coderd/database/queries/{workspaces,workspacebuilds,templateversions}.sql
CREATE TABLE workspace_authority (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
 organization_id uuid NOT NULL,
 control_user_id uuid NOT NULL
);
CREATE TABLE workspace_templates (
 id uuid PRIMARY KEY,
 organization_id uuid NOT NULL,
 name text NOT NULL CHECK(name='hc-dsh-initial'),
 active_version_id uuid NOT NULL,
 deleted boolean NOT NULL DEFAULT false,
 UNIQUE(organization_id,name)
);
CREATE TABLE workspace_template_versions (
 id uuid PRIMARY KEY,
 template_id uuid NOT NULL REFERENCES workspace_templates(id),
 organization_id uuid NOT NULL,
 name text NOT NULL,
 file_id uuid NOT NULL UNIQUE,
 source jsonb NOT NULL CHECK(jsonb_typeof(source)='object'),
 source_sha256 text NOT NULL CHECK(source_sha256 ~ '^[a-f0-9]{64}$'),
 created_by uuid NOT NULL,
 archived boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(template_id,name)
);
ALTER TABLE workspace_templates ADD CONSTRAINT workspace_template_active_fk FOREIGN KEY(active_version_id) REFERENCES workspace_template_versions(id) DEFERRABLE INITIALLY DEFERRED;
CREATE TABLE workspace_workspaces (
 id uuid PRIMARY KEY,
 name text NOT NULL CHECK(name ~ '^hc-[a-z2-7]{26}$'),
 owner_id uuid NOT NULL REFERENCES users(coder_user_id),
 organization_id uuid NOT NULL,
 template_id uuid NOT NULL REFERENCES workspace_templates(id),
 deleted boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(owner_id,name)
);
CREATE TABLE workspace_builds (
 id uuid PRIMARY KEY,
 workspace_id uuid NOT NULL REFERENCES workspace_workspaces(id),
 template_version_id uuid NOT NULL REFERENCES workspace_template_versions(id),
 build_number integer NOT NULL CHECK(build_number>0),
 transition text NOT NULL CHECK(transition IN ('start','stop','delete')),
 initiator_id uuid NOT NULL,
 job_id uuid NOT NULL UNIQUE,
 job_status text NOT NULL DEFAULT 'pending' CHECK(job_status IN ('pending','running','succeeded','failed','canceled')),
 error_code text NOT NULL DEFAULT '',
 parameters jsonb NOT NULL CHECK(jsonb_typeof(parameters)='array'),
 provisioner_state bytea,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 started_at timestamptz,
 completed_at timestamptz,
 UNIQUE(workspace_id,build_number)
);
CREATE UNIQUE INDEX workspace_one_active_build ON workspace_builds(workspace_id) WHERE job_status IN ('pending','running');
REVOKE ALL ON workspace_authority,workspace_templates,workspace_template_versions,workspace_workspaces,workspace_builds FROM PUBLIC;

-- Rename platform contracts without changing IDs, FK targets or original data.
ALTER TABLE users RENAME COLUMN coder_user_id TO workspace_owner_id;
ALTER TABLE users RENAME CONSTRAINT users_coder_user_id_key TO users_workspace_owner_id_key;
ALTER TABLE instances RENAME COLUMN coder_workspace_name TO workspace_name;
ALTER TABLE instances RENAME COLUMN coder_workspace_id TO workspace_id;
ALTER TABLE instances RENAME CONSTRAINT instances_coder_workspace_name_key TO instances_workspace_name_key;
ALTER TABLE instances RENAME CONSTRAINT instances_coder_workspace_id_key TO instances_workspace_id_key;

DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM approved_templates WHERE runtime_manifest ? 'coder_version' AND runtime_manifest ? 'workspace_source_version') THEN
  RAISE EXCEPTION 'ambiguous workspace source version';
 END IF;
END $$;
UPDATE approved_templates
 SET runtime_manifest=(runtime_manifest-'coder_version') || jsonb_build_object('workspace_source_version',runtime_manifest->'coder_version')
 WHERE runtime_manifest ? 'coder_version';
UPDATE operations SET error_code='WORKSPACE_UNAVAILABLE' WHERE error_code='CODER_UPSTREAM_UNAVAILABLE';
UPDATE instance_health SET reason_code='WORKSPACE_UNAVAILABLE' WHERE reason_code='CODER_UPSTREAM_UNAVAILABLE';

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

CREATE FUNCTION public.hc_identity_lock_provisioning(requested_org uuid,target_user uuid)
RETURNS TABLE(provider_account_id text,login_email text)
LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
    SELECT op.provider_account_id,op.login_email
      FROM public.identity_provisioning_operations op JOIN public.users u ON u.id=op.user_id
     WHERE op.org_id=requested_org AND op.user_id=target_user AND op.status='PENDING'
       AND u.identity_source='local' AND u.status='PENDING_IDENTITY'
     FOR UPDATE OF op
$$;
REVOKE ALL ON FUNCTION public.hc_identity_lock_provisioning(uuid,uuid) FROM PUBLIC;

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

-- External model connections remain immutable versions of the organization head.
ALTER TABLE model_configurations DROP CONSTRAINT model_configurations_provider_check;
ALTER TABLE model_configurations DROP CONSTRAINT model_configurations_protocol_check;
ALTER TABLE model_configurations ADD COLUMN manifest jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE model_configurations ADD CONSTRAINT model_connection_type CHECK (
 (provider='deepseek-official' AND protocol='deepseek-messages' AND manifest='{}'::jsonb) OR
 (provider='litellm' AND protocol='anthropic-messages' AND jsonb_typeof(manifest)='object'
  AND manifest ?& ARRAY['id','revision','digest','context_window','max_output_tokens','streaming','tool_use','response_models']
  AND manifest->>'id'=model AND manifest->>'digest' ~ '^sha256:[a-f0-9]{64}$'));
ALTER TABLE model_validations ADD COLUMN evidence jsonb NOT NULL DEFAULT '{}'::jsonb
 CHECK (jsonb_typeof(evidence)='object');
CREATE TABLE model_directories (
 config_id uuid NOT NULL,
 config_version bigint NOT NULL,
 checked_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL,
 status text NOT NULL CHECK(status IN ('HEALTHY','UNHEALTHY')),
 reason_code text NOT NULL DEFAULT '',
 items jsonb NOT NULL DEFAULT '[]'::jsonb CHECK(jsonb_typeof(items)='array' AND jsonb_array_length(items)<=100),
 PRIMARY KEY(config_id,config_version),
 FOREIGN KEY(config_id,config_version) REFERENCES model_configurations(id,version),
 CHECK(expires_at>checked_at)
);

-- Platform-owned provider/catalog metadata and private BYOK connections.
-- LiteLLM is a synchronized execution target, never the user-facing manager.
CREATE TABLE model_providers (
    id text PRIMARY KEY CHECK (id ~ '^[a-z][a-z0-9_-]{1,31}$'),
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 64),
    litellm_prefix text NOT NULL CHECK (litellm_prefix ~ '^[a-z][a-z0-9_]*$'),
    api_bases jsonb NOT NULL CHECK (jsonb_typeof(api_bases)='array' AND jsonb_array_length(api_bases) BETWEEN 1 AND 16),
    default_model text NOT NULL CHECK (length(default_model) BETWEEN 1 AND 128),
    parameters jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(parameters)='object'),
    enabled boolean NOT NULL DEFAULT true,
    version bigint NOT NULL DEFAULT 1 CHECK (version>0)
);
INSERT INTO model_providers(id,name,litellm_prefix,api_bases,default_model,parameters) VALUES
('openai','OpenAI','openai','["https://api.openai.com/v1"]','gpt-4.1','{}'),
('anthropic','Anthropic Claude','anthropic','["https://api.anthropic.com"]','claude-sonnet-4-5-20250929','{}'),
('gemini','Google Gemini','gemini','["https://generativelanguage.googleapis.com"]','gemini-2.5-flash','{"reasoning_effort":"none"}'),
('qwen','通义千问','dashscope','["https://dashscope.aliyuncs.com/compatible-mode/v1","https://dashscope-intl.aliyuncs.com/compatible-mode/v1"]','qwen-plus','{"extra_body":{"enable_thinking":false}}'),
('deepseek','DeepSeek','deepseek','["https://api.deepseek.com"]','deepseek-chat','{}'),
('moonshot','Kimi / Moonshot AI','moonshot','["https://api.moonshot.ai/v1","https://api.moonshot.cn/v1"]','kimi-k2.5','{"extra_body":{"thinking":{"type":"disabled"}}}'),
('zai','Z.AI / GLM','zai','["https://api.z.ai/api/paas/v4"]','glm-4.7','{"extra_body":{"thinking":{"type":"disabled"}}}');

CREATE TABLE model_catalog_entries (
    id uuid PRIMARY KEY,
    org_id uuid NOT NULL REFERENCES organizations(id),
    provider_id text NOT NULL REFERENCES model_providers(id),
    model_id text NOT NULL CHECK (model_id ~ '^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$'),
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    api_base text NOT NULL CHECK (api_base LIKE 'https://%' AND length(api_base)<=2048),
    response_models jsonb NOT NULL CHECK (jsonb_typeof(response_models)='array' AND jsonb_array_length(response_models) BETWEEN 1 AND 16),
    context_window bigint NOT NULL CHECK (context_window BETWEEN 1024 AND 10000000),
    max_output_tokens bigint NOT NULL CHECK (max_output_tokens BETWEEN 128 AND 1000000 AND max_output_tokens<=context_window),
    streaming boolean NOT NULL,
    tool_use boolean NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    version bigint NOT NULL DEFAULT 1 CHECK (version>0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(org_id,id), UNIQUE(org_id,provider_id,model_id,api_base)
);
CREATE FUNCTION public.hc_seed_model_catalog() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
    INSERT INTO public.model_catalog_entries(id,org_id,provider_id,model_id,name,api_base,response_models,context_window,max_output_tokens,streaming,tool_use)
    SELECT gen_random_uuid(),NEW.id,id,default_model,name,api_bases->>0,jsonb_build_array(default_model),32768,4096,true,true
    FROM public.model_providers WHERE enabled;
    RETURN NEW;
END;
$$;
REVOKE ALL ON FUNCTION public.hc_seed_model_catalog() FROM PUBLIC;
CREATE TRIGGER organizations_seed_model_catalog AFTER INSERT ON organizations FOR EACH ROW EXECUTE FUNCTION public.hc_seed_model_catalog();
INSERT INTO model_catalog_entries(id,org_id,provider_id,model_id,name,api_base,response_models,context_window,max_output_tokens,streaming,tool_use)
SELECT gen_random_uuid(),o.id,p.id,p.default_model,p.name,p.api_bases->>0,jsonb_build_array(p.default_model),32768,4096,true,true
FROM organizations o CROSS JOIN model_providers p WHERE p.enabled;

-- Keep the existing single organization head and its APIs intact; private
-- connections share the version/validation/runtime reference through a registry.
CREATE TABLE model_connection_registry (
    id uuid PRIMARY KEY,
    org_id uuid NOT NULL REFERENCES organizations(id),
    owner_id uuid,
    UNIQUE(org_id,id), UNIQUE(org_id,owner_id,id),
    FOREIGN KEY(org_id,owner_id) REFERENCES memberships(org_id,user_id)
);
INSERT INTO model_connection_registry(id,org_id) SELECT id,org_id FROM model_configuration_heads;
ALTER TABLE model_configurations DROP CONSTRAINT model_configurations_org_id_id_fkey;
ALTER TABLE model_configurations ADD CONSTRAINT model_configurations_registry_fk FOREIGN KEY(org_id,id) REFERENCES model_connection_registry(org_id,id);
CREATE FUNCTION public.hc_register_organization_model() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
    INSERT INTO public.model_connection_registry(id,org_id) VALUES(NEW.id,NEW.org_id);
    RETURN NEW;
END;
$$;
REVOKE ALL ON FUNCTION public.hc_register_organization_model() FROM PUBLIC;
CREATE TRIGGER model_heads_register BEFORE INSERT ON model_configuration_heads FOR EACH ROW EXECUTE FUNCTION public.hc_register_organization_model();

CREATE TABLE user_model_connections (
    id uuid PRIMARY KEY,
    org_id uuid NOT NULL,
    owner_id uuid NOT NULL,
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    current_version bigint NOT NULL DEFAULT 0 CHECK (current_version>=0),
    enabled boolean NOT NULL DEFAULT true,
    archived_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(org_id,owner_id,id),
    FOREIGN KEY(org_id,owner_id,id) REFERENCES model_connection_registry(org_id,owner_id,id),
    CHECK (archived_at IS NULL OR NOT enabled)
);
CREATE INDEX user_model_connections_owner ON user_model_connections(org_id,owner_id,created_at DESC,id DESC);
CREATE TABLE external_provider_secrets (
    id uuid PRIMARY KEY,
    org_id uuid NOT NULL,
    owner_id uuid NOT NULL,
    connection_id uuid NOT NULL,
    bound_version bigint NOT NULL CHECK (bound_version>0),
    key_id text NOT NULL,
    nonce bytea NOT NULL CHECK (octet_length(nonce)=12),
    ciphertext bytea NOT NULL CHECK (octet_length(ciphertext) BETWEEN 17 AND 4112),
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz,
    UNIQUE(org_id,owner_id,connection_id,id),
    FOREIGN KEY(org_id,owner_id,connection_id) REFERENCES user_model_connections(org_id,owner_id,id)
);
CREATE TABLE user_model_connection_versions (
    connection_id uuid NOT NULL,
    org_id uuid NOT NULL,
    owner_id uuid NOT NULL,
    version bigint NOT NULL CHECK (version>0),
    catalog_entry_id uuid NOT NULL,
    catalog_version bigint NOT NULL CHECK (catalog_version>0),
    provider_secret_id uuid NOT NULL,
    gateway_endpoint text NOT NULL CHECK (gateway_endpoint LIKE 'https://%' AND length(gateway_endpoint)<=2048),
    route_alias text NOT NULL UNIQUE CHECK (route_alias ~ '^hc-[a-f0-9]{32}-v[1-9][0-9]*$'),
    manifest jsonb NOT NULL CHECK (jsonb_typeof(manifest)='object'),
    sync_status text NOT NULL DEFAULT 'PENDING_SYNC' CHECK (sync_status IN ('PENDING_SYNC','SYNCING','UNCERTAIN','SYNCED','FAILED','REVOKING','REVOKED')),
    model_configuration_id uuid,
    model_configuration_version bigint,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(connection_id,version), UNIQUE(org_id,owner_id,connection_id,version),
    FOREIGN KEY(org_id,owner_id,connection_id) REFERENCES user_model_connections(org_id,owner_id,id),
    FOREIGN KEY(org_id,catalog_entry_id) REFERENCES model_catalog_entries(org_id,id),
    FOREIGN KEY(org_id,owner_id,connection_id,provider_secret_id) REFERENCES external_provider_secrets(org_id,owner_id,connection_id,id),
    FOREIGN KEY(org_id,model_configuration_id,model_configuration_version) REFERENCES model_configurations(org_id,id,version),
    CHECK ((model_configuration_id IS NULL)=(model_configuration_version IS NULL)),
    CHECK (model_configuration_id IS NULL OR (model_configuration_id=connection_id AND model_configuration_version=version)),
    CHECK (sync_status<>'SYNCED' OR model_configuration_id IS NOT NULL)
);
CREATE TABLE model_sync_operations (
    id uuid PRIMARY KEY,
    org_id uuid NOT NULL,
    owner_id uuid NOT NULL,
    connection_id uuid NOT NULL,
    connection_version bigint NOT NULL,
    action text NOT NULL CHECK (action IN ('UPSERT','REVOKE')),
    status text NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','RUNNING','UNCERTAIN','SUCCEEDED','FAILED')),
    remote_model_id text NOT NULL CHECK (length(remote_model_id) BETWEEN 1 AND 128),
    lease_token bigint NOT NULL DEFAULT 0 CHECK (lease_token>=0),
    lease_owner text,
    lease_expires_at timestamptz,
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 100),
    reason_code text NOT NULL DEFAULT '' CHECK (reason_code ~ '^[A-Z0-9_]{0,128}$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY(org_id,owner_id,connection_id,connection_version) REFERENCES user_model_connection_versions(org_id,owner_id,connection_id,version),
    UNIQUE(connection_id,connection_version,action)
);
CREATE INDEX model_sync_operations_pending ON model_sync_operations(created_at,id) WHERE status IN ('PENDING','RUNNING','UNCERTAIN');
CREATE TABLE user_model_defaults (
    org_id uuid NOT NULL,
    owner_id uuid NOT NULL,
    connection_id uuid NOT NULL,
    connection_version bigint NOT NULL,
    version bigint NOT NULL DEFAULT 1 CHECK (version>0),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(org_id,owner_id),
    FOREIGN KEY(org_id,owner_id,connection_id,connection_version) REFERENCES user_model_connection_versions(org_id,owner_id,connection_id,version)
);

INSERT INTO permissions(id,assignable,dependencies) VALUES
('personal-model:read',true,'{}'),
('personal-model:write',true,'{personal-model:read}'),
('personal-model:validate',true,'{personal-model:read}'),
('personal-model:revoke',true,'{personal-model:read}'),
('models:catalog:manage',false,'{configuration:read}');
INSERT INTO role_permissions(role_id,permission_id)
SELECT r.id,p.id FROM roles r CROSS JOIN permissions p
WHERE (r.code IN ('ADMIN','MEMBER') AND p.id LIKE 'personal-model:%') OR (r.code='ADMIN' AND p.id='models:catalog:manage');
CREATE OR REPLACE FUNCTION public.hc_seed_roles() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE admin_id uuid:=gen_random_uuid(); member_id uuid:=gen_random_uuid();
BEGIN
    INSERT INTO public.roles(id,org_id,code,name,builtin) VALUES
      (admin_id,NEW.id,'ADMIN','管理员',true),(member_id,NEW.id,'MEMBER','成员',true);
    INSERT INTO public.role_permissions SELECT admin_id,id FROM public.permissions;
    INSERT INTO public.role_permissions SELECT member_id,id FROM public.permissions
      WHERE id LIKE 'instances:%' OR id LIKE 'personal-model:%' OR id IN ('configuration:read','audit:read');
    RETURN NEW;
END;
$$;
UPDATE roles SET version=version+1,updated_at=clock_timestamp() WHERE code IN ('ADMIN','MEMBER');
UPDATE memberships SET auth_generation=auth_generation+1 WHERE role IN ('ADMIN','MEMBER');
UPDATE portal_sessions SET revoked_at=clock_timestamp() WHERE revoked_at IS NULL;
UPDATE access_sessions SET revoked_at=clock_timestamp() WHERE revoked_at IS NULL;

-- Registration must run only after the organization head was actually inserted.
-- BEFORE INSERT also fires for ON CONFLICT no-ops and can create orphan records
-- or reject a previously valid idempotent organization configuration save.
DROP TRIGGER model_heads_register ON model_configuration_heads;
CREATE TRIGGER model_heads_register AFTER INSERT ON model_configuration_heads
FOR EACH ROW EXECUTE FUNCTION public.hc_register_organization_model();

-- Keep a stable create receipt without persisting the submitted provider key.
ALTER TABLE user_model_connections ADD COLUMN create_key_sha256 bytea NOT NULL CHECK (octet_length(create_key_sha256)=32);
ALTER TABLE user_model_connections ADD COLUMN create_request_sha256 bytea NOT NULL CHECK (octet_length(create_request_sha256)=32);
ALTER TABLE user_model_connections ADD CONSTRAINT user_model_create_key UNIQUE(org_id,owner_id,create_key_sha256);

-- Persist the virtual credential before the first remote side effect so a
-- lost response never requires issuing a second unknown credential.
ALTER TABLE user_model_connection_versions ADD COLUMN runtime_credential_ref uuid;
ALTER TABLE user_model_connection_versions ADD CONSTRAINT personal_runtime_credential_fk
 FOREIGN KEY(org_id,runtime_credential_ref) REFERENCES secrets(org_id,id);
ALTER TABLE model_sync_operations ADD COLUMN retry_at timestamptz NOT NULL DEFAULT now();

CREATE FUNCTION public.hc_model_available(model_org uuid, model_owner uuid, config_id uuid, config_version bigint) RETURNS boolean
LANGUAGE sql STABLE SET search_path=pg_catalog,public AS $$
 SELECT EXISTS(SELECT 1 FROM public.model_connection_registry r WHERE r.org_id=model_org AND r.id=config_id
 AND (r.owner_id IS NULL OR (r.owner_id=model_owner AND EXISTS(
   SELECT 1 FROM public.user_model_connections h JOIN public.user_model_connection_versions v
   ON v.connection_id=h.id AND v.org_id=h.org_id AND v.owner_id=h.owner_id
   WHERE h.id=r.id AND h.enabled AND h.archived_at IS NULL AND v.version=config_version AND v.sync_status='SYNCED'
   AND v.model_configuration_id=config_id AND v.model_configuration_version=config_version))));
$$;
REVOKE ALL ON FUNCTION public.hc_model_available(uuid,uuid,uuid,bigint) FROM PUBLIC;

CREATE FUNCTION public.hc_check_instance_model_owner() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,public AS $$
BEGIN
 IF TG_OP='UPDATE' AND NEW.org_id=OLD.org_id AND NEW.owner_id=OLD.owner_id AND NEW.model_config_id=OLD.model_config_id AND NEW.model_config_version=OLD.model_config_version THEN RETURN NEW; END IF;
 IF NOT public.hc_model_available(NEW.org_id,NEW.owner_id,NEW.model_config_id,NEW.model_config_version) THEN
   RAISE EXCEPTION 'model connection is not available to instance owner' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER instances_model_owner BEFORE INSERT OR UPDATE OF model_config_id,model_config_version,owner_id,org_id ON instances
 FOR EACH ROW EXECUTE FUNCTION public.hc_check_instance_model_owner();

CREATE FUNCTION public.hc_check_organization_model_owner() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,public AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM public.model_connection_registry WHERE org_id=NEW.org_id AND id=NEW.model_config_id AND owner_id IS NULL) THEN
   RAISE EXCEPTION 'organization defaults require an organization model' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER defaults_model_owner BEFORE INSERT OR UPDATE OF model_config_id,org_id ON organization_defaults
 FOR EACH ROW EXECUTE FUNCTION public.hc_check_organization_model_owner();

CREATE FUNCTION public.hc_personal_model_authority(requested_org uuid, requested_owner uuid)
RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE active boolean;
BEGIN
 SELECT o.status='ACTIVE' AND u.status='ACTIVE' AND m.status='ACTIVE'
 INTO active FROM public.memberships m JOIN public.users u ON u.id=m.user_id JOIN public.organizations o ON o.id=m.org_id
 WHERE m.org_id=requested_org AND m.user_id=requested_owner FOR UPDATE OF m;
 IF NOT FOUND THEN RETURN false; END IF;
 PERFORM id FROM public.model_configuration_heads WHERE org_id=requested_org FOR UPDATE;
 RETURN active;
END;
$$;
REVOKE ALL ON FUNCTION public.hc_personal_model_authority(uuid,uuid) FROM PUBLIC;

CREATE OR REPLACE FUNCTION public.hc_model_available(model_org uuid, model_owner uuid, config_id uuid, config_version bigint) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT EXISTS(SELECT 1 FROM public.model_connection_registry r WHERE r.org_id=model_org AND r.id=config_id
 AND (r.owner_id IS NULL OR (r.owner_id=model_owner AND EXISTS(
   SELECT 1 FROM public.user_model_connections h JOIN public.user_model_connection_versions v ON v.connection_id=h.id AND v.org_id=h.org_id AND v.owner_id=h.owner_id
   JOIN public.model_catalog_entries e ON e.id=v.catalog_entry_id AND e.org_id=v.org_id JOIN public.model_providers p ON p.id=e.provider_id
   WHERE h.id=r.id AND h.enabled AND h.archived_at IS NULL AND v.version=config_version AND v.sync_status='SYNCED'
   AND e.enabled AND p.enabled AND v.model_configuration_id=config_id AND v.model_configuration_version=config_version))));
$$;

ALTER TABLE user_model_defaults ALTER COLUMN connection_id DROP NOT NULL;
ALTER TABLE user_model_defaults ALTER COLUMN connection_version DROP NOT NULL;
ALTER TABLE user_model_defaults ADD CONSTRAINT personal_default_binding_pair CHECK ((connection_id IS NULL)=(connection_version IS NULL));

-- Worker compensation uses canonical authority and queues remote removal.
-- Callers cannot use this function to revoke a healthy, active owner's model.
CREATE FUNCTION public.hc_reconcile_personal_model(requested_connection uuid) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE h public.user_model_connections%ROWTYPE; active boolean; affected bigint[];
BEGIN
 SELECT * INTO h FROM public.user_model_connections WHERE id=requested_connection;
 IF NOT FOUND THEN RETURN false; END IF;
 SELECT o.status='ACTIVE' AND u.status='ACTIVE' AND m.status='ACTIVE' INTO active
 FROM public.memberships m JOIN public.users u ON u.id=m.user_id JOIN public.organizations o ON o.id=m.org_id
 WHERE m.org_id=h.org_id AND m.user_id=h.owner_id FOR UPDATE OF m;
 PERFORM id FROM public.model_configuration_heads WHERE org_id=h.org_id FOR UPDATE;
 SELECT * INTO h FROM public.user_model_connections WHERE id=requested_connection FOR UPDATE;
 SELECT array_agg(v.version ORDER BY v.version) INTO affected
 FROM public.user_model_connection_versions v JOIN public.model_catalog_entries e ON e.id=v.catalog_entry_id AND e.org_id=v.org_id
 JOIN public.model_providers p ON p.id=e.provider_id
 WHERE v.connection_id=h.id AND v.sync_status IN ('PENDING_SYNC','SYNCING','UNCERTAIN','SYNCED','FAILED')
 AND (NOT COALESCE(active,false) OR NOT e.enabled OR NOT p.enabled);
 IF affected IS NULL THEN RETURN false; END IF;
 UPDATE public.user_model_connections SET enabled=false,updated_at=clock_timestamp() WHERE id=h.id AND current_version=ANY(affected);
 UPDATE public.external_provider_secrets SET revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE connection_id=h.id AND bound_version=ANY(affected);
 UPDATE public.secrets SET revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE id IN (
   SELECT runtime_credential_ref FROM public.user_model_connection_versions WHERE connection_id=h.id AND version=ANY(affected));
 UPDATE public.model_configurations SET status='REVOKED',revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE id=h.id AND version=ANY(affected);
 UPDATE public.model_validations SET status='UNHEALTHY',reason_code='AUTH_REVOKED',lease_expires_at=NULL,lease_token=lease_token+1 WHERE config_id=h.id AND config_version=ANY(affected);
 UPDATE public.instances SET desired_state=CASE WHEN desired_state='DELETED' THEN desired_state ELSE 'STOPPED' END,
   access_generation=access_generation+1,version=version+1,updated_at=clock_timestamp()
   WHERE model_config_id=h.id AND model_config_version=ANY(affected) AND deleted_at IS NULL;
 UPDATE public.access_routes SET enabled=false WHERE instance_id IN (SELECT id FROM public.instances WHERE model_config_id=h.id AND model_config_version=ANY(affected));
 UPDATE public.access_sessions SET revoked_at=clock_timestamp() WHERE revoked_at IS NULL AND instance_id IN (
   SELECT id FROM public.instances WHERE model_config_id=h.id AND model_config_version=ANY(affected));
 UPDATE public.model_sync_operations SET status='FAILED',reason_code='AUTH_REVOKED',lease_token=lease_token+1
   WHERE connection_id=h.id AND connection_version=ANY(affected) AND action='UPSERT' AND status IN ('PENDING','UNCERTAIN');
 UPDATE public.user_model_connection_versions SET sync_status='REVOKING' WHERE connection_id=h.id AND version=ANY(affected);
 INSERT INTO public.model_sync_operations(id,org_id,owner_id,connection_id,connection_version,action,remote_model_id)
   SELECT gen_random_uuid(),org_id,owner_id,connection_id,connection_version,'REVOKE',remote_model_id FROM public.model_sync_operations
   WHERE connection_id=h.id AND connection_version=ANY(affected) AND action='UPSERT' ON CONFLICT(connection_id,connection_version,action) DO NOTHING;
 INSERT INTO public.audit_events(id,org_id,owner_id,resource_id,action,outcome,trace_id)
   VALUES(gen_random_uuid(),h.org_id,h.owner_id,h.id,'PERSONAL_MODEL_AUTH_REVOKED','SUCCEEDED',gen_random_uuid()::text);
 RETURN true;
END;
$$;
REVOKE ALL ON FUNCTION public.hc_reconcile_personal_model(uuid) FROM PUBLIC;

CREATE OR REPLACE FUNCTION public.hc_personal_model_authority(requested_org uuid, requested_owner uuid)
RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE active boolean;
BEGIN
 SELECT o.status='ACTIVE' AND u.status='ACTIVE' AND m.status='ACTIVE'
 INTO active FROM public.memberships m JOIN public.users u ON u.id=m.user_id JOIN public.organizations o ON o.id=m.org_id
 WHERE m.org_id=requested_org AND m.user_id=requested_owner FOR UPDATE OF m;
 IF NOT FOUND THEN RETURN false; END IF;
 PERFORM id FROM public.model_configuration_heads WHERE org_id=requested_org FOR UPDATE;
 RETURN active AND public.hc_has_permission(requested_org,requested_owner,'personal-model:write');
END;
$$;

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

-- Read-only remote verification is independent of synchronization and paid
-- inference evidence. An upgrade never fabricates a successful remote check.
CREATE TABLE model_gateway_checks (
    connection_id uuid NOT NULL,
    connection_version bigint NOT NULL,
    org_id uuid NOT NULL,
    owner_id uuid NOT NULL,
    status text NOT NULL DEFAULT 'UNKNOWN' CHECK (status IN ('UNKNOWN','HEALTHY','UNHEALTHY')),
    checked_at timestamptz,
    expires_at timestamptz,
    reason_code text NOT NULL DEFAULT 'MODEL_GATEWAY_UNVERIFIED' CHECK (reason_code ~ '^[A-Z0-9_]{0,128}$'),
    attempted_at timestamptz,
    next_check_at timestamptz NOT NULL DEFAULT now(),
    lease_token bigint NOT NULL DEFAULT 0 CHECK (lease_token>=0),
    lease_expires_at timestamptz,
    PRIMARY KEY(connection_id,connection_version),
    FOREIGN KEY(org_id,owner_id,connection_id,connection_version)
        REFERENCES user_model_connection_versions(org_id,owner_id,connection_id,version) ON DELETE CASCADE,
    CHECK (status<>'HEALTHY' OR (checked_at IS NOT NULL AND expires_at IS NOT NULL AND expires_at>checked_at AND expires_at<=checked_at+interval '60 seconds' AND reason_code=''))
);
CREATE INDEX model_gateway_checks_due ON model_gateway_checks(next_check_at,connection_id,connection_version);
INSERT INTO model_gateway_checks(connection_id,connection_version,org_id,owner_id)
SELECT connection_id,version,org_id,owner_id FROM user_model_connection_versions WHERE sync_status='SYNCED';

CREATE OR REPLACE FUNCTION public.hc_model_available(model_org uuid, model_owner uuid, config_id uuid, config_version bigint) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT EXISTS(SELECT 1 FROM public.model_connection_registry r WHERE r.org_id=model_org AND r.id=config_id
 AND (r.owner_id IS NULL OR (r.owner_id=model_owner AND EXISTS(
   SELECT 1 FROM public.user_model_connections h JOIN public.user_model_connection_versions v ON v.connection_id=h.id AND v.org_id=h.org_id AND v.owner_id=h.owner_id
   JOIN public.model_catalog_entries e ON e.id=v.catalog_entry_id AND e.org_id=v.org_id JOIN public.model_providers p ON p.id=e.provider_id
   JOIN public.model_gateway_checks g ON g.connection_id=v.connection_id AND g.connection_version=v.version AND g.org_id=v.org_id AND g.owner_id=v.owner_id
   JOIN public.memberships member ON member.org_id=h.org_id AND member.user_id=h.owner_id
   JOIN public.users u ON u.id=member.user_id JOIN public.organizations o ON o.id=member.org_id
   WHERE h.id=r.id AND h.enabled AND h.archived_at IS NULL AND v.version=config_version AND v.sync_status='SYNCED'
   AND e.enabled AND p.enabled AND v.model_configuration_id=config_id AND v.model_configuration_version=config_version
   AND member.status='ACTIVE' AND u.status='ACTIVE' AND o.status='ACTIVE' AND public.hc_has_permission(h.org_id,h.owner_id,'personal-model:write')
   AND g.status='HEALTHY' AND g.checked_at<=clock_timestamp() AND g.expires_at>clock_timestamp()))));
$$;
REVOKE ALL ON model_gateway_checks FROM PUBLIC;

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

-- The platform Go model gateway replaces the external model proxy. Older
-- migration checksums remain unchanged; active metadata uses platform names.
ALTER TABLE model_providers RENAME COLUMN litellm_prefix TO adapter_prefix;
ALTER TABLE model_configurations DROP CONSTRAINT model_connection_type;
UPDATE model_configurations SET provider='platform-models' WHERE provider='litellm';
ALTER TABLE model_configurations ADD CONSTRAINT model_connection_type CHECK (
 (provider='deepseek-official' AND protocol='deepseek-messages' AND manifest='{}'::jsonb) OR
 (provider='platform-models' AND protocol='anthropic-messages' AND jsonb_typeof(manifest)='object'
  AND manifest ?& ARRAY['id','response_models','context_window','max_output_tokens','streaming','tool_use','revision','digest']
  AND manifest->>'id'=model AND manifest->>'digest' ~ '^sha256:[a-f0-9]{64}$'));

CREATE TABLE model_gateway_routes (
 id uuid PRIMARY KEY,
 org_id uuid NOT NULL,
 owner_id uuid NOT NULL,
 connection_id uuid NOT NULL,
 connection_version bigint NOT NULL,
 alias text NOT NULL UNIQUE CHECK(alias ~ '^hc-[a-f0-9]{32}-v[1-9][0-9]*$'),
 parameters jsonb NOT NULL CHECK(jsonb_typeof(parameters)='object' AND NOT parameters ? 'api_key'),
 enabled boolean NOT NULL DEFAULT true,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(connection_id,connection_version),
 FOREIGN KEY(org_id,owner_id,connection_id,connection_version) REFERENCES user_model_connection_versions(org_id,owner_id,connection_id,version)
);
CREATE TABLE model_gateway_keys (
 key_hash bytea PRIMARY KEY CHECK(octet_length(key_hash)=32),
 route_id uuid NOT NULL UNIQUE REFERENCES model_gateway_routes(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 revoked_at timestamptz,
 window_started timestamptz NOT NULL DEFAULT now(),
 requests integer NOT NULL DEFAULT 0 CHECK(requests BETWEEN 0 AND 60)
);
REVOKE ALL ON model_gateway_routes,model_gateway_keys FROM PUBLIC;

-- Existing personal versions must be registered with the native boundary and
-- capability tested again. Old inference evidence cannot certify this adapter.
UPDATE model_validations SET status='UNKNOWN',reason_code='VALIDATION_REQUIRED',expires_at=NULL,lease_token=lease_token+1,lease_expires_at=NULL
WHERE EXISTS(SELECT 1 FROM user_model_connection_versions v WHERE v.connection_id=config_id AND v.version=config_version AND v.sync_status='SYNCED');
UPDATE model_sync_operations SET status='PENDING',reason_code='',retry_at=clock_timestamp(),lease_token=lease_token+1,lease_expires_at=NULL
WHERE action='UPSERT' AND EXISTS(SELECT 1 FROM user_model_connection_versions v WHERE v.connection_id=model_sync_operations.connection_id AND v.version=connection_version AND v.sync_status='SYNCED');
UPDATE user_model_connection_versions SET sync_status='PENDING_SYNC' WHERE sync_status='SYNCED';
UPDATE model_gateway_checks SET status='UNKNOWN',reason_code='MODEL_GATEWAY_UNVERIFIED',expires_at=NULL,lease_token=lease_token+1,lease_expires_at=NULL,next_check_at=clock_timestamp();


-- Personal endpoint/model selections are isolated from the organization catalog.
ALTER TABLE model_catalog_entries ADD COLUMN owner_id uuid;
ALTER TABLE model_catalog_entries ADD CONSTRAINT model_catalog_owner_fk FOREIGN KEY(org_id,owner_id) REFERENCES memberships(org_id,user_id);
ALTER TABLE model_catalog_entries DROP CONSTRAINT model_catalog_entries_org_id_provider_id_model_id_api_base_key;
CREATE UNIQUE INDEX model_catalog_shared_key ON model_catalog_entries(org_id,provider_id,model_id,api_base) WHERE owner_id IS NULL;
CREATE INDEX model_catalog_personal_key ON model_catalog_entries(org_id,owner_id,provider_id,model_id,api_base) WHERE owner_id IS NOT NULL;
ALTER TABLE model_catalog_entries DROP CONSTRAINT model_catalog_entries_api_base_check;
ALTER TABLE model_catalog_entries ADD CONSTRAINT model_catalog_api_base_check CHECK(length(api_base)<=2048 AND (api_base LIKE 'https://%' OR (provider_id IN ('custom-openai','custom-anthropic') AND api_base LIKE 'http://%')));
INSERT INTO model_providers(id,name,adapter_prefix,api_bases,default_model) VALUES
('custom-openai','自定义 / 本地 · OpenAI','custom_openai','["http://localhost:11434/v1"]','user-defined'),
('custom-anthropic','自定义 / 本地 · Anthropic','custom_anthropic','["http://localhost:11434"]','user-defined');
-- Current compatible defaults; per-user discovery reads the live official API.
UPDATE model_providers SET default_model='deepseek-flash',parameters='{"extra_body":{"thinking":{"type":"disabled"}}}',version=version+1 WHERE id='deepseek';
UPDATE model_providers SET default_model='kimi-k2.6',version=version+1 WHERE id='moonshot';
CREATE OR REPLACE FUNCTION public.hc_seed_model_catalog() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 INSERT INTO public.model_catalog_entries(id,org_id,provider_id,model_id,name,api_base,response_models,context_window,max_output_tokens,streaming,tool_use)
 SELECT gen_random_uuid(),NEW.id,id,default_model,name,api_bases->>0,jsonb_build_array(default_model),32768,4096,true,true
 FROM public.model_providers WHERE enabled AND id NOT IN ('custom-openai','custom-anthropic');
 RETURN NEW;
END;
$$;
INSERT INTO model_catalog_entries(id,org_id,provider_id,model_id,name,api_base,response_models,context_window,max_output_tokens,streaming,tool_use)
SELECT gen_random_uuid(),o.id,p.id,p.default_model,p.name,p.api_bases->>0,jsonb_build_array(p.default_model),32768,4096,true,true
FROM organizations o CROSS JOIN model_providers p WHERE p.id IN ('deepseek','moonshot')
ON CONFLICT(org_id,provider_id,model_id,api_base) WHERE owner_id IS NULL DO NOTHING;
-- Retired defaults without frozen references no longer appear in the picker.
UPDATE model_catalog_entries e SET enabled=false,version=version+1,updated_at=clock_timestamp()
WHERE ((provider_id='deepseek' AND model_id IN ('deepseek-chat','deepseek-reasoner')) OR (provider_id='moonshot' AND model_id='kimi-k2.5'))
AND NOT EXISTS(SELECT 1 FROM user_model_connection_versions v WHERE v.catalog_entry_id=e.id);

CREATE TABLE personal_provider_connections (
 id uuid PRIMARY KEY,
 org_id uuid NOT NULL,
 owner_id uuid NOT NULL,
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 100),
 provider_id text NOT NULL REFERENCES model_providers(id),
 api_base text NOT NULL CHECK(length(api_base) BETWEEN 1 AND 2048),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 status text NOT NULL DEFAULT 'ACTIVE' CHECK(status IN ('ACTIVE','DELETING','DELETED')),
 create_key_sha256 bytea CHECK(create_key_sha256 IS NULL OR octet_length(create_key_sha256)=32),
 create_request_sha256 bytea CHECK(create_request_sha256 IS NULL OR octet_length(create_request_sha256)=32),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(org_id,owner_id,id),
 UNIQUE(org_id,owner_id,create_key_sha256),
 FOREIGN KEY(org_id,owner_id) REFERENCES memberships(org_id,user_id)
);
ALTER TABLE user_model_connections ADD COLUMN provider_connection_id uuid;
ALTER TABLE user_model_connections ADD COLUMN provider_retired boolean NOT NULL DEFAULT false;
ALTER TABLE user_model_connections ADD CONSTRAINT personal_provider_owner_fk FOREIGN KEY(org_id,owner_id,provider_connection_id) REFERENCES personal_provider_connections(org_id,owner_id,id);
CREATE INDEX personal_provider_models ON user_model_connections(provider_connection_id) WHERE archived_at IS NULL;
-- Existing connections retain their own credentials and immutable runtime IDs.
-- They are never merged merely because names or endpoint roots happen to match.
INSERT INTO personal_provider_connections(id,org_id,owner_id,name,provider_id,api_base)
SELECT h.id,h.org_id,h.owner_id,h.name,v.manifest->'catalog'->>'provider',v.manifest->'catalog'->>'api_base'
FROM user_model_connections h JOIN user_model_connection_versions v ON v.connection_id=h.id AND v.version=h.current_version
WHERE h.archived_at IS NULL;
UPDATE user_model_connections h SET provider_connection_id=h.id WHERE EXISTS(SELECT 1 FROM personal_provider_connections p WHERE p.id=h.id);
-- API cannot SELECT the provider credential ciphertext directly. This bounded
-- function returns only one active credential of the caller's own connection,
-- after rechecking the real Portal session and current database permission.
CREATE FUNCTION public.hc_personal_provider_credential(requested_org uuid, requested_owner uuid, requested_session uuid, requested_generation bigint, requested_provider uuid)
RETURNS TABLE(connection_id uuid, secret_id uuid, bound_version bigint,key_id text,nonce bytea,ciphertext bytea)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 IF public.hc_access_lock_principal(requested_org,requested_owner,requested_session,requested_generation) IS NULL
 OR NOT public.hc_has_permission(requested_org,requested_owner,'personal-model:write') THEN RETURN; END IF;
 RETURN QUERY SELECT h.id,s.id,s.bound_version,s.key_id,s.nonce,s.ciphertext
 FROM public.personal_provider_connections p JOIN public.user_model_connections h ON h.provider_connection_id=p.id AND h.org_id=p.org_id AND h.owner_id=p.owner_id
 JOIN public.user_model_connection_versions v ON v.connection_id=h.id AND v.version=h.current_version
 JOIN public.external_provider_secrets s ON s.id=v.provider_secret_id AND s.connection_id=h.id AND s.org_id=h.org_id AND s.owner_id=h.owner_id
 WHERE p.id=requested_provider AND p.org_id=requested_org AND p.owner_id=requested_owner AND p.status='ACTIVE'
 AND h.enabled AND NOT h.provider_retired AND h.archived_at IS NULL AND s.revoked_at IS NULL
 ORDER BY h.created_at,h.id LIMIT 1;
END;
$$;
REVOKE ALL ON FUNCTION public.hc_personal_provider_credential(uuid,uuid,uuid,bigint,uuid) FROM PUBLIC;

ALTER TABLE personal_provider_connections RENAME TO personal_provider_accounts;
ALTER TABLE user_model_connections RENAME COLUMN provider_connection_id TO provider_account_id;
ALTER TABLE user_model_connections RENAME CONSTRAINT personal_provider_owner_fk TO personal_provider_account_owner_fk;
ALTER INDEX personal_provider_models RENAME TO personal_provider_account_models;
DROP FUNCTION public.hc_personal_provider_credential(uuid,uuid,uuid,bigint,uuid);
CREATE FUNCTION public.hc_personal_provider_account_credential(requested_org uuid, requested_owner uuid, requested_session uuid, requested_generation bigint, requested_provider uuid)
RETURNS TABLE(connection_id uuid, secret_id uuid, bound_version bigint,key_id text,nonce bytea,ciphertext bytea)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 IF public.hc_access_lock_principal(requested_org,requested_owner,requested_session,requested_generation) IS NULL
 OR NOT public.hc_has_permission(requested_org,requested_owner,'personal-model:write') THEN RETURN; END IF;
 RETURN QUERY SELECT h.id,s.id,s.bound_version,s.key_id,s.nonce,s.ciphertext
 FROM public.personal_provider_accounts p JOIN public.user_model_connections h ON h.provider_account_id=p.id AND h.org_id=p.org_id AND h.owner_id=p.owner_id
 JOIN public.user_model_connection_versions v ON v.connection_id=h.id AND v.version=h.current_version
 JOIN public.external_provider_secrets s ON s.id=v.provider_secret_id AND s.connection_id=h.id AND s.org_id=h.org_id AND s.owner_id=h.owner_id
 WHERE p.id=requested_provider AND p.org_id=requested_org AND p.owner_id=requested_owner AND p.status='ACTIVE'
 AND h.enabled AND NOT h.provider_retired AND h.archived_at IS NULL AND s.revoked_at IS NULL
 ORDER BY h.created_at,h.id LIMIT 1;
END;
$$;
REVOKE ALL ON FUNCTION public.hc_personal_provider_account_credential(uuid,uuid,uuid,bigint,uuid) FROM PUBLIC;

-- Resource sizes are chosen from approved immutable tuples, independently of
-- model credentials. Existing instance snapshots and quotas are unchanged.
DROP INDEX approved_specs_single_enabled;
CREATE INDEX approved_specs_enabled ON approved_specs(cpu_millicores,memory_bytes,storage_bytes) WHERE enabled;
INSERT INTO approved_specs(id,cpu_millicores,memory_bytes,storage_bytes,enabled)
SELECT gen_random_uuid(),cpu,memory,10737418240,true
FROM (VALUES(1000::bigint),(2000::bigint)) cpus(cpu)
CROSS JOIN (VALUES(2147483648::bigint),(4294967296::bigint)) memories(memory)
WHERE EXISTS(SELECT 1 FROM approved_specs WHERE enabled AND cpu_millicores=2000 AND memory_bytes=4294967296 AND storage_bytes=10737418240)
AND NOT EXISTS(SELECT 1 FROM approved_specs WHERE enabled AND cpu_millicores=cpu AND memory_bytes=memory AND storage_bytes=10737418240);

-- Preserve authority serialization without blocking foreign-key reference locks.
-- Otherwise quota audit insertion can deadlock against a member creating an instance.
CREATE OR REPLACE FUNCTION public.hc_access_lock_principal(requested_org uuid, requested_user uuid, requested_session uuid, requested_generation bigint)
RETURNS text LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog, public AS $$
DECLARE current_role text;
BEGIN
    PERFORM user_id FROM public.memberships WHERE org_id=requested_org AND user_id=requested_user FOR NO KEY UPDATE;
    PERFORM id FROM public.portal_sessions WHERE id=requested_session AND org_id=requested_org AND user_id=requested_user FOR NO KEY UPDATE;
    SELECT m.role INTO current_role FROM public.memberships m
    JOIN public.users u ON u.id=m.user_id JOIN public.organizations o ON o.id=m.org_id
    JOIN public.portal_sessions ps ON ps.org_id=m.org_id AND ps.user_id=m.user_id
    WHERE m.org_id=requested_org AND m.user_id=requested_user AND ps.id=requested_session
    AND m.status='ACTIVE' AND u.status='ACTIVE' AND o.status='ACTIVE'
    AND m.auth_generation=requested_generation AND ps.auth_generation=m.auth_generation
    AND ps.revoked_at IS NULL AND ps.expires_at>clock_timestamp() AND ps.last_seen_at>clock_timestamp()-interval '30 minutes';
    RETURN current_role;
END;
$$;
REVOKE ALL ON FUNCTION public.hc_access_lock_principal(uuid,uuid,uuid,bigint) FROM PUBLIC;


-- Defaults belong to an owner-private provider account. Store the stable model
-- identity, so credential rotation uses its current version on the next create.
ALTER TABLE personal_provider_accounts ADD COLUMN default_model_id uuid;
ALTER TABLE personal_provider_accounts ADD CONSTRAINT provider_default_owner_fk
 FOREIGN KEY(org_id,owner_id,default_model_id) REFERENCES user_model_connections(org_id,owner_id,id);
UPDATE personal_provider_accounts g SET default_model_id=COALESCE(
 (SELECT h.id FROM user_model_defaults d JOIN user_model_connections h ON h.id=d.connection_id
  WHERE d.org_id=g.org_id AND d.owner_id=g.owner_id AND h.provider_account_id=g.id AND h.archived_at IS NULL AND h.enabled AND NOT h.provider_retired),
 (SELECT h.id FROM user_model_connections h WHERE h.provider_account_id=g.id AND h.org_id=g.org_id AND h.owner_id=g.owner_id
  AND h.archived_at IS NULL AND h.enabled AND NOT h.provider_retired ORDER BY h.created_at,h.id LIMIT 1)),
 version=version+1,updated_at=clock_timestamp() WHERE g.status='ACTIVE';

-- Instance names are display labels; runtime identifiers remain UUID-derived.
ALTER TABLE instances DROP CONSTRAINT instances_name_check;
ALTER TABLE instances ADD CONSTRAINT instances_name_check
 CHECK (char_length(name) BETWEEN 1 AND 32 AND name ~ '^[[:alnum:]_-]+$');

-- Runtime defaults (migration 34).
-- Runtime defaults can be initialized before any model account exists.
ALTER TABLE organization_defaults ALTER COLUMN model_config_id DROP NOT NULL;
ALTER TABLE organization_defaults ALTER COLUMN model_config_version DROP NOT NULL;
ALTER TABLE organization_defaults ADD CONSTRAINT organization_defaults_model_pair
    CHECK ((model_config_id IS NULL) = (model_config_version IS NULL));
ALTER TABLE organization_defaults ADD COLUMN retention_days integer NOT NULL DEFAULT 7
    CHECK (retention_days BETWEEN 1 AND 365);

CREATE OR REPLACE FUNCTION public.hc_check_organization_model_owner() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,public AS $$
BEGIN
 IF NEW.model_config_id IS NULL AND NEW.model_config_version IS NULL THEN RETURN NEW; END IF;
 IF NOT EXISTS(SELECT 1 FROM public.model_connection_registry WHERE org_id=NEW.org_id AND id=NEW.model_config_id AND owner_id IS NULL) THEN
   RAISE EXCEPTION 'organization defaults require an organization model' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;

-- Migration 35: all catalog permissions are available to custom roles.
UPDATE permissions
SET assignable = true
WHERE id IN (
    'users:read', 'users:manage', 'roles:read', 'roles:manage',
    'instances:read', 'instances:create', 'instances:start', 'instances:stop',
    'instances:delete', 'instances:configure', 'instances:access', 'instances:retry',
    'configuration:read', 'personal-model:read', 'personal-model:write',
    'personal-model:validate', 'personal-model:revoke', 'models:catalog:manage',
    'model:write', 'model:validate', 'model:revoke', 'defaults:write',
    'audit:read', 'audit:organization'
);

-- Migration 36: keep OpenAI Responses protocol separately frozen per account.
ALTER TABLE model_catalog_entries DROP CONSTRAINT model_catalog_api_base_check;
ALTER TABLE model_catalog_entries ADD CONSTRAINT model_catalog_api_base_check
 CHECK(length(api_base)<=2048 AND (api_base LIKE 'https://%' OR (provider_id IN ('custom-openai','custom-openai-responses','custom-anthropic') AND api_base LIKE 'http://%')));
INSERT INTO model_providers(id,name,adapter_prefix,api_bases,default_model) VALUES
('custom-openai-responses','自定义 / 本地 · OpenAI Responses','custom_openai_responses','["http://localhost:11434/v1"]','user-defined');
CREATE OR REPLACE FUNCTION public.hc_seed_model_catalog() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 INSERT INTO public.model_catalog_entries(id,org_id,provider_id,model_id,name,api_base,response_models,context_window,max_output_tokens,streaming,tool_use)
 SELECT gen_random_uuid(),NEW.id,id,default_model,name,api_bases->>0,jsonb_build_array(default_model),32768,4096,true,true
 FROM public.model_providers WHERE enabled AND id NOT IN ('custom-openai','custom-openai-responses','custom-anthropic');
 RETURN NEW;
END;
$$;

-- Migration 42: expand approved CPU and memory choices.
-- Expand compute choices for each already approved storage capacity.
-- Existing snapshots, defaults and user/system quotas remain unchanged.
INSERT INTO approved_specs(id,cpu_millicores,memory_bytes,storage_bytes,enabled)
SELECT gen_random_uuid(),cpu,memory,storage,true
FROM (VALUES(1000::bigint),(2000::bigint),(4000::bigint),(8000::bigint),(16000::bigint)) cpus(cpu)
CROSS JOIN (VALUES(2147483648::bigint),(4294967296::bigint),(8589934592::bigint),(17179869184::bigint),(34359738368::bigint),(68719476736::bigint)) memories(memory)
CROSS JOIN (
    SELECT DISTINCT storage_bytes AS storage FROM approved_specs
    WHERE enabled AND cpu_millicores IN (1000,2000) AND memory_bytes IN (2147483648,4294967296)
      AND storage_bytes BETWEEN 1073741824 AND 107374182400 AND storage_bytes%1073741824=0
) capacities
WHERE NOT EXISTS(SELECT 1 FROM approved_specs WHERE enabled AND cpu_millicores=cpu AND memory_bytes=memory AND storage_bytes=storage);

ALTER TABLE storage_assets
  ADD COLUMN usage_used_bytes bigint CHECK (usage_used_bytes >= 0),
  ADD COLUMN usage_available_bytes bigint CHECK (usage_available_bytes >= 0),
  ADD COLUMN usage_sampled_at timestamptz,
  ADD COLUMN usage_attempted_at timestamptz,
  ADD COLUMN usage_error boolean NOT NULL DEFAULT false,
  ADD CONSTRAINT storage_usage_observation CHECK (
    (usage_used_bytes IS NULL AND usage_available_bytes IS NULL AND usage_sampled_at IS NULL)
    OR (usage_used_bytes IS NOT NULL AND usage_available_bytes IS NOT NULL AND usage_sampled_at IS NOT NULL
        AND usage_used_bytes + usage_available_bytes <= capacity_bytes));

CREATE INDEX storage_assets_owner_list ON storage_assets(org_id,owner_id,id);
CREATE TABLE storage_backups (
  id uuid PRIMARY KEY,
  org_id uuid NOT NULL,
  owner_id uuid NOT NULL,
  instance_id uuid NOT NULL,
  asset_id uuid NOT NULL,
  capacity_bytes bigint NOT NULL CHECK(capacity_bytes>0),
  template_version uuid NOT NULL,
  image_digest text NOT NULL,
  status text NOT NULL CHECK(status IN ('RUNNING','SUCCEEDED','FAILED','UNCERTAIN','PRUNE_PENDING','PRUNED')),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  completed_at timestamptz,
  verified_at timestamptz,
  expires_at timestamptz,
  backup_sha256 text CHECK(backup_sha256 ~ '^[a-f0-9]{64}$'),
  set_id uuid,
  archive_path text NOT NULL,
  FOREIGN KEY(org_id,owner_id,instance_id,asset_id) REFERENCES storage_assets(org_id,owner_id,instance_id,id),
  CHECK(status<>'SUCCEEDED' OR (completed_at IS NOT NULL AND verified_at IS NOT NULL AND backup_sha256 IS NOT NULL))
);
CREATE INDEX storage_backups_asset_history ON storage_backups(org_id,owner_id,asset_id,created_at DESC,id DESC);
CREATE UNIQUE INDEX storage_backups_active ON storage_backups(asset_id) WHERE status IN ('RUNNING','UNCERTAIN');

-- Owner-confirmed permanent release is distinct from automatic retention cleanup.
INSERT INTO permissions(id,assignable,dependencies) VALUES('instances:release-storage',true,'{instances:read}');
INSERT INTO role_permissions(role_id,permission_id) SELECT id,'instances:release-storage' FROM roles WHERE builtin;
CREATE TABLE storage_release_requests (
 id uuid PRIMARY KEY,
 org_id uuid NOT NULL,
 owner_id uuid NOT NULL,
 instance_id uuid NOT NULL,
 asset_id uuid NOT NULL UNIQUE,
 confirmed_name text NOT NULL,
 status text NOT NULL CHECK(status IN ('ACCEPTED','RUNNING','UNCERTAIN','SUCCEEDED')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 retry_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 completed_at timestamptz,
 FOREIGN KEY(org_id,owner_id,instance_id,asset_id) REFERENCES storage_assets(org_id,owner_id,instance_id,id),
 CHECK((status='SUCCEEDED')=(completed_at IS NOT NULL))
);
CREATE INDEX storage_release_pending ON storage_release_requests(retry_at,id) WHERE status<>'SUCCEEDED';

CREATE TABLE storage_browse_requests (
 id uuid PRIMARY KEY,
 org_id uuid NOT NULL,
 owner_id uuid NOT NULL,
 instance_id uuid NOT NULL,
 asset_id uuid NOT NULL,
 action text NOT NULL CHECK(action IN ('LIST','PREVIEW','DOWNLOAD')),
 root text NOT NULL CHECK(root IN ('workspace','dsh')),
 path text NOT NULL CHECK(length(path)<=4096),
 after_name text NOT NULL DEFAULT '' CHECK(length(after_name)<=255),
 result_public_key bytea,
 status text NOT NULL CHECK(status IN ('QUEUED','RUNNING','SUCCEEDED','FAILED')),
 listing jsonb,
 content bytea,
 file_name text,
 error_code text,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 expires_at timestamptz NOT NULL,
 FOREIGN KEY(org_id,owner_id,instance_id,asset_id) REFERENCES storage_assets(org_id,owner_id,instance_id,id),
 CHECK(action='LIST' OR (path<>'' AND after_name='')),
 CHECK((action='LIST' AND result_public_key IS NULL) OR (action<>'LIST' AND result_public_key IS NOT NULL AND octet_length(result_public_key)=32)),
 CHECK(status<>'SUCCEEDED' OR (action='LIST' AND listing IS NOT NULL) OR (action<>'LIST' AND content IS NOT NULL AND file_name IS NOT NULL)),
 CHECK(status<>'FAILED' OR error_code IS NOT NULL)
);
CREATE INDEX storage_browse_queue ON storage_browse_requests(created_at,id) WHERE status='QUEUED';
CREATE INDEX storage_browse_expiry ON storage_browse_requests(expires_at);
CREATE UNIQUE INDEX storage_browse_one_active ON storage_browse_requests(asset_id) WHERE status IN ('QUEUED','RUNNING');

INSERT INTO permissions(id,assignable,dependencies) VALUES
 ('skills:read',true,'{}'),('skills:manage',true,'{skills:read}');
INSERT INTO role_permissions(role_id,permission_id)
 SELECT r.id,p.id FROM roles r CROSS JOIN permissions p WHERE r.builtin AND p.id IN ('skills:read','skills:manage');

CREATE TABLE personal_skills (
 id uuid PRIMARY KEY,
 org_id uuid NOT NULL REFERENCES organizations(id),
 owner_id uuid NOT NULL REFERENCES users(id),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 64),
 description text NOT NULL CHECK(length(description) BETWEEN 1 AND 2000),
 model_invocable boolean NOT NULL,
 user_invocable boolean NOT NULL,
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 file_count integer NOT NULL CHECK(file_count BETWEEN 1 AND 128),
 size_bytes integer NOT NULL CHECK(size_bytes BETWEEN 1 AND 12582912),
 key_id text NOT NULL, nonce bytea NOT NULL, ciphertext bytea NOT NULL,
 source_url text NOT NULL DEFAULT '',
 source_digest text NOT NULL DEFAULT '',
 synced_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(org_id,owner_id,name)
);
CREATE INDEX personal_skills_owner ON personal_skills(org_id,owner_id,updated_at DESC,id);

-- Import previews are encrypted, short-lived and scoped to the requesting owner.
CREATE TABLE skill_import_previews (
 id uuid PRIMARY KEY,
 org_id uuid NOT NULL REFERENCES organizations(id),
 owner_id uuid NOT NULL REFERENCES users(id),
 key_id text NOT NULL, nonce bytea NOT NULL, ciphertext bytea NOT NULL,
 expires_at timestamptz NOT NULL DEFAULT clock_timestamp()+interval '5 minutes'
);
CREATE INDEX skill_import_expiry ON skill_import_previews(expires_at);

-- Keep the previous package when an import or sync explicitly replaces a Skill.
CREATE TABLE skill_replacements (
 skill_id uuid PRIMARY KEY REFERENCES personal_skills(id) ON DELETE CASCADE,
 key_id text NOT NULL, nonce bytea NOT NULL, ciphertext bytea NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- Fresh organizations receive the same personal-library permissions.
CREATE OR REPLACE FUNCTION public.hc_seed_roles() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE admin_id uuid:=gen_random_uuid(); member_id uuid:=gen_random_uuid();
BEGIN
 INSERT INTO public.roles(id,org_id,code,name,builtin) VALUES
 (admin_id,NEW.id,'ADMIN','管理员',true),(member_id,NEW.id,'MEMBER','成员',true);
 INSERT INTO public.role_permissions SELECT admin_id,id FROM public.permissions;
 INSERT INTO public.role_permissions SELECT member_id,id FROM public.permissions
 WHERE id LIKE 'instances:%' OR id LIKE 'personal-model:%' OR id LIKE 'skills:%' OR id IN ('configuration:read','audit:read');
 RETURN NEW;
END;
$$;

-- Context content remains in independently deployed memory / knowledge services.
-- Connections and grants are platform metadata, not vector or graph storage.
INSERT INTO permissions(id,assignable,dependencies) VALUES
 ('memory:read',true,'{}'),('memory:manage',true,'{memory:read}'),
 ('knowledge:read',true,'{}'),('knowledge:manage',true,'{knowledge:read}');

-- Deliberately do not grant new capabilities to existing roles. Administrators
-- explicitly assign them through the existing, audited role workflow.

CREATE TABLE context_connections (
 id uuid PRIMARY KEY,
 org_id uuid NOT NULL REFERENCES organizations(id),
 owner_id uuid NOT NULL REFERENCES users(id),
 kind text NOT NULL CHECK(kind IN ('memory','knowledge')),
 backend text NOT NULL CHECK(backend IN ('openviking','mem0','graphiti','milvus','weaviate','qdrant')),
 endpoint_id text NOT NULL CHECK(length(endpoint_id) BETWEEN 1 AND 64),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 100),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 enabled boolean NOT NULL DEFAULT true,
 status text NOT NULL DEFAULT 'UNVERIFIED' CHECK(status IN ('UNVERIFIED','READY','FAILED')),
 capabilities jsonb NOT NULL DEFAULT '{}'::jsonb CHECK(jsonb_typeof(capabilities)='object'),
 key_id text NOT NULL, nonce bytea NOT NULL CHECK(octet_length(nonce)=12), ciphertext bytea NOT NULL,
 verified_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(org_id,owner_id,id),
 UNIQUE(org_id,owner_id,kind,name),
 CHECK((kind='memory' AND backend IN ('openviking','mem0','graphiti')) OR (kind='knowledge' AND backend IN ('milvus','weaviate','qdrant')))
);
CREATE INDEX context_connections_owner ON context_connections(org_id,owner_id,kind,updated_at DESC,id);

CREATE TABLE context_bindings (
 org_id uuid NOT NULL,
 owner_id uuid NOT NULL,
 instance_id uuid NOT NULL,
 connection_id uuid NOT NULL,
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 enabled boolean NOT NULL DEFAULT true,
 scopes jsonb NOT NULL DEFAULT '[]'::jsonb CHECK(jsonb_typeof(scopes)='array'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(instance_id,connection_id),
 FOREIGN KEY(org_id,owner_id,connection_id) REFERENCES context_connections(org_id,owner_id,id) ON DELETE CASCADE,
 FOREIGN KEY(org_id,owner_id,instance_id) REFERENCES instances(org_id,owner_id,id)
);

-- A durable operation intent precedes remote writes. UNKNOWN is reconciled with
-- the original key; callers must never infer rollback from a network timeout.
CREATE TABLE context_operations (
 id uuid PRIMARY KEY,
 org_id uuid NOT NULL,
 owner_id uuid NOT NULL,
 connection_id uuid NOT NULL,
 connection_version bigint NOT NULL CHECK(connection_version>0),
 action text NOT NULL CHECK(length(action) BETWEEN 1 AND 64),
 idempotency_sha256 bytea NOT NULL CHECK(octet_length(idempotency_sha256)=32),
 request_sha256 bytea NOT NULL CHECK(octet_length(request_sha256)=32),
 status text NOT NULL CHECK(status IN ('PENDING','SUCCEEDED','FAILED','UNKNOWN')),
 key_id text NOT NULL, nonce bytea NOT NULL CHECK(octet_length(nonce)=12), ciphertext bytea NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(connection_id,idempotency_sha256),
 FOREIGN KEY(org_id,owner_id,connection_id) REFERENCES context_connections(org_id,owner_id,id)
);

CREATE TABLE context_previews (
 id uuid PRIMARY KEY,
 org_id uuid NOT NULL,
 owner_id uuid NOT NULL,
 connection_id uuid NOT NULL,
 connection_version bigint NOT NULL CHECK(connection_version>0),
 purpose text NOT NULL CHECK(purpose IN ('memory-write','memory-import','memory-migration','document-import')),
 key_id text NOT NULL, nonce bytea NOT NULL CHECK(octet_length(nonce)=12), ciphertext bytea NOT NULL,
 expires_at timestamptz NOT NULL DEFAULT clock_timestamp()+interval '5 minutes',
 consumed_at timestamptz,
 FOREIGN KEY(org_id,owner_id,connection_id) REFERENCES context_connections(org_id,owner_id,id) ON DELETE CASCADE
);
CREATE INDEX context_preview_expiry ON context_previews(expires_at);

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

INSERT INTO permissions(id,assignable,dependencies) VALUES
 ('connectors:read',true,'{}'),
 ('connectors:manage',true,'{connectors:read}'),
 ('connectors:invoke',true,'{connectors:read}');
WITH granted AS (
 INSERT INTO role_permissions(role_id,permission_id)
 SELECT r.id,p.id FROM roles r CROSS JOIN permissions p
 WHERE r.builtin AND r.code IN ('ADMIN','MEMBER') AND p.id LIKE 'connectors:%'
 ON CONFLICT DO NOTHING RETURNING role_id
), changed AS (
 UPDATE roles r SET version=version+1,updated_at=clock_timestamp()
 WHERE EXISTS(SELECT 1 FROM granted g WHERE g.role_id=r.id) RETURNING r.id,r.org_id
)
INSERT INTO audit_events(id,org_id,resource_id,action,outcome,trace_id,details)
 SELECT gen_random_uuid(),org_id,id,'CONNECTOR_DEFAULT_PERMISSIONS_GRANTED','SUCCEEDED',gen_random_uuid()::text,jsonb_build_object('migration',49) FROM changed;

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
 OR id IN ('configuration:read','audit:read');
 RETURN NEW;
END;
$$;

CREATE TABLE mcp_connectors (
 id uuid PRIMARY KEY, org_id uuid NOT NULL REFERENCES organizations(id), owner_id uuid NOT NULL REFERENCES users(id),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 100),
 description text NOT NULL DEFAULT '' CHECK(length(description)<=1000),
 endpoint_id text NOT NULL, protocol_version text NOT NULL CHECK(protocol_version IN ('2026-07-28','2025-11-25')),
 auth_type text NOT NULL CHECK(auth_type IN ('none','bearer')),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0), catalog_version bigint NOT NULL DEFAULT 0,
 enabled boolean NOT NULL DEFAULT true, status text NOT NULL DEFAULT 'UNVERIFIED' CHECK(status IN ('UNVERIFIED','READY','FAILED','AUTH_REQUIRED')),
 server_info jsonb NOT NULL DEFAULT '{}', tools jsonb NOT NULL DEFAULT '[]',
 key_id text NOT NULL, nonce bytea NOT NULL CHECK(octet_length(nonce)=12), ciphertext bytea NOT NULL,
 verified_at timestamptz, deleted_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(), updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(org_id,owner_id,id)
);
CREATE UNIQUE INDEX mcp_connector_name ON mcp_connectors(org_id,owner_id,name) WHERE deleted_at IS NULL;
CREATE INDEX mcp_connector_owner ON mcp_connectors(org_id,owner_id,updated_at DESC,id) WHERE deleted_at IS NULL;
CREATE TABLE mcp_calls (
 id uuid PRIMARY KEY, org_id uuid NOT NULL, owner_id uuid NOT NULL, connector_id uuid NOT NULL,
 connector_version bigint NOT NULL, catalog_version bigint NOT NULL, tool_name text NOT NULL,
 request_sha256 text NOT NULL, idempotency_key text NOT NULL,
 status text NOT NULL CHECK(status IN ('RUNNING','SUCCEEDED','TOOL_ERROR','REJECTED','UNKNOWN')),
 reason text NOT NULL DEFAULT '', timeout_seconds integer NOT NULL CHECK(timeout_seconds BETWEEN 1 AND 120),
 cancel_requested boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(), completed_at timestamptz,
 UNIQUE(org_id,owner_id,idempotency_key),
 FOREIGN KEY(org_id,owner_id,connector_id) REFERENCES mcp_connectors(org_id,owner_id,id)
);
CREATE INDEX mcp_calls_owner ON mcp_calls(org_id,owner_id,created_at DESC,id);

ALTER TABLE mcp_connectors
 ADD COLUMN endpoint_url text NOT NULL DEFAULT '' CHECK(octet_length(endpoint_url)<=1000),
 ADD COLUMN catalog_id text NOT NULL DEFAULT '' CHECK(length(catalog_id)<=64);

ALTER TABLE mcp_connectors DROP CONSTRAINT mcp_connectors_auth_type_check;
ALTER TABLE mcp_connectors ADD CONSTRAINT mcp_connectors_auth_type_check CHECK(auth_type IN ('none','bearer','oauth2'));
CREATE TABLE mcp_oauth_transactions (
 id uuid PRIMARY KEY, org_id uuid NOT NULL, owner_id uuid NOT NULL, connector_id uuid NOT NULL,
 session_id uuid NOT NULL REFERENCES portal_sessions(id), connector_version bigint NOT NULL,
 state_sha256 text NOT NULL UNIQUE, expires_at timestamptz NOT NULL,
 key_id text NOT NULL, nonce bytea NOT NULL CHECK(octet_length(nonce)=12), ciphertext bytea NOT NULL,
 FOREIGN KEY(org_id,owner_id,connector_id) REFERENCES mcp_connectors(org_id,owner_id,id)
);
CREATE INDEX mcp_oauth_expiry ON mcp_oauth_transactions(expires_at);

-- Immutable, encrypted copies of the skills selected when a workspace is created.
CREATE TABLE workspace_skill_snapshots (
 instance_id uuid PRIMARY KEY REFERENCES instances(id) ON DELETE CASCADE,
 org_id uuid NOT NULL REFERENCES organizations(id),
 owner_id uuid NOT NULL REFERENCES users(id),
 key_id text NOT NULL,
 nonce bytea NOT NULL CHECK (octet_length(nonce)=12),
 ciphertext bytea NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
REVOKE ALL ON workspace_skill_snapshots FROM PUBLIC;

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

ALTER TABLE workspace_extensions ADD COLUMN applied_generation uuid;

ALTER TABLE personal_skills DROP CONSTRAINT personal_skills_file_count_check;
ALTER TABLE personal_skills DROP CONSTRAINT personal_skills_size_bytes_check;
ALTER TABLE personal_skills ALTER COLUMN size_bytes TYPE bigint;
ALTER TABLE personal_skills ADD CONSTRAINT personal_skills_file_count_check CHECK (file_count > 0);
ALTER TABLE personal_skills ADD CONSTRAINT personal_skills_size_bytes_check CHECK (size_bytes > 0);

ALTER TABLE storage_assets ADD COLUMN owner_removed_at timestamptz;
