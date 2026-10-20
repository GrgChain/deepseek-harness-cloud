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
    max_instances integer NOT NULL DEFAULT 10 CHECK (max_instances > 0),
    max_user_instances integer NOT NULL DEFAULT 2 CHECK (max_user_instances > 0),
    max_running integer NOT NULL DEFAULT 5 CHECK (max_running > 0),
    cpu_millicores bigint NOT NULL DEFAULT 10000 CHECK (cpu_millicores > 0),
    memory_bytes bigint NOT NULL DEFAULT 21474836480 CHECK (memory_bytes > 0),
    storage_bytes bigint NOT NULL DEFAULT 107374182400 CHECK (storage_bytes > 0),
    max_concurrent_creates integer NOT NULL DEFAULT 2 CHECK (max_concurrent_creates > 0)
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
