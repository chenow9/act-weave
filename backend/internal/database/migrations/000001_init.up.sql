-- ActWeave baseline schema (squashed from historical migrations 000001–000061).
-- Fresh installs apply this single migration. Intermediate step history is
-- preserved under migrations_archive/ for reference only (not embedded).
--
-- Generated for formal testing cutover: do not re-introduce step migrations
-- unless you intentionally un-squash. New schema changes continue as 000002+.


-- ##########################################################################
-- Source: 000001_migration_tooling.up.sql
-- ##########################################################################

-- Migration infrastructure marker. Business schema starts in later migrations.
SELECT 1;


-- ##########################################################################
-- Source: 000002_postgres_baseline.up.sql
-- ##########################################################################

CREATE EXTENSION IF NOT EXISTS citext;

DO $$
BEGIN
    EXECUTE format(
        'ALTER DATABASE %I SET timezone TO %L',
        current_database(),
        'UTC'
    );
END
$$;


-- ##########################################################################
-- Source: 000003_identity.up.sql
-- ##########################################################################

CREATE TABLE users (
    id UUID PRIMARY KEY,
    username CITEXT NOT NULL,
    email CITEXT,
    display_name VARCHAR(120) NOT NULL,
    avatar_url TEXT,
    status TEXT NOT NULL DEFAULT 'ACTIVE',
    platform_role TEXT NOT NULL DEFAULT 'USER',
    locale VARCHAR(16) NOT NULL DEFAULT 'zh-CN',
    timezone VARCHAR(64) NOT NULL DEFAULT 'Asia/Singapore',
    last_login_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT users_username_key UNIQUE (username),
    CONSTRAINT users_email_key UNIQUE (email),
    CONSTRAINT users_username_not_blank CHECK (length(btrim(username::TEXT)) > 0),
    CONSTRAINT users_display_name_not_blank CHECK (length(btrim(display_name)) > 0),
    CONSTRAINT users_status_check CHECK (status IN ('ACTIVE', 'LOCKED', 'DISABLED')),
    CONSTRAINT users_platform_role_check CHECK (platform_role IN ('USER', 'PLATFORM_ADMIN')),
    CONSTRAINT users_timestamps_check CHECK (updated_at >= created_at)
);

CREATE INDEX users_status_updated_idx
    ON users (status, updated_at DESC, id);

CREATE TABLE user_credentials (
    user_id UUID PRIMARY KEY,
    password_hash TEXT NOT NULL,
    password_algo TEXT NOT NULL,
    password_changed_at TIMESTAMPTZ NOT NULL,
    failed_attempts INTEGER NOT NULL DEFAULT 0,
    locked_until TIMESTAMPTZ,
    must_change_password BOOLEAN NOT NULL DEFAULT FALSE,
    CONSTRAINT user_credentials_user_fk
        FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT user_credentials_password_hash_not_blank
        CHECK (length(password_hash) > 0),
    CONSTRAINT user_credentials_password_algo_not_blank
        CHECK (length(btrim(password_algo)) > 0),
    CONSTRAINT user_credentials_failed_attempts_check
        CHECK (failed_attempts >= 0)
);

CREATE INDEX user_credentials_locked_until_idx
    ON user_credentials (locked_until)
    WHERE locked_until IS NOT NULL;

CREATE TABLE auth_sessions (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL,
    refresh_token_hash TEXT NOT NULL,
    user_agent TEXT,
    ip INET,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT auth_sessions_user_fk
        FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT auth_sessions_refresh_token_hash_key UNIQUE (refresh_token_hash),
    CONSTRAINT auth_sessions_refresh_token_hash_not_blank
        CHECK (length(refresh_token_hash) > 0),
    CONSTRAINT auth_sessions_expiry_check CHECK (expires_at > created_at),
    CONSTRAINT auth_sessions_revocation_check
        CHECK (revoked_at IS NULL OR revoked_at >= created_at),
    CONSTRAINT auth_sessions_last_seen_check CHECK (last_seen_at >= created_at)
);

CREATE INDEX auth_sessions_user_active_idx
    ON auth_sessions (user_id, expires_at DESC, id)
    WHERE revoked_at IS NULL;

CREATE INDEX auth_sessions_expires_at_idx
    ON auth_sessions (expires_at);


-- ##########################################################################
-- Source: 000004_user_lock_version.up.sql
-- ##########################################################################

ALTER TABLE users
    ADD COLUMN lock_version BIGINT NOT NULL DEFAULT 1;

ALTER TABLE users
    ADD CONSTRAINT users_lock_version_check CHECK (lock_version > 0);


-- ##########################################################################
-- Source: 000005_workspace_rbac.up.sql
-- ##########################################################################

CREATE TABLE workspaces (
    id UUID PRIMARY KEY,
    slug CITEXT NOT NULL,
    display_name VARCHAR(120) NOT NULL,
    mode TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'ACTIVE',
    owner_user_id UUID NOT NULL,
    default_agent_id UUID,
    default_model_config_id UUID,
    settings JSONB NOT NULL DEFAULT '{}'::JSONB,
    created_by UUID NOT NULL,
    updated_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    lock_version BIGINT NOT NULL DEFAULT 1,
    deleted_at TIMESTAMPTZ,
    CONSTRAINT workspaces_slug_key UNIQUE (slug),
    CONSTRAINT workspaces_owner_user_fk
        FOREIGN KEY (owner_user_id) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT workspaces_created_by_fk
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT workspaces_updated_by_fk
        FOREIGN KEY (updated_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT workspaces_slug_not_blank CHECK (length(btrim(slug::TEXT)) > 0),
    CONSTRAINT workspaces_display_name_not_blank CHECK (length(btrim(display_name)) > 0),
    CONSTRAINT workspaces_mode_check CHECK (mode IN ('PRODUCTION', 'SANDBOX')),
    CONSTRAINT workspaces_status_check CHECK (status IN ('ACTIVE', 'DISABLED')),
    CONSTRAINT workspaces_settings_object_check CHECK (jsonb_typeof(settings) = 'object'),
    CONSTRAINT workspaces_lock_version_check CHECK (lock_version > 0),
    CONSTRAINT workspaces_timestamps_check CHECK (updated_at >= created_at),
    CONSTRAINT workspaces_deleted_at_check CHECK (deleted_at IS NULL OR deleted_at >= created_at)
);

CREATE INDEX workspaces_status_updated_idx
    ON workspaces (status, updated_at DESC, id)
    WHERE deleted_at IS NULL;

CREATE INDEX workspaces_owner_user_idx
    ON workspaces (owner_user_id, id)
    WHERE deleted_at IS NULL;

CREATE TABLE workspace_members (
    workspace_id UUID NOT NULL,
    user_id UUID NOT NULL,
    role TEXT NOT NULL,
    invited_by UUID,
    joined_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    disabled_at TIMESTAMPTZ,
    PRIMARY KEY (workspace_id, user_id),
    CONSTRAINT workspace_members_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT workspace_members_user_fk
        FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT workspace_members_invited_by_fk
        FOREIGN KEY (invited_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT workspace_members_role_check
        CHECK (role IN ('OWNER', 'ADMIN', 'EDITOR', 'OPERATOR', 'VIEWER')),
    CONSTRAINT workspace_members_disabled_at_check
        CHECK (disabled_at IS NULL OR disabled_at >= joined_at)
);

CREATE INDEX workspace_members_user_active_idx
    ON workspace_members (user_id, workspace_id)
    WHERE disabled_at IS NULL;

CREATE INDEX workspace_members_workspace_role_idx
    ON workspace_members (workspace_id, role, user_id)
    WHERE disabled_at IS NULL;


-- ##########################################################################
-- Source: 000006_secrets.up.sql
-- ##########################################################################

CREATE TABLE secrets (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    name CITEXT NOT NULL,
    kind TEXT NOT NULL,
    active_version_id UUID,
    created_by UUID NOT NULL,
    updated_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    lock_version BIGINT NOT NULL DEFAULT 1,
    CONSTRAINT secrets_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT secrets_workspace_name_key UNIQUE (workspace_id, name),
    CONSTRAINT secrets_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT secrets_created_by_fk
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT secrets_updated_by_fk
        FOREIGN KEY (updated_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT secrets_name_not_blank CHECK (length(btrim(name::TEXT)) > 0),
    CONSTRAINT secrets_kind_not_blank CHECK (length(btrim(kind)) > 0),
    CONSTRAINT secrets_timestamps_check CHECK (updated_at >= created_at),
    CONSTRAINT secrets_lock_version_check CHECK (lock_version > 0)
);

CREATE INDEX secrets_workspace_updated_idx
    ON secrets (workspace_id, updated_at DESC, id);

CREATE TABLE secret_versions (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    secret_id UUID NOT NULL,
    version_no BIGINT NOT NULL,
    ciphertext BYTEA NOT NULL,
    nonce BYTEA NOT NULL,
    key_id VARCHAR(255) NOT NULL,
    fingerprint VARCHAR(128) NOT NULL,
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    revoked_at TIMESTAMPTZ,
    CONSTRAINT secret_versions_workspace_secret_id_key
        UNIQUE (workspace_id, secret_id, id),
    CONSTRAINT secret_versions_secret_version_key UNIQUE (secret_id, version_no),
    CONSTRAINT secret_versions_secret_fk
        FOREIGN KEY (workspace_id, secret_id)
        REFERENCES secrets (workspace_id, id)
        ON DELETE RESTRICT,
    CONSTRAINT secret_versions_created_by_fk
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT secret_versions_version_no_check CHECK (version_no > 0),
    CONSTRAINT secret_versions_ciphertext_not_empty CHECK (octet_length(ciphertext) > 0),
    CONSTRAINT secret_versions_nonce_not_empty CHECK (octet_length(nonce) > 0),
    CONSTRAINT secret_versions_key_id_not_blank CHECK (length(btrim(key_id)) > 0),
    CONSTRAINT secret_versions_fingerprint_not_blank CHECK (length(btrim(fingerprint)) > 0),
    CONSTRAINT secret_versions_revoked_at_check
        CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);

ALTER TABLE secrets
    ADD CONSTRAINT secrets_active_version_fk
    FOREIGN KEY (workspace_id, id, active_version_id)
    REFERENCES secret_versions (workspace_id, secret_id, id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

CREATE INDEX secret_versions_unrevoked_idx
    ON secret_versions (workspace_id, secret_id, version_no DESC)
    WHERE revoked_at IS NULL;


-- ##########################################################################
-- Source: 000007_model_configs.up.sql
-- ##########################################################################

CREATE TABLE model_configs (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    name CITEXT NOT NULL,
    provider TEXT NOT NULL,
    api_base TEXT NOT NULL,
    model_name TEXT NOT NULL,
    credential_secret_id UUID,
    options JSONB NOT NULL DEFAULT '{}'::JSONB,
    status TEXT NOT NULL DEFAULT 'UNVERIFIED',
    last_verified_at TIMESTAMPTZ,
    last_latency_ms INTEGER,
    last_error_code TEXT,
    created_by UUID NOT NULL,
    updated_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    lock_version BIGINT NOT NULL DEFAULT 1,
    deleted_at TIMESTAMPTZ,
    CONSTRAINT model_configs_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT model_configs_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT model_configs_credential_secret_fk
        FOREIGN KEY (workspace_id, credential_secret_id)
        REFERENCES secrets (workspace_id, id)
        ON DELETE RESTRICT,
    CONSTRAINT model_configs_created_by_fk
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT model_configs_updated_by_fk
        FOREIGN KEY (updated_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT model_configs_name_not_blank CHECK (length(btrim(name::TEXT)) > 0),
    CONSTRAINT model_configs_provider_not_blank CHECK (length(btrim(provider)) > 0),
    CONSTRAINT model_configs_api_base_not_blank CHECK (length(btrim(api_base)) > 0),
    CONSTRAINT model_configs_model_name_not_blank CHECK (length(btrim(model_name)) > 0),
    CONSTRAINT model_configs_options_object_check CHECK (jsonb_typeof(options) = 'object'),
    CONSTRAINT model_configs_status_check
        CHECK (status IN ('UNVERIFIED', 'VERIFIED', 'ERROR', 'DISABLED')),
    CONSTRAINT model_configs_last_latency_check
        CHECK (last_latency_ms IS NULL OR last_latency_ms >= 0),
    CONSTRAINT model_configs_verification_time_check
        CHECK (last_verified_at IS NULL OR last_verified_at >= created_at),
    CONSTRAINT model_configs_timestamps_check CHECK (updated_at >= created_at),
    CONSTRAINT model_configs_lock_version_check CHECK (lock_version > 0),
    CONSTRAINT model_configs_deleted_at_check
        CHECK (deleted_at IS NULL OR deleted_at >= created_at)
);

CREATE UNIQUE INDEX model_configs_workspace_name_active_key
    ON model_configs (workspace_id, name)
    WHERE deleted_at IS NULL;

CREATE INDEX model_configs_workspace_status_updated_idx
    ON model_configs (workspace_id, status, updated_at DESC, id)
    WHERE deleted_at IS NULL;

ALTER TABLE workspaces
    ADD CONSTRAINT workspaces_default_model_config_fk
    FOREIGN KEY (id, default_model_config_id)
    REFERENCES model_configs (workspace_id, id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;


-- ##########################################################################
-- Source: 000008_providers.up.sql
-- ##########################################################################

CREATE TABLE capability_providers (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    name CITEXT NOT NULL,
    provider_kind TEXT NOT NULL,
    driver_key TEXT NOT NULL,
    transport TEXT NOT NULL,
    endpoint_config JSONB NOT NULL DEFAULT '{}'::JSONB,
    driver_config JSONB NOT NULL DEFAULT '{}'::JSONB,
    discovery_mode TEXT NOT NULL DEFAULT 'ON_DEMAND',
    execution_profile_id UUID,
    status TEXT NOT NULL DEFAULT 'ACTIVE',
    last_synced_at TIMESTAMPTZ,
    last_error_code TEXT,
    created_by UUID NOT NULL,
    updated_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    lock_version BIGINT NOT NULL DEFAULT 1,
    deleted_at TIMESTAMPTZ,
    CONSTRAINT capability_providers_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT capability_providers_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT capability_providers_created_by_fk
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT capability_providers_updated_by_fk
        FOREIGN KEY (updated_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT capability_providers_name_not_blank CHECK (length(btrim(name::TEXT)) > 0),
    CONSTRAINT capability_providers_kind_check CHECK (
        provider_kind IN (
            'HTTP_OPENAPI', 'INTERNAL_REGISTRY', 'MCP_SERVER',
            'OPEN_CONNECTOR', 'SHELL_RUNTIME'
        )
    ),
    CONSTRAINT capability_providers_driver_key_not_blank CHECK (length(btrim(driver_key)) > 0),
    CONSTRAINT capability_providers_transport_not_blank CHECK (length(btrim(transport)) > 0),
    CONSTRAINT capability_providers_endpoint_config_object_check
        CHECK (jsonb_typeof(endpoint_config) = 'object'),
    CONSTRAINT capability_providers_driver_config_object_check
        CHECK (jsonb_typeof(driver_config) = 'object'),
    CONSTRAINT capability_providers_discovery_mode_check
        CHECK (discovery_mode IN ('MANUAL', 'ON_DEMAND', 'POLLING')),
    CONSTRAINT capability_providers_status_check
        CHECK (status IN ('ACTIVE', 'DISABLED', 'ERROR')),
    CONSTRAINT capability_providers_sync_time_check
        CHECK (last_synced_at IS NULL OR last_synced_at >= created_at),
    CONSTRAINT capability_providers_timestamps_check CHECK (updated_at >= created_at),
    CONSTRAINT capability_providers_lock_version_check CHECK (lock_version > 0),
    CONSTRAINT capability_providers_deleted_at_check
        CHECK (deleted_at IS NULL OR deleted_at >= created_at)
);

CREATE UNIQUE INDEX capability_providers_workspace_name_active_key
    ON capability_providers (workspace_id, name)
    WHERE deleted_at IS NULL;

CREATE INDEX capability_providers_workspace_status_updated_idx
    ON capability_providers (workspace_id, status, updated_at DESC, id)
    WHERE deleted_at IS NULL;

CREATE TABLE provider_assets (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    provider_id UUID NOT NULL,
    asset_kind TEXT NOT NULL,
    external_id TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    input_schema JSONB NOT NULL DEFAULT '{}'::JSONB,
    output_schema JSONB NOT NULL DEFAULT '{}'::JSONB,
    metadata JSONB NOT NULL DEFAULT '{}'::JSONB,
    source_revision TEXT,
    source_checksum TEXT NOT NULL,
    materialized_capability_id UUID,
    status TEXT NOT NULL DEFAULT 'ACTIVE',
    discovered_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT provider_assets_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT provider_assets_provider_external_key
        UNIQUE (provider_id, asset_kind, external_id),
    CONSTRAINT provider_assets_workspace_provider_fk
        FOREIGN KEY (workspace_id, provider_id)
        REFERENCES capability_providers (workspace_id, id)
        ON DELETE RESTRICT,
    CONSTRAINT provider_assets_kind_check
        CHECK (asset_kind IN ('TOOL', 'RESOURCE', 'PROMPT', 'AGENT_SKILL')),
    CONSTRAINT provider_assets_external_id_not_blank CHECK (length(btrim(external_id)) > 0),
    CONSTRAINT provider_assets_name_not_blank CHECK (length(btrim(name)) > 0),
    CONSTRAINT provider_assets_input_schema_object_check
        CHECK (jsonb_typeof(input_schema) = 'object'),
    CONSTRAINT provider_assets_output_schema_object_check
        CHECK (jsonb_typeof(output_schema) = 'object'),
    CONSTRAINT provider_assets_metadata_object_check CHECK (jsonb_typeof(metadata) = 'object'),
    CONSTRAINT provider_assets_source_checksum_not_blank
        CHECK (length(btrim(source_checksum)) > 0),
    CONSTRAINT provider_assets_status_check
        CHECK (status IN ('ACTIVE', 'STALE', 'UNAVAILABLE', 'MATERIALIZED')),
    CONSTRAINT provider_assets_seen_time_check CHECK (last_seen_at >= discovered_at)
);

CREATE INDEX provider_assets_workspace_provider_status_idx
    ON provider_assets (workspace_id, provider_id, status, last_seen_at DESC, id);

CREATE TABLE provider_sync_runs (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    provider_id UUID NOT NULL,
    status TEXT NOT NULL DEFAULT 'PENDING',
    cursor_before TEXT,
    cursor_after TEXT,
    discovered_count INTEGER NOT NULL DEFAULT 0,
    changed_count INTEGER NOT NULL DEFAULT 0,
    error_summary JSONB NOT NULL DEFAULT '{}'::JSONB,
    started_by UUID NOT NULL,
    started_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at TIMESTAMPTZ,
    CONSTRAINT provider_sync_runs_workspace_provider_fk
        FOREIGN KEY (workspace_id, provider_id)
        REFERENCES capability_providers (workspace_id, id)
        ON DELETE RESTRICT,
    CONSTRAINT provider_sync_runs_started_by_fk
        FOREIGN KEY (started_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT provider_sync_runs_status_check
        CHECK (status IN ('PENDING', 'RUNNING', 'SUCCEEDED', 'FAILED')),
    CONSTRAINT provider_sync_runs_discovered_count_check CHECK (discovered_count >= 0),
    CONSTRAINT provider_sync_runs_changed_count_check CHECK (changed_count >= 0),
    CONSTRAINT provider_sync_runs_count_order_check CHECK (changed_count <= discovered_count),
    CONSTRAINT provider_sync_runs_error_summary_object_check
        CHECK (jsonb_typeof(error_summary) = 'object'),
    CONSTRAINT provider_sync_runs_finished_at_check
        CHECK (finished_at IS NULL OR finished_at >= started_at)
);

CREATE INDEX provider_sync_runs_workspace_provider_started_idx
    ON provider_sync_runs (workspace_id, provider_id, started_at DESC, id);


-- ##########################################################################
-- Source: 000009_connections.up.sql
-- ##########################################################################

CREATE TABLE service_connections (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    provider_id UUID NOT NULL,
    name CITEXT NOT NULL,
    alias CITEXT NOT NULL,
    environment TEXT NOT NULL,
    external_account_ref TEXT,
    auth_mode TEXT NOT NULL,
    auth_config JSONB NOT NULL DEFAULT '{}'::JSONB,
    credential_secret_id UUID,
    granted_scopes JSONB NOT NULL DEFAULT '[]'::JSONB,
    policy JSONB NOT NULL DEFAULT '{}'::JSONB,
    status TEXT NOT NULL DEFAULT 'UNVERIFIED',
    last_verified_at TIMESTAMPTZ,
    last_error_code TEXT,
    created_by UUID NOT NULL,
    updated_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    lock_version BIGINT NOT NULL DEFAULT 1,
    deleted_at TIMESTAMPTZ,
    CONSTRAINT service_connections_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT service_connections_workspace_provider_fk
        FOREIGN KEY (workspace_id, provider_id)
        REFERENCES capability_providers (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT service_connections_credential_secret_fk
        FOREIGN KEY (workspace_id, credential_secret_id)
        REFERENCES secrets (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT service_connections_created_by_fk
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT service_connections_updated_by_fk
        FOREIGN KEY (updated_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT service_connections_name_not_blank CHECK (length(btrim(name::TEXT)) > 0),
    CONSTRAINT service_connections_alias_not_blank CHECK (length(btrim(alias::TEXT)) > 0),
    CONSTRAINT service_connections_environment_check
        CHECK (environment IN ('PRODUCTION', 'STAGING', 'DEVELOPMENT', 'TEST')),
    CONSTRAINT service_connections_auth_mode_not_blank CHECK (length(btrim(auth_mode)) > 0),
    CONSTRAINT service_connections_auth_config_object_check CHECK (jsonb_typeof(auth_config) = 'object'),
    CONSTRAINT service_connections_granted_scopes_array_check CHECK (jsonb_typeof(granted_scopes) = 'array'),
    CONSTRAINT service_connections_policy_object_check CHECK (jsonb_typeof(policy) = 'object'),
    CONSTRAINT service_connections_status_check
        CHECK (status IN ('UNVERIFIED', 'VERIFIED', 'ERROR', 'DISABLED')),
    CONSTRAINT service_connections_verify_time_check
        CHECK (last_verified_at IS NULL OR last_verified_at >= created_at),
    CONSTRAINT service_connections_timestamps_check CHECK (updated_at >= created_at),
    CONSTRAINT service_connections_lock_version_check CHECK (lock_version > 0),
    CONSTRAINT service_connections_deleted_at_check CHECK (deleted_at IS NULL OR deleted_at >= created_at)
);

CREATE UNIQUE INDEX service_connections_provider_alias_active_key
    ON service_connections (provider_id, alias) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX service_connections_workspace_name_active_key
    ON service_connections (workspace_id, name) WHERE deleted_at IS NULL;
CREATE INDEX service_connections_workspace_status_updated_idx
    ON service_connections (workspace_id, status, updated_at DESC, id) WHERE deleted_at IS NULL;

CREATE TABLE connection_verifications (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    connection_id UUID NOT NULL,
    status TEXT NOT NULL,
    diagnostics JSONB NOT NULL DEFAULT '{}'::JSONB,
    latency_ms INTEGER,
    tested_by UUID NOT NULL,
    tested_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    raw_object_id UUID,
    CONSTRAINT connection_verifications_workspace_connection_fk
        FOREIGN KEY (workspace_id, connection_id)
        REFERENCES service_connections (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT connection_verifications_tested_by_fk
        FOREIGN KEY (tested_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT connection_verifications_status_check CHECK (status IN ('SUCCEEDED', 'FAILED')),
    CONSTRAINT connection_verifications_diagnostics_object_check CHECK (jsonb_typeof(diagnostics) = 'object'),
    CONSTRAINT connection_verifications_latency_check CHECK (latency_ms IS NULL OR latency_ms >= 0)
);

CREATE INDEX connection_verifications_workspace_connection_tested_idx
    ON connection_verifications (workspace_id, connection_id, tested_at DESC, id);


-- ##########################################################################
-- Source: 000010_agents.up.sql
-- ##########################################################################

CREATE TABLE agents (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    name CITEXT NOT NULL,
    role_description TEXT NOT NULL DEFAULT '',
    current_prompt_revision_id UUID,
    model_config_id UUID NOT NULL,
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    status TEXT NOT NULL DEFAULT 'ACTIVE',
    created_by UUID NOT NULL,
    updated_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    lock_version BIGINT NOT NULL DEFAULT 1,
    deleted_at TIMESTAMPTZ,
    CONSTRAINT agents_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT agents_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT agents_workspace_model_config_fk
        FOREIGN KEY (workspace_id, model_config_id)
        REFERENCES model_configs (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT agents_created_by_fk
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT agents_updated_by_fk
        FOREIGN KEY (updated_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT agents_name_not_blank CHECK (length(btrim(name::TEXT)) > 0),
    CONSTRAINT agents_status_check CHECK (status IN ('ACTIVE', 'DISABLED', 'ERROR')),
    CONSTRAINT agents_lock_version_check CHECK (lock_version > 0),
    CONSTRAINT agents_timestamps_check CHECK (updated_at >= created_at),
    CONSTRAINT agents_deleted_at_check CHECK (deleted_at IS NULL OR deleted_at >= created_at)
);

CREATE UNIQUE INDEX agents_workspace_name_active_key
    ON agents (workspace_id, name) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX agents_workspace_default_active_key
    ON agents (workspace_id) WHERE is_default AND deleted_at IS NULL;
CREATE INDEX agents_workspace_status_updated_idx
    ON agents (workspace_id, status, updated_at DESC, id) WHERE deleted_at IS NULL;

CREATE TABLE agent_prompt_revisions (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    revision_no INTEGER NOT NULL,
    system_prompt TEXT NOT NULL,
    source TEXT NOT NULL,
    content_sha256 CHAR(64) NOT NULL,
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT agent_prompt_revisions_workspace_agent_id_key
        UNIQUE (workspace_id, agent_id, id),
    CONSTRAINT agent_prompt_revisions_agent_revision_key
        UNIQUE (agent_id, revision_no),
    CONSTRAINT agent_prompt_revisions_workspace_agent_fk
        FOREIGN KEY (workspace_id, agent_id)
        REFERENCES agents (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT agent_prompt_revisions_created_by_fk
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT agent_prompt_revisions_revision_no_check CHECK (revision_no > 0),
    CONSTRAINT agent_prompt_revisions_prompt_not_blank CHECK (length(btrim(system_prompt)) > 0),
    CONSTRAINT agent_prompt_revisions_source_check
        CHECK (source IN ('MANUAL', 'ENHANCED', 'GENERATED', 'IMPORTED')),
    CONSTRAINT agent_prompt_revisions_sha256_check
        CHECK (content_sha256 ~ '^[0-9a-f]{64}$')
);

CREATE INDEX agent_prompt_revisions_workspace_agent_revision_idx
    ON agent_prompt_revisions (workspace_id, agent_id, revision_no DESC, id);

ALTER TABLE agents
    ADD CONSTRAINT agents_current_prompt_revision_fk
    FOREIGN KEY (workspace_id, id, current_prompt_revision_id)
    REFERENCES agent_prompt_revisions (workspace_id, agent_id, id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

-- The pre-normalization column had no referential target. Legacy values are
-- intentionally discarded; this phase does not migrate the old state model.
UPDATE workspaces SET default_agent_id = NULL WHERE default_agent_id IS NOT NULL;

ALTER TABLE workspaces
    ADD CONSTRAINT workspaces_default_agent_fk
    FOREIGN KEY (id, default_agent_id)
    REFERENCES agents (workspace_id, id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE prompt_runs (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    agent_id UUID,
    operation_type TEXT NOT NULL,
    model_config_id UUID NOT NULL,
    model_snapshot JSONB NOT NULL,
    input_object_id UUID NOT NULL,
    output_object_id UUID,
    status TEXT NOT NULL DEFAULT 'PENDING',
    accepted_revision_id UUID,
    trace_id TEXT NOT NULL,
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at TIMESTAMPTZ,
    error_code TEXT,
    CONSTRAINT prompt_runs_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT prompt_runs_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT prompt_runs_workspace_agent_fk
        FOREIGN KEY (workspace_id, agent_id)
        REFERENCES agents (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT prompt_runs_workspace_model_config_fk
        FOREIGN KEY (workspace_id, model_config_id)
        REFERENCES model_configs (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT prompt_runs_accepted_revision_fk
        FOREIGN KEY (workspace_id, agent_id, accepted_revision_id)
        REFERENCES agent_prompt_revisions (workspace_id, agent_id, id) ON DELETE RESTRICT,
    CONSTRAINT prompt_runs_created_by_fk
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT prompt_runs_operation_check
        CHECK (operation_type IN ('ENHANCE', 'GENERATE', 'PREVIEW')),
    CONSTRAINT prompt_runs_model_snapshot_object_check
        CHECK (jsonb_typeof(model_snapshot) = 'object'),
    CONSTRAINT prompt_runs_status_check
        CHECK (status IN ('PENDING', 'RUNNING', 'SUCCEEDED', 'FAILED')),
    CONSTRAINT prompt_runs_trace_not_blank CHECK (length(btrim(trace_id)) > 0),
    CONSTRAINT prompt_runs_finished_status_check CHECK (
        (status IN ('PENDING', 'RUNNING') AND finished_at IS NULL)
        OR (status IN ('SUCCEEDED', 'FAILED') AND finished_at IS NOT NULL)
    ),
    CONSTRAINT prompt_runs_output_status_check CHECK (
        output_object_id IS NULL OR status = 'SUCCEEDED'
    ),
    CONSTRAINT prompt_runs_acceptance_check CHECK (
        accepted_revision_id IS NULL
        OR (agent_id IS NOT NULL AND status = 'SUCCEEDED' AND output_object_id IS NOT NULL)
    ),
    CONSTRAINT prompt_runs_error_status_check CHECK (
        (status = 'FAILED' AND error_code IS NOT NULL)
        OR (status <> 'FAILED' AND error_code IS NULL)
    )
);

CREATE INDEX prompt_runs_workspace_created_idx
    ON prompt_runs (workspace_id, created_at DESC, id);
CREATE INDEX prompt_runs_workspace_agent_created_idx
    ON prompt_runs (workspace_id, agent_id, created_at DESC, id)
    WHERE agent_id IS NOT NULL;
CREATE INDEX prompt_runs_trace_idx ON prompt_runs (trace_id, id);

CREATE FUNCTION reject_agent_prompt_revision_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'agent prompt revisions are immutable and permanently retained'
        USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER agent_prompt_revisions_immutable
BEFORE UPDATE OR DELETE ON agent_prompt_revisions
FOR EACH ROW EXECUTE FUNCTION reject_agent_prompt_revision_mutation();


-- ##########################################################################
-- Source: 000011_capabilities.up.sql
-- ##########################################################################

CREATE TABLE capabilities (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    kind TEXT NOT NULL,
    name CITEXT NOT NULL,
    slug CITEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'ACTIVE',
    active_release_id UUID,
    created_by UUID NOT NULL,
    updated_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    lock_version BIGINT NOT NULL DEFAULT 1,
    deleted_at TIMESTAMPTZ,
    CONSTRAINT capabilities_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT capabilities_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT capabilities_created_by_fk
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT capabilities_updated_by_fk
        FOREIGN KEY (updated_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT capabilities_kind_check CHECK (kind IN ('TOOL', 'WORKFLOW')),
    CONSTRAINT capabilities_name_not_blank CHECK (length(btrim(name::TEXT)) > 0),
    CONSTRAINT capabilities_slug_check
        CHECK (slug::TEXT ~ '^[a-z][a-z0-9-]{0,62}[a-z0-9]$' OR slug::TEXT ~ '^[a-z]$'),
    CONSTRAINT capabilities_status_check CHECK (status IN ('ACTIVE', 'DISABLED', 'ERROR')),
    CONSTRAINT capabilities_lock_version_check CHECK (lock_version > 0),
    CONSTRAINT capabilities_timestamps_check CHECK (updated_at >= created_at),
    CONSTRAINT capabilities_deleted_at_check CHECK (deleted_at IS NULL OR deleted_at >= created_at)
);

CREATE UNIQUE INDEX capabilities_workspace_slug_active_key
    ON capabilities (workspace_id, slug) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX capabilities_workspace_name_active_key
    ON capabilities (workspace_id, name) WHERE deleted_at IS NULL;
CREATE INDEX capabilities_workspace_status_updated_idx
    ON capabilities (workspace_id, status, updated_at DESC, id) WHERE deleted_at IS NULL;

CREATE TABLE capability_releases (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    capability_id UUID NOT NULL,
    release_no INTEGER NOT NULL,
    source_type TEXT NOT NULL,
    source_id UUID NOT NULL,
    callable_name TEXT NOT NULL,
    callable_description TEXT NOT NULL DEFAULT '',
    input_schema JSONB NOT NULL,
    output_schema JSONB NOT NULL,
    risk_level TEXT NOT NULL,
    side_effect_level TEXT NOT NULL,
    requires_confirmation BOOLEAN NOT NULL DEFAULT FALSE,
    checksum CHAR(64) NOT NULL,
    published_by UUID NOT NULL,
    published_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    retired_at TIMESTAMPTZ,
    CONSTRAINT capability_releases_workspace_capability_id_key
        UNIQUE (workspace_id, capability_id, id),
    CONSTRAINT capability_releases_capability_release_no_key
        UNIQUE (capability_id, release_no),
    CONSTRAINT capability_releases_workspace_capability_fk
        FOREIGN KEY (workspace_id, capability_id)
        REFERENCES capabilities (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT capability_releases_published_by_fk
        FOREIGN KEY (published_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT capability_releases_release_no_check CHECK (release_no > 0),
    CONSTRAINT capability_releases_source_type_check
        CHECK (source_type IN ('TOOL_VERSION', 'WORKFLOW_REVISION')),
    CONSTRAINT capability_releases_callable_name_check
        CHECK (callable_name ~ '^[A-Za-z_][A-Za-z0-9_]{0,63}$'),
    CONSTRAINT capability_releases_input_schema_object_check
        CHECK (jsonb_typeof(input_schema) = 'object'),
    CONSTRAINT capability_releases_output_schema_object_check
        CHECK (jsonb_typeof(output_schema) = 'object'),
    CONSTRAINT capability_releases_risk_check
        CHECK (risk_level IN ('LOW', 'MEDIUM', 'HIGH', 'CRITICAL')),
    CONSTRAINT capability_releases_side_effect_check
        CHECK (side_effect_level IN ('NONE', 'READ', 'WRITE', 'IRREVERSIBLE')),
    CONSTRAINT capability_releases_checksum_check CHECK (checksum ~ '^[0-9a-f]{64}$'),
    CONSTRAINT capability_releases_retired_at_check
        CHECK (retired_at IS NULL OR retired_at >= published_at)
);

CREATE INDEX capability_releases_workspace_capability_published_idx
    ON capability_releases (workspace_id, capability_id, release_no DESC, id);
CREATE INDEX capability_releases_workspace_callable_idx
    ON capability_releases (workspace_id, lower(callable_name), id);

ALTER TABLE capabilities
    ADD CONSTRAINT capabilities_active_release_fk
    FOREIGN KEY (workspace_id, id, active_release_id)
    REFERENCES capability_releases (workspace_id, capability_id, id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

-- The legacy column did not reference the normalized Capability table.
UPDATE provider_assets
SET materialized_capability_id = NULL
WHERE materialized_capability_id IS NOT NULL;

ALTER TABLE provider_assets
    ADD CONSTRAINT provider_assets_materialized_capability_fk
    FOREIGN KEY (workspace_id, materialized_capability_id)
    REFERENCES capabilities (workspace_id, id)
    ON DELETE RESTRICT;

CREATE FUNCTION enforce_capability_active_release()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    selected_callable TEXT;
BEGIN
    IF NEW.active_release_id IS NULL THEN
        RETURN NEW;
    END IF;

    PERFORM pg_advisory_xact_lock(hashtextextended(NEW.workspace_id::TEXT, 0));
    SELECT callable_name INTO selected_callable
    FROM capability_releases
    WHERE workspace_id = NEW.workspace_id
      AND capability_id = NEW.id
      AND id = NEW.active_release_id
      AND retired_at IS NULL;
    IF selected_callable IS NULL THEN
        RAISE EXCEPTION 'active release must be a non-retired release of the same capability'
            USING ERRCODE = '23514';
    END IF;
    IF EXISTS (
        SELECT 1
        FROM capabilities AS c
        JOIN capability_releases AS r
          ON r.workspace_id = c.workspace_id
         AND r.capability_id = c.id
         AND r.id = c.active_release_id
        WHERE c.workspace_id = NEW.workspace_id
          AND c.id <> NEW.id
          AND c.deleted_at IS NULL
          AND lower(r.callable_name) = lower(selected_callable)
    ) THEN
        RAISE EXCEPTION 'active capability callable name already exists in workspace'
            USING ERRCODE = '23505';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER capabilities_active_release_integrity
BEFORE INSERT OR UPDATE OF active_release_id, workspace_id ON capabilities
FOR EACH ROW EXECUTE FUNCTION enforce_capability_active_release();

CREATE FUNCTION enforce_capability_release_immutability()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'capability releases are immutable'
            USING ERRCODE = '55000';
    END IF;
    IF ROW(
        NEW.id, NEW.workspace_id, NEW.capability_id, NEW.release_no,
        NEW.source_type, NEW.source_id, NEW.callable_name, NEW.callable_description,
        NEW.input_schema, NEW.output_schema, NEW.risk_level, NEW.side_effect_level,
        NEW.requires_confirmation, NEW.checksum, NEW.published_by, NEW.published_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.capability_id, OLD.release_no,
        OLD.source_type, OLD.source_id, OLD.callable_name, OLD.callable_description,
        OLD.input_schema, OLD.output_schema, OLD.risk_level, OLD.side_effect_level,
        OLD.requires_confirmation, OLD.checksum, OLD.published_by, OLD.published_at
    ) OR OLD.retired_at IS NOT NULL OR NEW.retired_at IS NULL THEN
        RAISE EXCEPTION 'capability releases are immutable except for one-way retirement'
            USING ERRCODE = '55000';
    END IF;
    IF EXISTS (SELECT 1 FROM capabilities WHERE active_release_id = OLD.id) THEN
        RAISE EXCEPTION 'active capability release cannot be retired'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER capability_releases_immutable
BEFORE UPDATE OR DELETE ON capability_releases
FOR EACH ROW EXECUTE FUNCTION enforce_capability_release_immutability();


-- ##########################################################################
-- Source: 000012_agent_capability_bindings.up.sql
-- ##########################################################################

CREATE TABLE agent_capability_bindings (
    workspace_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    capability_id UUID NOT NULL,
    version_policy TEXT NOT NULL,
    pinned_release_id UUID,
    connection_id UUID,
    execution_policy_id UUID,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    config_overrides JSONB NOT NULL DEFAULT '{}'::JSONB,
    bound_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    lock_version BIGINT NOT NULL DEFAULT 1,
    PRIMARY KEY (agent_id, capability_id),
    CONSTRAINT agent_capability_bindings_workspace_agent_capability_key
        UNIQUE (workspace_id, agent_id, capability_id),
    CONSTRAINT agent_capability_bindings_workspace_agent_fk
        FOREIGN KEY (workspace_id, agent_id)
        REFERENCES agents (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT agent_capability_bindings_workspace_capability_fk
        FOREIGN KEY (workspace_id, capability_id)
        REFERENCES capabilities (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT agent_capability_bindings_pinned_release_fk
        FOREIGN KEY (workspace_id, capability_id, pinned_release_id)
        REFERENCES capability_releases (workspace_id, capability_id, id) ON DELETE RESTRICT,
    CONSTRAINT agent_capability_bindings_connection_fk
        FOREIGN KEY (workspace_id, connection_id)
        REFERENCES service_connections (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT agent_capability_bindings_bound_by_fk
        FOREIGN KEY (bound_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT agent_capability_bindings_version_policy_check
        CHECK (version_policy IN ('FOLLOW_ACTIVE', 'PINNED')),
    CONSTRAINT agent_capability_bindings_pinned_policy_check CHECK (
        (version_policy = 'FOLLOW_ACTIVE' AND pinned_release_id IS NULL)
        OR (version_policy = 'PINNED' AND pinned_release_id IS NOT NULL)
    ),
    CONSTRAINT agent_capability_bindings_config_object_check
        CHECK (jsonb_typeof(config_overrides) = 'object'),
    CONSTRAINT agent_capability_bindings_timestamps_check CHECK (updated_at >= created_at),
    CONSTRAINT agent_capability_bindings_lock_version_check CHECK (lock_version > 0)
);

CREATE INDEX agent_capability_bindings_workspace_agent_enabled_idx
    ON agent_capability_bindings (workspace_id, agent_id, enabled, capability_id);
CREATE INDEX agent_capability_bindings_workspace_capability_idx
    ON agent_capability_bindings (workspace_id, capability_id, agent_id);
CREATE INDEX agent_capability_bindings_workspace_connection_idx
    ON agent_capability_bindings (workspace_id, connection_id, agent_id)
    WHERE connection_id IS NOT NULL;


-- ##########################################################################
-- Source: 000013_tools.up.sql
-- ##########################################################################

ALTER TABLE provider_assets
    ADD CONSTRAINT provider_assets_workspace_provider_id_key
    UNIQUE (workspace_id, provider_id, id);

ALTER TABLE service_connections
    ADD CONSTRAINT service_connections_workspace_provider_id_key
    UNIQUE (workspace_id, provider_id, id);

CREATE TABLE tools (
    capability_id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    provider_id UUID NOT NULL,
    source_asset_id UUID,
    default_connection_id UUID,
    source_endpoint_id UUID,
    CONSTRAINT tools_workspace_capability_key UNIQUE (workspace_id, capability_id),
    CONSTRAINT tools_workspace_capability_provider_key
        UNIQUE (workspace_id, capability_id, provider_id),
    CONSTRAINT tools_workspace_capability_fk
        FOREIGN KEY (workspace_id, capability_id)
        REFERENCES capabilities (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT tools_workspace_provider_fk
        FOREIGN KEY (workspace_id, provider_id)
        REFERENCES capability_providers (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT tools_source_asset_fk
        FOREIGN KEY (workspace_id, provider_id, source_asset_id)
        REFERENCES provider_assets (workspace_id, provider_id, id) ON DELETE RESTRICT,
    CONSTRAINT tools_default_connection_fk
        FOREIGN KEY (workspace_id, provider_id, default_connection_id)
        REFERENCES service_connections (workspace_id, provider_id, id) ON DELETE RESTRICT
);

CREATE INDEX tools_workspace_provider_idx
    ON tools (workspace_id, provider_id, capability_id);
CREATE INDEX tools_workspace_source_asset_idx
    ON tools (workspace_id, source_asset_id)
    WHERE source_asset_id IS NOT NULL;

CREATE FUNCTION enforce_tool_capability_kind()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM capabilities
        WHERE workspace_id = NEW.workspace_id
          AND id = NEW.capability_id
          AND kind = 'TOOL'
          AND deleted_at IS NULL
    ) THEN
        RAISE EXCEPTION 'tool specialization requires an active TOOL capability identity'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER tools_capability_kind_integrity
BEFORE INSERT OR UPDATE OF workspace_id, capability_id ON tools
FOR EACH ROW EXECUTE FUNCTION enforce_tool_capability_kind();

CREATE TABLE tool_versions (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    capability_id UUID NOT NULL,
    version_no INTEGER NOT NULL,
    lifecycle_status TEXT NOT NULL DEFAULT 'DRAFT',
    executor_type TEXT NOT NULL,
    provider_id UUID NOT NULL,
    provider_asset_id UUID,
    default_connection_id UUID,
    handler_key TEXT,
    execution_profile_id UUID,
    action_schema_version TEXT NOT NULL,
    action_config JSONB NOT NULL,
    input_schema JSONB NOT NULL,
    output_schema JSONB NOT NULL,
    error_mappings JSONB NOT NULL DEFAULT '{}'::JSONB,
    runtime_policy JSONB NOT NULL DEFAULT '{}'::JSONB,
    risk_level TEXT NOT NULL,
    side_effect_level TEXT NOT NULL,
    requires_confirmation BOOLEAN NOT NULL DEFAULT FALSE,
    checksum CHAR(64) NOT NULL,
    created_by UUID NOT NULL,
    updated_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    published_at TIMESTAMPTZ,
    lock_version BIGINT NOT NULL DEFAULT 1,
    CONSTRAINT tool_versions_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT tool_versions_workspace_capability_id_key
        UNIQUE (workspace_id, capability_id, id),
    CONSTRAINT tool_versions_capability_version_key UNIQUE (capability_id, version_no),
    CONSTRAINT tool_versions_tool_provider_fk
        FOREIGN KEY (workspace_id, capability_id, provider_id)
        REFERENCES tools (workspace_id, capability_id, provider_id) ON DELETE RESTRICT,
    CONSTRAINT tool_versions_provider_asset_fk
        FOREIGN KEY (workspace_id, provider_id, provider_asset_id)
        REFERENCES provider_assets (workspace_id, provider_id, id) ON DELETE RESTRICT,
    CONSTRAINT tool_versions_default_connection_fk
        FOREIGN KEY (workspace_id, provider_id, default_connection_id)
        REFERENCES service_connections (workspace_id, provider_id, id) ON DELETE RESTRICT,
    CONSTRAINT tool_versions_created_by_fk
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT tool_versions_updated_by_fk
        FOREIGN KEY (updated_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT tool_versions_version_no_check CHECK (version_no > 0),
    CONSTRAINT tool_versions_lifecycle_status_check
        CHECK (lifecycle_status IN ('DRAFT', 'REVIEW', 'TESTED', 'PUBLISHED')),
    CONSTRAINT tool_versions_http_executor_check CHECK (executor_type = 'HTTP'),
    CONSTRAINT tool_versions_http_only_fields_check
        CHECK (handler_key IS NULL AND execution_profile_id IS NULL),
    CONSTRAINT tool_versions_action_schema_version_not_blank
        CHECK (length(btrim(action_schema_version)) > 0),
    CONSTRAINT tool_versions_action_config_object_check
        CHECK (jsonb_typeof(action_config) = 'object'),
    CONSTRAINT tool_versions_input_schema_object_check
        CHECK (jsonb_typeof(input_schema) = 'object'),
    CONSTRAINT tool_versions_output_schema_object_check
        CHECK (jsonb_typeof(output_schema) = 'object'),
    CONSTRAINT tool_versions_error_mappings_object_check
        CHECK (jsonb_typeof(error_mappings) = 'object'),
    CONSTRAINT tool_versions_runtime_policy_object_check
        CHECK (jsonb_typeof(runtime_policy) = 'object'),
    CONSTRAINT tool_versions_risk_level_check
        CHECK (risk_level IN ('LOW', 'MEDIUM', 'HIGH', 'CRITICAL')),
    CONSTRAINT tool_versions_side_effect_level_check
        CHECK (side_effect_level IN ('NONE', 'READ', 'WRITE', 'IRREVERSIBLE')),
    CONSTRAINT tool_versions_checksum_check CHECK (checksum ~ '^[0-9a-f]{64}$'),
    CONSTRAINT tool_versions_publish_state_check CHECK (
        (lifecycle_status = 'PUBLISHED' AND published_at IS NOT NULL)
        OR (lifecycle_status <> 'PUBLISHED' AND published_at IS NULL)
    ),
    CONSTRAINT tool_versions_publish_time_check
        CHECK (published_at IS NULL OR published_at >= created_at),
    CONSTRAINT tool_versions_timestamps_check CHECK (updated_at >= created_at),
    CONSTRAINT tool_versions_lock_version_check CHECK (lock_version > 0)
);

CREATE INDEX tool_versions_workspace_capability_version_idx
    ON tool_versions (workspace_id, capability_id, version_no DESC, id);
CREATE INDEX tool_versions_workspace_status_updated_idx
    ON tool_versions (workspace_id, lifecycle_status, updated_at DESC, id);

CREATE FUNCTION enforce_published_tool_version_immutability()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.lifecycle_status = 'PUBLISHED' THEN
        RAISE EXCEPTION 'published tool versions are immutable'
            USING ERRCODE = '55000';
    END IF;
    RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END;
$$;

CREATE TRIGGER published_tool_versions_immutable
BEFORE UPDATE OR DELETE ON tool_versions
FOR EACH ROW EXECUTE FUNCTION enforce_published_tool_version_immutability();

CREATE TABLE tool_tests (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    tool_version_id UUID NOT NULL,
    status TEXT NOT NULL,
    connectivity_passed BOOLEAN NOT NULL,
    response_schema_passed BOOLEAN NOT NULL,
    error_mapping_passed BOOLEAN NOT NULL,
    runtime_policy_passed BOOLEAN NOT NULL,
    request_summary JSONB NOT NULL DEFAULT '{}'::JSONB,
    response_summary JSONB NOT NULL DEFAULT '{}'::JSONB,
    latency_ms INTEGER,
    error_code TEXT,
    raw_object_id UUID,
    tested_by UUID NOT NULL,
    tested_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT tool_tests_workspace_version_fk
        FOREIGN KEY (workspace_id, tool_version_id)
        REFERENCES tool_versions (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT tool_tests_tested_by_fk
        FOREIGN KEY (tested_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT tool_tests_status_check CHECK (status IN ('SUCCEEDED', 'FAILED')),
    CONSTRAINT tool_tests_result_consistency_check CHECK (
        (status = 'SUCCEEDED' AND connectivity_passed AND response_schema_passed
            AND error_mapping_passed AND runtime_policy_passed AND error_code IS NULL)
        OR status = 'FAILED'
    ),
    CONSTRAINT tool_tests_request_summary_object_check
        CHECK (jsonb_typeof(request_summary) = 'object'),
    CONSTRAINT tool_tests_response_summary_object_check
        CHECK (jsonb_typeof(response_summary) = 'object'),
    CONSTRAINT tool_tests_latency_check CHECK (latency_ms IS NULL OR latency_ms >= 0),
    CONSTRAINT tool_tests_failed_error_check
        CHECK (status <> 'FAILED' OR error_code IS NOT NULL)
);

CREATE INDEX tool_tests_workspace_version_tested_idx
    ON tool_tests (workspace_id, tool_version_id, tested_at DESC, id);
CREATE INDEX tool_tests_workspace_status_tested_idx
    ON tool_tests (workspace_id, status, tested_at DESC, id);

CREATE FUNCTION reject_tool_test_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'tool test records are immutable and permanently retained'
        USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER tool_tests_immutable
BEFORE UPDATE OR DELETE ON tool_tests
FOR EACH ROW EXECUTE FUNCTION reject_tool_test_mutation();


-- ##########################################################################
-- Source: 000014_openapi_imports.up.sql
-- ##########################################################################

CREATE TABLE openapi_imports (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    provider_id UUID,
    connection_id UUID,
    source_type TEXT NOT NULL,
    source_uri TEXT,
    file_name TEXT NOT NULL,
    raw_object_id UUID NOT NULL,
    content_sha256 CHAR(64) NOT NULL,
    parser_version TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'PENDING',
    total_endpoints INTEGER NOT NULL DEFAULT 0,
    ready_endpoints INTEGER NOT NULL DEFAULT 0,
    issue_count INTEGER NOT NULL DEFAULT 0,
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT openapi_imports_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT openapi_imports_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT openapi_imports_workspace_provider_fk
        FOREIGN KEY (workspace_id, provider_id)
        REFERENCES capability_providers (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT openapi_imports_workspace_provider_connection_fk
        FOREIGN KEY (workspace_id, provider_id, connection_id)
        REFERENCES service_connections (workspace_id, provider_id, id) ON DELETE RESTRICT,
    CONSTRAINT openapi_imports_created_by_fk
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT openapi_imports_source_type_check
        CHECK (source_type IN ('FILE', 'URL', 'RAW')),
    CONSTRAINT openapi_imports_source_uri_check CHECK (
        (source_type = 'URL' AND source_uri IS NOT NULL AND length(btrim(source_uri)) > 0)
        OR (source_type <> 'URL' AND source_uri IS NULL)
    ),
    CONSTRAINT openapi_imports_connection_provider_check
        CHECK (connection_id IS NULL OR provider_id IS NOT NULL),
    CONSTRAINT openapi_imports_file_name_not_blank
        CHECK (length(btrim(file_name)) > 0),
    CONSTRAINT openapi_imports_content_sha256_check
        CHECK (content_sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT openapi_imports_parser_version_not_blank
        CHECK (length(btrim(parser_version)) > 0),
    CONSTRAINT openapi_imports_status_check
        CHECK (status IN ('PENDING', 'PARSING', 'SUCCEEDED', 'FAILED')),
    CONSTRAINT openapi_imports_total_endpoints_check CHECK (total_endpoints >= 0),
    CONSTRAINT openapi_imports_ready_endpoints_check
        CHECK (ready_endpoints >= 0 AND ready_endpoints <= total_endpoints),
    CONSTRAINT openapi_imports_issue_count_check CHECK (issue_count >= 0),
    CONSTRAINT openapi_imports_timestamps_check CHECK (updated_at >= created_at)
);

CREATE INDEX openapi_imports_workspace_status_created_idx
    ON openapi_imports (workspace_id, status, created_at DESC, id);
CREATE INDEX openapi_imports_workspace_checksum_created_idx
    ON openapi_imports (workspace_id, content_sha256, created_at DESC, id);
CREATE INDEX openapi_imports_workspace_provider_created_idx
    ON openapi_imports (workspace_id, provider_id, created_at DESC, id)
    WHERE provider_id IS NOT NULL;

CREATE TABLE openapi_endpoints (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    import_id UUID NOT NULL,
    method TEXT NOT NULL,
    path TEXT NOT NULL,
    operation_id TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    input_schema JSONB NOT NULL DEFAULT '{}'::JSONB,
    output_schema JSONB NOT NULL DEFAULT '{}'::JSONB,
    issues JSONB NOT NULL DEFAULT '[]'::JSONB,
    ready BOOLEAN NOT NULL DEFAULT FALSE,
    generated_capability_id UUID,
    CONSTRAINT openapi_endpoints_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT openapi_endpoints_import_method_path_key UNIQUE (import_id, method, path),
    CONSTRAINT openapi_endpoints_workspace_import_fk
        FOREIGN KEY (workspace_id, import_id)
        REFERENCES openapi_imports (workspace_id, id) ON DELETE CASCADE,
    CONSTRAINT openapi_endpoints_generated_capability_fk
        FOREIGN KEY (workspace_id, generated_capability_id)
        REFERENCES capabilities (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT openapi_endpoints_method_check
        CHECK (method IN ('CONNECT', 'DELETE', 'GET', 'HEAD', 'OPTIONS', 'PATCH', 'POST', 'PUT', 'TRACE')),
    CONSTRAINT openapi_endpoints_path_check
        CHECK (path ~ '^/[^[:space:]]*$'),
    CONSTRAINT openapi_endpoints_input_schema_object_check
        CHECK (jsonb_typeof(input_schema) = 'object'),
    CONSTRAINT openapi_endpoints_output_schema_object_check
        CHECK (jsonb_typeof(output_schema) = 'object'),
    CONSTRAINT openapi_endpoints_issues_array_check
        CHECK (jsonb_typeof(issues) = 'array')
);

CREATE INDEX openapi_endpoints_workspace_import_ready_idx
    ON openapi_endpoints (workspace_id, import_id, ready, method, path, id);
CREATE INDEX openapi_endpoints_workspace_generated_capability_idx
    ON openapi_endpoints (workspace_id, generated_capability_id, id)
    WHERE generated_capability_id IS NOT NULL;

-- The pre-normalization compatibility column had no referential integrity.
UPDATE tools
SET source_endpoint_id = NULL
WHERE source_endpoint_id IS NOT NULL;

ALTER TABLE tools
    ADD CONSTRAINT tools_source_endpoint_fk
    FOREIGN KEY (workspace_id, source_endpoint_id)
    REFERENCES openapi_endpoints (workspace_id, id) ON DELETE RESTRICT;


-- ##########################################################################
-- Source: 000015_openapi_source_revision.up.sql
-- ##########################################################################

ALTER TABLE openapi_imports
    ADD COLUMN source_revision TEXT,
    ADD CONSTRAINT openapi_imports_source_revision_not_blank
        CHECK (source_revision IS NULL OR length(btrim(source_revision)) > 0);

CREATE INDEX openapi_imports_workspace_provider_revision_idx
    ON openapi_imports (
        workspace_id, provider_id, source_revision, content_sha256, created_at DESC, id
    )
    WHERE provider_id IS NOT NULL AND source_revision IS NOT NULL;


-- ##########################################################################
-- Source: 000016_workflows.up.sql
-- ##########################################################################

CREATE TABLE workflows (
    capability_id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    current_draft_id UUID NOT NULL,
    active_revision_id UUID,
    latest_compilation_id UUID,
    CONSTRAINT workflows_workspace_capability_key UNIQUE (workspace_id, capability_id),
    CONSTRAINT workflows_workspace_capability_fk
        FOREIGN KEY (workspace_id, capability_id)
        REFERENCES capabilities (workspace_id, id) ON DELETE RESTRICT
);

CREATE FUNCTION enforce_workflow_capability_kind()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM capabilities
        WHERE workspace_id = NEW.workspace_id
          AND id = NEW.capability_id
          AND kind = 'WORKFLOW'
          AND deleted_at IS NULL
    ) THEN
        RAISE EXCEPTION 'workflow specialization requires an active WORKFLOW capability identity'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER workflows_capability_kind_integrity
BEFORE INSERT OR UPDATE OF workspace_id, capability_id ON workflows
FOR EACH ROW EXECUTE FUNCTION enforce_workflow_capability_kind();

CREATE TABLE workflow_drafts (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    capability_id UUID NOT NULL,
    draft_version BIGINT NOT NULL DEFAULT 1,
    schema_version TEXT NOT NULL,
    graph JSONB NOT NULL,
    graph_hash CHAR(64) NOT NULL,
    updated_by UUID NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    lock_version BIGINT NOT NULL DEFAULT 1,
    CONSTRAINT workflow_drafts_workspace_capability_id_key
        UNIQUE (workspace_id, capability_id, id),
    CONSTRAINT workflow_drafts_capability_key UNIQUE (capability_id),
    CONSTRAINT workflow_drafts_workflow_fk
        FOREIGN KEY (workspace_id, capability_id)
        REFERENCES workflows (workspace_id, capability_id) ON DELETE CASCADE,
    CONSTRAINT workflow_drafts_updated_by_fk
        FOREIGN KEY (updated_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT workflow_drafts_version_check CHECK (draft_version > 0),
    CONSTRAINT workflow_drafts_schema_version_not_blank
        CHECK (length(btrim(schema_version)) > 0),
    CONSTRAINT workflow_drafts_graph_object_check CHECK (jsonb_typeof(graph) = 'object'),
    CONSTRAINT workflow_drafts_graph_hash_check CHECK (graph_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT workflow_drafts_lock_version_check CHECK (lock_version > 0)
);

CREATE INDEX workflow_drafts_workspace_updated_idx
    ON workflow_drafts (workspace_id, updated_at DESC, id);

CREATE TABLE workflow_compilations (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    capability_id UUID NOT NULL,
    draft_id UUID NOT NULL,
    draft_version BIGINT NOT NULL,
    graph_hash CHAR(64) NOT NULL,
    compiler_version TEXT NOT NULL,
    status TEXT NOT NULL,
    spec JSONB NOT NULL,
    plan JSONB NOT NULL,
    issues JSONB NOT NULL DEFAULT '[]'::JSONB,
    plan_hash CHAR(64) NOT NULL,
    compiled_by UUID NOT NULL,
    compiled_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT workflow_compilations_workspace_capability_id_key
        UNIQUE (workspace_id, capability_id, id),
    CONSTRAINT workflow_compilations_workspace_draft_fk
        FOREIGN KEY (workspace_id, capability_id, draft_id)
        REFERENCES workflow_drafts (workspace_id, capability_id, id) ON DELETE RESTRICT,
    CONSTRAINT workflow_compilations_compiled_by_fk
        FOREIGN KEY (compiled_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT workflow_compilations_draft_version_check CHECK (draft_version > 0),
    CONSTRAINT workflow_compilations_graph_hash_check CHECK (graph_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT workflow_compilations_compiler_version_not_blank
        CHECK (length(btrim(compiler_version)) > 0),
    CONSTRAINT workflow_compilations_status_check
        CHECK (status IN ('VALID', 'INVALID', 'FAILED')),
    CONSTRAINT workflow_compilations_spec_object_check CHECK (jsonb_typeof(spec) = 'object'),
    CONSTRAINT workflow_compilations_plan_object_check CHECK (jsonb_typeof(plan) = 'object'),
    CONSTRAINT workflow_compilations_issues_array_check CHECK (jsonb_typeof(issues) = 'array'),
    CONSTRAINT workflow_compilations_plan_hash_check CHECK (plan_hash ~ '^[0-9a-f]{64}$')
);

CREATE INDEX workflow_compilations_workspace_capability_compiled_idx
    ON workflow_compilations (workspace_id, capability_id, compiled_at DESC, id);
CREATE INDEX workflow_compilations_workspace_status_compiled_idx
    ON workflow_compilations (workspace_id, status, compiled_at DESC, id);

CREATE FUNCTION reject_workflow_compilation_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'workflow compilations are immutable'
        USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER workflow_compilations_immutable
BEFORE UPDATE OR DELETE ON workflow_compilations
FOR EACH ROW EXECUTE FUNCTION reject_workflow_compilation_mutation();

CREATE TABLE workflow_revisions (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    capability_id UUID NOT NULL,
    revision_no INTEGER NOT NULL,
    source_compilation_id UUID NOT NULL,
    draft_snapshot JSONB NOT NULL,
    spec_snapshot JSONB NOT NULL,
    plan_snapshot JSONB NOT NULL,
    plan_hash CHAR(64) NOT NULL,
    status TEXT NOT NULL DEFAULT 'PUBLISHED',
    publish_note TEXT NOT NULL DEFAULT '',
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    activated_at TIMESTAMPTZ,
    retired_at TIMESTAMPTZ,
    CONSTRAINT workflow_revisions_workspace_capability_id_key
        UNIQUE (workspace_id, capability_id, id),
    CONSTRAINT workflow_revisions_capability_revision_key
        UNIQUE (capability_id, revision_no),
    CONSTRAINT workflow_revisions_source_compilation_fk
        FOREIGN KEY (workspace_id, capability_id, source_compilation_id)
        REFERENCES workflow_compilations (workspace_id, capability_id, id) ON DELETE RESTRICT,
    CONSTRAINT workflow_revisions_created_by_fk
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT workflow_revisions_revision_no_check CHECK (revision_no > 0),
    CONSTRAINT workflow_revisions_draft_snapshot_object_check
        CHECK (jsonb_typeof(draft_snapshot) = 'object'),
    CONSTRAINT workflow_revisions_spec_snapshot_object_check
        CHECK (jsonb_typeof(spec_snapshot) = 'object'),
    CONSTRAINT workflow_revisions_plan_snapshot_object_check
        CHECK (jsonb_typeof(plan_snapshot) = 'object'),
    CONSTRAINT workflow_revisions_plan_hash_check CHECK (plan_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT workflow_revisions_status_check CHECK (status IN ('PUBLISHED', 'RETIRED')),
    CONSTRAINT workflow_revisions_retirement_state_check CHECK (
        (status = 'PUBLISHED' AND retired_at IS NULL)
        OR (status = 'RETIRED' AND retired_at IS NOT NULL)
    ),
    CONSTRAINT workflow_revisions_activated_at_check
        CHECK (activated_at IS NULL OR activated_at >= created_at),
    CONSTRAINT workflow_revisions_retired_at_check
        CHECK (retired_at IS NULL OR retired_at >= created_at)
);

CREATE INDEX workflow_revisions_workspace_capability_revision_idx
    ON workflow_revisions (workspace_id, capability_id, revision_no DESC, id);

CREATE FUNCTION reject_workflow_revision_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'workflow revisions are immutable'
        USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER workflow_revisions_immutable
BEFORE UPDATE OR DELETE ON workflow_revisions
FOR EACH ROW EXECUTE FUNCTION reject_workflow_revision_mutation();

CREATE TABLE workflow_trial_runs (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    capability_id UUID NOT NULL,
    compilation_id UUID NOT NULL,
    execution_id UUID NOT NULL,
    status TEXT NOT NULL DEFAULT 'PENDING',
    input_hash CHAR(64) NOT NULL,
    started_by UUID NOT NULL,
    started_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at TIMESTAMPTZ,
    CONSTRAINT workflow_trial_runs_workspace_compilation_fk
        FOREIGN KEY (workspace_id, capability_id, compilation_id)
        REFERENCES workflow_compilations (workspace_id, capability_id, id) ON DELETE RESTRICT,
    CONSTRAINT workflow_trial_runs_started_by_fk
        FOREIGN KEY (started_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT workflow_trial_runs_status_check
        CHECK (status IN ('PENDING', 'RUNNING', 'SUCCEEDED', 'FAILED', 'CANCELLED')),
    CONSTRAINT workflow_trial_runs_input_hash_check CHECK (input_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT workflow_trial_runs_finish_state_check CHECK (
        (status IN ('PENDING', 'RUNNING') AND finished_at IS NULL)
        OR (status IN ('SUCCEEDED', 'FAILED', 'CANCELLED') AND finished_at IS NOT NULL)
    ),
    CONSTRAINT workflow_trial_runs_finished_at_check
        CHECK (finished_at IS NULL OR finished_at >= started_at)
);

CREATE INDEX workflow_trial_runs_workspace_capability_started_idx
    ON workflow_trial_runs (workspace_id, capability_id, started_at DESC, id);
CREATE INDEX workflow_trial_runs_workspace_status_started_idx
    ON workflow_trial_runs (workspace_id, status, started_at DESC, id);

ALTER TABLE workflows
    ADD CONSTRAINT workflows_current_draft_fk
        FOREIGN KEY (workspace_id, capability_id, current_draft_id)
        REFERENCES workflow_drafts (workspace_id, capability_id, id)
        ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
    ADD CONSTRAINT workflows_active_revision_fk
        FOREIGN KEY (workspace_id, capability_id, active_revision_id)
        REFERENCES workflow_revisions (workspace_id, capability_id, id)
        ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
    ADD CONSTRAINT workflows_latest_compilation_fk
        FOREIGN KEY (workspace_id, capability_id, latest_compilation_id)
        REFERENCES workflow_compilations (workspace_id, capability_id, id)
        ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED;

CREATE FUNCTION enforce_workflow_active_revision()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.active_revision_id IS NULL THEN
        RETURN NEW;
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM workflow_revisions
        WHERE workspace_id = NEW.workspace_id
          AND capability_id = NEW.capability_id
          AND id = NEW.active_revision_id
          AND status = 'PUBLISHED'
          AND retired_at IS NULL
    ) THEN
        RAISE EXCEPTION 'active workflow revision must be a published revision of the same workflow'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER workflows_active_revision_integrity
BEFORE INSERT OR UPDATE OF active_revision_id, workspace_id, capability_id ON workflows
FOR EACH ROW EXECUTE FUNCTION enforce_workflow_active_revision();


-- ##########################################################################
-- Source: 000017_chat.up.sql
-- ##########################################################################

CREATE TABLE chat_sessions (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'ACTIVE',
    created_by UUID NOT NULL,
    latest_run_id UUID,
    pending_confirmation_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    lock_version BIGINT NOT NULL DEFAULT 1,
    CONSTRAINT chat_sessions_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT chat_sessions_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT chat_sessions_workspace_agent_fk
        FOREIGN KEY (workspace_id, agent_id)
        REFERENCES agents (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT chat_sessions_created_by_fk
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT chat_sessions_status_check CHECK (status IN ('ACTIVE', 'ARCHIVED')),
    CONSTRAINT chat_sessions_lock_version_check CHECK (lock_version > 0),
    CONSTRAINT chat_sessions_timestamps_check CHECK (updated_at >= created_at)
);

CREATE INDEX chat_sessions_workspace_creator_updated_idx
    ON chat_sessions (workspace_id, created_by, status, updated_at DESC, id);
CREATE INDEX chat_sessions_workspace_agent_updated_idx
    ON chat_sessions (workspace_id, agent_id, updated_at DESC, id);

CREATE FUNCTION reject_chat_session_delete()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'chat sessions are permanently retained and cannot be deleted'
        USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER chat_sessions_no_delete
BEFORE DELETE ON chat_sessions
FOR EACH ROW EXECUTE FUNCTION reject_chat_session_delete();

CREATE TABLE chat_messages (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    session_id UUID NOT NULL,
    role TEXT NOT NULL,
    content TEXT,
    content_object_id UUID,
    content_sha256 CHAR(64) NOT NULL,
    status TEXT NOT NULL,
    run_id UUID,
    confirmation_id UUID,
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT chat_messages_workspace_session_id_key UNIQUE (workspace_id, session_id, id),
    CONSTRAINT chat_messages_workspace_session_fk
        FOREIGN KEY (workspace_id, session_id)
        REFERENCES chat_sessions (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT chat_messages_created_by_fk
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT chat_messages_role_check
        CHECK (role IN ('USER', 'ASSISTANT', 'SYSTEM', 'TOOL')),
    CONSTRAINT chat_messages_content_check CHECK (
        (content IS NOT NULL AND length(content) > 0) OR content_object_id IS NOT NULL
    ),
    CONSTRAINT chat_messages_sha256_check
        CHECK (content_sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT chat_messages_status_check CHECK (
        status IN (
            'RECEIVED', 'PROCESSING', 'NEEDS_INPUT', 'MATCHED_NONE',
            'PENDING_CONFIRMATION', 'EXECUTED', 'FAILED'
        )
    ),
    CONSTRAINT chat_messages_user_actor_check
        CHECK (role <> 'USER' OR created_by IS NOT NULL),
    CONSTRAINT chat_messages_confirmation_status_check CHECK (
        status <> 'PENDING_CONFIRMATION' OR confirmation_id IS NOT NULL
    )
);

CREATE INDEX chat_messages_workspace_session_created_idx
    ON chat_messages (workspace_id, session_id, created_at, id);
CREATE INDEX chat_messages_workspace_run_created_idx
    ON chat_messages (workspace_id, run_id, created_at, id)
    WHERE run_id IS NOT NULL;
CREATE INDEX chat_messages_workspace_confirmation_idx
    ON chat_messages (workspace_id, confirmation_id, id)
    WHERE confirmation_id IS NOT NULL;

CREATE FUNCTION enforce_chat_message_permanent_retention()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'chat messages are permanently retained and cannot be deleted'
            USING ERRCODE = '55000';
    END IF;
    IF ROW(
        NEW.id, NEW.workspace_id, NEW.session_id, NEW.role, NEW.content,
        NEW.content_object_id, NEW.content_sha256, NEW.created_by, NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.session_id, OLD.role, OLD.content,
        OLD.content_object_id, OLD.content_sha256, OLD.created_by, OLD.created_at
    ) THEN
        RAISE EXCEPTION 'chat message original content and identity are immutable'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER chat_messages_permanent_retention
BEFORE UPDATE OR DELETE ON chat_messages
FOR EACH ROW EXECUTE FUNCTION enforce_chat_message_permanent_retention();


-- ##########################################################################
-- Source: 000018_agent_runs.up.sql
-- ##########################################################################

ALTER TABLE capability_releases
    ADD CONSTRAINT capability_releases_workspace_id_key UNIQUE (workspace_id, id);

CREATE TABLE agent_runs (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    session_id UUID,
    agent_id UUID NOT NULL,
    status TEXT NOT NULL DEFAULT 'PENDING',
    trigger_type TEXT NOT NULL,
    triggered_by_type TEXT NOT NULL,
    triggered_by_id UUID NOT NULL,
    trace_id TEXT NOT NULL,
    model_snapshot JSONB NOT NULL,
    capability_snapshot JSONB NOT NULL,
    context_policy_snapshot JSONB NOT NULL DEFAULT '{}'::JSONB,
    input_summary JSONB NOT NULL DEFAULT '{}'::JSONB,
    output_summary JSONB NOT NULL DEFAULT '{}'::JSONB,
    error_code TEXT,
    started_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at TIMESTAMPTZ,
    lock_version BIGINT NOT NULL DEFAULT 1,
    CONSTRAINT agent_runs_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT agent_runs_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT agent_runs_workspace_session_fk
        FOREIGN KEY (workspace_id, session_id)
        REFERENCES chat_sessions (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT agent_runs_workspace_agent_fk
        FOREIGN KEY (workspace_id, agent_id)
        REFERENCES agents (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT agent_runs_status_check CHECK (
        status IN (
            'PENDING', 'RUNNING', 'WAITING_CONFIRMATION',
            'SUCCEEDED', 'FAILED', 'CANCELLED'
        )
    ),
    CONSTRAINT agent_runs_trigger_type_not_blank CHECK (length(btrim(trigger_type)) > 0),
    CONSTRAINT agent_runs_triggered_by_type_check
        CHECK (triggered_by_type IN ('USER', 'SERVICE_PRINCIPAL', 'SYSTEM')),
    CONSTRAINT agent_runs_trace_id_not_blank CHECK (length(btrim(trace_id)) > 0),
    CONSTRAINT agent_runs_model_snapshot_object_check
        CHECK (jsonb_typeof(model_snapshot) = 'object'),
    CONSTRAINT agent_runs_capability_snapshot_object_check
        CHECK (jsonb_typeof(capability_snapshot) = 'object'),
    CONSTRAINT agent_runs_context_snapshot_object_check
        CHECK (jsonb_typeof(context_policy_snapshot) = 'object'),
    CONSTRAINT agent_runs_input_summary_object_check
        CHECK (jsonb_typeof(input_summary) = 'object'),
    CONSTRAINT agent_runs_output_summary_object_check
        CHECK (jsonb_typeof(output_summary) = 'object'),
    CONSTRAINT agent_runs_finish_state_check CHECK (
        (status IN ('PENDING', 'RUNNING', 'WAITING_CONFIRMATION') AND finished_at IS NULL)
        OR (status IN ('SUCCEEDED', 'FAILED', 'CANCELLED') AND finished_at IS NOT NULL)
    ),
    CONSTRAINT agent_runs_finished_at_check
        CHECK (finished_at IS NULL OR finished_at >= started_at),
    CONSTRAINT agent_runs_error_state_check CHECK (
        (status = 'FAILED' AND error_code IS NOT NULL)
        OR (status <> 'FAILED' AND error_code IS NULL)
    ),
    CONSTRAINT agent_runs_lock_version_check CHECK (lock_version > 0)
);

CREATE INDEX agent_runs_workspace_started_idx
    ON agent_runs (workspace_id, started_at DESC, id);
CREATE INDEX agent_runs_workspace_status_started_idx
    ON agent_runs (workspace_id, status, started_at DESC, id);
CREATE INDEX agent_runs_workspace_session_started_idx
    ON agent_runs (workspace_id, session_id, started_at DESC, id)
    WHERE session_id IS NOT NULL;
CREATE INDEX agent_runs_trace_idx ON agent_runs (trace_id, id);

CREATE FUNCTION enforce_agent_run_permanent_snapshot()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'agent runs are permanently retained and cannot be deleted'
            USING ERRCODE = '55000';
    END IF;
    IF ROW(
        NEW.id, NEW.workspace_id, NEW.session_id, NEW.agent_id,
        NEW.trigger_type, NEW.triggered_by_type, NEW.triggered_by_id, NEW.trace_id,
        NEW.model_snapshot, NEW.capability_snapshot, NEW.context_policy_snapshot,
        NEW.input_summary, NEW.started_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.session_id, OLD.agent_id,
        OLD.trigger_type, OLD.triggered_by_type, OLD.triggered_by_id, OLD.trace_id,
        OLD.model_snapshot, OLD.capability_snapshot, OLD.context_policy_snapshot,
        OLD.input_summary, OLD.started_at
    ) THEN
        RAISE EXCEPTION 'agent run identity and start snapshots are immutable'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER agent_runs_permanent_snapshot
BEFORE UPDATE OR DELETE ON agent_runs
FOR EACH ROW EXECUTE FUNCTION enforce_agent_run_permanent_snapshot();

CREATE TABLE agent_run_steps (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    run_id UUID NOT NULL,
    sequence_no INTEGER NOT NULL,
    step_type TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'QUEUED',
    capability_release_id UUID,
    input_summary JSONB NOT NULL DEFAULT '{}'::JSONB,
    output_summary JSONB NOT NULL DEFAULT '{}'::JSONB,
    raw_object_id UUID,
    started_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at TIMESTAMPTZ,
    error_code TEXT,
    CONSTRAINT agent_run_steps_workspace_run_id_key UNIQUE (workspace_id, run_id, id),
    CONSTRAINT agent_run_steps_run_sequence_key UNIQUE (run_id, sequence_no),
    CONSTRAINT agent_run_steps_workspace_run_fk
        FOREIGN KEY (workspace_id, run_id)
        REFERENCES agent_runs (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT agent_run_steps_workspace_release_fk
        FOREIGN KEY (workspace_id, capability_release_id)
        REFERENCES capability_releases (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT agent_run_steps_sequence_check CHECK (sequence_no > 0),
    CONSTRAINT agent_run_steps_type_not_blank CHECK (length(btrim(step_type)) > 0),
    CONSTRAINT agent_run_steps_status_check CHECK (
        status IN (
            'QUEUED', 'RUNNING', 'WAITING_CONFIRMATION',
            'SUCCEEDED', 'FAILED', 'SKIPPED', 'CANCELLED'
        )
    ),
    CONSTRAINT agent_run_steps_input_summary_object_check
        CHECK (jsonb_typeof(input_summary) = 'object'),
    CONSTRAINT agent_run_steps_output_summary_object_check
        CHECK (jsonb_typeof(output_summary) = 'object'),
    CONSTRAINT agent_run_steps_finish_state_check CHECK (
        (status IN ('QUEUED', 'RUNNING', 'WAITING_CONFIRMATION') AND finished_at IS NULL)
        OR (status IN ('SUCCEEDED', 'FAILED', 'SKIPPED', 'CANCELLED') AND finished_at IS NOT NULL)
    ),
    CONSTRAINT agent_run_steps_finished_at_check
        CHECK (finished_at IS NULL OR finished_at >= started_at),
    CONSTRAINT agent_run_steps_error_state_check CHECK (
        (status = 'FAILED' AND error_code IS NOT NULL)
        OR (status <> 'FAILED' AND error_code IS NULL)
    )
);

CREATE INDEX agent_run_steps_workspace_run_sequence_idx
    ON agent_run_steps (workspace_id, run_id, sequence_no, id);
CREATE INDEX agent_run_steps_workspace_status_started_idx
    ON agent_run_steps (workspace_id, status, started_at DESC, id);

CREATE FUNCTION enforce_agent_run_step_permanent_evidence()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'agent run steps are permanently retained and cannot be deleted'
            USING ERRCODE = '55000';
    END IF;
    IF ROW(
        NEW.id, NEW.workspace_id, NEW.run_id, NEW.sequence_no, NEW.step_type,
        NEW.capability_release_id, NEW.input_summary, NEW.started_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.run_id, OLD.sequence_no, OLD.step_type,
        OLD.capability_release_id, OLD.input_summary, OLD.started_at
    ) THEN
        RAISE EXCEPTION 'agent run step identity and start evidence are immutable'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER agent_run_steps_permanent_evidence
BEFORE UPDATE OR DELETE ON agent_run_steps
FOR EACH ROW EXECUTE FUNCTION enforce_agent_run_step_permanent_evidence();

ALTER TABLE chat_sessions
    ADD CONSTRAINT chat_sessions_latest_run_fk
        FOREIGN KEY (workspace_id, latest_run_id)
        REFERENCES agent_runs (workspace_id, id)
        ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE chat_messages
    ADD CONSTRAINT chat_messages_run_fk
        FOREIGN KEY (workspace_id, run_id)
        REFERENCES agent_runs (workspace_id, id) ON DELETE RESTRICT;


-- ##########################################################################
-- Source: 000019_workflow_executions.up.sql
-- ##########################################################################

CREATE TABLE workflow_executions (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    workflow_id UUID NOT NULL,
    revision_id UUID NOT NULL,
    agent_run_id UUID,
    trigger_type TEXT NOT NULL,
    triggered_by_type TEXT NOT NULL,
    triggered_by_id UUID NOT NULL,
    trace_id TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'PENDING',
    input_summary JSONB NOT NULL DEFAULT '{}'::JSONB,
    output_summary JSONB NOT NULL DEFAULT '{}'::JSONB,
    error_code TEXT,
    started_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at TIMESTAMPTZ,
    lock_version BIGINT NOT NULL DEFAULT 1,
    CONSTRAINT workflow_executions_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT workflow_executions_workspace_workflow_fk
        FOREIGN KEY (workspace_id, workflow_id)
        REFERENCES workflows (workspace_id, capability_id) ON DELETE RESTRICT,
    CONSTRAINT workflow_executions_workspace_revision_fk
        FOREIGN KEY (workspace_id, workflow_id, revision_id)
        REFERENCES workflow_revisions (workspace_id, capability_id, id) ON DELETE RESTRICT,
    CONSTRAINT workflow_executions_workspace_agent_run_fk
        FOREIGN KEY (workspace_id, agent_run_id)
        REFERENCES agent_runs (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT workflow_executions_trigger_type_not_blank
        CHECK (length(btrim(trigger_type)) > 0),
    CONSTRAINT workflow_executions_triggered_by_type_check
        CHECK (triggered_by_type IN ('USER', 'SERVICE_PRINCIPAL', 'SYSTEM')),
    CONSTRAINT workflow_executions_trace_id_not_blank CHECK (length(btrim(trace_id)) > 0),
    CONSTRAINT workflow_executions_status_check CHECK (
        status IN (
            'PENDING', 'RUNNING', 'WAITING_CONFIRMATION',
            'SUCCEEDED', 'FAILED', 'CANCELLED'
        )
    ),
    CONSTRAINT workflow_executions_input_summary_object_check
        CHECK (jsonb_typeof(input_summary) = 'object'),
    CONSTRAINT workflow_executions_output_summary_object_check
        CHECK (jsonb_typeof(output_summary) = 'object'),
    CONSTRAINT workflow_executions_finish_state_check CHECK (
        (status IN ('PENDING', 'RUNNING', 'WAITING_CONFIRMATION') AND finished_at IS NULL)
        OR (status IN ('SUCCEEDED', 'FAILED', 'CANCELLED') AND finished_at IS NOT NULL)
    ),
    CONSTRAINT workflow_executions_finished_at_check
        CHECK (finished_at IS NULL OR finished_at >= started_at),
    CONSTRAINT workflow_executions_error_state_check CHECK (
        (status = 'FAILED' AND error_code IS NOT NULL)
        OR (status <> 'FAILED' AND error_code IS NULL)
    ),
    CONSTRAINT workflow_executions_lock_version_check CHECK (lock_version > 0)
);

CREATE INDEX workflow_executions_workspace_started_idx
    ON workflow_executions (workspace_id, started_at DESC, id);
CREATE INDEX workflow_executions_workspace_status_started_idx
    ON workflow_executions (workspace_id, status, started_at DESC, id);
CREATE INDEX workflow_executions_workspace_workflow_started_idx
    ON workflow_executions (workspace_id, workflow_id, started_at DESC, id);
CREATE INDEX workflow_executions_workspace_agent_run_started_idx
    ON workflow_executions (workspace_id, agent_run_id, started_at DESC, id)
    WHERE agent_run_id IS NOT NULL;
CREATE INDEX workflow_executions_trace_idx ON workflow_executions (trace_id, id);

CREATE FUNCTION enforce_workflow_execution_state()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'workflow executions are permanently retained and cannot be deleted'
            USING ERRCODE = '55000';
    END IF;
    IF ROW(
        NEW.id, NEW.workspace_id, NEW.workflow_id, NEW.revision_id, NEW.agent_run_id,
        NEW.trigger_type, NEW.triggered_by_type, NEW.triggered_by_id, NEW.trace_id,
        NEW.input_summary, NEW.started_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.workflow_id, OLD.revision_id, OLD.agent_run_id,
        OLD.trigger_type, OLD.triggered_by_type, OLD.triggered_by_id, OLD.trace_id,
        OLD.input_summary, OLD.started_at
    ) THEN
        RAISE EXCEPTION 'workflow execution identity and start evidence are immutable'
            USING ERRCODE = '55000';
    END IF;
    IF OLD.status IN ('SUCCEEDED', 'FAILED', 'CANCELLED') THEN
        RAISE EXCEPTION 'terminal workflow execution is immutable'
            USING ERRCODE = '55000';
    END IF;
    IF NEW.lock_version <> OLD.lock_version + 1 THEN
        RAISE EXCEPTION 'workflow execution update requires the next lock version'
            USING ERRCODE = '40001';
    END IF;
    IF NEW.status <> OLD.status AND NOT (
        (OLD.status = 'PENDING' AND NEW.status IN ('RUNNING', 'FAILED', 'CANCELLED'))
        OR (OLD.status = 'RUNNING' AND NEW.status IN (
            'WAITING_CONFIRMATION', 'SUCCEEDED', 'FAILED', 'CANCELLED'
        ))
        OR (OLD.status = 'WAITING_CONFIRMATION' AND NEW.status IN (
            'RUNNING', 'FAILED', 'CANCELLED'
        ))
    ) THEN
        RAISE EXCEPTION 'illegal workflow execution status transition from % to %',
            OLD.status, NEW.status USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER workflow_executions_state_guard
BEFORE UPDATE OR DELETE ON workflow_executions
FOR EACH ROW EXECUTE FUNCTION enforce_workflow_execution_state();

CREATE TABLE execution_steps (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    execution_id UUID NOT NULL,
    node_id TEXT NOT NULL,
    node_type TEXT NOT NULL,
    sequence_no INTEGER NOT NULL,
    status TEXT NOT NULL DEFAULT 'QUEUED',
    input_summary JSONB NOT NULL DEFAULT '{}'::JSONB,
    output_summary JSONB NOT NULL DEFAULT '{}'::JSONB,
    raw_object_id UUID,
    started_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at TIMESTAMPTZ,
    error_code TEXT,
    CONSTRAINT execution_steps_workspace_execution_id_key
        UNIQUE (workspace_id, execution_id, id),
    CONSTRAINT execution_steps_execution_sequence_key UNIQUE (execution_id, sequence_no),
    CONSTRAINT execution_steps_workspace_execution_fk
        FOREIGN KEY (workspace_id, execution_id)
        REFERENCES workflow_executions (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT execution_steps_sequence_check CHECK (sequence_no > 0),
    CONSTRAINT execution_steps_status_check CHECK (
        status IN (
            'QUEUED', 'RUNNING', 'WAITING_CONFIRMATION',
            'SUCCEEDED', 'FAILED', 'SKIPPED', 'CANCELLED'
        )
    ),
    CONSTRAINT execution_steps_input_summary_object_check
        CHECK (jsonb_typeof(input_summary) = 'object'),
    CONSTRAINT execution_steps_output_summary_object_check
        CHECK (jsonb_typeof(output_summary) = 'object'),
    CONSTRAINT execution_steps_finish_state_check CHECK (
        (status IN ('QUEUED', 'RUNNING', 'WAITING_CONFIRMATION') AND finished_at IS NULL)
        OR (status IN ('SUCCEEDED', 'FAILED', 'SKIPPED', 'CANCELLED') AND finished_at IS NOT NULL)
    ),
    CONSTRAINT execution_steps_finished_at_check
        CHECK (finished_at IS NULL OR finished_at >= started_at),
    CONSTRAINT execution_steps_error_state_check CHECK (
        (status = 'FAILED' AND error_code IS NOT NULL)
        OR (status <> 'FAILED' AND error_code IS NULL)
    )
);

CREATE INDEX execution_steps_execution_sequence_idx
    ON execution_steps (execution_id, sequence_no, id);
CREATE INDEX execution_steps_workspace_status_started_idx
    ON execution_steps (workspace_id, status, started_at DESC, id);

CREATE FUNCTION enforce_execution_step_state()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'workflow execution steps are permanently retained and cannot be deleted'
            USING ERRCODE = '55000';
    END IF;
    IF ROW(
        NEW.id, NEW.workspace_id, NEW.execution_id, NEW.node_id, NEW.node_type,
        NEW.sequence_no, NEW.input_summary, NEW.started_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.execution_id, OLD.node_id, OLD.node_type,
        OLD.sequence_no, OLD.input_summary, OLD.started_at
    ) THEN
        RAISE EXCEPTION 'workflow execution step identity and start evidence are immutable'
            USING ERRCODE = '55000';
    END IF;
    IF OLD.status IN ('SUCCEEDED', 'FAILED', 'SKIPPED', 'CANCELLED') THEN
        RAISE EXCEPTION 'terminal workflow execution step is immutable'
            USING ERRCODE = '55000';
    END IF;
    IF NEW.status <> OLD.status AND NOT (
        (OLD.status = 'QUEUED' AND NEW.status IN ('RUNNING', 'FAILED', 'SKIPPED', 'CANCELLED'))
        OR (OLD.status = 'RUNNING' AND NEW.status IN (
            'WAITING_CONFIRMATION', 'SUCCEEDED', 'FAILED', 'SKIPPED', 'CANCELLED'
        ))
        OR (OLD.status = 'WAITING_CONFIRMATION' AND NEW.status IN (
            'RUNNING', 'FAILED', 'CANCELLED'
        ))
    ) THEN
        RAISE EXCEPTION 'illegal workflow execution step status transition from % to %',
            OLD.status, NEW.status USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER execution_steps_state_guard
BEFORE UPDATE OR DELETE ON execution_steps
FOR EACH ROW EXECUTE FUNCTION enforce_execution_step_state();

-- Existing trial rows used opaque UUIDs before workflow_executions existed and
-- cannot be linked safely. Old data is explicitly disposable for this refactor.
DELETE FROM workflow_trial_runs
WHERE NOT EXISTS (
    SELECT 1
    FROM workflow_executions
    WHERE workflow_executions.workspace_id = workflow_trial_runs.workspace_id
      AND workflow_executions.id = workflow_trial_runs.execution_id
);

ALTER TABLE workflow_trial_runs
    ADD CONSTRAINT workflow_trial_runs_execution_fk
        FOREIGN KEY (workspace_id, execution_id)
        REFERENCES workflow_executions (workspace_id, id) ON DELETE RESTRICT;


-- ##########################################################################
-- Source: 000020_tool_invocations.up.sql
-- ##########################################################################

ALTER TABLE tool_versions
    ADD CONSTRAINT tool_versions_workspace_capability_version_provider_key
        UNIQUE (workspace_id, capability_id, id, provider_id);

CREATE TABLE tool_invocations (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    tool_id UUID NOT NULL,
    tool_version_id UUID NOT NULL,
    capability_release_id UUID NOT NULL,
    provider_id UUID NOT NULL,
    connection_id UUID,
    execution_lease_id UUID,
    provider_request_id TEXT,
    agent_run_id UUID,
    workflow_execution_id UUID,
    execution_step_id UUID,
    actor_type TEXT NOT NULL,
    actor_id UUID NOT NULL,
    trace_id TEXT NOT NULL,
    idempotency_key TEXT,
    status TEXT NOT NULL DEFAULT 'PENDING',
    input_summary JSONB NOT NULL DEFAULT '{}'::JSONB,
    output_summary JSONB NOT NULL DEFAULT '{}'::JSONB,
    raw_object_id UUID,
    latency_ms BIGINT,
    error_code TEXT,
    started_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at TIMESTAMPTZ,
    CONSTRAINT tool_invocations_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT tool_invocations_workspace_tool_fk
        FOREIGN KEY (workspace_id, tool_id)
        REFERENCES tools (workspace_id, capability_id) ON DELETE RESTRICT,
    CONSTRAINT tool_invocations_workspace_version_provider_fk
        FOREIGN KEY (workspace_id, tool_id, tool_version_id, provider_id)
        REFERENCES tool_versions (
            workspace_id, capability_id, id, provider_id
        ) ON DELETE RESTRICT,
    CONSTRAINT tool_invocations_workspace_release_fk
        FOREIGN KEY (workspace_id, tool_id, capability_release_id)
        REFERENCES capability_releases (workspace_id, capability_id, id) ON DELETE RESTRICT,
    CONSTRAINT tool_invocations_workspace_provider_fk
        FOREIGN KEY (workspace_id, provider_id)
        REFERENCES capability_providers (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT tool_invocations_workspace_connection_fk
        FOREIGN KEY (workspace_id, provider_id, connection_id)
        REFERENCES service_connections (workspace_id, provider_id, id) ON DELETE RESTRICT,
    CONSTRAINT tool_invocations_workspace_agent_run_fk
        FOREIGN KEY (workspace_id, agent_run_id)
        REFERENCES agent_runs (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT tool_invocations_workspace_workflow_execution_fk
        FOREIGN KEY (workspace_id, workflow_execution_id)
        REFERENCES workflow_executions (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT tool_invocations_workspace_execution_step_fk
        FOREIGN KEY (workspace_id, workflow_execution_id, execution_step_id)
        REFERENCES execution_steps (workspace_id, execution_id, id) ON DELETE RESTRICT,
    CONSTRAINT tool_invocations_execution_step_parent_check
        CHECK (execution_step_id IS NULL OR workflow_execution_id IS NOT NULL),
    CONSTRAINT tool_invocations_actor_type_check
        CHECK (actor_type IN ('USER', 'SERVICE_PRINCIPAL', 'SYSTEM')),
    CONSTRAINT tool_invocations_trace_id_not_blank CHECK (length(btrim(trace_id)) > 0),
    CONSTRAINT tool_invocations_idempotency_key_check CHECK (
        idempotency_key IS NULL
        OR (length(btrim(idempotency_key)) > 0 AND length(idempotency_key) <= 255)
    ),
    CONSTRAINT tool_invocations_provider_request_id_check
        CHECK (provider_request_id IS NULL OR length(btrim(provider_request_id)) > 0),
    CONSTRAINT tool_invocations_status_check
        CHECK (status IN ('PENDING', 'RUNNING', 'SUCCEEDED', 'FAILED', 'CANCELLED')),
    CONSTRAINT tool_invocations_input_summary_object_check
        CHECK (jsonb_typeof(input_summary) = 'object'),
    CONSTRAINT tool_invocations_output_summary_object_check
        CHECK (jsonb_typeof(output_summary) = 'object'),
    CONSTRAINT tool_invocations_finish_state_check CHECK (
        (status IN ('PENDING', 'RUNNING') AND finished_at IS NULL AND latency_ms IS NULL)
        OR (status IN ('SUCCEEDED', 'FAILED', 'CANCELLED')
            AND finished_at IS NOT NULL AND latency_ms IS NOT NULL)
    ),
    CONSTRAINT tool_invocations_finished_at_check
        CHECK (finished_at IS NULL OR finished_at >= started_at),
    CONSTRAINT tool_invocations_latency_check CHECK (latency_ms IS NULL OR latency_ms >= 0),
    CONSTRAINT tool_invocations_error_state_check CHECK (
        (status = 'FAILED' AND error_code IS NOT NULL AND length(btrim(error_code)) > 0)
        OR (status <> 'FAILED' AND error_code IS NULL)
    )
);

CREATE UNIQUE INDEX tool_invocations_idempotency_key
    ON tool_invocations (workspace_id, tool_version_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
CREATE INDEX tool_invocations_workspace_started_idx
    ON tool_invocations (workspace_id, started_at DESC, id);
CREATE INDEX tool_invocations_workspace_status_started_idx
    ON tool_invocations (workspace_id, status, started_at DESC, id);
CREATE INDEX tool_invocations_workspace_agent_run_started_idx
    ON tool_invocations (workspace_id, agent_run_id, started_at DESC, id)
    WHERE agent_run_id IS NOT NULL;
CREATE INDEX tool_invocations_workspace_workflow_started_idx
    ON tool_invocations (workspace_id, workflow_execution_id, started_at DESC, id)
    WHERE workflow_execution_id IS NOT NULL;
CREATE INDEX tool_invocations_trace_idx ON tool_invocations (trace_id, id);

CREATE FUNCTION enforce_tool_invocation_integrity()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    parent_agent_run UUID;
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM capability_releases
        WHERE workspace_id = NEW.workspace_id
          AND capability_id = NEW.tool_id
          AND id = NEW.capability_release_id
          AND source_type = 'TOOL_VERSION'
          AND source_id = NEW.tool_version_id
          AND retired_at IS NULL
    ) THEN
        RAISE EXCEPTION 'tool invocation release must resolve to its immutable tool version'
            USING ERRCODE = '23514';
    END IF;
    IF NOT EXISTS (
        SELECT 1
        FROM tool_versions
        WHERE workspace_id = NEW.workspace_id
          AND capability_id = NEW.tool_id
          AND id = NEW.tool_version_id
          AND provider_id = NEW.provider_id
          AND lifecycle_status = 'PUBLISHED'
    ) THEN
        RAISE EXCEPTION 'tool invocation requires a published tool version'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.workflow_execution_id IS NOT NULL AND NEW.agent_run_id IS NOT NULL THEN
        SELECT agent_run_id INTO parent_agent_run
        FROM workflow_executions
        WHERE workspace_id = NEW.workspace_id
          AND id = NEW.workflow_execution_id;
        IF parent_agent_run IS DISTINCT FROM NEW.agent_run_id THEN
            RAISE EXCEPTION 'tool invocation run chain does not match workflow execution parent'
                USING ERRCODE = '23514';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER tool_invocations_integrity
BEFORE INSERT OR UPDATE OF
    workspace_id, tool_id, tool_version_id, capability_release_id, provider_id,
    connection_id, agent_run_id, workflow_execution_id, execution_step_id
ON tool_invocations
FOR EACH ROW EXECUTE FUNCTION enforce_tool_invocation_integrity();

CREATE FUNCTION enforce_tool_invocation_state()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'tool invocations are permanently retained and cannot be deleted'
            USING ERRCODE = '55000';
    END IF;
    IF ROW(
        NEW.id, NEW.workspace_id, NEW.tool_id, NEW.tool_version_id,
        NEW.capability_release_id, NEW.provider_id, NEW.connection_id,
        NEW.execution_lease_id, NEW.agent_run_id, NEW.workflow_execution_id,
        NEW.execution_step_id, NEW.actor_type, NEW.actor_id, NEW.trace_id,
        NEW.idempotency_key, NEW.input_summary, NEW.started_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.tool_id, OLD.tool_version_id,
        OLD.capability_release_id, OLD.provider_id, OLD.connection_id,
        OLD.execution_lease_id, OLD.agent_run_id, OLD.workflow_execution_id,
        OLD.execution_step_id, OLD.actor_type, OLD.actor_id, OLD.trace_id,
        OLD.idempotency_key, OLD.input_summary, OLD.started_at
    ) THEN
        RAISE EXCEPTION 'tool invocation identity and request evidence are immutable'
            USING ERRCODE = '55000';
    END IF;
    IF OLD.status IN ('SUCCEEDED', 'FAILED', 'CANCELLED') THEN
        RAISE EXCEPTION 'terminal tool invocation is immutable'
            USING ERRCODE = '55000';
    END IF;
    IF NEW.status <> OLD.status AND NOT (
        (OLD.status = 'PENDING' AND NEW.status IN ('RUNNING', 'FAILED', 'CANCELLED'))
        OR (OLD.status = 'RUNNING' AND NEW.status IN ('SUCCEEDED', 'FAILED', 'CANCELLED'))
    ) THEN
        RAISE EXCEPTION 'illegal tool invocation status transition from % to %',
            OLD.status, NEW.status USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER tool_invocations_state_guard
BEFORE UPDATE OR DELETE ON tool_invocations
FOR EACH ROW EXECUTE FUNCTION enforce_tool_invocation_state();


-- ##########################################################################
-- Source: 000021_run_state_snapshots.up.sql
-- ##########################################################################

ALTER TABLE agent_runs
    ADD COLUMN snapshot_schema_version TEXT NOT NULL DEFAULT 'run.v1',
    ADD COLUMN authorization_snapshot JSONB NOT NULL DEFAULT '{}'::JSONB,
    ADD CONSTRAINT agent_runs_snapshot_schema_version_not_blank
        CHECK (length(btrim(snapshot_schema_version)) > 0),
    ADD CONSTRAINT agent_runs_authorization_snapshot_object_check
        CHECK (jsonb_typeof(authorization_snapshot) = 'object');

ALTER TABLE workflow_executions
    ADD COLUMN snapshot_schema_version TEXT NOT NULL DEFAULT 'run.v1',
    ADD COLUMN authorization_snapshot JSONB NOT NULL DEFAULT '{}'::JSONB,
    ADD CONSTRAINT workflow_executions_snapshot_schema_version_not_blank
        CHECK (length(btrim(snapshot_schema_version)) > 0),
    ADD CONSTRAINT workflow_executions_authorization_snapshot_object_check
        CHECK (jsonb_typeof(authorization_snapshot) = 'object');

CREATE OR REPLACE FUNCTION enforce_agent_run_permanent_snapshot()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'agent runs are permanently retained and cannot be deleted'
            USING ERRCODE = '55000';
    END IF;
    IF ROW(
        NEW.id, NEW.workspace_id, NEW.session_id, NEW.agent_id,
        NEW.trigger_type, NEW.triggered_by_type, NEW.triggered_by_id, NEW.trace_id,
        NEW.model_snapshot, NEW.capability_snapshot, NEW.context_policy_snapshot,
        NEW.authorization_snapshot, NEW.snapshot_schema_version,
        NEW.input_summary, NEW.started_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.session_id, OLD.agent_id,
        OLD.trigger_type, OLD.triggered_by_type, OLD.triggered_by_id, OLD.trace_id,
        OLD.model_snapshot, OLD.capability_snapshot, OLD.context_policy_snapshot,
        OLD.authorization_snapshot, OLD.snapshot_schema_version,
        OLD.input_summary, OLD.started_at
    ) THEN
        RAISE EXCEPTION 'agent run identity and start snapshots are immutable'
            USING ERRCODE = '55000';
    END IF;
    IF OLD.status IN ('SUCCEEDED', 'FAILED', 'CANCELLED') THEN
        RAISE EXCEPTION 'terminal agent run is immutable'
            USING ERRCODE = '55000';
    END IF;
    IF NEW.lock_version <> OLD.lock_version + 1 THEN
        RAISE EXCEPTION 'agent run update requires the next lock version'
            USING ERRCODE = '40001';
    END IF;
    IF NEW.status <> OLD.status AND NOT (
        (OLD.status = 'PENDING' AND NEW.status IN ('RUNNING', 'FAILED', 'CANCELLED'))
        OR (OLD.status = 'RUNNING' AND NEW.status IN (
            'WAITING_CONFIRMATION', 'SUCCEEDED', 'FAILED', 'CANCELLED'
        ))
        OR (OLD.status = 'WAITING_CONFIRMATION' AND NEW.status IN (
            'RUNNING', 'FAILED', 'CANCELLED'
        ))
    ) THEN
        RAISE EXCEPTION 'illegal agent run status transition from % to %',
            OLD.status, NEW.status USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION enforce_workflow_execution_state()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'workflow executions are permanently retained and cannot be deleted'
            USING ERRCODE = '55000';
    END IF;
    IF ROW(
        NEW.id, NEW.workspace_id, NEW.workflow_id, NEW.revision_id, NEW.agent_run_id,
        NEW.trigger_type, NEW.triggered_by_type, NEW.triggered_by_id, NEW.trace_id,
        NEW.snapshot_schema_version, NEW.authorization_snapshot,
        NEW.input_summary, NEW.started_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.workflow_id, OLD.revision_id, OLD.agent_run_id,
        OLD.trigger_type, OLD.triggered_by_type, OLD.triggered_by_id, OLD.trace_id,
        OLD.snapshot_schema_version, OLD.authorization_snapshot,
        OLD.input_summary, OLD.started_at
    ) THEN
        RAISE EXCEPTION 'workflow execution identity and start evidence are immutable'
            USING ERRCODE = '55000';
    END IF;
    IF OLD.status IN ('SUCCEEDED', 'FAILED', 'CANCELLED') THEN
        RAISE EXCEPTION 'terminal workflow execution is immutable'
            USING ERRCODE = '55000';
    END IF;
    IF NEW.lock_version <> OLD.lock_version + 1 THEN
        RAISE EXCEPTION 'workflow execution update requires the next lock version'
            USING ERRCODE = '40001';
    END IF;
    IF NEW.status <> OLD.status AND NOT (
        (OLD.status = 'PENDING' AND NEW.status IN ('RUNNING', 'FAILED', 'CANCELLED'))
        OR (OLD.status = 'RUNNING' AND NEW.status IN (
            'WAITING_CONFIRMATION', 'SUCCEEDED', 'FAILED', 'CANCELLED'
        ))
        OR (OLD.status = 'WAITING_CONFIRMATION' AND NEW.status IN (
            'RUNNING', 'FAILED', 'CANCELLED'
        ))
    ) THEN
        RAISE EXCEPTION 'illegal workflow execution status transition from % to %',
            OLD.status, NEW.status USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;


-- ##########################################################################
-- Source: 000022_run_events.up.sql
-- ##########################################################################

CREATE TABLE run_events (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    run_id UUID NOT NULL,
    sequence_no BIGINT NOT NULL,
    event_type TEXT NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::JSONB,
    terminal BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT run_events_workspace_run_id_key UNIQUE (workspace_id, run_id, id),
    CONSTRAINT run_events_run_sequence_key UNIQUE (run_id, sequence_no),
    CONSTRAINT run_events_workspace_run_fk
        FOREIGN KEY (workspace_id, run_id)
        REFERENCES agent_runs (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT run_events_sequence_check CHECK (sequence_no > 0),
    CONSTRAINT run_events_type_check CHECK (
        event_type IN (
            'RUN_STARTED', 'STEP_STARTED', 'STEP_COMPLETED',
            'RUN_WAITING_CONFIRMATION', 'RUN_RESUMED',
            'RUN_COMPLETED', 'RUN_FAILED', 'RUN_CANCELLED'
        )
    ),
    CONSTRAINT run_events_payload_object_check CHECK (jsonb_typeof(payload) = 'object'),
    CONSTRAINT run_events_terminal_type_check CHECK (
        terminal = (event_type IN ('RUN_COMPLETED', 'RUN_FAILED', 'RUN_CANCELLED'))
    )
);

CREATE INDEX run_events_workspace_run_sequence_idx
    ON run_events (workspace_id, run_id, sequence_no, id);
CREATE UNIQUE INDEX run_events_one_terminal_per_run_key
    ON run_events (run_id) WHERE terminal;

CREATE FUNCTION enforce_run_event_fact()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    current_status TEXT;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'run events are immutable and permanently retained'
            USING ERRCODE = '55000';
    END IF;
    SELECT status INTO current_status
    FROM agent_runs
    WHERE workspace_id = NEW.workspace_id AND id = NEW.run_id;
    IF current_status IN ('SUCCEEDED', 'FAILED', 'CANCELLED') THEN
        IF NOT NEW.terminal OR
           (current_status = 'SUCCEEDED' AND NEW.event_type <> 'RUN_COMPLETED') OR
           (current_status = 'FAILED' AND NEW.event_type <> 'RUN_FAILED') OR
           (current_status = 'CANCELLED' AND NEW.event_type <> 'RUN_CANCELLED') THEN
            RAISE EXCEPTION 'terminal run only accepts its matching terminal event'
                USING ERRCODE = '23514';
        END IF;
    ELSIF NEW.terminal THEN
        RAISE EXCEPTION 'terminal event requires a terminal run state'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER run_events_fact_guard
BEFORE INSERT OR UPDATE OR DELETE ON run_events
FOR EACH ROW EXECUTE FUNCTION enforce_run_event_fact();


-- ##########################################################################
-- Source: 000023_execution_confirmations.up.sql
-- ##########################################################################

CREATE TABLE execution_confirmations (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    execution_id UUID,
    run_id UUID,
    node_id TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'PENDING',
    reason TEXT NOT NULL,
    risk_reasons JSONB NOT NULL DEFAULT '[]'::JSONB,
    scope_snapshot JSONB NOT NULL,
    release_id UUID NOT NULL,
    input_hash CHAR(64) NOT NULL,
    connection_id UUID,
    plan_hash CHAR(64),
    resume_token_hash CHAR(64) NOT NULL,
    requested_by UUID NOT NULL,
    confirmed_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMPTZ NOT NULL,
    confirmed_at TIMESTAMPTZ,
    cancelled_at TIMESTAMPTZ,
    lock_version BIGINT NOT NULL DEFAULT 1,
    CONSTRAINT execution_confirmations_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT execution_confirmations_workspace_execution_fk
        FOREIGN KEY (workspace_id, execution_id)
        REFERENCES workflow_executions (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT execution_confirmations_workspace_run_fk
        FOREIGN KEY (workspace_id, run_id)
        REFERENCES agent_runs (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT execution_confirmations_workspace_release_fk
        FOREIGN KEY (workspace_id, release_id)
        REFERENCES capability_releases (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT execution_confirmations_workspace_connection_fk
        FOREIGN KEY (workspace_id, connection_id)
        REFERENCES service_connections (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT execution_confirmations_requested_by_fk
        FOREIGN KEY (requested_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT execution_confirmations_confirmed_by_fk
        FOREIGN KEY (confirmed_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT execution_confirmations_target_check
        CHECK (execution_id IS NOT NULL OR run_id IS NOT NULL),
    CONSTRAINT execution_confirmations_node_id_not_blank CHECK (length(btrim(node_id)) > 0),
    CONSTRAINT execution_confirmations_status_check
        CHECK (status IN ('PENDING', 'CONFIRMED', 'CANCELLED', 'EXPIRED')),
    CONSTRAINT execution_confirmations_reason_not_blank CHECK (length(btrim(reason)) > 0),
    CONSTRAINT execution_confirmations_risk_reasons_array_check
        CHECK (jsonb_typeof(risk_reasons) = 'array'),
    CONSTRAINT execution_confirmations_scope_snapshot_object_check
        CHECK (jsonb_typeof(scope_snapshot) = 'object'),
    CONSTRAINT execution_confirmations_input_hash_check
        CHECK (input_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT execution_confirmations_plan_hash_check
        CHECK (plan_hash IS NULL OR plan_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT execution_confirmations_resume_token_hash_check
        CHECK (resume_token_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT execution_confirmations_requester_check
        CHECK (confirmed_by IS NULL OR confirmed_by = requested_by),
    CONSTRAINT execution_confirmations_state_check CHECK (
        (status = 'PENDING' AND confirmed_by IS NULL AND confirmed_at IS NULL AND cancelled_at IS NULL)
        OR (status = 'CONFIRMED' AND confirmed_by = requested_by
            AND confirmed_at IS NOT NULL AND cancelled_at IS NULL)
        OR (status = 'CANCELLED' AND confirmed_by IS NULL
            AND confirmed_at IS NULL AND cancelled_at IS NOT NULL)
        OR (status = 'EXPIRED' AND confirmed_by IS NULL
            AND confirmed_at IS NULL AND cancelled_at IS NULL)
    ),
    CONSTRAINT execution_confirmations_expiry_check CHECK (expires_at > created_at),
    CONSTRAINT execution_confirmations_confirmed_at_check
        CHECK (confirmed_at IS NULL OR (confirmed_at >= created_at AND confirmed_at <= expires_at)),
    CONSTRAINT execution_confirmations_cancelled_at_check
        CHECK (cancelled_at IS NULL OR cancelled_at >= created_at),
    CONSTRAINT execution_confirmations_lock_version_check CHECK (lock_version > 0)
);

CREATE INDEX execution_confirmations_workspace_status_created_idx
    ON execution_confirmations (workspace_id, status, created_at DESC, id);
CREATE INDEX execution_confirmations_workspace_run_created_idx
    ON execution_confirmations (workspace_id, run_id, created_at DESC, id)
    WHERE run_id IS NOT NULL;
CREATE INDEX execution_confirmations_workspace_execution_created_idx
    ON execution_confirmations (workspace_id, execution_id, created_at DESC, id)
    WHERE execution_id IS NOT NULL;
CREATE INDEX execution_confirmations_pending_expiry_idx
    ON execution_confirmations (expires_at, id) WHERE status = 'PENDING';

CREATE FUNCTION enforce_execution_confirmation_fact()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    parent_run UUID;
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'execution confirmations are permanently retained'
            USING ERRCODE = '55000';
    END IF;
    IF TG_OP = 'UPDATE' AND ROW(
        NEW.id, NEW.workspace_id, NEW.execution_id, NEW.run_id, NEW.node_id,
        NEW.reason, NEW.risk_reasons, NEW.scope_snapshot, NEW.release_id,
        NEW.input_hash, NEW.connection_id, NEW.plan_hash, NEW.resume_token_hash,
        NEW.requested_by, NEW.created_at, NEW.expires_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.execution_id, OLD.run_id, OLD.node_id,
        OLD.reason, OLD.risk_reasons, OLD.scope_snapshot, OLD.release_id,
        OLD.input_hash, OLD.connection_id, OLD.plan_hash, OLD.resume_token_hash,
        OLD.requested_by, OLD.created_at, OLD.expires_at
    ) THEN
        RAISE EXCEPTION 'execution confirmation request snapshot is immutable'
            USING ERRCODE = '55000';
    END IF;
    IF NEW.execution_id IS NOT NULL AND NEW.run_id IS NOT NULL THEN
        SELECT agent_run_id INTO parent_run FROM workflow_executions
        WHERE workspace_id = NEW.workspace_id AND id = NEW.execution_id;
        IF parent_run IS DISTINCT FROM NEW.run_id THEN
            RAISE EXCEPTION 'confirmation execution and run chain mismatch'
                USING ERRCODE = '23514';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER execution_confirmations_fact_guard
BEFORE INSERT OR UPDATE OR DELETE ON execution_confirmations
FOR EACH ROW EXECUTE FUNCTION enforce_execution_confirmation_fact();

CREATE TABLE chat_confirmations (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    session_id UUID NOT NULL,
    run_id UUID NOT NULL,
    execution_confirmation_id UUID NOT NULL UNIQUE,
    target_type TEXT NOT NULL,
    target_release_id UUID NOT NULL,
    risk_level TEXT NOT NULL,
    risk_reasons JSONB NOT NULL DEFAULT '[]'::JSONB,
    input_summary JSONB NOT NULL DEFAULT '{}'::JSONB,
    status TEXT NOT NULL DEFAULT 'PENDING',
    confirmed_by UUID,
    confirmed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT chat_confirmations_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT chat_confirmations_workspace_session_fk
        FOREIGN KEY (workspace_id, session_id)
        REFERENCES chat_sessions (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT chat_confirmations_workspace_run_fk
        FOREIGN KEY (workspace_id, run_id)
        REFERENCES agent_runs (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT chat_confirmations_workspace_execution_confirmation_fk
        FOREIGN KEY (workspace_id, execution_confirmation_id)
        REFERENCES execution_confirmations (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT chat_confirmations_workspace_release_fk
        FOREIGN KEY (workspace_id, target_release_id)
        REFERENCES capability_releases (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT chat_confirmations_confirmed_by_fk
        FOREIGN KEY (confirmed_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT chat_confirmations_target_type_check CHECK (target_type IN ('TOOL', 'WORKFLOW')),
    CONSTRAINT chat_confirmations_risk_level_check
        CHECK (risk_level IN ('LOW', 'MEDIUM', 'HIGH', 'CRITICAL')),
    CONSTRAINT chat_confirmations_risk_reasons_array_check
        CHECK (jsonb_typeof(risk_reasons) = 'array'),
    CONSTRAINT chat_confirmations_input_summary_object_check
        CHECK (jsonb_typeof(input_summary) = 'object'),
    CONSTRAINT chat_confirmations_status_check
        CHECK (status IN ('PENDING', 'CONFIRMED', 'CANCELLED', 'EXPIRED')),
    CONSTRAINT chat_confirmations_confirmed_state_check CHECK (
        (status = 'CONFIRMED' AND confirmed_by IS NOT NULL AND confirmed_at IS NOT NULL)
        OR (status <> 'CONFIRMED' AND confirmed_by IS NULL AND confirmed_at IS NULL)
    )
);

CREATE INDEX chat_confirmations_workspace_session_created_idx
    ON chat_confirmations (workspace_id, session_id, created_at DESC, id);
CREATE INDEX chat_confirmations_workspace_run_created_idx
    ON chat_confirmations (workspace_id, run_id, created_at DESC, id);

CREATE FUNCTION reject_chat_confirmation_delete()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'chat confirmations are permanently retained'
        USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER chat_confirmations_no_delete
BEFORE DELETE ON chat_confirmations
FOR EACH ROW EXECUTE FUNCTION reject_chat_confirmation_delete();

ALTER TABLE chat_sessions
    ADD CONSTRAINT chat_sessions_pending_confirmation_fk
        FOREIGN KEY (workspace_id, pending_confirmation_id)
        REFERENCES chat_confirmations (workspace_id, id)
        ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE chat_messages
    ADD CONSTRAINT chat_messages_confirmation_fk
        FOREIGN KEY (workspace_id, confirmation_id)
        REFERENCES chat_confirmations (workspace_id, id) ON DELETE RESTRICT;


-- ##########################################################################
-- Source: 000024_confirmation_resume_checkpoints.up.sql
-- ##########################################################################

CREATE TABLE confirmation_resume_checkpoints (
    confirmation_id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    kind TEXT NOT NULL,
    run_id UUID,
    execution_id UUID,
    agent_run_step_id UUID,
    execution_step_id UUID,
    node_id TEXT NOT NULL,
    run_wait_lock_version BIGINT,
    execution_wait_lock_version BIGINT,
    status TEXT NOT NULL DEFAULT 'PENDING',
    snapshot_schema_version TEXT NOT NULL,
    request_snapshot JSONB NOT NULL,
    resolved_snapshot JSONB NOT NULL,
    input_payload JSONB NOT NULL,
    input_hash CHAR(64) NOT NULL,
    plan_hash CHAR(64),
    terminal_on_success BOOLEAN NOT NULL DEFAULT FALSE,
    result_snapshot JSONB NOT NULL DEFAULT '{}'::JSONB,
    error_code TEXT,
    claim_id UUID,
    claim_expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    lock_version BIGINT NOT NULL DEFAULT 1,
    CONSTRAINT confirmation_resume_checkpoints_workspace_id_key
        UNIQUE (workspace_id, confirmation_id),
    CONSTRAINT confirmation_resume_checkpoints_confirmation_fk
        FOREIGN KEY (workspace_id, confirmation_id)
        REFERENCES execution_confirmations (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT confirmation_resume_checkpoints_run_fk
        FOREIGN KEY (workspace_id, run_id)
        REFERENCES agent_runs (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT confirmation_resume_checkpoints_execution_fk
        FOREIGN KEY (workspace_id, execution_id)
        REFERENCES workflow_executions (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT confirmation_resume_checkpoints_agent_step_fk
        FOREIGN KEY (workspace_id, run_id, agent_run_step_id)
        REFERENCES agent_run_steps (workspace_id, run_id, id) ON DELETE RESTRICT,
    CONSTRAINT confirmation_resume_checkpoints_execution_step_fk
        FOREIGN KEY (workspace_id, execution_id, execution_step_id)
        REFERENCES execution_steps (workspace_id, execution_id, id) ON DELETE RESTRICT,
    CONSTRAINT confirmation_resume_checkpoints_kind_check
        CHECK (kind IN ('TOOL', 'WORKFLOW')),
    CONSTRAINT confirmation_resume_checkpoints_target_check
        CHECK (run_id IS NOT NULL OR execution_id IS NOT NULL),
    CONSTRAINT confirmation_resume_checkpoints_agent_step_parent_check
        CHECK (agent_run_step_id IS NULL OR run_id IS NOT NULL),
    CONSTRAINT confirmation_resume_checkpoints_execution_step_parent_check
        CHECK (execution_step_id IS NULL OR execution_id IS NOT NULL),
    CONSTRAINT confirmation_resume_checkpoints_node_not_blank
        CHECK (length(btrim(node_id)) > 0),
    CONSTRAINT confirmation_resume_checkpoints_run_lock_check
        CHECK ((run_id IS NULL) = (run_wait_lock_version IS NULL)
            AND (run_wait_lock_version IS NULL OR run_wait_lock_version > 1)),
    CONSTRAINT confirmation_resume_checkpoints_execution_lock_check
        CHECK ((execution_id IS NULL) = (execution_wait_lock_version IS NULL)
            AND (execution_wait_lock_version IS NULL OR execution_wait_lock_version > 1)),
    CONSTRAINT confirmation_resume_checkpoints_status_check
        CHECK (status IN ('PENDING', 'CLAIMED', 'EXECUTING', 'SUCCEEDED', 'FAILED', 'CANCELLED')),
    CONSTRAINT confirmation_resume_checkpoints_snapshot_version_not_blank
        CHECK (length(btrim(snapshot_schema_version)) > 0),
    CONSTRAINT confirmation_resume_checkpoints_request_object_check
        CHECK (jsonb_typeof(request_snapshot) = 'object'),
    CONSTRAINT confirmation_resume_checkpoints_resolved_object_check
        CHECK (jsonb_typeof(resolved_snapshot) = 'object'),
    CONSTRAINT confirmation_resume_checkpoints_input_object_check
        CHECK (jsonb_typeof(input_payload) = 'object'),
    CONSTRAINT confirmation_resume_checkpoints_result_object_check
        CHECK (jsonb_typeof(result_snapshot) = 'object'),
    CONSTRAINT confirmation_resume_checkpoints_input_hash_check
        CHECK (input_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT confirmation_resume_checkpoints_plan_hash_check
        CHECK (plan_hash IS NULL OR plan_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT confirmation_resume_checkpoints_claim_pair_check
        CHECK ((claim_id IS NULL) = (claim_expires_at IS NULL)),
    CONSTRAINT confirmation_resume_checkpoints_state_check CHECK (
        (status = 'PENDING' AND claim_id IS NULL AND started_at IS NULL
            AND completed_at IS NULL AND error_code IS NULL)
        OR (status = 'CLAIMED' AND claim_id IS NOT NULL AND started_at IS NULL
            AND completed_at IS NULL AND error_code IS NULL)
        OR (status = 'EXECUTING' AND claim_id IS NOT NULL AND started_at IS NOT NULL
            AND completed_at IS NULL AND error_code IS NULL)
        OR (status = 'SUCCEEDED' AND started_at IS NOT NULL
            AND completed_at IS NOT NULL AND error_code IS NULL)
        OR (status = 'FAILED' AND started_at IS NOT NULL
            AND completed_at IS NOT NULL AND error_code IS NOT NULL)
        OR (status = 'CANCELLED' AND completed_at IS NOT NULL AND error_code IS NULL)
    ),
    CONSTRAINT confirmation_resume_checkpoints_times_check CHECK (
        (started_at IS NULL OR started_at >= created_at)
        AND (completed_at IS NULL OR completed_at >= created_at)
        AND (completed_at IS NULL OR started_at IS NULL OR completed_at >= started_at)
    ),
    CONSTRAINT confirmation_resume_checkpoints_lock_check CHECK (lock_version > 0)
);

CREATE INDEX confirmation_resume_checkpoints_workspace_status_created_idx
    ON confirmation_resume_checkpoints (workspace_id, status, created_at DESC, confirmation_id);
CREATE INDEX confirmation_resume_checkpoints_reclaim_idx
    ON confirmation_resume_checkpoints (claim_expires_at, confirmation_id)
    WHERE status = 'CLAIMED';

CREATE FUNCTION enforce_confirmation_resume_checkpoint()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'confirmation resume checkpoints are permanently retained'
            USING ERRCODE = '55000';
    END IF;
    IF ROW(
        NEW.confirmation_id, NEW.workspace_id, NEW.kind, NEW.run_id,
        NEW.execution_id, NEW.agent_run_step_id, NEW.execution_step_id,
        NEW.node_id, NEW.run_wait_lock_version, NEW.execution_wait_lock_version,
        NEW.snapshot_schema_version, NEW.request_snapshot,
        NEW.resolved_snapshot, NEW.input_payload, NEW.input_hash,
        NEW.plan_hash, NEW.terminal_on_success, NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.confirmation_id, OLD.workspace_id, OLD.kind, OLD.run_id,
        OLD.execution_id, OLD.agent_run_step_id, OLD.execution_step_id,
        OLD.node_id, OLD.run_wait_lock_version, OLD.execution_wait_lock_version,
        OLD.snapshot_schema_version, OLD.request_snapshot,
        OLD.resolved_snapshot, OLD.input_payload, OLD.input_hash,
        OLD.plan_hash, OLD.terminal_on_success, OLD.created_at
    ) THEN
        RAISE EXCEPTION 'confirmation resume checkpoint facts are immutable'
            USING ERRCODE = '55000';
    END IF;
    IF OLD.status IN ('SUCCEEDED', 'FAILED', 'CANCELLED') THEN
        RAISE EXCEPTION 'terminal confirmation resume checkpoint is immutable'
            USING ERRCODE = '55000';
    END IF;
    IF NEW.lock_version <> OLD.lock_version + 1 THEN
        RAISE EXCEPTION 'confirmation resume checkpoint requires next lock version'
            USING ERRCODE = '40001';
    END IF;
    IF NEW.status <> OLD.status AND NOT (
        (OLD.status = 'PENDING' AND NEW.status IN ('CLAIMED', 'CANCELLED'))
        OR (OLD.status = 'CLAIMED' AND NEW.status IN ('EXECUTING', 'CANCELLED'))
        OR (OLD.status = 'EXECUTING' AND NEW.status IN ('SUCCEEDED', 'FAILED'))
    ) THEN
        RAISE EXCEPTION 'illegal confirmation resume checkpoint transition from % to %',
            OLD.status, NEW.status USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER confirmation_resume_checkpoints_fact_guard
BEFORE UPDATE OR DELETE ON confirmation_resume_checkpoints
FOR EACH ROW EXECUTE FUNCTION enforce_confirmation_resume_checkpoint();


-- ##########################################################################
-- Source: 000025_chat_confirmation_projection.up.sql
-- ##########################################################################

CREATE UNIQUE INDEX chat_confirmations_one_pending_per_session_idx
    ON chat_confirmations (workspace_id, session_id)
    WHERE status = 'PENDING';

CREATE FUNCTION enforce_chat_confirmation_projection()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    execution_row execution_confirmations%ROWTYPE;
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'chat confirmations are permanently retained'
            USING ERRCODE = '55000';
    END IF;

    SELECT * INTO execution_row
    FROM execution_confirmations
    WHERE workspace_id = NEW.workspace_id
      AND id = NEW.execution_confirmation_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'chat confirmation execution target not found'
            USING ERRCODE = '23503';
    END IF;
    IF NEW.run_id IS DISTINCT FROM execution_row.run_id
       OR NEW.target_release_id IS DISTINCT FROM execution_row.release_id THEN
        RAISE EXCEPTION 'chat confirmation target differs from execution confirmation'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.status IS DISTINCT FROM execution_row.status
       OR NEW.confirmed_by IS DISTINCT FROM execution_row.confirmed_by
       OR NEW.confirmed_at IS DISTINCT FROM execution_row.confirmed_at THEN
        RAISE EXCEPTION 'chat confirmation state is derived from execution confirmation'
            USING ERRCODE = '55000';
    END IF;

    IF TG_OP = 'UPDATE' AND ROW(
        NEW.id, NEW.workspace_id, NEW.session_id, NEW.run_id,
        NEW.execution_confirmation_id, NEW.target_type, NEW.target_release_id,
        NEW.risk_level, NEW.risk_reasons, NEW.input_summary, NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.session_id, OLD.run_id,
        OLD.execution_confirmation_id, OLD.target_type, OLD.target_release_id,
        OLD.risk_level, OLD.risk_reasons, OLD.input_summary, OLD.created_at
    ) THEN
        RAISE EXCEPTION 'chat confirmation display mapping is immutable'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER chat_confirmations_no_delete ON chat_confirmations;
CREATE TRIGGER chat_confirmations_projection_guard
BEFORE INSERT OR UPDATE OR DELETE ON chat_confirmations
FOR EACH ROW EXECUTE FUNCTION enforce_chat_confirmation_projection();

CREATE FUNCTION synchronize_chat_confirmation_projection()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    chat_confirmation_id UUID;
BEGIN
    UPDATE chat_confirmations
    SET status = NEW.status,
        confirmed_by = NEW.confirmed_by,
        confirmed_at = NEW.confirmed_at
    WHERE workspace_id = NEW.workspace_id
      AND execution_confirmation_id = NEW.id
    RETURNING id INTO chat_confirmation_id;

    IF chat_confirmation_id IS NOT NULL AND NEW.status <> 'PENDING' THEN
        UPDATE chat_sessions
        SET pending_confirmation_id = NULL,
            updated_at = clock_timestamp(),
            lock_version = lock_version + 1
        WHERE workspace_id = NEW.workspace_id
          AND pending_confirmation_id = chat_confirmation_id;

        UPDATE chat_messages
        SET status = CASE WHEN NEW.status = 'CONFIRMED' THEN 'PROCESSING' ELSE 'FAILED' END
        WHERE workspace_id = NEW.workspace_id
          AND confirmation_id = chat_confirmation_id
          AND status = 'PENDING_CONFIRMATION';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER execution_confirmations_chat_projection_sync
AFTER UPDATE OF status, confirmed_by, confirmed_at ON execution_confirmations
FOR EACH ROW
WHEN (OLD.status IS DISTINCT FROM NEW.status)
EXECUTE FUNCTION synchronize_chat_confirmation_projection();


-- ##########################################################################
-- Source: 000026_stored_objects.up.sql
-- ##########################################################################

CREATE TABLE stored_objects (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    bucket TEXT NOT NULL,
    object_key TEXT NOT NULL,
    kind TEXT NOT NULL,
    content_type TEXT NOT NULL,
    size_bytes BIGINT NOT NULL,
    sha256 CHAR(64) NOT NULL,
    encryption_key_id TEXT,
    classification TEXT NOT NULL,
    retention_mode TEXT NOT NULL,
    retention_until TIMESTAMPTZ,
    created_by_type TEXT NOT NULL,
    created_by_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT stored_objects_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT stored_objects_bucket_key_key UNIQUE (bucket, object_key),
    CONSTRAINT stored_objects_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT stored_objects_bucket_not_blank
        CHECK (length(btrim(bucket)) BETWEEN 3 AND 63),
    CONSTRAINT stored_objects_key_not_blank
        CHECK (length(btrim(object_key)) BETWEEN 1 AND 1024),
    CONSTRAINT stored_objects_kind_check CHECK (kind IN (
        'OPENAPI_SOURCE', 'PROMPT_RUN_INPUT', 'PROMPT_RUN_OUTPUT', 'MODEL_TURN',
        'CHAT_MESSAGE', 'TOOL_TEST_PAYLOAD', 'TOOL_INVOCATION_PAYLOAD',
        'EXECUTION_CHECKPOINT', 'AUDIT_EVENT_PAYLOAD', 'AUDIT_EXPORT'
    )),
    CONSTRAINT stored_objects_content_type_not_blank
        CHECK (length(btrim(content_type)) BETWEEN 1 AND 255),
    CONSTRAINT stored_objects_size_check CHECK (size_bytes >= 0),
    CONSTRAINT stored_objects_sha256_check CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT stored_objects_encryption_key_not_blank
        CHECK (encryption_key_id IS NULL OR length(btrim(encryption_key_id)) > 0),
    CONSTRAINT stored_objects_classification_check
        CHECK (classification IN ('PUBLIC', 'INTERNAL', 'SENSITIVE', 'RESTRICTED')),
    CONSTRAINT stored_objects_retention_mode_check
        CHECK (retention_mode IN ('PERMANENT', 'EXPIRING')),
    CONSTRAINT stored_objects_retention_check CHECK (
        (retention_mode = 'PERMANENT' AND retention_until IS NULL)
        OR (retention_mode = 'EXPIRING' AND retention_until > created_at)
    ),
    CONSTRAINT stored_objects_created_by_type_check
        CHECK (created_by_type IN ('USER', 'SERVICE_PRINCIPAL', 'SYSTEM'))
);

CREATE INDEX stored_objects_workspace_kind_created_idx
    ON stored_objects (workspace_id, kind, created_at DESC, id);
CREATE INDEX stored_objects_workspace_classification_created_idx
    ON stored_objects (workspace_id, classification, created_at DESC, id);
CREATE INDEX stored_objects_expiring_retention_idx
    ON stored_objects (retention_until, id)
    WHERE retention_mode = 'EXPIRING';

CREATE FUNCTION enforce_stored_object_metadata()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        RAISE EXCEPTION 'stored object metadata is immutable'
            USING ERRCODE = '55000';
    END IF;
    IF TG_OP = 'DELETE' AND OLD.retention_mode = 'PERMANENT' THEN
        RAISE EXCEPTION 'permanent stored object metadata cannot be deleted'
            USING ERRCODE = '55000';
    END IF;
    IF TG_OP = 'DELETE' AND OLD.retention_until > clock_timestamp() THEN
        RAISE EXCEPTION 'stored object retention has not expired'
            USING ERRCODE = '55000';
    END IF;
    RETURN OLD;
END;
$$;

CREATE TRIGGER stored_objects_metadata_guard
BEFORE UPDATE OR DELETE ON stored_objects
FOR EACH ROW EXECUTE FUNCTION enforce_stored_object_metadata();


-- ##########################################################################
-- Source: 000027_stored_object_security_policy.up.sql
-- ##########################################################################

ALTER TABLE stored_objects
    ADD CONSTRAINT stored_objects_classification_encryption_check CHECK (
        (classification IN ('SENSITIVE', 'RESTRICTED') AND encryption_key_id IS NOT NULL)
        OR (classification IN ('PUBLIC', 'INTERNAL') AND encryption_key_id IS NULL)
    ),
    ADD CONSTRAINT stored_objects_permanent_content_policy_check CHECK (
        kind NOT IN (
            'PROMPT_RUN_INPUT', 'PROMPT_RUN_OUTPUT', 'MODEL_TURN', 'CHAT_MESSAGE',
            'TOOL_TEST_PAYLOAD', 'TOOL_INVOCATION_PAYLOAD', 'EXECUTION_CHECKPOINT'
        )
        OR (
            classification IN ('SENSITIVE', 'RESTRICTED')
            AND retention_mode = 'PERMANENT'
            AND retention_until IS NULL
        )
    ),
    ADD CONSTRAINT stored_objects_openapi_retention_check CHECK (
        kind <> 'OPENAPI_SOURCE'
        OR (
            classification <> 'PUBLIC'
            AND retention_mode = 'PERMANENT'
            AND retention_until IS NULL
        )
    ),
    ADD CONSTRAINT stored_objects_audit_export_retention_check CHECK (
        kind <> 'AUDIT_EXPORT'
        OR (retention_mode = 'EXPIRING' AND retention_until IS NOT NULL)
    );


-- ##########################################################################
-- Source: 000028_permanent_content_references.up.sql
-- ##########################################################################

ALTER TABLE prompt_runs
    ADD COLUMN input_sha256 CHAR(64) NOT NULL,
    ADD COLUMN input_length BIGINT NOT NULL,
    ADD COLUMN output_sha256 CHAR(64),
    ADD COLUMN output_length BIGINT,
    ADD CONSTRAINT prompt_runs_input_sha256_check
        CHECK (input_sha256 ~ '^[0-9a-f]{64}$'),
    ADD CONSTRAINT prompt_runs_input_length_check CHECK (input_length > 0),
    ADD CONSTRAINT prompt_runs_output_evidence_check CHECK (
        (output_object_id IS NULL AND output_sha256 IS NULL AND output_length IS NULL)
        OR (
            output_object_id IS NOT NULL
            AND output_sha256 ~ '^[0-9a-f]{64}$'
            AND output_length > 0
        )
    ),
    ADD CONSTRAINT prompt_runs_input_object_fk
        FOREIGN KEY (workspace_id, input_object_id)
        REFERENCES stored_objects (workspace_id, id) ON DELETE RESTRICT,
    ADD CONSTRAINT prompt_runs_output_object_fk
        FOREIGN KEY (workspace_id, output_object_id)
        REFERENCES stored_objects (workspace_id, id) ON DELETE RESTRICT;

CREATE FUNCTION enforce_prompt_run_permanent_content()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'prompt runs are permanently retained and cannot be deleted'
            USING ERRCODE = '55000';
    END IF;
    IF ROW(
        NEW.id, NEW.workspace_id, NEW.agent_id, NEW.operation_type,
        NEW.model_config_id, NEW.model_snapshot, NEW.input_object_id,
        NEW.input_sha256, NEW.input_length, NEW.trace_id, NEW.created_by,
        NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.agent_id, OLD.operation_type,
        OLD.model_config_id, OLD.model_snapshot, OLD.input_object_id,
        OLD.input_sha256, OLD.input_length, OLD.trace_id, OLD.created_by,
        OLD.created_at
    ) THEN
        RAISE EXCEPTION 'prompt run input evidence is immutable'
            USING ERRCODE = '55000';
    END IF;
    IF OLD.output_object_id IS NOT NULL AND ROW(
        NEW.output_object_id, NEW.output_sha256, NEW.output_length
    ) IS DISTINCT FROM ROW(
        OLD.output_object_id, OLD.output_sha256, OLD.output_length
    ) THEN
        RAISE EXCEPTION 'prompt run output evidence is immutable'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER prompt_runs_permanent_content
BEFORE UPDATE OR DELETE ON prompt_runs
FOR EACH ROW EXECUTE FUNCTION enforce_prompt_run_permanent_content();

ALTER TABLE chat_messages
    ADD COLUMN content_length BIGINT NOT NULL,
    DROP CONSTRAINT chat_messages_content_check,
    ADD CONSTRAINT chat_messages_content_carrier_check CHECK (
        (content IS NOT NULL AND length(content) > 0 AND content_object_id IS NULL)
        OR (content IS NULL AND content_object_id IS NOT NULL)
    ),
    ADD CONSTRAINT chat_messages_content_length_check CHECK (content_length > 0),
    ADD CONSTRAINT chat_messages_content_object_fk
        FOREIGN KEY (workspace_id, content_object_id)
        REFERENCES stored_objects (workspace_id, id) ON DELETE RESTRICT;

CREATE OR REPLACE FUNCTION enforce_chat_message_permanent_retention()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'chat messages are permanently retained and cannot be deleted'
            USING ERRCODE = '55000';
    END IF;
    IF ROW(
        NEW.id, NEW.workspace_id, NEW.session_id, NEW.role, NEW.content,
        NEW.content_object_id, NEW.content_sha256, NEW.content_length,
        NEW.created_by, NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.session_id, OLD.role, OLD.content,
        OLD.content_object_id, OLD.content_sha256, OLD.content_length,
        OLD.created_by, OLD.created_at
    ) THEN
        RAISE EXCEPTION 'chat message original content and identity are immutable'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

ALTER TABLE agent_run_steps
    ADD COLUMN raw_sha256 CHAR(64),
    ADD COLUMN raw_length BIGINT,
    ADD CONSTRAINT agent_run_steps_raw_evidence_check CHECK (
        (raw_object_id IS NULL AND raw_sha256 IS NULL AND raw_length IS NULL)
        OR (
            raw_object_id IS NOT NULL
            AND raw_sha256 ~ '^[0-9a-f]{64}$'
            AND raw_length > 0
        )
    ),
    ADD CONSTRAINT agent_run_steps_model_turn_evidence_check CHECK (
        step_type <> 'MODEL'
        OR status NOT IN ('SUCCEEDED', 'FAILED')
        OR raw_object_id IS NOT NULL
    ),
    ADD CONSTRAINT agent_run_steps_raw_object_fk
        FOREIGN KEY (workspace_id, raw_object_id)
        REFERENCES stored_objects (workspace_id, id) ON DELETE RESTRICT;

CREATE OR REPLACE FUNCTION enforce_agent_run_step_permanent_evidence()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'agent run steps are permanently retained and cannot be deleted'
            USING ERRCODE = '55000';
    END IF;
    IF ROW(
        NEW.id, NEW.workspace_id, NEW.run_id, NEW.sequence_no, NEW.step_type,
        NEW.capability_release_id, NEW.input_summary, NEW.started_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.run_id, OLD.sequence_no, OLD.step_type,
        OLD.capability_release_id, OLD.input_summary, OLD.started_at
    ) THEN
        RAISE EXCEPTION 'agent run step identity and start evidence are immutable'
            USING ERRCODE = '55000';
    END IF;
    IF OLD.raw_object_id IS NOT NULL AND ROW(
        NEW.raw_object_id, NEW.raw_sha256, NEW.raw_length
    ) IS DISTINCT FROM ROW(
        OLD.raw_object_id, OLD.raw_sha256, OLD.raw_length
    ) THEN
        RAISE EXCEPTION 'agent run step raw evidence is immutable'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;


-- ##########################################################################
-- Source: 000029_permanent_tool_payload_references.up.sql
-- ##########################################################################

ALTER TABLE tool_tests
    ALTER COLUMN raw_object_id SET NOT NULL,
    ADD CONSTRAINT tool_tests_raw_object_fk
        FOREIGN KEY (workspace_id, raw_object_id)
        REFERENCES stored_objects (workspace_id, id) ON DELETE RESTRICT;

ALTER TABLE tool_invocations
    ADD CONSTRAINT tool_invocations_raw_object_fk
        FOREIGN KEY (workspace_id, raw_object_id)
        REFERENCES stored_objects (workspace_id, id) ON DELETE RESTRICT,
    ADD CONSTRAINT tool_invocations_terminal_raw_object_check CHECK (
        (status IN ('PENDING', 'RUNNING') AND raw_object_id IS NULL)
        OR (status IN ('SUCCEEDED', 'FAILED', 'CANCELLED') AND raw_object_id IS NOT NULL)
    );

CREATE FUNCTION enforce_permanent_tool_payload_reference()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    expected_kind TEXT;
BEGIN
    IF TG_TABLE_NAME = 'tool_tests' THEN
        expected_kind := 'TOOL_TEST_PAYLOAD';
    ELSE
        expected_kind := 'TOOL_INVOCATION_PAYLOAD';
    END IF;
    IF NEW.raw_object_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM stored_objects
        WHERE workspace_id = NEW.workspace_id
          AND id = NEW.raw_object_id
          AND kind = expected_kind
          AND classification IN ('SENSITIVE', 'RESTRICTED')
          AND retention_mode = 'PERMANENT'
          AND retention_until IS NULL
    ) THEN
        RAISE EXCEPTION '% requires a permanent % object in the same workspace',
            TG_TABLE_NAME, expected_kind USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER tool_tests_permanent_payload
BEFORE INSERT OR UPDATE OF workspace_id, raw_object_id ON tool_tests
FOR EACH ROW EXECUTE FUNCTION enforce_permanent_tool_payload_reference();

CREATE TRIGGER tool_invocations_permanent_payload
BEFORE INSERT OR UPDATE OF workspace_id, raw_object_id ON tool_invocations
FOR EACH ROW EXECUTE FUNCTION enforce_permanent_tool_payload_reference();


-- ##########################################################################
-- Source: 000030_audit_outbox_exports.up.sql
-- ##########################################################################

CREATE TABLE audit_events (
    id UUID NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    workspace_id UUID,
    actor_type TEXT NOT NULL,
    actor_id UUID,
    actor_display TEXT NOT NULL,
    action TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id UUID,
    result TEXT NOT NULL,
    request_id TEXT,
    trace_id TEXT,
    source_ip INET,
    user_agent TEXT,
    changes JSONB NOT NULL DEFAULT '{}'::JSONB,
    metadata JSONB NOT NULL DEFAULT '{}'::JSONB,
    payload_object_id UUID,
    schema_version TEXT NOT NULL,
    PRIMARY KEY (occurred_at, id),
    CONSTRAINT audit_events_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT audit_events_payload_workspace_check
        CHECK (payload_object_id IS NULL OR workspace_id IS NOT NULL),
    CONSTRAINT audit_events_payload_object_fk
        FOREIGN KEY (workspace_id, payload_object_id)
        REFERENCES stored_objects (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT audit_events_actor_type_check
        CHECK (actor_type IN ('USER', 'SERVICE_PRINCIPAL', 'SYSTEM')),
    CONSTRAINT audit_events_actor_id_check
        CHECK (actor_type <> 'USER' OR actor_id IS NOT NULL),
    CONSTRAINT audit_events_actor_display_check
        CHECK (length(btrim(actor_display)) BETWEEN 1 AND 255),
    CONSTRAINT audit_events_action_check
        CHECK (action ~ '^[a-z][a-z0-9]*(\.[a-z][a-z0-9_-]*)+$'),
    CONSTRAINT audit_events_resource_type_check
        CHECK (resource_type ~ '^[A-Z][A-Z0-9_]{0,127}$'),
    CONSTRAINT audit_events_result_check CHECK (result IN ('SUCCESS', 'FAILURE', 'DENIED')),
    CONSTRAINT audit_events_request_id_check
        CHECK (request_id IS NULL OR length(btrim(request_id)) BETWEEN 1 AND 255),
    CONSTRAINT audit_events_trace_id_check
        CHECK (trace_id IS NULL OR length(btrim(trace_id)) BETWEEN 1 AND 255),
    CONSTRAINT audit_events_user_agent_check
        CHECK (user_agent IS NULL OR length(user_agent) <= 1024),
    CONSTRAINT audit_events_changes_object_check CHECK (jsonb_typeof(changes) = 'object'),
    CONSTRAINT audit_events_metadata_object_check CHECK (jsonb_typeof(metadata) = 'object'),
    CONSTRAINT audit_events_schema_version_check
        CHECK (schema_version ~ '^[a-z][a-z0-9_.-]{0,63}$')
) PARTITION BY RANGE (occurred_at);

CREATE TABLE audit_events_default PARTITION OF audit_events DEFAULT;

CREATE INDEX audit_events_workspace_occurred_idx
    ON audit_events (workspace_id, occurred_at DESC, id);
CREATE INDEX audit_events_workspace_actor_occurred_idx
    ON audit_events (workspace_id, actor_type, actor_id, occurred_at DESC, id);
CREATE INDEX audit_events_workspace_resource_occurred_idx
    ON audit_events (workspace_id, resource_type, resource_id, occurred_at DESC, id);
CREATE INDEX audit_events_workspace_action_occurred_idx
    ON audit_events (workspace_id, action, occurred_at DESC, id);
CREATE INDEX audit_events_request_id_idx
    ON audit_events (request_id, occurred_at DESC, id) WHERE request_id IS NOT NULL;
CREATE INDEX audit_events_trace_id_idx
    ON audit_events (trace_id, occurred_at DESC, id) WHERE trace_id IS NOT NULL;
CREATE INDEX audit_events_platform_occurred_idx
    ON audit_events (occurred_at DESC, id) WHERE workspace_id IS NULL;

CREATE FUNCTION reject_audit_event_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'audit events are insert-only'
        USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER audit_events_insert_only
BEFORE UPDATE OR DELETE ON audit_events
FOR EACH ROW EXECUTE FUNCTION reject_audit_event_mutation();

CREATE TABLE outbox_events (
    id UUID PRIMARY KEY,
    workspace_id UUID,
    aggregate_type TEXT NOT NULL,
    aggregate_id UUID NOT NULL,
    event_type TEXT NOT NULL,
    payload JSONB NOT NULL,
    schema_version TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    available_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    published_at TIMESTAMPTZ,
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT outbox_events_idempotency_key_key UNIQUE (idempotency_key),
    CONSTRAINT outbox_events_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT outbox_events_aggregate_type_check
        CHECK (aggregate_type ~ '^[A-Z][A-Z0-9_]{0,127}$'),
    CONSTRAINT outbox_events_event_type_check
        CHECK (event_type ~ '^[a-z][a-z0-9]*(\.[a-z][a-z0-9_-]*)+$'),
    CONSTRAINT outbox_events_payload_object_check CHECK (jsonb_typeof(payload) = 'object'),
    CONSTRAINT outbox_events_schema_version_check
        CHECK (schema_version ~ '^[a-z][a-z0-9_.-]{0,63}$'),
    CONSTRAINT outbox_events_idempotency_key_check
        CHECK (length(btrim(idempotency_key)) BETWEEN 1 AND 255),
    CONSTRAINT outbox_events_attempts_check CHECK (attempts >= 0),
    CONSTRAINT outbox_events_last_error_check
        CHECK (last_error IS NULL OR length(last_error) <= 2048),
    CONSTRAINT outbox_events_timestamps_check CHECK (
        available_at >= occurred_at
        AND created_at >= occurred_at
        AND (published_at IS NULL OR published_at >= occurred_at)
    )
);

CREATE INDEX outbox_events_unpublished_available_idx
    ON outbox_events (available_at, occurred_at, id) WHERE published_at IS NULL;
CREATE INDEX outbox_events_workspace_aggregate_idx
    ON outbox_events (workspace_id, aggregate_type, aggregate_id, occurred_at DESC, id);
CREATE INDEX outbox_events_event_type_occurred_idx
    ON outbox_events (event_type, occurred_at DESC, id);

CREATE TABLE audit_exports (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    filter_snapshot JSONB NOT NULL,
    status TEXT NOT NULL DEFAULT 'PENDING',
    object_id UUID,
    requested_by UUID NOT NULL,
    requested_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    completed_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ NOT NULL,
    error_code TEXT,
    CONSTRAINT audit_exports_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT audit_exports_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT audit_exports_object_fk
        FOREIGN KEY (workspace_id, object_id)
        REFERENCES stored_objects (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT audit_exports_requested_by_fk
        FOREIGN KEY (requested_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT audit_exports_filter_object_check CHECK (jsonb_typeof(filter_snapshot) = 'object'),
    CONSTRAINT audit_exports_status_check
        CHECK (status IN ('PENDING', 'RUNNING', 'SUCCEEDED', 'FAILED', 'EXPIRED')),
    CONSTRAINT audit_exports_expiry_check CHECK (expires_at > requested_at),
    CONSTRAINT audit_exports_result_check CHECK (
        (status IN ('PENDING', 'RUNNING') AND object_id IS NULL AND completed_at IS NULL AND error_code IS NULL)
        OR (status = 'SUCCEEDED' AND object_id IS NOT NULL AND completed_at IS NOT NULL AND error_code IS NULL)
        OR (status = 'FAILED' AND object_id IS NULL AND completed_at IS NOT NULL AND error_code IS NOT NULL)
        OR (status = 'EXPIRED' AND completed_at IS NOT NULL)
    ),
    CONSTRAINT audit_exports_completed_at_check
        CHECK (completed_at IS NULL OR completed_at >= requested_at),
    CONSTRAINT audit_exports_error_code_check
        CHECK (error_code IS NULL OR error_code ~ '^[A-Z][A-Z0-9_]{0,127}$')
);

CREATE INDEX audit_exports_workspace_requested_idx
    ON audit_exports (workspace_id, requested_at DESC, id);
CREATE INDEX audit_exports_pending_idx
    ON audit_exports (status, requested_at, id) WHERE status IN ('PENDING', 'RUNNING');
CREATE INDEX audit_exports_expiry_idx
    ON audit_exports (expires_at, id) WHERE status = 'SUCCEEDED';

CREATE FUNCTION enforce_audit_export_object()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.object_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM stored_objects
        WHERE workspace_id = NEW.workspace_id
          AND id = NEW.object_id
          AND kind = 'AUDIT_EXPORT'
          AND retention_mode = 'EXPIRING'
          AND retention_until IS NOT NULL
          AND retention_until <= NEW.expires_at
    ) THEN
        RAISE EXCEPTION 'audit export requires a matching expiring AUDIT_EXPORT object'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER audit_exports_object_guard
BEFORE INSERT OR UPDATE OF workspace_id, object_id, expires_at ON audit_exports
FOR EACH ROW EXECUTE FUNCTION enforce_audit_export_object();


-- ##########################################################################
-- Source: 000031_audit_payload_policy.up.sql
-- ##########################################################################

ALTER TABLE stored_objects
    ADD CONSTRAINT stored_objects_audit_event_payload_policy_check CHECK (
        kind <> 'AUDIT_EVENT_PAYLOAD'
        OR (
            classification IN ('SENSITIVE', 'RESTRICTED')
            AND retention_mode = 'PERMANENT'
            AND retention_until IS NULL
        )
    );

CREATE FUNCTION enforce_audit_event_payload_reference()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.payload_object_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM stored_objects
        WHERE workspace_id = NEW.workspace_id
          AND id = NEW.payload_object_id
          AND kind = 'AUDIT_EVENT_PAYLOAD'
          AND classification IN ('SENSITIVE', 'RESTRICTED')
          AND retention_mode = 'PERMANENT'
          AND retention_until IS NULL
    ) THEN
        RAISE EXCEPTION 'audit event payload requires a permanent sensitive AUDIT_EVENT_PAYLOAD object'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER audit_events_payload_guard
BEFORE INSERT OR UPDATE OF workspace_id, payload_object_id ON audit_events
FOR EACH ROW EXECUTE FUNCTION enforce_audit_event_payload_reference();


-- ##########################################################################
-- Source: 000032_transactional_outbox_contract.up.sql
-- ##########################################################################

ALTER TABLE outbox_events
    DROP CONSTRAINT outbox_events_timestamps_check,
    ALTER COLUMN created_at SET DEFAULT clock_timestamp(),
    ADD CONSTRAINT outbox_events_timestamps_check CHECK (
        available_at >= occurred_at
        AND (published_at IS NULL OR published_at >= occurred_at)
    ),
    ADD CONSTRAINT outbox_events_payload_schema_version_check CHECK (
        payload ? 'schemaVersion'
        AND jsonb_typeof(payload->'schemaVersion') = 'string'
        AND payload->>'schemaVersion' = schema_version
    );


-- ##########################################################################
-- Source: 000033_outbox_claim_lease.up.sql
-- ##########################################################################

ALTER TABLE outbox_events
    ADD COLUMN claim_token UUID,
    ADD COLUMN claimed_at TIMESTAMPTZ,
    ADD COLUMN claim_expires_at TIMESTAMPTZ,
    ADD CONSTRAINT outbox_events_claim_lease_check CHECK (
        (claim_token IS NULL AND claimed_at IS NULL AND claim_expires_at IS NULL)
        OR (
            claim_token IS NOT NULL
            AND claimed_at IS NOT NULL
            AND claim_expires_at IS NOT NULL
            AND claim_expires_at > claimed_at
            AND published_at IS NULL
        )
    );

CREATE INDEX outbox_events_claimable_idx
    ON outbox_events (available_at, claim_expires_at, occurred_at, id)
    WHERE published_at IS NULL;


-- ##########################################################################
-- Source: 000034_workflow_trial_execution_sources.up.sql
-- ##########################################################################

ALTER TABLE workflow_executions
    ADD COLUMN compilation_id UUID,
    ALTER COLUMN revision_id DROP NOT NULL,
    ADD CONSTRAINT workflow_executions_workspace_compilation_fk
        FOREIGN KEY (workspace_id, workflow_id, compilation_id)
        REFERENCES workflow_compilations (workspace_id, capability_id, id)
        ON DELETE RESTRICT,
    ADD CONSTRAINT workflow_executions_exact_source_check CHECK (
        (revision_id IS NOT NULL AND compilation_id IS NULL)
        OR (revision_id IS NULL AND compilation_id IS NOT NULL)
    );

CREATE INDEX workflow_executions_workspace_compilation_started_idx
    ON workflow_executions (workspace_id, compilation_id, started_at DESC, id)
    WHERE compilation_id IS NOT NULL;

CREATE OR REPLACE FUNCTION enforce_workflow_execution_state()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'workflow executions are permanently retained and cannot be deleted'
            USING ERRCODE = '55000';
    END IF;
    IF ROW(
        NEW.id, NEW.workspace_id, NEW.workflow_id, NEW.revision_id, NEW.compilation_id,
        NEW.agent_run_id, NEW.trigger_type, NEW.triggered_by_type, NEW.triggered_by_id,
        NEW.trace_id, NEW.input_summary, NEW.started_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.workflow_id, OLD.revision_id, OLD.compilation_id,
        OLD.agent_run_id, OLD.trigger_type, OLD.triggered_by_type, OLD.triggered_by_id,
        OLD.trace_id, OLD.input_summary, OLD.started_at
    ) THEN
        RAISE EXCEPTION 'workflow execution identity and start evidence are immutable'
            USING ERRCODE = '55000';
    END IF;
    IF OLD.status IN ('SUCCEEDED', 'FAILED', 'CANCELLED') THEN
        RAISE EXCEPTION 'terminal workflow execution is immutable'
            USING ERRCODE = '55000';
    END IF;
    IF NEW.lock_version <> OLD.lock_version + 1 THEN
        RAISE EXCEPTION 'workflow execution update requires the next lock version'
            USING ERRCODE = '40001';
    END IF;
    IF NEW.status <> OLD.status AND NOT (
        (OLD.status = 'PENDING' AND NEW.status IN ('RUNNING', 'FAILED', 'CANCELLED'))
        OR (OLD.status = 'RUNNING' AND NEW.status IN (
            'WAITING_CONFIRMATION', 'SUCCEEDED', 'FAILED', 'CANCELLED'
        ))
        OR (OLD.status = 'WAITING_CONFIRMATION' AND NEW.status IN (
            'RUNNING', 'FAILED', 'CANCELLED'
        ))
    ) THEN
        RAISE EXCEPTION 'illegal workflow execution status transition from % to %',
            OLD.status, NEW.status USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;


-- ##########################################################################
-- Source: 000035_workspace_slug_reuse_after_soft_delete.up.sql
-- ##########################################################################

ALTER TABLE workspaces
    DROP CONSTRAINT workspaces_slug_key;

CREATE UNIQUE INDEX workspaces_slug_active_key
    ON workspaces (slug)
    WHERE deleted_at IS NULL;


-- ##########################################################################
-- Source: 000036_provider_sync_running_uniqueness.up.sql
-- ##########################################################################

WITH ranked AS (
    SELECT id,
           row_number() OVER (
               PARTITION BY workspace_id, provider_id
               ORDER BY started_at DESC, id DESC
           ) AS position
    FROM provider_sync_runs
    WHERE status = 'RUNNING'
)
UPDATE provider_sync_runs AS runs
SET status = 'FAILED',
    error_summary = '{"code":"SUPERSEDED_CONCURRENT_SYNC"}'::JSONB,
    finished_at = GREATEST(clock_timestamp(), runs.started_at)
FROM ranked
WHERE runs.id = ranked.id AND ranked.position > 1;

CREATE UNIQUE INDEX provider_sync_runs_provider_running_key
    ON provider_sync_runs (workspace_id, provider_id)
    WHERE status = 'RUNNING';


-- ##########################################################################
-- Source: 000037_protocol_events.up.sql
-- ##########################################################################

ALTER TABLE chat_sessions
    ADD CONSTRAINT chat_sessions_workspace_agent_id_key
        UNIQUE (workspace_id, agent_id, id);

ALTER TABLE agent_runs
    ADD CONSTRAINT agent_runs_workspace_agent_session_id_key
        UNIQUE (workspace_id, agent_id, session_id, id);

CREATE TABLE protocol_event_streams (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    conversation_id UUID NOT NULL,
    run_id UUID NOT NULL,
    next_sequence BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT protocol_event_streams_workspace_run_key
        UNIQUE (workspace_id, run_id),
    CONSTRAINT protocol_event_streams_scope_id_key
        UNIQUE (workspace_id, agent_id, conversation_id, run_id, id),
    CONSTRAINT protocol_event_streams_workspace_fk
        FOREIGN KEY (workspace_id)
        REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT protocol_event_streams_workspace_agent_fk
        FOREIGN KEY (workspace_id, agent_id)
        REFERENCES agents (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT protocol_event_streams_conversation_fk
        FOREIGN KEY (workspace_id, agent_id, conversation_id)
        REFERENCES chat_sessions (workspace_id, agent_id, id) ON DELETE RESTRICT,
    CONSTRAINT protocol_event_streams_run_scope_fk
        FOREIGN KEY (workspace_id, agent_id, conversation_id, run_id)
        REFERENCES agent_runs (workspace_id, agent_id, session_id, id) ON DELETE RESTRICT,
    CONSTRAINT protocol_event_streams_next_sequence_check
        CHECK (next_sequence > 0)
);

CREATE INDEX protocol_event_streams_scope_created_idx
    ON protocol_event_streams (
        workspace_id, agent_id, conversation_id, created_at DESC, id
    );

CREATE TABLE protocol_events (
    global_position BIGINT GENERATED ALWAYS AS IDENTITY,
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    conversation_id UUID NOT NULL,
    run_id UUID NOT NULL,
    stream_id UUID NOT NULL,
    sequence_no BIGINT NOT NULL,
    event_type TEXT NOT NULL,
    spec_version TEXT NOT NULL,
    item_id UUID,
    interaction_id UUID,
    payload JSONB NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT protocol_events_global_position_key UNIQUE (global_position),
    CONSTRAINT protocol_events_stream_sequence_key UNIQUE (stream_id, sequence_no),
    CONSTRAINT protocol_events_workspace_run_id_key UNIQUE (workspace_id, run_id, id),
    CONSTRAINT protocol_events_stream_scope_fk
        FOREIGN KEY (workspace_id, agent_id, conversation_id, run_id, stream_id)
        REFERENCES protocol_event_streams (
            workspace_id, agent_id, conversation_id, run_id, id
        ) ON DELETE RESTRICT,
    CONSTRAINT protocol_events_sequence_check CHECK (sequence_no > 0),
    CONSTRAINT protocol_events_type_check CHECK (
        event_type ~ '^[a-z][a-z_]*\.[a-z][a-z_]*$'
    ),
    CONSTRAINT protocol_events_spec_version_not_blank CHECK (
        length(btrim(spec_version)) > 0 AND length(spec_version) <= 32
    ),
    CONSTRAINT protocol_events_payload_object_check CHECK (
        jsonb_typeof(payload) = 'object'
    )
);

CREATE INDEX protocol_events_scope_sequence_idx
    ON protocol_events (
        workspace_id, agent_id, conversation_id, run_id, sequence_no
    );
CREATE INDEX protocol_events_global_delivery_idx
    ON protocol_events (global_position, id);
CREATE INDEX protocol_events_item_sequence_idx
    ON protocol_events (workspace_id, run_id, item_id, sequence_no)
    WHERE item_id IS NOT NULL;
CREATE INDEX protocol_events_interaction_sequence_idx
    ON protocol_events (workspace_id, run_id, interaction_id, sequence_no)
    WHERE interaction_id IS NOT NULL;


-- ##########################################################################
-- Source: 000038_run_items.up.sql
-- ##########################################################################

ALTER TABLE agent_runs
    ADD CONSTRAINT agent_runs_workspace_agent_id_key
        UNIQUE (workspace_id, agent_id, id);

CREATE TABLE run_items (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    run_id UUID NOT NULL,
    ordinal INTEGER NOT NULL,
    item_type TEXT NOT NULL,
    status TEXT NOT NULL,
    source_type TEXT NOT NULL,
    source_id UUID,
    snapshot JSONB NOT NULL,
    started_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    completed_at TIMESTAMPTZ,
    CONSTRAINT run_items_workspace_run_id_key
        UNIQUE (workspace_id, run_id, id),
    CONSTRAINT run_items_run_ordinal_key UNIQUE (run_id, ordinal),
    CONSTRAINT run_items_run_scope_fk
        FOREIGN KEY (workspace_id, agent_id, run_id)
        REFERENCES agent_runs (workspace_id, agent_id, id) ON DELETE RESTRICT,
    CONSTRAINT run_items_ordinal_check CHECK (ordinal > 0),
    CONSTRAINT run_items_type_check CHECK (
        item_type ~ '^[a-z][a-z0-9_]{0,63}$'
    ),
    CONSTRAINT run_items_status_check CHECK (
        status IN (
            'in_progress', 'waiting', 'completed', 'failed',
            'declined', 'cancelled', 'unknown'
        )
    ),
    CONSTRAINT run_items_source_type_check CHECK (
        source_type IN (
            'CHAT_MESSAGE', 'MODEL_RESPONSE', 'TOOL_INVOCATION',
            'WORKFLOW_EXECUTION', 'WORKFLOW_STEP', 'EXECUTION_CONFIRMATION',
            'STORED_OBJECT', 'RUNTIME', 'UNKNOWN'
        )
    ),
    CONSTRAINT run_items_snapshot_object_check CHECK (
        jsonb_typeof(snapshot) = 'object'
    ),
    CONSTRAINT run_items_completion_state_check CHECK (
        (status IN ('in_progress', 'waiting', 'unknown') AND completed_at IS NULL)
        OR
        (status IN ('completed', 'failed', 'declined', 'cancelled') AND completed_at IS NOT NULL)
    ),
    CONSTRAINT run_items_timestamps_check CHECK (
        completed_at IS NULL OR completed_at >= started_at
    )
);

CREATE INDEX run_items_scope_ordinal_idx
    ON run_items (workspace_id, agent_id, run_id, ordinal, id);
CREATE INDEX run_items_scope_status_started_idx
    ON run_items (workspace_id, agent_id, status, started_at DESC, id);
CREATE INDEX run_items_source_ref_idx
    ON run_items (workspace_id, source_type, source_id, id)
    WHERE source_id IS NOT NULL;


-- ##########################################################################
-- Source: 000039_protocol_event_envelope_guards.up.sql
-- ##########################################################################

CREATE FUNCTION validate_protocol_event_envelope()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NOT (
        NEW.payload ?& ARRAY[
            'specVersion', 'type', 'eventId', 'streamId', 'sequence',
            'occurredAt', 'workspaceId', 'agentId', 'conversationId',
            'runId', 'traceId', 'data'
        ]
        AND NEW.event_type <> 'stream.error'
        AND NEW.payload->>'type' <> 'stream.error'
        AND lower(NEW.payload->>'eventId') = NEW.id::TEXT
        AND NEW.payload->>'type' = NEW.event_type
        AND NEW.payload->>'specVersion' = NEW.spec_version
        AND jsonb_typeof(NEW.payload->'sequence') = 'number'
        AND NEW.payload->>'sequence' ~ '^[1-9][0-9]*$'
        AND NEW.payload->>'sequence' = NEW.sequence_no::TEXT
        AND lower(NEW.payload->>'workspaceId') = NEW.workspace_id::TEXT
        AND lower(NEW.payload->>'agentId') = NEW.agent_id::TEXT
        AND lower(NEW.payload->>'conversationId') = NEW.conversation_id::TEXT
        AND lower(NEW.payload->>'runId') = NEW.run_id::TEXT
        AND lower(NEW.payload->>'streamId') = 'run:' || NEW.run_id::TEXT
        AND length(btrim(NEW.payload->>'occurredAt')) > 0
        AND length(btrim(NEW.payload->>'traceId')) > 0
        AND jsonb_typeof(NEW.payload->'data') = 'object'
    ) THEN
        RAISE EXCEPTION 'protocol event envelope does not match persisted columns'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'protocol_events_envelope_consistency';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER protocol_events_validate_envelope
BEFORE INSERT ON protocol_events
FOR EACH ROW EXECUTE FUNCTION validate_protocol_event_envelope();

CREATE FUNCTION reject_protocol_event_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'protocol events are immutable and permanently retained'
        USING ERRCODE = '55000',
              CONSTRAINT = 'protocol_events_immutable';
END;
$$;

CREATE TRIGGER protocol_events_immutable
BEFORE UPDATE OR DELETE ON protocol_events
FOR EACH ROW EXECUTE FUNCTION reject_protocol_event_mutation();


-- ##########################################################################
-- Source: 000040_run_events_cutover.up.sql
-- ##########################################################################

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM run_events re
        JOIN agent_runs ar
          ON ar.workspace_id=re.workspace_id AND ar.id=re.run_id
        WHERE ar.session_id IS NULL
    ) THEN
        RAISE EXCEPTION 'legacy run events require an explicit conversation mapping before AAP cutover'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'run_events_cutover_conversation_required';
    END IF;
    IF EXISTS (
        SELECT 1
        FROM run_events re
        JOIN protocol_event_streams pes
          ON pes.workspace_id=re.workspace_id AND pes.run_id=re.run_id
    ) THEN
        RAISE EXCEPTION 'legacy run has an existing protocol stream; resolve dual-write state before cutover'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'run_events_cutover_single_source';
    END IF;
END;
$$;

INSERT INTO protocol_event_streams(
    id,workspace_id,agent_id,conversation_id,run_id,next_sequence,created_at
)
SELECT
    re.run_id,re.workspace_id,ar.agent_id,ar.session_id,re.run_id,
    max(re.sequence_no)+1,min(re.created_at)
FROM run_events re
JOIN agent_runs ar
  ON ar.workspace_id=re.workspace_id AND ar.id=re.run_id
GROUP BY re.run_id,re.workspace_id,ar.agent_id,ar.session_id;

WITH legacy AS (
    SELECT
        re.*,ar.agent_id,ar.session_id,ar.trace_id,ar.trigger_type,
        ar.started_at,ar.finished_at,
        CASE re.event_type
            WHEN 'RUN_STARTED' THEN 'run.started'
            WHEN 'STEP_STARTED' THEN 'item.started'
            WHEN 'STEP_COMPLETED' THEN 'item.completed'
            WHEN 'RUN_WAITING_CONFIRMATION' THEN 'run.waiting'
            WHEN 'RUN_RESUMED' THEN 'run.resumed'
            WHEN 'RUN_COMPLETED' THEN 'run.completed'
            WHEN 'RUN_FAILED' THEN 'run.failed'
            WHEN 'RUN_CANCELLED' THEN 'run.cancelled'
        END AS mapped_type,
        CASE
            WHEN re.payload->>'stepId' ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
                THEN (re.payload->>'stepId')::UUID
            ELSE re.id
        END AS mapped_item_id,
        CASE
            WHEN re.payload->>'chatConfirmationId' ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
                THEN (re.payload->>'chatConfirmationId')::UUID
            ELSE re.id
        END AS mapped_interaction_id
    FROM run_events re
    JOIN agent_runs ar
      ON ar.workspace_id=re.workspace_id AND ar.id=re.run_id
)
INSERT INTO protocol_events(
    id,workspace_id,agent_id,conversation_id,run_id,stream_id,
    sequence_no,event_type,spec_version,item_id,payload,occurred_at
)
SELECT
    legacy.id,legacy.workspace_id,legacy.agent_id,legacy.session_id,legacy.run_id,
    legacy.run_id,legacy.sequence_no,legacy.mapped_type,'1.0',
    CASE WHEN legacy.event_type LIKE 'STEP_%' THEN legacy.mapped_item_id END,
    jsonb_build_object(
        'specVersion','1.0',
        'type',legacy.mapped_type,
        'eventId',legacy.id,
        'streamId','run:' || legacy.run_id::TEXT,
        'sequence',legacy.sequence_no,
        'occurredAt',legacy.created_at,
        'workspaceId',legacy.workspace_id,
        'agentId',legacy.agent_id,
        'conversationId',legacy.session_id,
        'runId',legacy.run_id,
        'traceId',legacy.trace_id,
        'data',
            jsonb_build_object(
                'legacyEventType',legacy.event_type,
                'legacyPayload',legacy.payload
            ) ||
            CASE
                WHEN legacy.event_type LIKE 'RUN_%' THEN
                    jsonb_build_object(
                        'run',jsonb_strip_nulls(jsonb_build_object(
                            'id',legacy.run_id,
                            'conversationId',legacy.session_id,
                            'agentId',legacy.agent_id,
                            'status',CASE legacy.event_type
                                WHEN 'RUN_STARTED' THEN 'running'
                                WHEN 'RUN_WAITING_CONFIRMATION' THEN 'waiting_interaction'
                                WHEN 'RUN_RESUMED' THEN 'running'
                                WHEN 'RUN_COMPLETED' THEN 'completed'
                                WHEN 'RUN_FAILED' THEN 'failed'
                                WHEN 'RUN_CANCELLED' THEN 'cancelled'
                            END,
                            'trigger',CASE upper(legacy.trigger_type)
                                WHEN 'CHAT' THEN 'message'
                                WHEN 'WORKFLOW' THEN 'workflow'
                                WHEN 'API' THEN 'api'
                                ELSE 'system'
                            END,
                            'startedAt',legacy.started_at,
                            'completedAt',CASE
                                WHEN legacy.event_type IN ('RUN_COMPLETED','RUN_FAILED','RUN_CANCELLED')
                                    THEN coalesce(legacy.finished_at,legacy.created_at)
                            END
                        ))
                    ) || CASE legacy.event_type
                        WHEN 'RUN_WAITING_CONFIRMATION' THEN jsonb_build_object(
                            'interactionIds',jsonb_build_array(legacy.mapped_interaction_id)
                        )
                        WHEN 'RUN_RESUMED' THEN jsonb_build_object(
                            'interactionId',legacy.mapped_interaction_id
                        )
                        ELSE '{}'::JSONB
                    END
                ELSE
                    jsonb_build_object(
                        'item',jsonb_build_object(
                            'id',legacy.mapped_item_id,
                            'type','notice',
                            'status',CASE legacy.event_type
                                WHEN 'STEP_STARTED' THEN 'in_progress'
                                ELSE 'completed'
                            END,
                            'code','LEGACY_' || legacy.event_type,
                            'message','Imported legacy execution step event'
                        )
                    )
            END
    ),
    legacy.created_at
FROM legacy
ORDER BY legacy.created_at,legacy.run_id,legacy.sequence_no;

CREATE FUNCTION reject_legacy_run_event_insert()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'run_events cutover is complete; append through protocol_events'
        USING ERRCODE = '55000',
              CONSTRAINT = 'run_events_cutover_complete';
END;
$$;

CREATE TRIGGER run_events_cutover_complete
BEFORE INSERT ON run_events
FOR EACH ROW EXECUTE FUNCTION reject_legacy_run_event_insert();


-- ##########################################################################
-- Source: 000041_agent_access_clients.up.sql
-- ##########################################################################

CREATE FUNCTION agent_access_cors_origins_valid(origins JSONB)
RETURNS BOOLEAN
LANGUAGE SQL
IMMUTABLE
PARALLEL SAFE
STRICT
AS $$
    SELECT CASE
        WHEN jsonb_typeof(origins) <> 'array' THEN FALSE
        WHEN jsonb_array_length(origins) > 32 THEN FALSE
        ELSE
            NOT EXISTS (
                SELECT 1
                FROM jsonb_array_elements(origins) AS element(value)
                WHERE jsonb_typeof(element.value) <> 'string'
                   OR length(element.value #>> '{}') = 0
                   OR length(element.value #>> '{}') > 2048
                   OR btrim(element.value #>> '{}') <> element.value #>> '{}'
                   OR NOT (
                        element.value #>> '{}' ~ '^https://[A-Za-z0-9.-]+(:[0-9]{1,5})?$'
                        OR element.value #>> '{}' ~ '^http://(localhost|127\.0\.0\.1)(:[0-9]{1,5})?$'
                   )
            )
            AND jsonb_array_length(origins) = (
                SELECT count(DISTINCT element.value #>> '{}')
                FROM jsonb_array_elements(origins) AS element(value)
            )
    END
$$;

CREATE TABLE service_principals (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    name VARCHAR(120) NOT NULL,
    status TEXT NOT NULL DEFAULT 'ACTIVE',
    security_version BIGINT NOT NULL DEFAULT 1,
    created_by UUID NOT NULL,
    updated_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    disabled_at TIMESTAMPTZ,
    lock_version BIGINT NOT NULL DEFAULT 1,
    CONSTRAINT service_principals_workspace_id_key UNIQUE (workspace_id, id),
    CONSTRAINT service_principals_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT service_principals_created_by_fk
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT service_principals_updated_by_fk
        FOREIGN KEY (updated_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT service_principals_name_not_blank
        CHECK (length(btrim(name)) > 0),
    CONSTRAINT service_principals_status_check
        CHECK (status IN ('ACTIVE', 'DISABLED')),
    CONSTRAINT service_principals_security_version_check
        CHECK (security_version > 0),
    CONSTRAINT service_principals_lock_version_check
        CHECK (lock_version > 0),
    CONSTRAINT service_principals_timestamps_check
        CHECK (updated_at >= created_at),
    CONSTRAINT service_principals_disabled_state_check CHECK (
        (status = 'ACTIVE' AND disabled_at IS NULL)
        OR (status = 'DISABLED' AND disabled_at IS NOT NULL)
    ),
    CONSTRAINT service_principals_disabled_at_check
        CHECK (disabled_at IS NULL OR disabled_at >= created_at)
);

CREATE INDEX service_principals_workspace_status_updated_idx
    ON service_principals (workspace_id, status, updated_at DESC, id);

CREATE TABLE agent_access_clients (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    service_principal_id UUID NOT NULL,
    client_id TEXT NOT NULL,
    name VARCHAR(120) NOT NULL,
    status TEXT NOT NULL DEFAULT 'ACTIVE',
    auth_method TEXT NOT NULL,
    jwks_uri TEXT,
    trusted_subject_issuer TEXT,
    trusted_subject_jwks_uri TEXT,
    allowed_cors_origins JSONB NOT NULL DEFAULT '[]'::JSONB,
    token_ttl_seconds INTEGER NOT NULL DEFAULT 600,
    created_by UUID NOT NULL,
    updated_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    disabled_at TIMESTAMPTZ,
    lock_version BIGINT NOT NULL DEFAULT 1,
    CONSTRAINT agent_access_clients_client_id_key UNIQUE (client_id),
    CONSTRAINT agent_access_clients_workspace_id_key UNIQUE (workspace_id, id),
    CONSTRAINT agent_access_clients_workspace_principal_key
        UNIQUE (workspace_id, service_principal_id),
    CONSTRAINT agent_access_clients_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT agent_access_clients_principal_scope_fk
        FOREIGN KEY (workspace_id, service_principal_id)
        REFERENCES service_principals (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT agent_access_clients_created_by_fk
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT agent_access_clients_updated_by_fk
        FOREIGN KEY (updated_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT agent_access_clients_client_id_format_check CHECK (
        client_id ~ '^awcl_[A-Za-z0-9_-]{32,128}$'
    ),
    CONSTRAINT agent_access_clients_name_not_blank
        CHECK (length(btrim(name)) > 0),
    CONSTRAINT agent_access_clients_status_check
        CHECK (status IN ('ACTIVE', 'DISABLED')),
    CONSTRAINT agent_access_clients_auth_method_check
        CHECK (auth_method IN ('client_secret_basic', 'private_key_jwt')),
    CONSTRAINT agent_access_clients_auth_config_check CHECK (
        (auth_method = 'client_secret_basic' AND jwks_uri IS NULL)
        OR (auth_method = 'private_key_jwt' AND jwks_uri IS NOT NULL)
    ),
    CONSTRAINT agent_access_clients_jwks_uri_check CHECK (
        jwks_uri IS NULL OR (
            length(jwks_uri) <= 2048
            AND btrim(jwks_uri) = jwks_uri
            AND jwks_uri ~ '^https://[^[:space:]#]+$'
        )
    ),
    CONSTRAINT agent_access_clients_subject_trust_pair_check CHECK (
        (trusted_subject_issuer IS NULL) = (trusted_subject_jwks_uri IS NULL)
    ),
    CONSTRAINT agent_access_clients_subject_issuer_check CHECK (
        trusted_subject_issuer IS NULL OR (
            length(trusted_subject_issuer) <= 2048
            AND btrim(trusted_subject_issuer) = trusted_subject_issuer
            AND trusted_subject_issuer ~ '^https://[^[:space:]?#]+/?$'
        )
    ),
    CONSTRAINT agent_access_clients_subject_jwks_uri_check CHECK (
        trusted_subject_jwks_uri IS NULL OR (
            length(trusted_subject_jwks_uri) <= 2048
            AND btrim(trusted_subject_jwks_uri) = trusted_subject_jwks_uri
            AND trusted_subject_jwks_uri ~ '^https://[^[:space:]#]+$'
        )
    ),
    CONSTRAINT agent_access_clients_cors_origins_check
        CHECK (agent_access_cors_origins_valid(allowed_cors_origins)),
    CONSTRAINT agent_access_clients_token_ttl_check
        CHECK (token_ttl_seconds BETWEEN 60 AND 900),
    CONSTRAINT agent_access_clients_lock_version_check
        CHECK (lock_version > 0),
    CONSTRAINT agent_access_clients_timestamps_check
        CHECK (updated_at >= created_at),
    CONSTRAINT agent_access_clients_disabled_state_check CHECK (
        (status = 'ACTIVE' AND disabled_at IS NULL)
        OR (status = 'DISABLED' AND disabled_at IS NOT NULL)
    ),
    CONSTRAINT agent_access_clients_disabled_at_check
        CHECK (disabled_at IS NULL OR disabled_at >= created_at)
);

CREATE INDEX agent_access_clients_workspace_status_updated_idx
    ON agent_access_clients (workspace_id, status, updated_at DESC, id);



-- ##########################################################################
-- Source: 000042_agent_access_credentials.up.sql
-- ##########################################################################

CREATE TABLE agent_access_credentials (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    client_id UUID NOT NULL,
    credential_type TEXT NOT NULL,
    secret_hash BYTEA,
    jwk_thumbprint BYTEA,
    certificate_thumbprint BYTEA,
    public_hint VARCHAR(120) NOT NULL,
    valid_from TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    expires_at TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    revoked_by UUID,
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    lock_version BIGINT NOT NULL DEFAULT 1,
    CONSTRAINT agent_access_credentials_workspace_client_id_key
        UNIQUE (workspace_id, client_id, id),
    CONSTRAINT agent_access_credentials_client_scope_fk
        FOREIGN KEY (workspace_id, client_id)
        REFERENCES agent_access_clients (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT agent_access_credentials_revoked_by_fk
        FOREIGN KEY (revoked_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT agent_access_credentials_created_by_fk
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT agent_access_credentials_type_check CHECK (
        credential_type IN ('client_secret', 'jwk', 'mtls_certificate')
    ),
    CONSTRAINT agent_access_credentials_material_check CHECK (
        (
            credential_type = 'client_secret'
            AND secret_hash IS NOT NULL
            AND octet_length(secret_hash) = 32
            AND jwk_thumbprint IS NULL
            AND certificate_thumbprint IS NULL
        ) OR (
            credential_type = 'jwk'
            AND secret_hash IS NULL
            AND jwk_thumbprint IS NOT NULL
            AND octet_length(jwk_thumbprint) = 32
            AND certificate_thumbprint IS NULL
        ) OR (
            credential_type = 'mtls_certificate'
            AND secret_hash IS NULL
            AND jwk_thumbprint IS NULL
            AND certificate_thumbprint IS NOT NULL
            AND octet_length(certificate_thumbprint) = 32
        )
    ),
    CONSTRAINT agent_access_credentials_public_hint_check
        CHECK (length(btrim(public_hint)) > 0),
    CONSTRAINT agent_access_credentials_validity_check
        CHECK (expires_at IS NULL OR expires_at > valid_from),
    CONSTRAINT agent_access_credentials_last_used_check
        CHECK (last_used_at IS NULL OR last_used_at >= valid_from),
    CONSTRAINT agent_access_credentials_revocation_pair_check
        CHECK ((revoked_at IS NULL) = (revoked_by IS NULL)),
    CONSTRAINT agent_access_credentials_revoked_at_check
        CHECK (revoked_at IS NULL OR revoked_at >= valid_from),
    CONSTRAINT agent_access_credentials_lock_version_check
        CHECK (lock_version > 0)
);

CREATE INDEX agent_access_credentials_active_lookup_idx
    ON agent_access_credentials (
        workspace_id, client_id, credential_type, valid_from DESC, id
    )
    WHERE revoked_at IS NULL;

CREATE INDEX agent_access_credentials_expiry_idx
    ON agent_access_credentials (expires_at, id)
    WHERE revoked_at IS NULL AND expires_at IS NOT NULL;

CREATE UNIQUE INDEX agent_access_credentials_secret_hash_key
    ON agent_access_credentials (secret_hash)
    WHERE secret_hash IS NOT NULL;

CREATE FUNCTION enforce_agent_access_credential_evidence()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'Agent Access credentials must be revoked and cannot be deleted'
            USING ERRCODE = '55000',
                  CONSTRAINT = 'agent_access_credentials_permanent_evidence';
    END IF;
    IF ROW(
        NEW.id, NEW.workspace_id, NEW.client_id, NEW.credential_type,
        NEW.secret_hash, NEW.jwk_thumbprint, NEW.certificate_thumbprint,
        NEW.public_hint, NEW.valid_from,
        NEW.created_by, NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.client_id, OLD.credential_type,
        OLD.secret_hash, OLD.jwk_thumbprint, OLD.certificate_thumbprint,
        OLD.public_hint, OLD.valid_from,
        OLD.created_by, OLD.created_at
    ) THEN
        RAISE EXCEPTION 'Agent Access credential authentication evidence is immutable'
            USING ERRCODE = '55000',
                  CONSTRAINT = 'agent_access_credentials_immutable_auth';
    END IF;
    IF NEW.expires_at IS DISTINCT FROM OLD.expires_at AND (
        NEW.expires_at IS NULL
        OR NEW.expires_at <= clock_timestamp()
        OR (OLD.expires_at IS NOT NULL AND NEW.expires_at >= OLD.expires_at)
    ) THEN
        RAISE EXCEPTION 'Agent Access credential expiry may only be shortened to a future instant'
            USING ERRCODE = '55000',
                  CONSTRAINT = 'agent_access_credentials_expiry_extension_forbidden';
    END IF;
    IF OLD.revoked_at IS NOT NULL AND ROW(NEW.revoked_at, NEW.revoked_by)
        IS DISTINCT FROM ROW(OLD.revoked_at, OLD.revoked_by) THEN
        RAISE EXCEPTION 'Agent Access credential revocation is permanent'
            USING ERRCODE = '55000',
                  CONSTRAINT = 'agent_access_credentials_permanent_revocation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER agent_access_credentials_permanent_evidence
BEFORE UPDATE OR DELETE ON agent_access_credentials
FOR EACH ROW EXECUTE FUNCTION enforce_agent_access_credential_evidence();


-- ##########################################################################
-- Source: 000043_agent_access_grants.up.sql
-- ##########################################################################

CREATE FUNCTION agent_access_grant_scopes_valid(scopes JSONB)
RETURNS BOOLEAN
LANGUAGE plpgsql
IMMUTABLE
PARALLEL SAFE
STRICT
AS $$
DECLARE
    scope_value JSONB;
BEGIN
    IF jsonb_typeof(scopes) <> 'array'
       OR jsonb_array_length(scopes) < 1
       OR jsonb_array_length(scopes) > 9 THEN
        RETURN FALSE;
    END IF;
    FOR scope_value IN SELECT value FROM jsonb_array_elements(scopes)
    LOOP
        IF jsonb_typeof(scope_value) <> 'string'
           OR scope_value #>> '{}' NOT IN (
                'agent:read',
                'conversation:create',
                'conversation:read',
                'run:create',
                'run:read',
                'run:cancel',
                'event:read',
                'interaction:decide',
                'artifact:read'
           ) THEN
            RETURN FALSE;
        END IF;
    END LOOP;
    RETURN jsonb_array_length(scopes) = (
        SELECT count(DISTINCT value #>> '{}') FROM jsonb_array_elements(scopes)
    );
END;
$$;

CREATE FUNCTION agent_access_grant_policy_valid(policy JSONB)
RETURNS BOOLEAN
LANGUAGE plpgsql
IMMUTABLE
PARALLEL SAFE
STRICT
AS $$
DECLARE
    service_decision JSONB;
BEGIN
    IF jsonb_typeof(policy) <> 'object' OR EXISTS (
        SELECT 1 FROM jsonb_object_keys(policy) AS key
        WHERE key <> 'serviceDecision'
    ) THEN
        RETURN FALSE;
    END IF;
    IF NOT policy ? 'serviceDecision' THEN
        RETURN TRUE;
    END IF;
    service_decision := policy->'serviceDecision';
    IF jsonb_typeof(service_decision) <> 'object'
       OR NOT service_decision ? 'enabled'
       OR jsonb_typeof(service_decision->'enabled') <> 'boolean'
       OR EXISTS (
            SELECT 1 FROM jsonb_object_keys(service_decision) AS key
            WHERE key NOT IN ('enabled', 'maxRisk')
       ) THEN
        RETURN FALSE;
    END IF;
    IF (service_decision->>'enabled')::BOOLEAN THEN
        RETURN service_decision->>'maxRisk' IN ('low', 'medium')
               AND jsonb_typeof(service_decision->'maxRisk') = 'string';
    END IF;
    RETURN NOT service_decision ? 'maxRisk';
END;
$$;

CREATE TABLE agent_access_grants (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    client_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    scopes JSONB NOT NULL,
    policy JSONB NOT NULL DEFAULT '{}'::JSONB,
    status TEXT NOT NULL DEFAULT 'ACTIVE',
    valid_from TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    expires_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    revoked_by UUID,
    created_by UUID NOT NULL,
    updated_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    lock_version BIGINT NOT NULL DEFAULT 1,
    CONSTRAINT agent_access_grants_workspace_id_key UNIQUE (workspace_id, id),
    CONSTRAINT agent_access_grants_client_scope_fk
        FOREIGN KEY (workspace_id, client_id)
        REFERENCES agent_access_clients (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT agent_access_grants_agent_scope_fk
        FOREIGN KEY (workspace_id, agent_id)
        REFERENCES agents (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT agent_access_grants_revoked_by_fk
        FOREIGN KEY (revoked_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT agent_access_grants_created_by_fk
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT agent_access_grants_updated_by_fk
        FOREIGN KEY (updated_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT agent_access_grants_scopes_check
        CHECK (agent_access_grant_scopes_valid(scopes)),
    CONSTRAINT agent_access_grants_policy_check
        CHECK (agent_access_grant_policy_valid(policy)),
    CONSTRAINT agent_access_grants_status_check
        CHECK (status IN ('ACTIVE', 'REVOKED')),
    CONSTRAINT agent_access_grants_validity_check
        CHECK (expires_at IS NULL OR expires_at > valid_from),
    CONSTRAINT agent_access_grants_revocation_state_check CHECK (
        (status = 'ACTIVE' AND revoked_at IS NULL AND revoked_by IS NULL)
        OR (status = 'REVOKED' AND revoked_at IS NOT NULL AND revoked_by IS NOT NULL)
    ),
    CONSTRAINT agent_access_grants_revoked_at_check
        CHECK (revoked_at IS NULL OR revoked_at >= valid_from),
    CONSTRAINT agent_access_grants_lock_version_check
        CHECK (lock_version > 0),
    CONSTRAINT agent_access_grants_timestamps_check
        CHECK (updated_at >= created_at)
);

CREATE INDEX agent_access_grants_client_agent_validity_idx
    ON agent_access_grants (
        workspace_id, client_id, agent_id, valid_from, expires_at, id
    )
    WHERE status = 'ACTIVE';

CREATE INDEX agent_access_grants_agent_status_updated_idx
    ON agent_access_grants (workspace_id, agent_id, status, updated_at DESC, id);

CREATE FUNCTION enforce_agent_access_grant_window()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND ROW(NEW.id, NEW.workspace_id, NEW.client_id, NEW.agent_id)
        IS DISTINCT FROM ROW(OLD.id, OLD.workspace_id, OLD.client_id, OLD.agent_id) THEN
        RAISE EXCEPTION 'Agent Access Grant identity is immutable'
            USING ERRCODE = '55000',
                  CONSTRAINT = 'agent_access_grants_immutable_identity';
    END IF;
    IF TG_OP = 'UPDATE' AND OLD.status = 'REVOKED' AND ROW(NEW.status, NEW.revoked_at, NEW.revoked_by)
        IS DISTINCT FROM ROW(OLD.status, OLD.revoked_at, OLD.revoked_by) THEN
        RAISE EXCEPTION 'Agent Access Grant revocation is permanent'
            USING ERRCODE = '55000',
                  CONSTRAINT = 'agent_access_grants_permanent_revocation';
    END IF;
    IF NEW.status = 'ACTIVE' THEN
        -- Serialize all Grant mutations for one Client. This closes the race
        -- between the overlap query and a concurrent insert without requiring
        -- a database-wide extension or a coarse table lock.
        PERFORM 1
        FROM agent_access_clients
        WHERE workspace_id = NEW.workspace_id AND id = NEW.client_id
        FOR UPDATE;
        IF EXISTS (
            SELECT 1
            FROM agent_access_grants existing
            WHERE existing.workspace_id = NEW.workspace_id
              AND existing.client_id = NEW.client_id
              AND existing.agent_id = NEW.agent_id
              AND existing.status = 'ACTIVE'
              AND existing.id <> NEW.id
              AND tstzrange(existing.valid_from, existing.expires_at, '[)')
                  && tstzrange(NEW.valid_from, NEW.expires_at, '[)')
        ) THEN
            RAISE EXCEPTION 'overlapping active Agent Access Grant'
                USING ERRCODE = '23P01',
                      CONSTRAINT = 'agent_access_grants_active_window_excl';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER agent_access_grants_window_guard
BEFORE INSERT OR UPDATE ON agent_access_grants
FOR EACH ROW EXECUTE FUNCTION enforce_agent_access_grant_window();



-- ##########################################################################
-- Source: 000044_external_subjects.up.sql
-- ##########################################################################

CREATE TABLE external_subjects (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    client_id UUID NOT NULL,
    issuer TEXT NOT NULL,
    subject_hash BYTEA NOT NULL,
    display_ref VARCHAR(120),
    status TEXT NOT NULL DEFAULT 'ACTIVE',
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    disabled_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    lock_version BIGINT NOT NULL DEFAULT 1,
    CONSTRAINT external_subjects_workspace_id_key UNIQUE (workspace_id, id),
    CONSTRAINT external_subjects_identity_key
        UNIQUE (workspace_id, client_id, issuer, subject_hash),
    CONSTRAINT external_subjects_client_scope_fk
        FOREIGN KEY (workspace_id, client_id)
        REFERENCES agent_access_clients (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT external_subjects_issuer_check CHECK (
        length(issuer) <= 2048
        AND btrim(issuer) = issuer
        AND issuer ~ '^https://[^[:space:]?#]+/?$'
    ),
    CONSTRAINT external_subjects_subject_hash_check
        CHECK (octet_length(subject_hash) = 32),
    CONSTRAINT external_subjects_display_ref_check CHECK (
        display_ref IS NULL OR display_ref ~ '^ref_[A-Za-z0-9_-]{1,116}$'
    ),
    CONSTRAINT external_subjects_status_check
        CHECK (status IN ('ACTIVE', 'DISABLED')),
    CONSTRAINT external_subjects_seen_order_check
        CHECK (last_seen_at >= first_seen_at),
    CONSTRAINT external_subjects_disabled_state_check CHECK (
        (status = 'ACTIVE' AND disabled_at IS NULL)
        OR (status = 'DISABLED' AND disabled_at IS NOT NULL)
    ),
    CONSTRAINT external_subjects_disabled_at_check
        CHECK (disabled_at IS NULL OR disabled_at >= first_seen_at),
    CONSTRAINT external_subjects_timestamps_check CHECK (
        created_at >= first_seen_at AND updated_at >= created_at
    ),
    CONSTRAINT external_subjects_lock_version_check
        CHECK (lock_version > 0)
);

CREATE INDEX external_subjects_client_status_seen_idx
    ON external_subjects (
        workspace_id, client_id, status, last_seen_at DESC, id
    );

CREATE FUNCTION enforce_external_subject_identity()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF ROW(
        NEW.id, NEW.workspace_id, NEW.client_id, NEW.issuer,
        NEW.subject_hash, NEW.first_seen_at, NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.client_id, OLD.issuer,
        OLD.subject_hash, OLD.first_seen_at, OLD.created_at
    ) THEN
        RAISE EXCEPTION 'External Subject identity evidence is immutable'
            USING ERRCODE = '55000',
                  CONSTRAINT = 'external_subjects_immutable_identity';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER external_subjects_identity_guard
BEFORE UPDATE ON external_subjects
FOR EACH ROW EXECUTE FUNCTION enforce_external_subject_identity();


-- ##########################################################################
-- Source: 000045_agent_access_management_commands.up.sql
-- ##########################################################################

CREATE TABLE agent_access_management_commands (
    workspace_id UUID NOT NULL,
    actor_id UUID NOT NULL,
    idempotency_key UUID NOT NULL,
    operation VARCHAR(255) NOT NULL,
    request_hash BYTEA NOT NULL,
    state TEXT NOT NULL DEFAULT 'PENDING',
    response_status INTEGER,
    response_body JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    completed_at TIMESTAMPTZ,
    PRIMARY KEY (workspace_id, actor_id, idempotency_key),
    CONSTRAINT agent_access_management_commands_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT agent_access_management_commands_actor_fk
        FOREIGN KEY (actor_id) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT agent_access_management_commands_operation_check CHECK (
        operation ~ '^[a-z0-9][a-z0-9:-]{0,254}$'
    ),
    CONSTRAINT agent_access_management_commands_request_hash_check
        CHECK (octet_length(request_hash) = 32),
    CONSTRAINT agent_access_management_commands_state_check
        CHECK (state IN ('PENDING', 'COMPLETED')),
    CONSTRAINT agent_access_management_commands_response_check CHECK (
        (state = 'PENDING' AND response_status IS NULL
            AND response_body IS NULL AND completed_at IS NULL)
        OR
        (state = 'COMPLETED' AND response_status BETWEEN 200 AND 299
            AND response_body IS NOT NULL AND completed_at IS NOT NULL
            AND jsonb_typeof(response_body) = 'object')
    ),
    CONSTRAINT agent_access_management_commands_completed_at_check
        CHECK (completed_at IS NULL OR completed_at >= created_at)
);

CREATE INDEX agent_access_management_commands_created_idx
    ON agent_access_management_commands (workspace_id, created_at DESC);

CREATE FUNCTION enforce_agent_access_management_command_lifecycle()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF ROW(
        NEW.workspace_id, NEW.actor_id, NEW.idempotency_key,
        NEW.operation, NEW.request_hash, NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.workspace_id, OLD.actor_id, OLD.idempotency_key,
        OLD.operation, OLD.request_hash, OLD.created_at
    ) THEN
        RAISE EXCEPTION 'Agent Access management command identity is immutable'
            USING ERRCODE = '55000',
                  CONSTRAINT = 'agent_access_management_commands_immutable_identity';
    END IF;
    IF OLD.state <> 'PENDING' OR NEW.state <> 'COMPLETED' THEN
        RAISE EXCEPTION 'Agent Access management command transition is invalid'
            USING ERRCODE = '55000',
                  CONSTRAINT = 'agent_access_management_commands_lifecycle';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER agent_access_management_commands_lifecycle_guard
BEFORE UPDATE ON agent_access_management_commands
FOR EACH ROW EXECUTE FUNCTION enforce_agent_access_management_command_lifecycle();


-- ##########################################################################
-- Source: 000046_agent_access_client_assertion_jtis.up.sql
-- ##########################################################################

CREATE TABLE agent_access_client_assertion_jtis (
    client_id UUID NOT NULL,
    jti_hash BYTEA NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (client_id, jti_hash),
    CONSTRAINT agent_access_client_assertion_jtis_client_fk
        FOREIGN KEY (client_id) REFERENCES agent_access_clients (id) ON DELETE RESTRICT,
    CONSTRAINT agent_access_client_assertion_jtis_hash_check
        CHECK (octet_length(jti_hash) = 32),
    CONSTRAINT agent_access_client_assertion_jtis_expiry_check
        CHECK (expires_at > created_at)
);

CREATE INDEX agent_access_client_assertion_jtis_expiry_idx
    ON agent_access_client_assertion_jtis (expires_at, client_id);

CREATE FUNCTION enforce_agent_access_client_assertion_jti_immutable()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        RAISE EXCEPTION 'Agent Access Client Assertion JTI evidence is immutable'
            USING ERRCODE = '55000',
                  CONSTRAINT = 'agent_access_client_assertion_jtis_immutable';
    END IF;
    RETURN OLD;
END;
$$;

CREATE TRIGGER agent_access_client_assertion_jtis_immutable
BEFORE UPDATE ON agent_access_client_assertion_jtis
FOR EACH ROW EXECUTE FUNCTION enforce_agent_access_client_assertion_jti_immutable();



-- ##########################################################################
-- Source: 000047_agent_access_token_ttl.up.sql
-- ##########################################################################

ALTER TABLE agent_access_clients
    DROP CONSTRAINT agent_access_clients_token_ttl_check;

-- AAP v1 only issues 5-15 minute Access Tokens. Fail the migration instead of
-- silently lengthening an operator-selected TTL on an existing Client.
ALTER TABLE agent_access_clients
    ADD CONSTRAINT agent_access_clients_token_ttl_check
    CHECK (token_ttl_seconds BETWEEN 300 AND 900);


-- ##########################################################################
-- Source: 000048_principal_refs.up.sql
-- ##########################################################################

CREATE TABLE principal_refs (
    workspace_id UUID NOT NULL,
    principal_type TEXT NOT NULL,
    principal_id UUID NOT NULL,
    system_key TEXT,
    origin TEXT NOT NULL DEFAULT 'DIRECTORY',
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (workspace_id, principal_type, principal_id),
    CONSTRAINT principal_refs_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT principal_refs_type_check CHECK (
        principal_type IN ('USER', 'SERVICE_PRINCIPAL', 'EXTERNAL_SUBJECT', 'SYSTEM')
    ),
    CONSTRAINT principal_refs_origin_check CHECK (
        origin IN ('DIRECTORY', 'SYSTEM', 'LEGACY_EXECUTION')
    ),
    CONSTRAINT principal_refs_system_key_check CHECK (
        (principal_type = 'SYSTEM'
            AND system_key IS NOT NULL
            AND length(btrim(system_key)) BETWEEN 1 AND 120
            AND system_key = lower(system_key)
            AND system_key ~ '^[a-z][a-z0-9._:-]{0,119}$'
            AND origin IN ('SYSTEM', 'LEGACY_EXECUTION'))
        OR
        (principal_type <> 'SYSTEM' AND system_key IS NULL AND origin <> 'SYSTEM')
    )
);

CREATE UNIQUE INDEX principal_refs_workspace_system_key
    ON principal_refs (workspace_id, system_key)
    WHERE principal_type = 'SYSTEM';
CREATE INDEX principal_refs_identity_idx
    ON principal_refs (principal_type, principal_id, workspace_id);

-- Directory identities receive one stable reference per Workspace. A User can
-- legitimately have different references in multiple Workspaces while the
-- underlying User ID and all existing User-owned records remain unchanged.
INSERT INTO principal_refs(workspace_id,principal_type,principal_id,origin,created_at)
SELECT workspace_id,'USER',user_id,'DIRECTORY',joined_at
FROM workspace_members
ON CONFLICT DO NOTHING;

INSERT INTO principal_refs(workspace_id,principal_type,principal_id,origin,created_at)
SELECT id,'USER',owner_user_id,'DIRECTORY',created_at
FROM workspaces
ON CONFLICT DO NOTHING;

INSERT INTO principal_refs(workspace_id,principal_type,principal_id,origin,created_at)
SELECT workspace_id,'SERVICE_PRINCIPAL',id,'DIRECTORY',created_at
FROM service_principals
ON CONFLICT DO NOTHING;

INSERT INTO principal_refs(workspace_id,principal_type,principal_id,origin,created_at)
SELECT workspace_id,'EXTERNAL_SUBJECT',id,'DIRECTORY',created_at
FROM external_subjects
ON CONFLICT DO NOTHING;

-- Old execution rows had a typed UUID but no referential target. Preserve
-- their exact historical meaning without manufacturing a User. Resolved
-- status remains false when the referenced directory identity never existed.
INSERT INTO principal_refs(
    workspace_id,principal_type,principal_id,system_key,origin,created_at
)
SELECT workspace_id,principal_type,principal_id,
       CASE WHEN principal_type='SYSTEM' THEN 'legacy:' || principal_id::TEXT ELSE NULL END,
       'LEGACY_EXECUTION',min(occurred_at)
FROM (
    SELECT workspace_id,triggered_by_type AS principal_type,
           triggered_by_id AS principal_id,started_at AS occurred_at
    FROM agent_runs
    UNION ALL
    SELECT workspace_id,triggered_by_type,triggered_by_id,started_at
    FROM workflow_executions
    UNION ALL
    SELECT workspace_id,actor_type,actor_id,started_at
    FROM tool_invocations
) legacy
GROUP BY workspace_id,principal_type,principal_id
ON CONFLICT DO NOTHING;

CREATE FUNCTION validate_principal_ref_target()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.origin = 'LEGACY_EXECUTION' THEN
        RAISE EXCEPTION 'Legacy Principal Ref origin is reserved for migration'
            USING ERRCODE = '23514', CONSTRAINT = 'principal_refs_legacy_origin_reserved';
    END IF;
    CASE NEW.principal_type
        WHEN 'USER' THEN
            IF NEW.origin <> 'DIRECTORY' OR NOT EXISTS (
                SELECT 1 FROM workspaces
                WHERE id=NEW.workspace_id AND owner_user_id=NEW.principal_id
                UNION ALL
                SELECT 1 FROM workspace_members
                WHERE workspace_id=NEW.workspace_id AND user_id=NEW.principal_id
            ) THEN
                RAISE EXCEPTION 'User Principal does not belong to Workspace'
                    USING ERRCODE = '23503', CONSTRAINT = 'principal_refs_user_target_fk';
            END IF;
        WHEN 'SERVICE_PRINCIPAL' THEN
            IF NEW.origin <> 'DIRECTORY' OR NOT EXISTS (
                SELECT 1 FROM service_principals
                WHERE workspace_id=NEW.workspace_id AND id=NEW.principal_id
            ) THEN
                RAISE EXCEPTION 'Service Principal does not belong to Workspace'
                    USING ERRCODE = '23503', CONSTRAINT = 'principal_refs_service_target_fk';
            END IF;
        WHEN 'EXTERNAL_SUBJECT' THEN
            IF NEW.origin <> 'DIRECTORY' OR NOT EXISTS (
                SELECT 1 FROM external_subjects
                WHERE workspace_id=NEW.workspace_id AND id=NEW.principal_id
            ) THEN
                RAISE EXCEPTION 'External Subject does not belong to Workspace'
                    USING ERRCODE = '23503', CONSTRAINT = 'principal_refs_subject_target_fk';
            END IF;
        WHEN 'SYSTEM' THEN
            IF NEW.origin <> 'SYSTEM' THEN
                RAISE EXCEPTION 'System Principal requires explicit SYSTEM origin'
                    USING ERRCODE = '23514', CONSTRAINT = 'principal_refs_system_origin';
            END IF;
        ELSE
            RAISE EXCEPTION 'Unknown Principal type'
                USING ERRCODE = '23514', CONSTRAINT = 'principal_refs_type_check';
    END CASE;
    RETURN NEW;
END;
$$;

CREATE TRIGGER principal_refs_target_guard
BEFORE INSERT ON principal_refs
FOR EACH ROW EXECUTE FUNCTION validate_principal_ref_target();

CREATE FUNCTION reject_principal_ref_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'Principal Refs are immutable and permanently retained'
        USING ERRCODE = '55000', CONSTRAINT = 'principal_refs_immutable';
END;
$$;

CREATE TRIGGER principal_refs_immutable_guard
BEFORE UPDATE OR DELETE ON principal_refs
FOR EACH ROW EXECUTE FUNCTION reject_principal_ref_mutation();

CREATE FUNCTION register_directory_principal_ref()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_TABLE_NAME = 'workspaces' THEN
        INSERT INTO principal_refs(workspace_id,principal_type,principal_id,origin,created_at)
        VALUES(NEW.id,'USER',NEW.owner_user_id,'DIRECTORY',NEW.created_at)
        ON CONFLICT DO NOTHING;
    ELSIF TG_TABLE_NAME = 'workspace_members' THEN
        INSERT INTO principal_refs(workspace_id,principal_type,principal_id,origin,created_at)
        VALUES(NEW.workspace_id,'USER',NEW.user_id,'DIRECTORY',NEW.joined_at)
        ON CONFLICT DO NOTHING;
    ELSIF TG_TABLE_NAME = 'service_principals' THEN
        INSERT INTO principal_refs(workspace_id,principal_type,principal_id,origin,created_at)
        VALUES(NEW.workspace_id,'SERVICE_PRINCIPAL',NEW.id,'DIRECTORY',NEW.created_at)
        ON CONFLICT DO NOTHING;
    ELSIF TG_TABLE_NAME = 'external_subjects' THEN
        INSERT INTO principal_refs(workspace_id,principal_type,principal_id,origin,created_at)
        VALUES(NEW.workspace_id,'EXTERNAL_SUBJECT',NEW.id,'DIRECTORY',NEW.created_at)
        ON CONFLICT DO NOTHING;
    ELSE
        RAISE EXCEPTION 'Unsupported Principal directory source'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER workspaces_owner_principal_ref
AFTER INSERT ON workspaces
FOR EACH ROW EXECUTE FUNCTION register_directory_principal_ref();
CREATE TRIGGER workspace_members_principal_ref
AFTER INSERT ON workspace_members
FOR EACH ROW EXECUTE FUNCTION register_directory_principal_ref();
CREATE TRIGGER service_principals_principal_ref
AFTER INSERT ON service_principals
FOR EACH ROW EXECUTE FUNCTION register_directory_principal_ref();
CREATE TRIGGER external_subjects_principal_ref
AFTER INSERT ON external_subjects
FOR EACH ROW EXECUTE FUNCTION register_directory_principal_ref();

ALTER TABLE agent_runs
    ADD CONSTRAINT agent_runs_principal_ref_fk
    FOREIGN KEY (workspace_id,triggered_by_type,triggered_by_id)
    REFERENCES principal_refs (workspace_id,principal_type,principal_id)
    ON DELETE RESTRICT;

ALTER TABLE workflow_executions
    ADD CONSTRAINT workflow_executions_principal_ref_fk
    FOREIGN KEY (workspace_id,triggered_by_type,triggered_by_id)
    REFERENCES principal_refs (workspace_id,principal_type,principal_id)
    ON DELETE RESTRICT;

ALTER TABLE tool_invocations
    ADD CONSTRAINT tool_invocations_principal_ref_fk
    FOREIGN KEY (workspace_id,actor_type,actor_id)
    REFERENCES principal_refs (workspace_id,principal_type,principal_id)
    ON DELETE RESTRICT;


-- ##########################################################################
-- Source: 000049_principal_aware_chat_ownership.up.sql
-- ##########################################################################

-- Chat facts preserve the transport Actor separately from the represented
-- Subject. created_by remains a compatibility projection for internal Users;
-- external callers never manufacture a User row.
ALTER TABLE chat_sessions
    ADD COLUMN actor_type TEXT,
    ADD COLUMN actor_id UUID,
    ADD COLUMN subject_type TEXT,
    ADD COLUMN subject_id UUID,
    ADD COLUMN client_id UUID,
    ADD COLUMN ownership_mode TEXT,
    ADD COLUMN ownership_policy_version BIGINT;

ALTER TABLE chat_messages
    ADD COLUMN actor_type TEXT,
    ADD COLUMN actor_id UUID,
    ADD COLUMN subject_type TEXT,
    ADD COLUMN subject_id UUID,
    ADD COLUMN client_id UUID,
    ADD COLUMN ownership_mode TEXT,
    ADD COLUMN ownership_policy_version BIGINT;

-- A stable runtime Principal represents assistant/system-produced Chat
-- messages. The same typed UUID is safe across Workspaces because Principal
-- Refs are Workspace scoped.
INSERT INTO principal_refs(
    workspace_id,principal_type,principal_id,system_key,origin,created_at
)
SELECT id,'SYSTEM','00000000-0000-0000-0000-000000000001'::UUID,
       'actweave:chat-runtime','SYSTEM',created_at
FROM workspaces
ON CONFLICT DO NOTHING;

-- Future Workspaces receive the same explicit runtime Ref together with the
-- existing owner User Ref.
CREATE OR REPLACE FUNCTION register_directory_principal_ref()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_TABLE_NAME = 'workspaces' THEN
        INSERT INTO principal_refs(workspace_id,principal_type,principal_id,origin,created_at)
        VALUES(NEW.id,'USER',NEW.owner_user_id,'DIRECTORY',NEW.created_at)
        ON CONFLICT DO NOTHING;
        INSERT INTO principal_refs(
            workspace_id,principal_type,principal_id,system_key,origin,created_at
        ) VALUES(
            NEW.id,'SYSTEM','00000000-0000-0000-0000-000000000001'::UUID,
            'actweave:chat-runtime','SYSTEM',NEW.created_at
        ) ON CONFLICT DO NOTHING;
    ELSIF TG_TABLE_NAME = 'workspace_members' THEN
        INSERT INTO principal_refs(workspace_id,principal_type,principal_id,origin,created_at)
        VALUES(NEW.workspace_id,'USER',NEW.user_id,'DIRECTORY',NEW.joined_at)
        ON CONFLICT DO NOTHING;
    ELSIF TG_TABLE_NAME = 'service_principals' THEN
        INSERT INTO principal_refs(workspace_id,principal_type,principal_id,origin,created_at)
        VALUES(NEW.workspace_id,'SERVICE_PRINCIPAL',NEW.id,'DIRECTORY',NEW.created_at)
        ON CONFLICT DO NOTHING;
    ELSIF TG_TABLE_NAME = 'external_subjects' THEN
        INSERT INTO principal_refs(workspace_id,principal_type,principal_id,origin,created_at)
        VALUES(NEW.workspace_id,'EXTERNAL_SUBJECT',NEW.id,'DIRECTORY',NEW.created_at)
        ON CONFLICT DO NOTHING;
    ELSE
        RAISE EXCEPTION 'Unsupported Principal directory source'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

UPDATE chat_sessions
SET actor_type='USER',actor_id=created_by,
    subject_type='USER',subject_id=created_by,
    ownership_mode='SUBJECT_OWNED',ownership_policy_version=1;

UPDATE chat_messages cm
SET actor_type=CASE WHEN cm.created_by IS NULL THEN 'SYSTEM' ELSE 'USER' END,
    actor_id=coalesce(
        cm.created_by,'00000000-0000-0000-0000-000000000001'::UUID
    ),
    subject_type=cs.subject_type,subject_id=cs.subject_id,
    client_id=cs.client_id,ownership_mode=cs.ownership_mode,
    ownership_policy_version=cs.ownership_policy_version
FROM chat_sessions cs
WHERE cs.workspace_id=cm.workspace_id AND cs.id=cm.session_id;

ALTER TABLE chat_sessions ALTER COLUMN created_by DROP NOT NULL;

ALTER TABLE chat_sessions
    ALTER COLUMN actor_type SET NOT NULL,
    ALTER COLUMN actor_id SET NOT NULL,
    ALTER COLUMN ownership_mode SET NOT NULL,
    ALTER COLUMN ownership_policy_version SET NOT NULL,
    ADD CONSTRAINT chat_sessions_actor_ref_fk
        FOREIGN KEY (workspace_id,actor_type,actor_id)
        REFERENCES principal_refs(workspace_id,principal_type,principal_id)
        ON DELETE RESTRICT,
    ADD CONSTRAINT chat_sessions_subject_ref_fk
        FOREIGN KEY (workspace_id,subject_type,subject_id)
        REFERENCES principal_refs(workspace_id,principal_type,principal_id)
        ON DELETE RESTRICT,
    ADD CONSTRAINT chat_sessions_client_scope_fk
        FOREIGN KEY (workspace_id,client_id)
        REFERENCES agent_access_clients(workspace_id,id) ON DELETE RESTRICT,
    ADD CONSTRAINT chat_sessions_subject_pair_check CHECK (
        (subject_type IS NULL) = (subject_id IS NULL)
    ),
    ADD CONSTRAINT chat_sessions_ownership_mode_check CHECK (
        ownership_mode IN ('SUBJECT_OWNED','POLICY_SHARED')
    ),
    ADD CONSTRAINT chat_sessions_ownership_policy_version_check
        CHECK (ownership_policy_version > 0);

ALTER TABLE chat_messages
    ALTER COLUMN actor_type SET NOT NULL,
    ALTER COLUMN actor_id SET NOT NULL,
    ALTER COLUMN ownership_mode SET NOT NULL,
    ALTER COLUMN ownership_policy_version SET NOT NULL,
    ADD CONSTRAINT chat_messages_actor_ref_fk
        FOREIGN KEY (workspace_id,actor_type,actor_id)
        REFERENCES principal_refs(workspace_id,principal_type,principal_id)
        ON DELETE RESTRICT,
    ADD CONSTRAINT chat_messages_subject_ref_fk
        FOREIGN KEY (workspace_id,subject_type,subject_id)
        REFERENCES principal_refs(workspace_id,principal_type,principal_id)
        ON DELETE RESTRICT,
    ADD CONSTRAINT chat_messages_client_scope_fk
        FOREIGN KEY (workspace_id,client_id)
        REFERENCES agent_access_clients(workspace_id,id) ON DELETE RESTRICT,
    ADD CONSTRAINT chat_messages_subject_pair_check CHECK (
        (subject_type IS NULL) = (subject_id IS NULL)
    ),
    ADD CONSTRAINT chat_messages_ownership_mode_check CHECK (
        ownership_mode IN ('SUBJECT_OWNED','POLICY_SHARED')
    ),
    ADD CONSTRAINT chat_messages_ownership_policy_version_check
        CHECK (ownership_policy_version > 0);

ALTER TABLE chat_messages DROP CONSTRAINT chat_messages_user_actor_check;

CREATE FUNCTION validate_chat_session_principal_ownership()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.actor_type IS NULL AND NEW.actor_id IS NULL AND NEW.created_by IS NOT NULL THEN
        NEW.actor_type := 'USER';
        NEW.actor_id := NEW.created_by;
        NEW.subject_type := 'USER';
        NEW.subject_id := NEW.created_by;
    END IF;
    NEW.ownership_mode := coalesce(NEW.ownership_mode,'SUBJECT_OWNED');
    NEW.ownership_policy_version := coalesce(NEW.ownership_policy_version,1);

    IF NEW.actor_type = 'USER' THEN
        IF NEW.created_by IS DISTINCT FROM NEW.actor_id
           OR NEW.subject_type IS DISTINCT FROM 'USER'
           OR NEW.subject_id IS DISTINCT FROM NEW.actor_id
           OR NEW.client_id IS NOT NULL
           OR NEW.ownership_mode <> 'SUBJECT_OWNED' THEN
            RAISE EXCEPTION 'Internal User Chat ownership is inconsistent'
                USING ERRCODE='23514',CONSTRAINT='chat_sessions_user_ownership_check';
        END IF;
    ELSIF NEW.actor_type = 'SERVICE_PRINCIPAL' THEN
        IF NEW.created_by IS NOT NULL OR NEW.client_id IS NULL OR NOT EXISTS (
            SELECT 1 FROM agent_access_clients c
            WHERE c.workspace_id=NEW.workspace_id AND c.id=NEW.client_id
              AND c.service_principal_id=NEW.actor_id
        ) THEN
            RAISE EXCEPTION 'Service Principal Chat Client binding is invalid'
                USING ERRCODE='23514',CONSTRAINT='chat_sessions_client_actor_check';
        END IF;
        IF NEW.subject_id IS NOT NULL AND (
            NEW.subject_type <> 'EXTERNAL_SUBJECT' OR NOT EXISTS (
                SELECT 1 FROM external_subjects s
                WHERE s.workspace_id=NEW.workspace_id AND s.id=NEW.subject_id
                  AND s.client_id=NEW.client_id
            )
        ) THEN
            RAISE EXCEPTION 'External Subject Chat binding is invalid'
                USING ERRCODE='23514',CONSTRAINT='chat_sessions_external_subject_check';
        END IF;
        IF NEW.subject_id IS NOT NULL AND NEW.ownership_mode <> 'SUBJECT_OWNED' THEN
            RAISE EXCEPTION 'Subject-owned Chat cannot be policy shared'
                USING ERRCODE='23514',CONSTRAINT='chat_sessions_subject_private_check';
        END IF;
    ELSE
        RAISE EXCEPTION 'Chat Session Actor must be User or Service Principal'
            USING ERRCODE='23514',CONSTRAINT='chat_sessions_actor_type_check';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER chat_sessions_principal_ownership_guard
BEFORE INSERT ON chat_sessions
FOR EACH ROW EXECUTE FUNCTION validate_chat_session_principal_ownership();

CREATE FUNCTION reject_chat_session_ownership_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF ROW(
        NEW.workspace_id,NEW.agent_id,NEW.created_by,NEW.actor_type,NEW.actor_id,
        NEW.subject_type,NEW.subject_id,NEW.client_id,NEW.ownership_mode,
        NEW.ownership_policy_version,NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.workspace_id,OLD.agent_id,OLD.created_by,OLD.actor_type,OLD.actor_id,
        OLD.subject_type,OLD.subject_id,OLD.client_id,OLD.ownership_mode,
        OLD.ownership_policy_version,OLD.created_at
    ) THEN
        RAISE EXCEPTION 'Chat Session ownership and identity are immutable'
            USING ERRCODE='55000',CONSTRAINT='chat_sessions_ownership_immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER chat_sessions_ownership_immutable_guard
BEFORE UPDATE ON chat_sessions
FOR EACH ROW EXECUTE FUNCTION reject_chat_session_ownership_mutation();

CREATE FUNCTION validate_chat_message_principal_ownership()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    session_actor_type TEXT;
    session_actor_id UUID;
    session_subject_type TEXT;
    session_subject_id UUID;
    session_client_id UUID;
    session_mode TEXT;
    session_policy_version BIGINT;
BEGIN
    SELECT actor_type,actor_id,subject_type,subject_id,client_id,
           ownership_mode,ownership_policy_version
    INTO STRICT session_actor_type,session_actor_id,session_subject_type,
        session_subject_id,session_client_id,session_mode,session_policy_version
    FROM chat_sessions
    WHERE workspace_id=NEW.workspace_id AND id=NEW.session_id;

    IF NEW.actor_type IS NULL AND NEW.actor_id IS NULL THEN
        IF NEW.created_by IS NOT NULL THEN
            NEW.actor_type := 'USER';
            NEW.actor_id := NEW.created_by;
        ELSE
            NEW.actor_type := 'SYSTEM';
            NEW.actor_id := '00000000-0000-0000-0000-000000000001'::UUID;
        END IF;
    END IF;
    IF NEW.subject_type IS NULL AND NEW.subject_id IS NULL THEN
        NEW.subject_type := session_subject_type;
        NEW.subject_id := session_subject_id;
    END IF;
    NEW.client_id := coalesce(NEW.client_id,session_client_id);
    NEW.ownership_mode := coalesce(NEW.ownership_mode,session_mode);
    NEW.ownership_policy_version := coalesce(
        NEW.ownership_policy_version,session_policy_version
    );

    IF ROW(NEW.client_id,NEW.ownership_mode,NEW.ownership_policy_version)
       IS DISTINCT FROM ROW(session_client_id,session_mode,session_policy_version) THEN
        RAISE EXCEPTION 'Chat Message ownership must match its Session'
            USING ERRCODE='23514',CONSTRAINT='chat_messages_session_ownership_check';
    END IF;
    IF session_mode='SUBJECT_OWNED' AND
       ROW(NEW.subject_type,NEW.subject_id) IS DISTINCT FROM
       ROW(session_subject_type,session_subject_id) THEN
        RAISE EXCEPTION 'Subject-owned Message must retain its Session Subject'
            USING ERRCODE='23514',CONSTRAINT='chat_messages_subject_owner_check';
    END IF;
    IF session_mode='POLICY_SHARED' AND NEW.subject_id IS NOT NULL AND (
        NEW.subject_type <> 'EXTERNAL_SUBJECT' OR NOT EXISTS (
            SELECT 1 FROM external_subjects s
            WHERE s.workspace_id=NEW.workspace_id AND s.id=NEW.subject_id
              AND s.client_id=session_client_id
        )
    ) THEN
        RAISE EXCEPTION 'Policy-shared Message Subject is outside its Client'
            USING ERRCODE='23514',CONSTRAINT='chat_messages_shared_subject_check';
    END IF;
    IF NEW.actor_type = 'USER' THEN
        IF NEW.created_by IS DISTINCT FROM NEW.actor_id THEN
            RAISE EXCEPTION 'User Message actor projection is inconsistent'
                USING ERRCODE='23514',CONSTRAINT='chat_messages_user_actor_check';
        END IF;
    ELSIF NEW.actor_type IN ('SERVICE_PRINCIPAL','SYSTEM') THEN
        IF NEW.created_by IS NOT NULL THEN
            RAISE EXCEPTION 'Machine Message must not reference a User creator'
                USING ERRCODE='23514',CONSTRAINT='chat_messages_machine_actor_check';
        END IF;
    ELSE
        RAISE EXCEPTION 'External Subject cannot be a Message transport Actor'
            USING ERRCODE='23514',CONSTRAINT='chat_messages_actor_type_check';
    END IF;
    IF NEW.role = 'USER' AND (
        NEW.actor_type IS DISTINCT FROM session_actor_type
        OR NEW.actor_id IS DISTINCT FROM session_actor_id
    ) THEN
        RAISE EXCEPTION 'User Message Actor must match its Session Actor'
            USING ERRCODE='23514',CONSTRAINT='chat_messages_user_session_actor_check';
    END IF;
    RETURN NEW;
EXCEPTION
    WHEN NO_DATA_FOUND THEN
        RAISE EXCEPTION 'Chat Message Session does not exist'
            USING ERRCODE='23503',CONSTRAINT='chat_messages_workspace_session_fk';
END;
$$;

CREATE TRIGGER chat_messages_principal_ownership_guard
BEFORE INSERT ON chat_messages
FOR EACH ROW EXECUTE FUNCTION validate_chat_message_principal_ownership();

-- Extend the permanent-retention invariant to every newly persisted identity
-- and ownership field. Mutable delivery/execution status remains unchanged.
CREATE OR REPLACE FUNCTION enforce_chat_message_permanent_retention()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'chat messages are permanently retained and cannot be deleted'
            USING ERRCODE = '55000';
    END IF;
    IF ROW(
        NEW.id,NEW.workspace_id,NEW.session_id,NEW.role,NEW.content,
        NEW.content_object_id,NEW.content_sha256,NEW.content_length,
        NEW.created_by,NEW.created_at,
        NEW.actor_type,NEW.actor_id,NEW.subject_type,NEW.subject_id,NEW.client_id,
        NEW.ownership_mode,NEW.ownership_policy_version
    ) IS DISTINCT FROM ROW(
        OLD.id,OLD.workspace_id,OLD.session_id,OLD.role,OLD.content,
        OLD.content_object_id,OLD.content_sha256,OLD.content_length,
        OLD.created_by,OLD.created_at,
        OLD.actor_type,OLD.actor_id,OLD.subject_type,OLD.subject_id,OLD.client_id,
        OLD.ownership_mode,OLD.ownership_policy_version
    ) THEN
        RAISE EXCEPTION 'chat message original content and identity are immutable'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

DROP INDEX chat_sessions_workspace_creator_updated_idx;
CREATE INDEX chat_sessions_workspace_owner_updated_idx ON chat_sessions(
    workspace_id,client_id,subject_type,subject_id,actor_type,actor_id,
    ownership_mode,status,updated_at DESC,id
);
CREATE INDEX chat_sessions_workspace_client_agent_updated_idx ON chat_sessions(
    workspace_id,client_id,agent_id,updated_at DESC,id
) WHERE client_id IS NOT NULL;


-- ##########################################################################
-- Source: 000050_execution_principal_snapshots.up.sql
-- ##########################################################################

ALTER TABLE agent_runs
    ADD COLUMN principal_snapshot_version TEXT,
    ADD COLUMN subject_type TEXT,
    ADD COLUMN subject_id UUID,
    ADD COLUMN client_id UUID,
    ADD COLUMN grant_id UUID,
    ADD COLUMN grant_version BIGINT,
    ADD COLUMN agent_policy_version BIGINT;

ALTER TABLE workflow_executions
    ADD COLUMN principal_snapshot_version TEXT,
    ADD COLUMN subject_type TEXT,
    ADD COLUMN subject_id UUID,
    ADD COLUMN client_id UUID,
    ADD COLUMN grant_id UUID,
    ADD COLUMN grant_version BIGINT,
    ADD COLUMN agent_policy_version BIGINT;

ALTER TABLE tool_invocations
    ADD COLUMN principal_snapshot_version TEXT,
    ADD COLUMN subject_type TEXT,
    ADD COLUMN subject_id UUID,
    ADD COLUMN client_id UUID,
    ADD COLUMN grant_id UUID,
    ADD COLUMN grant_version BIGINT,
    ADD COLUMN agent_policy_version BIGINT,
    ADD COLUMN authorization_snapshot JSONB NOT NULL DEFAULT '{}'::JSONB;

-- Historical typed actors are preserved exactly. Only User has an unambiguous
-- historical Subject; old machine calls remain explicitly legacy rather than
-- inventing Client/Grant authorization facts.
ALTER TABLE agent_runs DISABLE TRIGGER agent_runs_permanent_snapshot;
ALTER TABLE workflow_executions DISABLE TRIGGER workflow_executions_state_guard;
ALTER TABLE tool_invocations DISABLE TRIGGER tool_invocations_state_guard;

UPDATE agent_runs SET
    principal_snapshot_version='legacy.v1',
    subject_type=CASE WHEN triggered_by_type='USER' THEN 'USER' END,
    subject_id=CASE WHEN triggered_by_type='USER' THEN triggered_by_id END;
UPDATE workflow_executions SET
    principal_snapshot_version='legacy.v1',
    subject_type=CASE WHEN triggered_by_type='USER' THEN 'USER' END,
    subject_id=CASE WHEN triggered_by_type='USER' THEN triggered_by_id END;
UPDATE tool_invocations SET
    principal_snapshot_version='legacy.v1',
    subject_type=CASE WHEN actor_type='USER' THEN 'USER' END,
    subject_id=CASE WHEN actor_type='USER' THEN actor_id END;

ALTER TABLE agent_runs ENABLE TRIGGER agent_runs_permanent_snapshot;
ALTER TABLE workflow_executions ENABLE TRIGGER workflow_executions_state_guard;
ALTER TABLE tool_invocations ENABLE TRIGGER tool_invocations_state_guard;

ALTER TABLE agent_runs ALTER COLUMN principal_snapshot_version SET NOT NULL;
ALTER TABLE workflow_executions ALTER COLUMN principal_snapshot_version SET NOT NULL;
ALTER TABLE tool_invocations ALTER COLUMN principal_snapshot_version SET NOT NULL;

ALTER TABLE agent_runs
    ADD CONSTRAINT agent_runs_principal_snapshot_version_check CHECK (
        principal_snapshot_version IN ('legacy.v1','execution.principal.v1')
    ),
    ADD CONSTRAINT agent_runs_subject_pair_check
        CHECK ((subject_type IS NULL)=(subject_id IS NULL)),
    ADD CONSTRAINT agent_runs_subject_ref_fk
        FOREIGN KEY(workspace_id,subject_type,subject_id)
        REFERENCES principal_refs(workspace_id,principal_type,principal_id)
        ON DELETE RESTRICT,
    ADD CONSTRAINT agent_runs_client_scope_fk
        FOREIGN KEY(workspace_id,client_id)
        REFERENCES agent_access_clients(workspace_id,id) ON DELETE RESTRICT,
    ADD CONSTRAINT agent_runs_grant_scope_fk
        FOREIGN KEY(workspace_id,grant_id)
        REFERENCES agent_access_grants(workspace_id,id) ON DELETE RESTRICT,
    ADD CONSTRAINT agent_runs_external_version_pair_check CHECK (
        (client_id IS NULL AND grant_id IS NULL AND grant_version IS NULL
         AND agent_policy_version IS NULL)
        OR
        (client_id IS NOT NULL AND grant_id IS NOT NULL AND grant_version > 0
         AND agent_policy_version > 0)
    );

ALTER TABLE workflow_executions
    ADD CONSTRAINT workflow_executions_principal_snapshot_version_check CHECK (
        principal_snapshot_version IN ('legacy.v1','execution.principal.v1')
    ),
    ADD CONSTRAINT workflow_executions_subject_pair_check
        CHECK ((subject_type IS NULL)=(subject_id IS NULL)),
    ADD CONSTRAINT workflow_executions_subject_ref_fk
        FOREIGN KEY(workspace_id,subject_type,subject_id)
        REFERENCES principal_refs(workspace_id,principal_type,principal_id)
        ON DELETE RESTRICT,
    ADD CONSTRAINT workflow_executions_client_scope_fk
        FOREIGN KEY(workspace_id,client_id)
        REFERENCES agent_access_clients(workspace_id,id) ON DELETE RESTRICT,
    ADD CONSTRAINT workflow_executions_grant_scope_fk
        FOREIGN KEY(workspace_id,grant_id)
        REFERENCES agent_access_grants(workspace_id,id) ON DELETE RESTRICT,
    ADD CONSTRAINT workflow_executions_external_version_pair_check CHECK (
        (client_id IS NULL AND grant_id IS NULL AND grant_version IS NULL
         AND agent_policy_version IS NULL)
        OR
        (client_id IS NOT NULL AND grant_id IS NOT NULL AND grant_version > 0
         AND agent_policy_version > 0)
    );

ALTER TABLE tool_invocations
    ADD CONSTRAINT tool_invocations_principal_snapshot_version_check CHECK (
        principal_snapshot_version IN ('legacy.v1','execution.principal.v1')
    ),
    ADD CONSTRAINT tool_invocations_subject_pair_check
        CHECK ((subject_type IS NULL)=(subject_id IS NULL)),
    ADD CONSTRAINT tool_invocations_subject_ref_fk
        FOREIGN KEY(workspace_id,subject_type,subject_id)
        REFERENCES principal_refs(workspace_id,principal_type,principal_id)
        ON DELETE RESTRICT,
    ADD CONSTRAINT tool_invocations_client_scope_fk
        FOREIGN KEY(workspace_id,client_id)
        REFERENCES agent_access_clients(workspace_id,id) ON DELETE RESTRICT,
    ADD CONSTRAINT tool_invocations_grant_scope_fk
        FOREIGN KEY(workspace_id,grant_id)
        REFERENCES agent_access_grants(workspace_id,id) ON DELETE RESTRICT,
    ADD CONSTRAINT tool_invocations_external_version_pair_check CHECK (
        (client_id IS NULL AND grant_id IS NULL AND grant_version IS NULL
         AND agent_policy_version IS NULL)
        OR
        (client_id IS NOT NULL AND grant_id IS NOT NULL AND grant_version > 0
         AND agent_policy_version > 0)
    ),
    ADD CONSTRAINT tool_invocations_authorization_snapshot_object_check
        CHECK(jsonb_typeof(authorization_snapshot)='object');

CREATE FUNCTION execution_authorization_envelope_matches(
    snapshot JSONB,workspace_id UUID,actor_type TEXT,actor_id UUID,
    subject_type TEXT,subject_id UUID,client_id UUID,grant_id UUID,
    grant_version BIGINT,agent_policy_version BIGINT
)
RETURNS BOOLEAN
LANGUAGE SQL
IMMUTABLE
PARALLEL SAFE
AS $$
    SELECT jsonb_typeof(snapshot)='object'
       AND snapshot->>'specVersion'='execution.principal.v1'
       AND snapshot->>'workspaceId'=workspace_id::TEXT
       AND jsonb_typeof(snapshot->'actor')='object'
       AND snapshot#>>'{actor,type}'=actor_type
       AND snapshot#>>'{actor,id}'=actor_id::TEXT
       AND (
         (subject_id IS NULL AND NOT snapshot ? 'subject')
         OR
         (subject_id IS NOT NULL AND jsonb_typeof(snapshot->'subject')='object'
          AND snapshot#>>'{subject,type}'=subject_type
          AND snapshot#>>'{subject,id}'=subject_id::TEXT)
       )
       AND (snapshot->>'clientId') IS NOT DISTINCT FROM client_id::TEXT
       AND (snapshot->>'grantId') IS NOT DISTINCT FROM grant_id::TEXT
       AND (snapshot->>'grantVersion') IS NOT DISTINCT FROM grant_version::TEXT
       AND (snapshot->>'agentPolicyVersion') IS NOT DISTINCT FROM agent_policy_version::TEXT
       AND jsonb_typeof(snapshot->'evidence')='object'
$$;

CREATE FUNCTION validate_execution_principal_snapshot()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    actor_type_value TEXT;
    actor_id_value UUID;
    target_agent_id UUID;
    parent_version TEXT;
    parent_actor_type TEXT;
    parent_actor_id UUID;
    parent_subject_type TEXT;
    parent_subject_id UUID;
    parent_client_id UUID;
    parent_grant_id UUID;
    parent_grant_version BIGINT;
    parent_policy_version BIGINT;
BEGIN
    IF TG_TABLE_NAME='tool_invocations' THEN
        actor_type_value := NEW.actor_type;
        actor_id_value := NEW.actor_id;
    ELSE
        actor_type_value := NEW.triggered_by_type;
        actor_id_value := NEW.triggered_by_id;
    END IF;

    IF NEW.principal_snapshot_version IS NULL THEN
        IF actor_type_value='USER' THEN
            NEW.principal_snapshot_version := 'execution.principal.v1';
            NEW.subject_type := 'USER';
            NEW.subject_id := actor_id_value;
        ELSIF actor_type_value='SYSTEM' THEN
            NEW.principal_snapshot_version := 'execution.principal.v1';
            NEW.subject_type := NULL;
            NEW.subject_id := NULL;
        ELSE
            RAISE EXCEPTION 'Service Principal execution requires explicit authorization snapshot'
                USING ERRCODE='23514',CONSTRAINT='execution_external_snapshot_required';
        END IF;
        NEW.authorization_snapshot := jsonb_strip_nulls(jsonb_build_object(
            'specVersion','execution.principal.v1','workspaceId',NEW.workspace_id,
            'actor',jsonb_build_object('type',actor_type_value,'id',actor_id_value),
            'subject',CASE WHEN NEW.subject_id IS NULL THEN NULL ELSE
                jsonb_build_object('type',NEW.subject_type,'id',NEW.subject_id) END,
            'evidence',NEW.authorization_snapshot
        ));
    ELSIF NEW.principal_snapshot_version='legacy.v1' THEN
        RAISE EXCEPTION 'legacy Principal snapshots are reserved for migrated facts'
            USING ERRCODE='23514',CONSTRAINT='execution_legacy_snapshot_reserved';
    END IF;

    IF actor_type_value='USER' THEN
        IF NEW.subject_type IS DISTINCT FROM 'USER'
           OR NEW.subject_id IS DISTINCT FROM actor_id_value
           OR NEW.client_id IS NOT NULL OR NEW.grant_id IS NOT NULL
           OR NEW.grant_version IS NOT NULL OR NEW.agent_policy_version IS NOT NULL THEN
            RAISE EXCEPTION 'User execution Principal snapshot is inconsistent'
                USING ERRCODE='23514',CONSTRAINT='execution_user_snapshot_check';
        END IF;
    ELSIF actor_type_value='SYSTEM' THEN
        IF NEW.subject_id IS NOT NULL OR NEW.client_id IS NOT NULL OR NEW.grant_id IS NOT NULL
           OR NEW.grant_version IS NOT NULL OR NEW.agent_policy_version IS NOT NULL THEN
            RAISE EXCEPTION 'System execution Principal snapshot is inconsistent'
                USING ERRCODE='23514',CONSTRAINT='execution_system_snapshot_check';
        END IF;
    ELSIF actor_type_value='SERVICE_PRINCIPAL' THEN
        IF NEW.client_id IS NULL OR NEW.grant_id IS NULL OR NEW.grant_version IS NULL
           OR NEW.agent_policy_version IS NULL OR NOT EXISTS (
             SELECT 1 FROM agent_access_clients c
             WHERE c.workspace_id=NEW.workspace_id AND c.id=NEW.client_id
               AND c.service_principal_id=actor_id_value AND c.status='ACTIVE'
           ) OR (NEW.subject_id IS NOT NULL AND (
             NEW.subject_type <> 'EXTERNAL_SUBJECT' OR NOT EXISTS (
               SELECT 1 FROM external_subjects s
               WHERE s.workspace_id=NEW.workspace_id AND s.id=NEW.subject_id
                 AND s.client_id=NEW.client_id
             )
           )) THEN
            RAISE EXCEPTION 'External execution Principal binding is invalid'
                USING ERRCODE='23514',CONSTRAINT='execution_external_binding_check';
        END IF;
    ELSE
        RAISE EXCEPTION 'Unknown execution Actor type'
            USING ERRCODE='23514',CONSTRAINT='execution_actor_type_check';
    END IF;

    IF TG_TABLE_NAME='agent_runs' THEN
        target_agent_id := NEW.agent_id;
    ELSIF TG_TABLE_NAME='workflow_executions' THEN
        IF NEW.agent_run_id IS NOT NULL THEN
            SELECT principal_snapshot_version,triggered_by_type,triggered_by_id,
                   subject_type,subject_id,client_id,grant_id,grant_version,agent_policy_version
            INTO STRICT parent_version,parent_actor_type,parent_actor_id,parent_subject_type,
                 parent_subject_id,parent_client_id,parent_grant_id,parent_grant_version,
                 parent_policy_version
            FROM agent_runs WHERE workspace_id=NEW.workspace_id AND id=NEW.agent_run_id;
        END IF;
    ELSIF TG_TABLE_NAME='tool_invocations' THEN
        IF NEW.workflow_execution_id IS NOT NULL THEN
            SELECT principal_snapshot_version,triggered_by_type,triggered_by_id,
                   subject_type,subject_id,client_id,grant_id,grant_version,agent_policy_version
            INTO STRICT parent_version,parent_actor_type,parent_actor_id,parent_subject_type,
                 parent_subject_id,parent_client_id,parent_grant_id,parent_grant_version,
                 parent_policy_version
            FROM workflow_executions
            WHERE workspace_id=NEW.workspace_id AND id=NEW.workflow_execution_id;
        ELSIF NEW.agent_run_id IS NOT NULL THEN
            SELECT principal_snapshot_version,triggered_by_type,triggered_by_id,
                   subject_type,subject_id,client_id,grant_id,grant_version,agent_policy_version
            INTO STRICT parent_version,parent_actor_type,parent_actor_id,parent_subject_type,
                 parent_subject_id,parent_client_id,parent_grant_id,parent_grant_version,
                 parent_policy_version
            FROM agent_runs WHERE workspace_id=NEW.workspace_id AND id=NEW.agent_run_id;
        END IF;
    END IF;

    IF parent_version IS NOT NULL AND ROW(
        NEW.principal_snapshot_version,actor_type_value,actor_id_value,
        NEW.subject_type,NEW.subject_id,NEW.client_id,NEW.grant_id,
        NEW.grant_version,NEW.agent_policy_version
    ) IS DISTINCT FROM ROW(
        parent_version,parent_actor_type,parent_actor_id,parent_subject_type,
        parent_subject_id,parent_client_id,parent_grant_id,parent_grant_version,
        parent_policy_version
    ) THEN
        RAISE EXCEPTION 'Child execution Principal snapshot differs from its parent'
            USING ERRCODE='23514',CONSTRAINT='execution_parent_snapshot_check';
    END IF;

    IF actor_type_value='SERVICE_PRINCIPAL' AND parent_version IS NULL AND NOT EXISTS (
        SELECT 1 FROM agent_access_grants g
        JOIN agents a ON a.workspace_id=g.workspace_id AND a.id=g.agent_id
        WHERE g.workspace_id=NEW.workspace_id AND g.id=NEW.grant_id
          AND g.client_id=NEW.client_id AND g.lock_version=NEW.grant_version
          AND a.lock_version=NEW.agent_policy_version
          AND (target_agent_id IS NULL OR g.agent_id=target_agent_id)
          AND g.status='ACTIVE' AND g.valid_from <= clock_timestamp()
          AND (g.expires_at IS NULL OR g.expires_at > clock_timestamp())
    ) THEN
        RAISE EXCEPTION 'External execution Grant snapshot is stale or mismatched'
            USING ERRCODE='23514',CONSTRAINT='execution_grant_snapshot_check';
    END IF;

    IF NOT execution_authorization_envelope_matches(
        NEW.authorization_snapshot,NEW.workspace_id,actor_type_value,actor_id_value,
        NEW.subject_type,NEW.subject_id,NEW.client_id,NEW.grant_id,
        NEW.grant_version,NEW.agent_policy_version
    ) THEN
        RAISE EXCEPTION 'Execution authorization envelope differs from typed snapshot'
            USING ERRCODE='23514',CONSTRAINT='execution_authorization_envelope_check';
    END IF;
    RETURN NEW;
EXCEPTION
    WHEN NO_DATA_FOUND THEN
        RAISE EXCEPTION 'Execution parent does not exist'
            USING ERRCODE='23503',CONSTRAINT='execution_parent_snapshot_fk';
END;
$$;

CREATE TRIGGER agent_runs_principal_snapshot_guard
BEFORE INSERT ON agent_runs FOR EACH ROW
EXECUTE FUNCTION validate_execution_principal_snapshot();
CREATE TRIGGER workflow_executions_principal_snapshot_guard
BEFORE INSERT ON workflow_executions FOR EACH ROW
EXECUTE FUNCTION validate_execution_principal_snapshot();
CREATE TRIGGER tool_invocations_principal_snapshot_guard
BEFORE INSERT ON tool_invocations FOR EACH ROW
EXECUTE FUNCTION validate_execution_principal_snapshot();

CREATE OR REPLACE FUNCTION enforce_agent_run_permanent_snapshot()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='DELETE' THEN
        RAISE EXCEPTION 'agent runs are permanently retained and cannot be deleted'
            USING ERRCODE='55000';
    END IF;
    IF ROW(
        NEW.id,NEW.workspace_id,NEW.session_id,NEW.agent_id,NEW.trigger_type,
        NEW.triggered_by_type,NEW.triggered_by_id,NEW.trace_id,NEW.model_snapshot,
        NEW.capability_snapshot,NEW.context_policy_snapshot,NEW.authorization_snapshot,
        NEW.snapshot_schema_version,NEW.input_summary,NEW.started_at,
        NEW.principal_snapshot_version,NEW.subject_type,NEW.subject_id,NEW.client_id,
        NEW.grant_id,NEW.grant_version,NEW.agent_policy_version
    ) IS DISTINCT FROM ROW(
        OLD.id,OLD.workspace_id,OLD.session_id,OLD.agent_id,OLD.trigger_type,
        OLD.triggered_by_type,OLD.triggered_by_id,OLD.trace_id,OLD.model_snapshot,
        OLD.capability_snapshot,OLD.context_policy_snapshot,OLD.authorization_snapshot,
        OLD.snapshot_schema_version,OLD.input_summary,OLD.started_at,
        OLD.principal_snapshot_version,OLD.subject_type,OLD.subject_id,OLD.client_id,
        OLD.grant_id,OLD.grant_version,OLD.agent_policy_version
    ) THEN
        RAISE EXCEPTION 'agent run identity and start snapshots are immutable'
            USING ERRCODE='55000';
    END IF;
    IF OLD.status IN ('SUCCEEDED','FAILED','CANCELLED') THEN
        RAISE EXCEPTION 'terminal agent run is immutable' USING ERRCODE='55000';
    END IF;
    IF NEW.lock_version <> OLD.lock_version+1 THEN
        RAISE EXCEPTION 'agent run update requires the next lock version' USING ERRCODE='40001';
    END IF;
    IF NEW.status <> OLD.status AND NOT (
        (OLD.status='PENDING' AND NEW.status IN ('RUNNING','FAILED','CANCELLED')) OR
        (OLD.status='RUNNING' AND NEW.status IN ('WAITING_CONFIRMATION','SUCCEEDED','FAILED','CANCELLED')) OR
        (OLD.status='WAITING_CONFIRMATION' AND NEW.status IN ('RUNNING','FAILED','CANCELLED'))
    ) THEN
        RAISE EXCEPTION 'illegal agent run status transition from % to %',OLD.status,NEW.status
            USING ERRCODE='55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION enforce_workflow_execution_state()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='DELETE' THEN
        RAISE EXCEPTION 'workflow executions are permanently retained and cannot be deleted'
            USING ERRCODE='55000';
    END IF;
    IF ROW(
        NEW.id,NEW.workspace_id,NEW.workflow_id,NEW.revision_id,NEW.compilation_id,
        NEW.agent_run_id,NEW.trigger_type,NEW.triggered_by_type,NEW.triggered_by_id,
        NEW.trace_id,NEW.snapshot_schema_version,NEW.authorization_snapshot,
        NEW.input_summary,NEW.started_at,NEW.principal_snapshot_version,
        NEW.subject_type,NEW.subject_id,NEW.client_id,NEW.grant_id,
        NEW.grant_version,NEW.agent_policy_version
    ) IS DISTINCT FROM ROW(
        OLD.id,OLD.workspace_id,OLD.workflow_id,OLD.revision_id,OLD.compilation_id,
        OLD.agent_run_id,OLD.trigger_type,OLD.triggered_by_type,OLD.triggered_by_id,
        OLD.trace_id,OLD.snapshot_schema_version,OLD.authorization_snapshot,
        OLD.input_summary,OLD.started_at,OLD.principal_snapshot_version,
        OLD.subject_type,OLD.subject_id,OLD.client_id,OLD.grant_id,
        OLD.grant_version,OLD.agent_policy_version
    ) THEN
        RAISE EXCEPTION 'workflow execution identity and start evidence are immutable'
            USING ERRCODE='55000';
    END IF;
    IF OLD.status IN ('SUCCEEDED','FAILED','CANCELLED') THEN
        RAISE EXCEPTION 'terminal workflow execution is immutable' USING ERRCODE='55000';
    END IF;
    IF NEW.lock_version <> OLD.lock_version+1 THEN
        RAISE EXCEPTION 'workflow execution update requires the next lock version' USING ERRCODE='40001';
    END IF;
    IF NEW.status <> OLD.status AND NOT (
        (OLD.status='PENDING' AND NEW.status IN ('RUNNING','FAILED','CANCELLED')) OR
        (OLD.status='RUNNING' AND NEW.status IN ('WAITING_CONFIRMATION','SUCCEEDED','FAILED','CANCELLED')) OR
        (OLD.status='WAITING_CONFIRMATION' AND NEW.status IN ('RUNNING','FAILED','CANCELLED'))
    ) THEN
        RAISE EXCEPTION 'illegal workflow execution status transition from % to %',OLD.status,NEW.status
            USING ERRCODE='55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION enforce_tool_invocation_state()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='DELETE' THEN
        RAISE EXCEPTION 'tool invocations are permanently retained and cannot be deleted'
            USING ERRCODE='55000';
    END IF;
    IF ROW(
        NEW.id,NEW.workspace_id,NEW.tool_id,NEW.tool_version_id,
        NEW.capability_release_id,NEW.provider_id,NEW.connection_id,
        NEW.execution_lease_id,NEW.agent_run_id,NEW.workflow_execution_id,
        NEW.execution_step_id,NEW.actor_type,NEW.actor_id,NEW.trace_id,
        NEW.idempotency_key,NEW.input_summary,NEW.started_at,
        NEW.principal_snapshot_version,NEW.subject_type,NEW.subject_id,NEW.client_id,
        NEW.grant_id,NEW.grant_version,NEW.agent_policy_version,NEW.authorization_snapshot
    ) IS DISTINCT FROM ROW(
        OLD.id,OLD.workspace_id,OLD.tool_id,OLD.tool_version_id,
        OLD.capability_release_id,OLD.provider_id,OLD.connection_id,
        OLD.execution_lease_id,OLD.agent_run_id,OLD.workflow_execution_id,
        OLD.execution_step_id,OLD.actor_type,OLD.actor_id,OLD.trace_id,
        OLD.idempotency_key,OLD.input_summary,OLD.started_at,
        OLD.principal_snapshot_version,OLD.subject_type,OLD.subject_id,OLD.client_id,
        OLD.grant_id,OLD.grant_version,OLD.agent_policy_version,OLD.authorization_snapshot
    ) THEN
        RAISE EXCEPTION 'tool invocation identity and request evidence are immutable'
            USING ERRCODE='55000';
    END IF;
    IF OLD.status IN ('SUCCEEDED','FAILED','CANCELLED') THEN
        RAISE EXCEPTION 'terminal tool invocation is immutable' USING ERRCODE='55000';
    END IF;
    IF NEW.status <> OLD.status AND NOT (
        (OLD.status='PENDING' AND NEW.status IN ('RUNNING','FAILED','CANCELLED')) OR
        (OLD.status='RUNNING' AND NEW.status IN ('SUCCEEDED','FAILED','CANCELLED'))
    ) THEN
        RAISE EXCEPTION 'illegal tool invocation status transition from % to %',OLD.status,NEW.status
            USING ERRCODE='55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE INDEX agent_runs_workspace_principal_started_idx ON agent_runs(
    workspace_id,client_id,subject_type,subject_id,started_at DESC,id
) WHERE client_id IS NOT NULL;
CREATE INDEX workflow_executions_workspace_principal_started_idx ON workflow_executions(
    workspace_id,client_id,subject_type,subject_id,started_at DESC,id
) WHERE client_id IS NOT NULL;
CREATE INDEX tool_invocations_workspace_principal_started_idx ON tool_invocations(
    workspace_id,client_id,subject_type,subject_id,started_at DESC,id
) WHERE client_id IS NOT NULL;


-- ##########################################################################
-- Source: 000051_principal_aware_confirmations.up.sql
-- ##########################################################################

DROP TRIGGER IF EXISTS execution_confirmations_chat_projection_sync ON execution_confirmations;
DROP FUNCTION IF EXISTS synchronize_chat_confirmation_projection();
DROP TRIGGER IF EXISTS chat_confirmations_projection_guard ON chat_confirmations;
DROP FUNCTION IF EXISTS enforce_chat_confirmation_projection();
DROP TRIGGER IF EXISTS execution_confirmations_fact_guard ON execution_confirmations;
DROP FUNCTION IF EXISTS enforce_execution_confirmation_fact();

ALTER TABLE execution_confirmations
    DROP CONSTRAINT execution_confirmations_requested_by_fk,
    DROP CONSTRAINT execution_confirmations_confirmed_by_fk,
    DROP CONSTRAINT execution_confirmations_requester_check,
    DROP CONSTRAINT execution_confirmations_state_check,
    ALTER COLUMN requested_by DROP NOT NULL,
    ADD COLUMN request_principal_snapshot_version TEXT,
    ADD COLUMN request_actor_type TEXT,
    ADD COLUMN request_actor_id UUID,
    ADD COLUMN request_subject_type TEXT,
    ADD COLUMN request_subject_id UUID,
    ADD COLUMN request_client_id UUID,
    ADD COLUMN request_grant_id UUID,
    ADD COLUMN request_grant_version BIGINT,
    ADD COLUMN request_agent_policy_version BIGINT,
    ADD COLUMN decision_principal_snapshot_version TEXT,
    ADD COLUMN decision_actor_type TEXT,
    ADD COLUMN decision_actor_id UUID,
    ADD COLUMN decision_subject_type TEXT,
    ADD COLUMN decision_subject_id UUID,
    ADD COLUMN decision_client_id UUID,
    ADD COLUMN decision_grant_id UUID,
    ADD COLUMN decision_grant_version BIGINT,
    ADD COLUMN decision_agent_policy_version BIGINT,
    ADD COLUMN decision_policy_snapshot JSONB NOT NULL DEFAULT '{}'::JSONB;

-- Historical requests identify a real User, but cancelled rows never stored
-- who cancelled them. Mark the record legacy instead of inventing evidence.
UPDATE execution_confirmations SET
    request_principal_snapshot_version='legacy.v1',
    request_actor_type='USER',request_actor_id=requested_by,
    request_subject_type='USER',request_subject_id=requested_by,
    decision_principal_snapshot_version=CASE WHEN status='CONFIRMED' THEN 'legacy.v1' END,
    decision_actor_type=CASE WHEN status='CONFIRMED' THEN 'USER' END,
    decision_actor_id=CASE WHEN status='CONFIRMED' THEN confirmed_by END,
    decision_subject_type=CASE WHEN status='CONFIRMED' THEN 'USER' END,
    decision_subject_id=CASE WHEN status='CONFIRMED' THEN confirmed_by END,
    decision_policy_snapshot=CASE WHEN status='CONFIRMED'
        THEN '{"mode":"actweave_user","legacy":true}'::JSONB ELSE '{}'::JSONB END;

ALTER TABLE execution_confirmations
    ALTER COLUMN request_principal_snapshot_version SET NOT NULL,
    ALTER COLUMN request_actor_type SET NOT NULL,
    ALTER COLUMN request_actor_id SET NOT NULL,
    ADD CONSTRAINT execution_confirmations_request_snapshot_version_check CHECK (
        request_principal_snapshot_version IN ('legacy.v1','execution.principal.v1')
    ),
    ADD CONSTRAINT execution_confirmations_request_subject_pair_check
        CHECK((request_subject_type IS NULL)=(request_subject_id IS NULL)),
    ADD CONSTRAINT execution_confirmations_request_external_pair_check CHECK (
        (request_client_id IS NULL AND request_grant_id IS NULL
         AND request_grant_version IS NULL AND request_agent_policy_version IS NULL)
        OR
        (request_client_id IS NOT NULL AND request_grant_id IS NOT NULL
         AND request_grant_version > 0 AND request_agent_policy_version > 0)
    ),
    ADD CONSTRAINT execution_confirmations_decision_snapshot_pair_check CHECK (
        (decision_principal_snapshot_version IS NULL AND decision_actor_type IS NULL
         AND decision_actor_id IS NULL AND decision_subject_type IS NULL
         AND decision_subject_id IS NULL AND decision_client_id IS NULL
         AND decision_grant_id IS NULL AND decision_grant_version IS NULL
         AND decision_agent_policy_version IS NULL)
        OR
        (decision_principal_snapshot_version IN ('legacy.v1','execution.principal.v1')
         AND decision_actor_type IS NOT NULL AND decision_actor_id IS NOT NULL
         AND (decision_subject_type IS NULL)=(decision_subject_id IS NULL)
         AND ((decision_client_id IS NULL AND decision_grant_id IS NULL
               AND decision_grant_version IS NULL AND decision_agent_policy_version IS NULL)
              OR
              (decision_client_id IS NOT NULL AND decision_grant_id IS NOT NULL
               AND decision_grant_version > 0 AND decision_agent_policy_version > 0)))
    ),
    ADD CONSTRAINT execution_confirmations_decision_policy_object_check
        CHECK(jsonb_typeof(decision_policy_snapshot)='object'),
    ADD CONSTRAINT execution_confirmations_requested_by_projection_check CHECK (
        (request_actor_type='USER' AND requested_by=request_actor_id)
        OR (request_actor_type<>'USER' AND requested_by IS NULL)
    ),
    ADD CONSTRAINT execution_confirmations_confirmed_by_projection_check CHECK (
        (status='CONFIRMED' AND decision_actor_type='USER' AND confirmed_by=decision_actor_id)
        OR ((status<>'CONFIRMED' OR decision_actor_type IS DISTINCT FROM 'USER')
            AND confirmed_by IS NULL)
    ),
    ADD CONSTRAINT execution_confirmations_requested_by_fk
        FOREIGN KEY(requested_by) REFERENCES users(id) ON DELETE RESTRICT,
    ADD CONSTRAINT execution_confirmations_confirmed_by_fk
        FOREIGN KEY(confirmed_by) REFERENCES users(id) ON DELETE RESTRICT,
    ADD CONSTRAINT execution_confirmations_principal_state_check CHECK (
        (status='PENDING' AND decision_principal_snapshot_version IS NULL
         AND confirmed_at IS NULL AND cancelled_at IS NULL)
        OR
        (status='CONFIRMED' AND decision_principal_snapshot_version IS NOT NULL
         AND confirmed_at IS NOT NULL AND cancelled_at IS NULL)
        OR
        (status='CANCELLED' AND confirmed_at IS NULL AND cancelled_at IS NOT NULL
         AND (decision_principal_snapshot_version IS NOT NULL
              OR request_principal_snapshot_version='legacy.v1'))
        OR
        (status='EXPIRED' AND decision_principal_snapshot_version IS NULL
         AND confirmed_at IS NULL AND cancelled_at IS NULL)
    ),
    ADD CONSTRAINT execution_confirmations_request_actor_ref_fk
        FOREIGN KEY(workspace_id,request_actor_type,request_actor_id)
        REFERENCES principal_refs(workspace_id,principal_type,principal_id) ON DELETE RESTRICT,
    ADD CONSTRAINT execution_confirmations_request_subject_ref_fk
        FOREIGN KEY(workspace_id,request_subject_type,request_subject_id)
        REFERENCES principal_refs(workspace_id,principal_type,principal_id) ON DELETE RESTRICT,
    ADD CONSTRAINT execution_confirmations_request_client_fk
        FOREIGN KEY(workspace_id,request_client_id)
        REFERENCES agent_access_clients(workspace_id,id) ON DELETE RESTRICT,
    ADD CONSTRAINT execution_confirmations_request_grant_fk
        FOREIGN KEY(workspace_id,request_grant_id)
        REFERENCES agent_access_grants(workspace_id,id) ON DELETE RESTRICT,
    ADD CONSTRAINT execution_confirmations_decision_actor_ref_fk
        FOREIGN KEY(workspace_id,decision_actor_type,decision_actor_id)
        REFERENCES principal_refs(workspace_id,principal_type,principal_id) ON DELETE RESTRICT,
    ADD CONSTRAINT execution_confirmations_decision_subject_ref_fk
        FOREIGN KEY(workspace_id,decision_subject_type,decision_subject_id)
        REFERENCES principal_refs(workspace_id,principal_type,principal_id) ON DELETE RESTRICT,
    ADD CONSTRAINT execution_confirmations_decision_client_fk
        FOREIGN KEY(workspace_id,decision_client_id)
        REFERENCES agent_access_clients(workspace_id,id) ON DELETE RESTRICT,
    ADD CONSTRAINT execution_confirmations_decision_grant_fk
        FOREIGN KEY(workspace_id,decision_grant_id)
        REFERENCES agent_access_grants(workspace_id,id) ON DELETE RESTRICT;

CREATE FUNCTION validate_execution_confirmation_principal()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    parent_version TEXT;
    parent_actor_type TEXT;
    parent_actor_id UUID;
    parent_subject_type TEXT;
    parent_subject_id UUID;
    parent_client_id UUID;
    parent_grant_id UUID;
    parent_grant_version BIGINT;
    parent_policy_version BIGINT;
    parent_run UUID;
    decision_mode TEXT;
    max_risk TEXT;
    release_risk TEXT;
    side_effect TEXT;
    mandatory BOOLEAN;
BEGIN
    IF TG_OP='DELETE' THEN
        RAISE EXCEPTION 'execution confirmations are permanently retained'
            USING ERRCODE='55000';
    END IF;

    IF TG_OP='INSERT' THEN
        IF NEW.status<>'PENDING' OR NEW.request_principal_snapshot_version<>'execution.principal.v1' THEN
            RAISE EXCEPTION 'new confirmation requires a modern pending Principal snapshot'
                USING ERRCODE='23514',CONSTRAINT='execution_confirmation_modern_request_check';
        END IF;
    ELSE
        IF ROW(
            NEW.id,NEW.workspace_id,NEW.execution_id,NEW.run_id,NEW.node_id,
            NEW.reason,NEW.risk_reasons,NEW.scope_snapshot,NEW.release_id,
            NEW.input_hash,NEW.connection_id,NEW.plan_hash,NEW.resume_token_hash,
            NEW.requested_by,NEW.created_at,NEW.expires_at,
            NEW.request_principal_snapshot_version,NEW.request_actor_type,
            NEW.request_actor_id,NEW.request_subject_type,NEW.request_subject_id,
            NEW.request_client_id,NEW.request_grant_id,NEW.request_grant_version,
            NEW.request_agent_policy_version
        ) IS DISTINCT FROM ROW(
            OLD.id,OLD.workspace_id,OLD.execution_id,OLD.run_id,OLD.node_id,
            OLD.reason,OLD.risk_reasons,OLD.scope_snapshot,OLD.release_id,
            OLD.input_hash,OLD.connection_id,OLD.plan_hash,OLD.resume_token_hash,
            OLD.requested_by,OLD.created_at,OLD.expires_at,
            OLD.request_principal_snapshot_version,OLD.request_actor_type,
            OLD.request_actor_id,OLD.request_subject_type,OLD.request_subject_id,
            OLD.request_client_id,OLD.request_grant_id,OLD.request_grant_version,
            OLD.request_agent_policy_version
        ) THEN
            RAISE EXCEPTION 'execution confirmation request snapshot is immutable'
                USING ERRCODE='55000';
        END IF;
        IF OLD.status IN ('CONFIRMED','CANCELLED','EXPIRED') THEN
            RAISE EXCEPTION 'terminal execution confirmation is immutable' USING ERRCODE='55000';
        END IF;
        IF NEW.lock_version<>OLD.lock_version+1 THEN
            RAISE EXCEPTION 'execution confirmation requires next lock version' USING ERRCODE='40001';
        END IF;
        IF NEW.status NOT IN ('CONFIRMED','CANCELLED','EXPIRED') THEN
            RAISE EXCEPTION 'illegal execution confirmation transition' USING ERRCODE='55000';
        END IF;
    END IF;

    IF NEW.request_actor_type='USER' THEN
        IF NEW.request_subject_type IS DISTINCT FROM 'USER'
           OR NEW.request_subject_id IS DISTINCT FROM NEW.request_actor_id
           OR NEW.request_client_id IS NOT NULL THEN
            RAISE EXCEPTION 'User confirmation request identity is invalid'
                USING ERRCODE='23514',CONSTRAINT='execution_confirmation_request_identity_check';
        END IF;
    ELSIF NEW.request_actor_type='SERVICE_PRINCIPAL' THEN
        IF NEW.request_client_id IS NULL OR NEW.request_grant_id IS NULL
           OR NOT EXISTS (
             SELECT 1 FROM agent_access_clients c
             WHERE c.workspace_id=NEW.workspace_id AND c.id=NEW.request_client_id
               AND c.service_principal_id=NEW.request_actor_id
           ) OR NOT EXISTS (
             SELECT 1 FROM agent_access_grants g
             WHERE g.workspace_id=NEW.workspace_id AND g.id=NEW.request_grant_id
               AND g.client_id=NEW.request_client_id
           ) OR (NEW.request_subject_id IS NOT NULL AND (
             NEW.request_subject_type<>'EXTERNAL_SUBJECT' OR NOT EXISTS (
               SELECT 1 FROM external_subjects s
               WHERE s.workspace_id=NEW.workspace_id AND s.id=NEW.request_subject_id
                 AND s.client_id=NEW.request_client_id
             )
           )) THEN
            RAISE EXCEPTION 'external confirmation request identity is invalid'
                USING ERRCODE='23514',CONSTRAINT='execution_confirmation_request_identity_check';
        END IF;
    ELSE
        RAISE EXCEPTION 'confirmation requester must be User or Service Principal'
            USING ERRCODE='23514',CONSTRAINT='execution_confirmation_request_identity_check';
    END IF;

    IF NEW.execution_id IS NOT NULL THEN
        SELECT principal_snapshot_version,triggered_by_type,triggered_by_id,
               subject_type,subject_id,client_id,grant_id,grant_version,
               agent_policy_version,agent_run_id
        INTO STRICT parent_version,parent_actor_type,parent_actor_id,parent_subject_type,
             parent_subject_id,parent_client_id,parent_grant_id,parent_grant_version,
             parent_policy_version,parent_run
        FROM workflow_executions
        WHERE workspace_id=NEW.workspace_id AND id=NEW.execution_id;
        IF NEW.run_id IS NOT NULL AND parent_run IS DISTINCT FROM NEW.run_id THEN
            RAISE EXCEPTION 'confirmation execution and run chain mismatch'
                USING ERRCODE='23514',CONSTRAINT='execution_confirmation_parent_chain_check';
        END IF;
    ELSE
        SELECT principal_snapshot_version,triggered_by_type,triggered_by_id,
               subject_type,subject_id,client_id,grant_id,grant_version,
               agent_policy_version
        INTO STRICT parent_version,parent_actor_type,parent_actor_id,parent_subject_type,
             parent_subject_id,parent_client_id,parent_grant_id,parent_grant_version,
             parent_policy_version
        FROM agent_runs WHERE workspace_id=NEW.workspace_id AND id=NEW.run_id;
    END IF;
    IF NOT (
        NEW.request_principal_snapshot_version=parent_version
        OR (parent_version='legacy.v1'
            AND NEW.request_principal_snapshot_version='execution.principal.v1'
            AND NEW.request_actor_type='USER')
    ) OR ROW(
        NEW.request_actor_type,NEW.request_actor_id,
        NEW.request_subject_type,NEW.request_subject_id,NEW.request_client_id,
        NEW.request_grant_id,NEW.request_grant_version,NEW.request_agent_policy_version
    ) IS DISTINCT FROM ROW(
        parent_actor_type,parent_actor_id,parent_subject_type,parent_subject_id,
        parent_client_id,parent_grant_id,parent_grant_version,parent_policy_version
    ) THEN
        RAISE EXCEPTION 'confirmation requester differs from parent execution snapshot'
            USING ERRCODE='23514',CONSTRAINT='execution_confirmation_parent_principal_check';
    END IF;

    IF NEW.decision_principal_snapshot_version IS NOT NULL THEN
        IF NEW.decision_principal_snapshot_version<>'execution.principal.v1'
           AND NEW.request_principal_snapshot_version<>'legacy.v1' THEN
            RAISE EXCEPTION 'new decision requires modern Principal snapshot'
                USING ERRCODE='23514',CONSTRAINT='execution_confirmation_decision_identity_check';
        END IF;
        IF ROW(
            NEW.decision_actor_type,NEW.decision_actor_id,
            NEW.decision_subject_type,NEW.decision_subject_id,
            NEW.decision_client_id,NEW.decision_grant_id
        ) IS DISTINCT FROM ROW(
            NEW.request_actor_type,NEW.request_actor_id,
            NEW.request_subject_type,NEW.request_subject_id,
            NEW.request_client_id,NEW.request_grant_id
        ) THEN
            RAISE EXCEPTION 'confirmation decision Principal differs from requester'
                USING ERRCODE='23514',CONSTRAINT='execution_confirmation_decision_identity_check';
        END IF;
        decision_mode := NEW.decision_policy_snapshot->>'mode';
        IF NEW.decision_actor_type='USER' THEN
            IF decision_mode<>'actweave_user' THEN
                RAISE EXCEPTION 'User decision policy evidence is invalid'
                    USING ERRCODE='23514',CONSTRAINT='execution_confirmation_decision_identity_check';
            END IF;
        ELSIF NEW.decision_subject_id IS NOT NULL THEN
            IF NEW.decision_subject_type<>'EXTERNAL_SUBJECT'
               OR decision_mode<>'external_subject' THEN
                RAISE EXCEPTION 'External Subject decision evidence is invalid'
                    USING ERRCODE='23514',CONSTRAINT='execution_confirmation_decision_identity_check';
            END IF;
        ELSE
            max_risk := NEW.decision_policy_snapshot->>'maxRisk';
            release_risk := lower(COALESCE(NEW.scope_snapshot#>>'{release,riskLevel}',''));
            side_effect := upper(COALESCE(NEW.scope_snapshot#>>'{release,sideEffectLevel}',''));
            mandatory := COALESCE((NEW.scope_snapshot#>>'{decision,mandatory}')::BOOLEAN,FALSE);
            IF decision_mode<>'service_principal'
               OR NEW.decision_policy_snapshot->>'enabled'<>'true'
               OR max_risk NOT IN ('low','medium')
               OR release_risk NOT IN ('low','medium')
               OR (max_risk='low' AND release_risk<>'low')
               OR mandatory OR side_effect='IRREVERSIBLE'
               OR NOT EXISTS (
                 SELECT 1 FROM agent_access_grants g
                 JOIN agents a ON a.workspace_id=g.workspace_id AND a.id=g.agent_id
                 WHERE g.workspace_id=NEW.workspace_id AND g.id=NEW.decision_grant_id
                   AND g.client_id=NEW.decision_client_id
                   AND g.lock_version=NEW.decision_grant_version
                   AND a.lock_version=NEW.decision_agent_policy_version
                   AND g.status='ACTIVE' AND g.valid_from<=clock_timestamp()
                   AND (g.expires_at IS NULL OR g.expires_at>clock_timestamp())
                   AND g.scopes ? 'interaction:decide'
                   AND g.policy#>>'{serviceDecision,enabled}'='true'
                   AND g.policy#>>'{serviceDecision,maxRisk}'=max_risk
               ) THEN
                RAISE EXCEPTION 'Service Principal is not allowed to decide this confirmation'
                    USING ERRCODE='23514',CONSTRAINT='execution_confirmation_service_decision_check';
            END IF;
        END IF;
    END IF;
    RETURN NEW;
EXCEPTION
    WHEN NO_DATA_FOUND THEN
        RAISE EXCEPTION 'confirmation parent execution does not exist'
            USING ERRCODE='23503',CONSTRAINT='execution_confirmation_parent_fk';
END;
$$;

CREATE TRIGGER execution_confirmations_fact_guard
BEFORE INSERT OR UPDATE OR DELETE ON execution_confirmations
FOR EACH ROW EXECUTE FUNCTION validate_execution_confirmation_principal();

ALTER TABLE chat_confirmations
    DROP CONSTRAINT chat_confirmations_confirmed_state_check,
    ADD CONSTRAINT chat_confirmations_confirmed_state_check CHECK (
        (status='CONFIRMED' AND confirmed_at IS NOT NULL)
        OR (status<>'CONFIRMED' AND confirmed_by IS NULL AND confirmed_at IS NULL)
    );

CREATE FUNCTION enforce_chat_confirmation_projection()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    execution_row execution_confirmations%ROWTYPE;
BEGIN
    IF TG_OP='DELETE' THEN
        RAISE EXCEPTION 'chat confirmations are permanently retained' USING ERRCODE='55000';
    END IF;
    SELECT * INTO execution_row FROM execution_confirmations
    WHERE workspace_id=NEW.workspace_id AND id=NEW.execution_confirmation_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'chat confirmation execution target not found' USING ERRCODE='23503';
    END IF;
    IF NEW.run_id IS DISTINCT FROM execution_row.run_id
       OR NEW.target_release_id IS DISTINCT FROM execution_row.release_id THEN
        RAISE EXCEPTION 'chat confirmation target differs from execution confirmation'
            USING ERRCODE='23514';
    END IF;
    IF NEW.status IS DISTINCT FROM execution_row.status
       OR NEW.confirmed_by IS DISTINCT FROM execution_row.confirmed_by
       OR NEW.confirmed_at IS DISTINCT FROM execution_row.confirmed_at THEN
        RAISE EXCEPTION 'chat confirmation state is derived from execution confirmation'
            USING ERRCODE='55000';
    END IF;
    IF TG_OP='UPDATE' AND ROW(
        NEW.id,NEW.workspace_id,NEW.session_id,NEW.run_id,
        NEW.execution_confirmation_id,NEW.target_type,NEW.target_release_id,
        NEW.risk_level,NEW.risk_reasons,NEW.input_summary,NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.id,OLD.workspace_id,OLD.session_id,OLD.run_id,
        OLD.execution_confirmation_id,OLD.target_type,OLD.target_release_id,
        OLD.risk_level,OLD.risk_reasons,OLD.input_summary,OLD.created_at
    ) THEN
        RAISE EXCEPTION 'chat confirmation display mapping is immutable' USING ERRCODE='55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER chat_confirmations_projection_guard
BEFORE INSERT OR UPDATE OR DELETE ON chat_confirmations
FOR EACH ROW EXECUTE FUNCTION enforce_chat_confirmation_projection();

CREATE FUNCTION synchronize_chat_confirmation_projection()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    chat_confirmation_id UUID;
BEGIN
    UPDATE chat_confirmations
    SET status=NEW.status,confirmed_by=NEW.confirmed_by,confirmed_at=NEW.confirmed_at
    WHERE workspace_id=NEW.workspace_id AND execution_confirmation_id=NEW.id
    RETURNING id INTO chat_confirmation_id;
    IF chat_confirmation_id IS NOT NULL AND NEW.status<>'PENDING' THEN
        UPDATE chat_sessions SET pending_confirmation_id=NULL,
            updated_at=clock_timestamp(),lock_version=lock_version+1
        WHERE workspace_id=NEW.workspace_id AND pending_confirmation_id=chat_confirmation_id;
        UPDATE chat_messages
        SET status=CASE WHEN NEW.status='CONFIRMED' THEN 'PROCESSING' ELSE 'FAILED' END
        WHERE workspace_id=NEW.workspace_id AND confirmation_id=chat_confirmation_id
          AND status='PENDING_CONFIRMATION';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER execution_confirmations_chat_projection_sync
AFTER UPDATE OF status,confirmed_by,confirmed_at ON execution_confirmations
FOR EACH ROW WHEN (OLD.status IS DISTINCT FROM NEW.status)
EXECUTE FUNCTION synchronize_chat_confirmation_projection();

CREATE INDEX execution_confirmations_workspace_request_principal_idx
    ON execution_confirmations(workspace_id,request_client_id,request_subject_type,
        request_subject_id,created_at DESC,id);
CREATE INDEX execution_confirmations_workspace_decision_principal_idx
    ON execution_confirmations(workspace_id,decision_client_id,decision_subject_type,
        decision_subject_id,created_at DESC,id)
    WHERE decision_principal_snapshot_version IS NOT NULL;


-- ##########################################################################
-- Source: 000052_subject_ownership_policy.up.sql
-- ##########################################################################

CREATE OR REPLACE FUNCTION agent_access_grant_policy_valid(policy JSONB)
RETURNS BOOLEAN
LANGUAGE plpgsql
IMMUTABLE
PARALLEL SAFE
STRICT
AS $$
DECLARE
    service_decision JSONB;
    subject_sharing JSONB;
    resource_value JSONB;
BEGIN
    IF jsonb_typeof(policy) <> 'object' OR EXISTS (
        SELECT 1 FROM jsonb_object_keys(policy) AS key
        WHERE key NOT IN ('serviceDecision', 'subjectSharing')
    ) THEN
        RETURN FALSE;
    END IF;

    IF policy ? 'serviceDecision' THEN
        service_decision := policy->'serviceDecision';
        IF jsonb_typeof(service_decision) <> 'object'
           OR NOT service_decision ? 'enabled'
           OR jsonb_typeof(service_decision->'enabled') <> 'boolean'
           OR EXISTS (
                SELECT 1 FROM jsonb_object_keys(service_decision) AS key
                WHERE key NOT IN ('enabled', 'maxRisk')
           ) THEN
            RETURN FALSE;
        END IF;
        IF (service_decision->>'enabled')::BOOLEAN THEN
            IF service_decision->>'maxRisk' NOT IN ('low', 'medium')
               OR jsonb_typeof(service_decision->'maxRisk') <> 'string' THEN
                RETURN FALSE;
            END IF;
        ELSIF service_decision ? 'maxRisk' THEN
            RETURN FALSE;
        END IF;
    END IF;

    IF NOT policy ? 'subjectSharing' THEN
        RETURN TRUE;
    END IF;
    subject_sharing := policy->'subjectSharing';
    IF jsonb_typeof(subject_sharing) <> 'object'
       OR NOT subject_sharing ? 'enabled'
       OR jsonb_typeof(subject_sharing->'enabled') <> 'boolean'
       OR EXISTS (
            SELECT 1 FROM jsonb_object_keys(subject_sharing) AS key
            WHERE key NOT IN ('enabled', 'resources')
       ) THEN
        RETURN FALSE;
    END IF;
    IF NOT (subject_sharing->>'enabled')::BOOLEAN THEN
        RETURN NOT subject_sharing ? 'resources';
    END IF;
    IF jsonb_typeof(subject_sharing->'resources') <> 'array'
       OR jsonb_array_length(subject_sharing->'resources') < 1
       OR jsonb_array_length(subject_sharing->'resources') > 5 THEN
        RETURN FALSE;
    END IF;
    FOR resource_value IN SELECT value FROM jsonb_array_elements(subject_sharing->'resources')
    LOOP
        IF jsonb_typeof(resource_value) <> 'string'
           OR resource_value #>> '{}' NOT IN (
                'conversation','run','event','interaction','artifact'
           ) THEN
            RETURN FALSE;
        END IF;
    END LOOP;
    RETURN jsonb_array_length(subject_sharing->'resources') = (
        SELECT count(DISTINCT value #>> '{}')
        FROM jsonb_array_elements(subject_sharing->'resources')
    );
END;
$$;



-- ##########################################################################
-- Source: 000053_interaction_decision_binding.up.sql
-- ##########################################################################

ALTER TABLE execution_confirmations
    ADD COLUMN target_item_id UUID,
    ADD COLUMN interaction_binding_hash CHAR(64),
    ADD CONSTRAINT execution_confirmations_interaction_binding_pair_check CHECK (
        (target_item_id IS NULL) = (interaction_binding_hash IS NULL)
    ),
    ADD CONSTRAINT execution_confirmations_interaction_binding_hash_check CHECK (
        interaction_binding_hash IS NULL OR interaction_binding_hash ~ '^[0-9a-f]{64}$'
    );

ALTER TABLE confirmation_resume_checkpoints
    ADD COLUMN target_item_id UUID,
    ADD COLUMN interaction_binding_hash CHAR(64),
    ADD CONSTRAINT confirmation_resume_interaction_binding_pair_check CHECK (
        (target_item_id IS NULL) = (interaction_binding_hash IS NULL)
    ),
    ADD CONSTRAINT confirmation_resume_interaction_binding_hash_check CHECK (
        interaction_binding_hash IS NULL OR interaction_binding_hash ~ '^[0-9a-f]{64}$'
    );

CREATE FUNCTION enforce_interaction_confirmation_binding()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND ROW(NEW.target_item_id, NEW.interaction_binding_hash)
        IS DISTINCT FROM ROW(OLD.target_item_id, OLD.interaction_binding_hash) THEN
        RAISE EXCEPTION 'interaction confirmation binding is immutable'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER execution_confirmations_interaction_binding_guard
BEFORE UPDATE ON execution_confirmations
FOR EACH ROW EXECUTE FUNCTION enforce_interaction_confirmation_binding();

CREATE FUNCTION enforce_confirmation_resume_interaction_binding()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    confirmation_target UUID;
    confirmation_hash CHAR(64);
BEGIN
    IF TG_OP = 'UPDATE' AND ROW(NEW.target_item_id, NEW.interaction_binding_hash)
        IS DISTINCT FROM ROW(OLD.target_item_id, OLD.interaction_binding_hash) THEN
        RAISE EXCEPTION 'confirmation resume interaction binding is immutable'
            USING ERRCODE = '55000';
    END IF;
    SELECT target_item_id, interaction_binding_hash
      INTO confirmation_target, confirmation_hash
      FROM execution_confirmations
     WHERE workspace_id = NEW.workspace_id AND id = NEW.confirmation_id;
    IF ROW(NEW.target_item_id, NEW.interaction_binding_hash)
        IS DISTINCT FROM ROW(confirmation_target, confirmation_hash) THEN
        RAISE EXCEPTION 'confirmation resume interaction binding mismatch'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER confirmation_resume_interaction_binding_guard
BEFORE INSERT OR UPDATE ON confirmation_resume_checkpoints
FOR EACH ROW EXECUTE FUNCTION enforce_confirmation_resume_interaction_binding();

CREATE TABLE interaction_decision_commands (
    workspace_id UUID NOT NULL,
    confirmation_id UUID NOT NULL,
    principal_binding_hash CHAR(64) NOT NULL,
    idempotency_key UUID NOT NULL,
    request_hash CHAR(64) NOT NULL,
    decision TEXT NOT NULL,
    expected_version BIGINT NOT NULL,
    confirmation_status TEXT NOT NULL,
    confirmation_version BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (workspace_id, confirmation_id, principal_binding_hash, idempotency_key),
    CONSTRAINT interaction_decision_commands_confirmation_fk
        FOREIGN KEY (workspace_id, confirmation_id)
        REFERENCES execution_confirmations (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT interaction_decision_commands_principal_hash_check
        CHECK (principal_binding_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT interaction_decision_commands_request_hash_check
        CHECK (request_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT interaction_decision_commands_decision_check
        CHECK (decision IN ('approve', 'decline', 'cancel')),
    CONSTRAINT interaction_decision_commands_expected_version_check
        CHECK (expected_version > 0),
    CONSTRAINT interaction_decision_commands_result_check CHECK (
        (decision = 'approve' AND confirmation_status = 'CONFIRMED')
        OR (decision IN ('decline', 'cancel') AND confirmation_status = 'CANCELLED')
    ),
    CONSTRAINT interaction_decision_commands_confirmation_version_check
        CHECK (confirmation_version = expected_version + 1)
);

CREATE INDEX interaction_decision_commands_confirmation_created_idx
    ON interaction_decision_commands (workspace_id, confirmation_id, created_at, idempotency_key);

CREATE FUNCTION enforce_interaction_decision_command_fact()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' OR TG_OP = 'UPDATE' THEN
        RAISE EXCEPTION 'interaction decision commands are immutable'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER interaction_decision_commands_fact_guard
BEFORE UPDATE OR DELETE ON interaction_decision_commands
FOR EACH ROW EXECUTE FUNCTION enforce_interaction_decision_command_fact();


-- ##########################################################################
-- Source: 000054_agent_access_data_commands.up.sql
-- ##########################################################################

CREATE TABLE agent_access_data_commands (
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    client_id UUID NOT NULL,
    service_principal_id UUID NOT NULL,
    subject_id UUID NOT NULL,
    operation TEXT NOT NULL,
    idempotency_key UUID NOT NULL,
    request_hash BYTEA NOT NULL CHECK (octet_length(request_hash) = 32),
    resource_type TEXT,
    resource_id UUID,
    response_version BIGINT CHECK (response_version IS NULL OR response_version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (
        workspace_id, agent_id, client_id, service_principal_id,
        subject_id, operation, idempotency_key
    ),
    CHECK (operation IN (
        'conversation.create', 'run.create', 'run.cancel', 'interaction.decide'
    )),
    CHECK ((resource_type IS NULL) = (resource_id IS NULL)),
    CHECK (resource_type IS NULL OR resource_type IN ('CONVERSATION', 'RUN', 'INTERACTION')),
    CHECK (expires_at >= created_at + INTERVAL '24 hours')
);

CREATE INDEX agent_access_data_commands_expiry_idx
    ON agent_access_data_commands (expires_at, workspace_id, agent_id);

CREATE INDEX agent_access_data_commands_resource_idx
    ON agent_access_data_commands (workspace_id, resource_type, resource_id)
    WHERE resource_id IS NOT NULL;

COMMENT ON TABLE agent_access_data_commands IS
    'Unified durable data-plane command receipts; request hashes only, never raw tokens or command bodies';


-- ##########################################################################
-- Source: 000055_trusted_subject_issuer.up.sql
-- ##########################################################################

-- M9-T1: Trusted Subject Issuer configuration for Agent Access Clients.
-- Inline JWKS and JWKS URI are mutually exclusive. OIDC Discovery is not used;
-- only an explicit fixed HTTPS JWKS URI or an inline JWKS document is allowed.

CREATE FUNCTION agent_access_subject_algorithms_valid(algorithms JSONB)
RETURNS BOOLEAN
LANGUAGE SQL
IMMUTABLE
PARALLEL SAFE
AS $$
    SELECT CASE
        WHEN algorithms IS NULL THEN TRUE
        WHEN jsonb_typeof(algorithms) <> 'array' THEN FALSE
        WHEN jsonb_array_length(algorithms) < 1 OR jsonb_array_length(algorithms) > 8 THEN FALSE
        ELSE
            NOT EXISTS (
                SELECT 1
                FROM jsonb_array_elements(algorithms) AS element(value)
                WHERE jsonb_typeof(element.value) <> 'string'
                   OR element.value #>> '{}' NOT IN ('EdDSA', 'PS256')
            )
            AND jsonb_array_length(algorithms) = (
                SELECT count(DISTINCT element.value #>> '{}')
                FROM jsonb_array_elements(algorithms) AS element(value)
            )
    END
$$;

CREATE FUNCTION agent_access_subject_claim_policy_valid(policy JSONB)
RETURNS BOOLEAN
LANGUAGE SQL
IMMUTABLE
PARALLEL SAFE
AS $$
    SELECT CASE
        WHEN policy IS NULL THEN TRUE
        WHEN jsonb_typeof(policy) <> 'object' THEN FALSE
        WHEN policy ?| ARRAY['subject', 'email', 'phone', 'rawToken', 'subjectToken'] THEN FALSE
        ELSE
            (SELECT count(*) FROM jsonb_object_keys(policy)) = 4
            AND policy ? 'subjectClaim'
            AND policy ? 'requireJti'
            AND policy ? 'maxSubjectBytes'
            AND policy ? 'maxTokenTTLSeconds'
            AND jsonb_typeof(policy->'subjectClaim') = 'string'
            AND policy->>'subjectClaim' = 'sub'
            AND jsonb_typeof(policy->'requireJti') = 'boolean'
            AND jsonb_typeof(policy->'maxSubjectBytes') = 'number'
            AND (policy->>'maxSubjectBytes') ~ '^[0-9]+$'
            AND (policy->>'maxSubjectBytes')::INTEGER BETWEEN 1 AND 256
            AND jsonb_typeof(policy->'maxTokenTTLSeconds') = 'number'
            AND (policy->>'maxTokenTTLSeconds') ~ '^[0-9]+$'
            AND (policy->>'maxTokenTTLSeconds')::INTEGER BETWEEN 60 AND 86400
    END
$$;

CREATE FUNCTION agent_access_subject_inline_jwks_valid(jwks JSONB)
RETURNS BOOLEAN
LANGUAGE SQL
IMMUTABLE
PARALLEL SAFE
AS $$
    SELECT CASE
        WHEN jwks IS NULL THEN TRUE
        WHEN jsonb_typeof(jwks) <> 'object' THEN FALSE
        WHEN NOT (jwks ? 'keys') THEN FALSE
        WHEN jsonb_typeof(jwks->'keys') <> 'array' THEN FALSE
        WHEN jsonb_array_length(jwks->'keys') < 1 OR jsonb_array_length(jwks->'keys') > 32 THEN FALSE
        WHEN length(jwks::text) > 262144 THEN FALSE
        ELSE TRUE
    END
$$;

ALTER TABLE agent_access_clients
    ADD COLUMN trusted_subject_audience TEXT,
    ADD COLUMN trusted_subject_inline_jwks JSONB,
    ADD COLUMN trusted_subject_algorithms JSONB,
    ADD COLUMN trusted_subject_claim_policy JSONB;

-- Incomplete pre-M9 trust pairs cannot satisfy the expanded contract.
-- No production Token Exchange depends on these rows yet.
UPDATE agent_access_clients
SET
    trusted_subject_issuer = NULL,
    trusted_subject_jwks_uri = NULL
WHERE trusted_subject_issuer IS NOT NULL
   OR trusted_subject_jwks_uri IS NOT NULL;

ALTER TABLE agent_access_clients
    DROP CONSTRAINT agent_access_clients_subject_trust_pair_check;

ALTER TABLE agent_access_clients
    DROP CONSTRAINT agent_access_clients_subject_issuer_check;

ALTER TABLE agent_access_clients
    DROP CONSTRAINT agent_access_clients_subject_jwks_uri_check;

ALTER TABLE agent_access_clients
    ADD CONSTRAINT agent_access_clients_subject_trust_presence_check CHECK (
        (
            trusted_subject_issuer IS NULL
            AND trusted_subject_audience IS NULL
            AND trusted_subject_jwks_uri IS NULL
            AND trusted_subject_inline_jwks IS NULL
            AND trusted_subject_algorithms IS NULL
            AND trusted_subject_claim_policy IS NULL
        )
        OR (
            trusted_subject_issuer IS NOT NULL
            AND trusted_subject_audience IS NOT NULL
            AND trusted_subject_algorithms IS NOT NULL
            AND trusted_subject_claim_policy IS NOT NULL
            AND (
                (trusted_subject_jwks_uri IS NOT NULL AND trusted_subject_inline_jwks IS NULL)
                OR (trusted_subject_jwks_uri IS NULL AND trusted_subject_inline_jwks IS NOT NULL)
            )
        )
    );

ALTER TABLE agent_access_clients
    ADD CONSTRAINT agent_access_clients_subject_issuer_check CHECK (
        trusted_subject_issuer IS NULL OR (
            length(trusted_subject_issuer) <= 2048
            AND btrim(trusted_subject_issuer) = trusted_subject_issuer
            AND trusted_subject_issuer ~ '^https://[^[:space:]?#]+/?$'
        )
    );

ALTER TABLE agent_access_clients
    ADD CONSTRAINT agent_access_clients_subject_audience_check CHECK (
        trusted_subject_audience IS NULL OR (
            length(trusted_subject_audience) BETWEEN 1 AND 2048
            AND btrim(trusted_subject_audience) = trusted_subject_audience
            AND position(E'\n' IN trusted_subject_audience) = 0
            AND position(E'\r' IN trusted_subject_audience) = 0
            AND position(E'\t' IN trusted_subject_audience) = 0
        )
    );

ALTER TABLE agent_access_clients
    ADD CONSTRAINT agent_access_clients_subject_jwks_uri_check CHECK (
        trusted_subject_jwks_uri IS NULL OR (
            length(trusted_subject_jwks_uri) <= 2048
            AND btrim(trusted_subject_jwks_uri) = trusted_subject_jwks_uri
            AND trusted_subject_jwks_uri ~ '^https://[^[:space:]#]+$'
        )
    );

ALTER TABLE agent_access_clients
    ADD CONSTRAINT agent_access_clients_subject_inline_jwks_check
        CHECK (agent_access_subject_inline_jwks_valid(trusted_subject_inline_jwks));

ALTER TABLE agent_access_clients
    ADD CONSTRAINT agent_access_clients_subject_algorithms_check
        CHECK (agent_access_subject_algorithms_valid(trusted_subject_algorithms));

ALTER TABLE agent_access_clients
    ADD CONSTRAINT agent_access_clients_subject_claim_policy_check
        CHECK (agent_access_subject_claim_policy_valid(trusted_subject_claim_policy));


-- ##########################################################################
-- Source: 000056_subject_token_jtis.up.sql
-- ##########################################################################

-- M9-T2: durable Subject Token JTI replay protection for OAuth Token Exchange.
-- Mirrors Client Assertion JTI evidence: immutable rows, 32-byte keyed hashes.

CREATE TABLE agent_access_subject_token_jtis (
    client_id UUID NOT NULL,
    jti_hash BYTEA NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (client_id, jti_hash),
    CONSTRAINT agent_access_subject_token_jtis_client_fk
        FOREIGN KEY (client_id) REFERENCES agent_access_clients (id) ON DELETE RESTRICT,
    CONSTRAINT agent_access_subject_token_jtis_hash_check
        CHECK (octet_length(jti_hash) = 32),
    CONSTRAINT agent_access_subject_token_jtis_expiry_check
        CHECK (expires_at > created_at)
);

CREATE INDEX agent_access_subject_token_jtis_expiry_idx
    ON agent_access_subject_token_jtis (expires_at, client_id);

CREATE FUNCTION enforce_agent_access_subject_token_jti_immutable()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        RAISE EXCEPTION 'Agent Access Subject Token JTI evidence is immutable'
            USING ERRCODE = '55000',
                  CONSTRAINT = 'agent_access_subject_token_jtis_immutable';
    END IF;
    RETURN OLD;
END;
$$;

CREATE TRIGGER agent_access_subject_token_jtis_immutable
BEFORE UPDATE ON agent_access_subject_token_jtis
FOR EACH ROW EXECUTE FUNCTION enforce_agent_access_subject_token_jti_immutable();


-- ##########################################################################
-- Source: 000057_runtime_continuation_claims.up.sql
-- ##########################################################################

-- Durable multi-replica lease for post-SUCCEEDED runtime continue (EnqueueContinue).
-- Terminal confirmation_resume_checkpoints rows are immutable, so the lease lives
-- in a sibling table keyed by confirmation_id.
CREATE TABLE runtime_continuation_claims (
    confirmation_id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    run_id UUID NOT NULL,
    claim_id UUID,
    claim_expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    lock_version BIGINT NOT NULL DEFAULT 1,
    CONSTRAINT runtime_continuation_claims_workspace_id_key
        UNIQUE (workspace_id, confirmation_id),
    CONSTRAINT runtime_continuation_claims_confirmation_fk
        FOREIGN KEY (workspace_id, confirmation_id)
        REFERENCES execution_confirmations (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT runtime_continuation_claims_run_fk
        FOREIGN KEY (workspace_id, run_id)
        REFERENCES agent_runs (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT runtime_continuation_claims_claim_pair_check
        CHECK ((claim_id IS NULL) = (claim_expires_at IS NULL)),
    CONSTRAINT runtime_continuation_claims_claim_window_check
        CHECK (
            claim_expires_at IS NULL
            OR claim_id IS NOT NULL
        ),
    CONSTRAINT runtime_continuation_claims_lock_check CHECK (lock_version > 0)
);

CREATE INDEX runtime_continuation_claims_reclaim_idx
    ON runtime_continuation_claims (claim_expires_at, confirmation_id)
    WHERE claim_id IS NOT NULL;


-- ##########################################################################
-- Source: 000058_eino_checkpoints.up.sql
-- ##########################################################################

-- Eino CheckPointStore persistence (adk / compose gob payloads).
-- checkpoint_id is the full multi-tenant key: ws/{workspaceID}/...
-- workspace_id is a redundant column parsed from the ID and validated on write.
-- expires_at aligns with confirmation TTL (PR3 wires write/renew + cleanup job).
CREATE TABLE eino_checkpoints (
    checkpoint_id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    owner_id TEXT NOT NULL,
    payload BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT eino_checkpoints_checkpoint_id_not_blank
        CHECK (length(btrim(checkpoint_id)) > 0),
    CONSTRAINT eino_checkpoints_workspace_id_not_blank
        CHECK (length(btrim(workspace_id)) > 0),
    CONSTRAINT eino_checkpoints_owner_id_not_blank
        CHECK (length(btrim(owner_id)) > 0),
    CONSTRAINT eino_checkpoints_kind_check
        CHECK (kind IN ('agent_run', 'workflow_execution'))
);

CREATE INDEX eino_checkpoints_workspace_id_idx
    ON eino_checkpoints (workspace_id);

CREATE INDEX eino_checkpoints_expires_at_idx
    ON eino_checkpoints (expires_at);

CREATE INDEX eino_checkpoints_owner_idx
    ON eino_checkpoints (kind, owner_id);


-- ##########################################################################
-- Source: 000059_workflow_generate_sessions.up.sql
-- ##########################################################################

-- SmartGenerateSession storage for multi-turn intelligent orchestration (D15).
-- Console-only generate context; not ChatSession / AAP Conversation.
-- Application generates UUIDv7 for entity ids (no random UUID defaults).

CREATE TABLE workflow_generate_sessions (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    workflow_id UUID,
    model_config_id UUID NOT NULL,
    status TEXT NOT NULL DEFAULT 'OPEN',
    prompt_id TEXT,
    prompt_hash CHAR(64),
    constraints JSONB NOT NULL DEFAULT '{}'::JSONB,
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    closed_at TIMESTAMPTZ,
    lock_version BIGINT NOT NULL DEFAULT 1,
    CONSTRAINT workflow_generate_sessions_workspace_id_id_key
        UNIQUE (workspace_id, id),
    CONSTRAINT workflow_generate_sessions_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT workflow_generate_sessions_workspace_agent_fk
        FOREIGN KEY (workspace_id, agent_id)
        REFERENCES agents (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT workflow_generate_sessions_workspace_model_config_fk
        FOREIGN KEY (workspace_id, model_config_id)
        REFERENCES model_configs (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT workflow_generate_sessions_workspace_workflow_fk
        FOREIGN KEY (workspace_id, workflow_id)
        REFERENCES workflows (workspace_id, capability_id) ON DELETE RESTRICT,
    CONSTRAINT workflow_generate_sessions_created_by_fk
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT workflow_generate_sessions_status_check
        CHECK (status IN ('OPEN', 'CLOSED')),
    CONSTRAINT workflow_generate_sessions_closed_state_check CHECK (
        (status = 'OPEN' AND closed_at IS NULL)
        OR (status = 'CLOSED' AND closed_at IS NOT NULL)
    ),
    CONSTRAINT workflow_generate_sessions_prompt_hash_check CHECK (
        prompt_hash IS NULL OR prompt_hash ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT workflow_generate_sessions_constraints_object_check
        CHECK (jsonb_typeof(constraints) = 'object'),
    CONSTRAINT workflow_generate_sessions_lock_version_check CHECK (lock_version > 0),
    CONSTRAINT workflow_generate_sessions_timestamps_check CHECK (updated_at >= created_at),
    CONSTRAINT workflow_generate_sessions_closed_at_check
        CHECK (closed_at IS NULL OR closed_at >= created_at)
);

CREATE INDEX workflow_generate_sessions_workspace_status_updated_idx
    ON workflow_generate_sessions (workspace_id, status, updated_at DESC, id);

CREATE INDEX workflow_generate_sessions_workspace_agent_updated_idx
    ON workflow_generate_sessions (workspace_id, agent_id, updated_at DESC, id);

CREATE INDEX workflow_generate_sessions_workspace_workflow_idx
    ON workflow_generate_sessions (workspace_id, workflow_id, id)
    WHERE workflow_id IS NOT NULL;

CREATE TABLE workflow_generate_turns (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    session_id UUID NOT NULL,
    turn_index INTEGER NOT NULL,
    user_message TEXT NOT NULL,
    assistant_message TEXT,
    generation_id UUID NOT NULL,
    guard_ok BOOLEAN NOT NULL DEFAULT FALSE,
    guard_report JSONB NOT NULL DEFAULT '{}'::JSONB,
    draft_version BIGINT,
    status TEXT NOT NULL,
    error_code TEXT,
    prompt_id TEXT,
    prompt_hash CHAR(64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT workflow_generate_turns_workspace_session_id_key
        UNIQUE (workspace_id, session_id, id),
    CONSTRAINT workflow_generate_turns_session_turn_index_key
        UNIQUE (session_id, turn_index),
    CONSTRAINT workflow_generate_turns_generation_id_key
        UNIQUE (generation_id),
    CONSTRAINT workflow_generate_turns_workspace_session_fk
        FOREIGN KEY (workspace_id, session_id)
        REFERENCES workflow_generate_sessions (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT workflow_generate_turns_turn_index_check CHECK (turn_index > 0),
    CONSTRAINT workflow_generate_turns_user_message_not_blank
        CHECK (length(btrim(user_message)) > 0),
    CONSTRAINT workflow_generate_turns_status_check
        CHECK (status IN ('SUCCEEDED', 'GUARD_REJECTED', 'FAILED')),
    CONSTRAINT workflow_generate_turns_guard_report_object_check
        CHECK (jsonb_typeof(guard_report) = 'object'),
    CONSTRAINT workflow_generate_turns_draft_version_check
        CHECK (draft_version IS NULL OR draft_version > 0),
    CONSTRAINT workflow_generate_turns_prompt_hash_check CHECK (
        prompt_hash IS NULL OR prompt_hash ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT workflow_generate_turns_success_guard_check CHECK (
        (status = 'SUCCEEDED' AND guard_ok = TRUE AND draft_version IS NOT NULL)
        OR (status <> 'SUCCEEDED')
    ),
    CONSTRAINT workflow_generate_turns_failed_guard_check CHECK (
        (status = 'GUARD_REJECTED' AND guard_ok = FALSE)
        OR (status <> 'GUARD_REJECTED')
    )
);

CREATE INDEX workflow_generate_turns_workspace_session_index_idx
    ON workflow_generate_turns (workspace_id, session_id, turn_index ASC, id);

CREATE INDEX workflow_generate_turns_workspace_created_idx
    ON workflow_generate_turns (workspace_id, created_at DESC, id);


-- ##########################################################################
-- Source: 000060_outbound_identity_hard_cutover.up.sql
-- ##########################################################################

-- 000060: outbound identity hard cutover (T4 physical delete).
-- Production semantics: single transactional hard cut. After COMMIT only
-- roll-forward is allowed. Down migration exists only for schema tests and
-- cannot restore deleted secrets, versions, ciphertext, or credential_secret_id.
--
-- Target connections: service_connections whose provider_kind = 'HTTP_OPENAPI'
-- (including soft-deleted rows). No automatic mode inference from legacy auth.

-- ---------------------------------------------------------------------------
-- Schema: Provider / Connection policy columns
-- ---------------------------------------------------------------------------

ALTER TABLE capability_providers
    ADD COLUMN outbound_identity_policy_version BIGINT NOT NULL DEFAULT 1;

ALTER TABLE capability_providers
    ADD CONSTRAINT capability_providers_outbound_identity_policy_version_check
        CHECK (outbound_identity_policy_version > 0);

ALTER TABLE service_connections
    ADD COLUMN outbound_identity JSONB,
    ADD COLUMN outbound_identity_policy_version BIGINT NOT NULL DEFAULT 1,
    ADD COLUMN migration_state TEXT NOT NULL DEFAULT 'NONE',
    ADD COLUMN machine_credential_secret_id UUID;

ALTER TABLE service_connections
    ADD CONSTRAINT service_connections_outbound_identity_object_check
        CHECK (outbound_identity IS NULL OR jsonb_typeof(outbound_identity) = 'object'),
    ADD CONSTRAINT service_connections_outbound_identity_policy_version_check
        CHECK (outbound_identity_policy_version > 0),
    ADD CONSTRAINT service_connections_migration_state_check
        CHECK (migration_state IN ('NONE', 'MIGRATION_REQUIRED')),
    ADD CONSTRAINT service_connections_machine_credential_secret_fk
        FOREIGN KEY (workspace_id, machine_credential_secret_id)
        REFERENCES secrets (workspace_id, id) ON DELETE RESTRICT;

CREATE INDEX service_connections_workspace_migration_state_idx
    ON service_connections (workspace_id, migration_state, updated_at DESC, id)
    WHERE deleted_at IS NULL;

CREATE INDEX service_connections_machine_credential_secret_idx
    ON service_connections (workspace_id, machine_credential_secret_id)
    WHERE machine_credential_secret_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- Schema: runtime instance / affinity metadata (no Token / Vault locator)
-- ---------------------------------------------------------------------------

CREATE TABLE outbound_runtime_instances (
    instance_id TEXT NOT NULL,
    boot_id TEXT NOT NULL,
    workspace_scope TEXT NOT NULL DEFAULT 'cluster',
    internal_address TEXT NOT NULL,
    routing_public_key BYTEA NOT NULL,
    heartbeat_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    draining BOOLEAN NOT NULL DEFAULT FALSE,
    started_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (instance_id, boot_id),
    CONSTRAINT outbound_runtime_instances_instance_id_not_blank
        CHECK (length(btrim(instance_id)) BETWEEN 1 AND 128),
    CONSTRAINT outbound_runtime_instances_boot_id_not_blank
        CHECK (length(btrim(boot_id)) BETWEEN 1 AND 128),
    CONSTRAINT outbound_runtime_instances_internal_address_not_blank
        CHECK (length(btrim(internal_address)) BETWEEN 1 AND 512),
    CONSTRAINT outbound_runtime_instances_routing_public_key_not_empty
        CHECK (octet_length(routing_public_key) > 0),
    CONSTRAINT outbound_runtime_instances_timestamps_check
        CHECK (updated_at >= started_at)
);

CREATE INDEX outbound_runtime_instances_heartbeat_idx
    ON outbound_runtime_instances (heartbeat_at DESC, instance_id, boot_id);

CREATE TABLE outbound_runtime_affinities (
    workspace_id UUID NOT NULL,
    root_scope_type TEXT NOT NULL,
    root_scope_id UUID NOT NULL,
    owner_instance_id TEXT NOT NULL,
    owner_boot_id TEXT NOT NULL,
    root_deadline_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (workspace_id, root_scope_type, root_scope_id),
    CONSTRAINT outbound_runtime_affinities_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT outbound_runtime_affinities_owner_fk
        FOREIGN KEY (owner_instance_id, owner_boot_id)
        REFERENCES outbound_runtime_instances (instance_id, boot_id)
        ON DELETE RESTRICT,
    CONSTRAINT outbound_runtime_affinities_root_scope_type_check
        CHECK (root_scope_type IN (
            'AGENT_RUN',
            'DIRECT_INVOCATION',
            'TOOL_TEST',
            'WORKFLOW_TRIAL',
            'WORKFLOW_EXECUTION',
            'DEBUG_ATTACHMENT'
        )),
    CONSTRAINT outbound_runtime_affinities_timestamps_check
        CHECK (updated_at >= created_at)
);

CREATE INDEX outbound_runtime_affinities_owner_idx
    ON outbound_runtime_affinities (owner_instance_id, owner_boot_id, root_deadline_at);

CREATE INDEX outbound_runtime_affinities_deadline_idx
    ON outbound_runtime_affinities (root_deadline_at, workspace_id);

-- ---------------------------------------------------------------------------
-- Hard-cut data mutation + T4 physical Secret delete (all-or-nothing)
-- ---------------------------------------------------------------------------

DO $$
DECLARE
    blocked_model_refs BIGINT;
    blocked_nontarget_refs BIGINT;
    target_connection_count BIGINT;
    candidate_secret_count BIGINT;
    candidate_version_count BIGINT;
    deleted_secret_count BIGINT;
    deleted_version_count BIGINT;
    remaining_secret_refs BIGINT;
    remaining_version_refs BIGINT;
    remaining_connection_refs BIGINT;
    remaining_model_refs BIGINT;
    ws RECORD;
    audit_connection_count BIGINT;
    audit_secret_count BIGINT;
    audit_version_count BIGINT;
BEGIN
    -- 1) Lock target connections (HTTP_OPENAPI providers, including soft-deleted).
    PERFORM 1
    FROM service_connections AS c
    INNER JOIN capability_providers AS p
        ON p.workspace_id = c.workspace_id AND p.id = c.provider_id
    WHERE p.provider_kind = 'HTTP_OPENAPI'
    ORDER BY c.workspace_id, c.id
    FOR UPDATE OF c;

    -- 2) Determine candidate Secret set from target connections before clearing refs.
    CREATE TEMP TABLE outbound_cutover_candidate_secrets (
        workspace_id UUID NOT NULL,
        secret_id UUID NOT NULL,
        PRIMARY KEY (workspace_id, secret_id)
    ) ON COMMIT DROP;

    INSERT INTO outbound_cutover_candidate_secrets (workspace_id, secret_id)
    SELECT DISTINCT c.workspace_id, c.credential_secret_id
    FROM service_connections AS c
    INNER JOIN capability_providers AS p
        ON p.workspace_id = c.workspace_id AND p.id = c.provider_id
    WHERE p.provider_kind = 'HTTP_OPENAPI'
      AND c.credential_secret_id IS NOT NULL;

    -- Lock candidate secrets and their versions.
    PERFORM 1
    FROM secrets AS s
    INNER JOIN outbound_cutover_candidate_secrets AS cand
        ON cand.workspace_id = s.workspace_id AND cand.secret_id = s.id
    ORDER BY s.workspace_id, s.id
    FOR UPDATE OF s;

    PERFORM 1
    FROM secret_versions AS v
    INNER JOIN outbound_cutover_candidate_secrets AS cand
        ON cand.workspace_id = v.workspace_id AND cand.secret_id = v.secret_id
    ORDER BY v.workspace_id, v.secret_id, v.id
    FOR UPDATE OF v;

    -- Lock referencing rows that could share candidates.
    PERFORM 1
    FROM model_configs AS m
    INNER JOIN outbound_cutover_candidate_secrets AS cand
        ON cand.workspace_id = m.workspace_id AND cand.secret_id = m.credential_secret_id
    ORDER BY m.workspace_id, m.id
    FOR UPDATE OF m;

    PERFORM 1
    FROM service_connections AS c
    INNER JOIN outbound_cutover_candidate_secrets AS cand
        ON cand.workspace_id = c.workspace_id AND cand.secret_id = c.credential_secret_id
    ORDER BY c.workspace_id, c.id
    FOR UPDATE OF c;

    -- 3) Preflight: model_configs or non-target Connection sharing blocks entire cutover.
    SELECT COUNT(*) INTO blocked_model_refs
    FROM model_configs AS m
    INNER JOIN outbound_cutover_candidate_secrets AS cand
        ON cand.workspace_id = m.workspace_id AND cand.secret_id = m.credential_secret_id;

    SELECT COUNT(*) INTO blocked_nontarget_refs
    FROM service_connections AS c
    INNER JOIN outbound_cutover_candidate_secrets AS cand
        ON cand.workspace_id = c.workspace_id AND cand.secret_id = c.credential_secret_id
    INNER JOIN capability_providers AS p
        ON p.workspace_id = c.workspace_id AND p.id = c.provider_id
    WHERE p.provider_kind <> 'HTTP_OPENAPI';

    IF blocked_model_refs > 0 OR blocked_nontarget_refs > 0 THEN
        RAISE EXCEPTION
            'outbound identity hard cutover blocked: shared secret references exist (model_configs=%, non_target_connections=%). Rebind out-of-scope consumers before migration. No mutations applied.',
            blocked_model_refs, blocked_nontarget_refs
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT COUNT(*) INTO target_connection_count
    FROM service_connections AS c
    INNER JOIN capability_providers AS p
        ON p.workspace_id = c.workspace_id AND p.id = c.provider_id
    WHERE p.provider_kind = 'HTTP_OPENAPI';

    SELECT COUNT(*) INTO candidate_secret_count
    FROM outbound_cutover_candidate_secrets;

    SELECT COUNT(*) INTO candidate_version_count
    FROM secret_versions AS v
    INNER JOIN outbound_cutover_candidate_secrets AS cand
        ON cand.workspace_id = v.workspace_id AND cand.secret_id = v.secret_id;

    -- 4) Disable active target connections; mark migration required.
    UPDATE service_connections AS c
    SET status = 'DISABLED',
        migration_state = 'MIGRATION_REQUIRED',
        last_error_code = NULL,
        updated_at = clock_timestamp(),
        lock_version = c.lock_version + 1
    FROM capability_providers AS p
    WHERE p.workspace_id = c.workspace_id
      AND p.id = c.provider_id
      AND p.provider_kind = 'HTTP_OPENAPI'
      AND c.deleted_at IS NULL;

    -- Soft-deleted targets also require migration if restored; do not change status.
    UPDATE service_connections AS c
    SET migration_state = 'MIGRATION_REQUIRED',
        updated_at = clock_timestamp(),
        lock_version = c.lock_version + 1
    FROM capability_providers AS p
    WHERE p.workspace_id = c.workspace_id
      AND p.id = c.provider_id
      AND p.provider_kind = 'HTTP_OPENAPI'
      AND c.deleted_at IS NOT NULL
      AND c.migration_state <> 'MIGRATION_REQUIRED';

    -- 5) Clear credential_secret_id on all target connections (incl. soft-deleted).
    UPDATE service_connections AS c
    SET credential_secret_id = NULL,
        updated_at = clock_timestamp(),
        lock_version = c.lock_version + 1
    FROM capability_providers AS p
    WHERE p.workspace_id = c.workspace_id
      AND p.id = c.provider_id
      AND p.provider_kind = 'HTTP_OPENAPI'
      AND c.credential_secret_id IS NOT NULL;

    -- 6) Re-prove candidate FK refs are zero before delete.
    SELECT COUNT(*) INTO remaining_connection_refs
    FROM service_connections AS c
    INNER JOIN outbound_cutover_candidate_secrets AS cand
        ON cand.workspace_id = c.workspace_id AND cand.secret_id = c.credential_secret_id;

    SELECT COUNT(*) INTO remaining_model_refs
    FROM model_configs AS m
    INNER JOIN outbound_cutover_candidate_secrets AS cand
        ON cand.workspace_id = m.workspace_id AND cand.secret_id = m.credential_secret_id;

    IF remaining_connection_refs <> 0 OR remaining_model_refs <> 0 THEN
        RAISE EXCEPTION
            'outbound identity hard cutover failed: candidate secret references remain after clear (connections=%, model_configs=%). Rolling back.',
            remaining_connection_refs, remaining_model_refs
            USING ERRCODE = 'check_violation';
    END IF;

    -- 7) SYSTEM audit per workspace — aggregate counts only (no Secret IDs/names).
    FOR ws IN
        SELECT workspace_id
        FROM (
            SELECT c.workspace_id
            FROM service_connections AS c
            INNER JOIN capability_providers AS p
                ON p.workspace_id = c.workspace_id AND p.id = c.provider_id
            WHERE p.provider_kind = 'HTTP_OPENAPI'
            UNION
            SELECT cand.workspace_id
            FROM outbound_cutover_candidate_secrets AS cand
        ) AS scoped
        ORDER BY workspace_id
    LOOP
        SELECT COUNT(*) INTO audit_connection_count
        FROM service_connections AS c
        INNER JOIN capability_providers AS p
            ON p.workspace_id = c.workspace_id AND p.id = c.provider_id
        WHERE p.provider_kind = 'HTTP_OPENAPI'
          AND c.workspace_id = ws.workspace_id;

        SELECT COUNT(*) INTO audit_secret_count
        FROM outbound_cutover_candidate_secrets
        WHERE workspace_id = ws.workspace_id;

        SELECT COUNT(*) INTO audit_version_count
        FROM secret_versions AS v
        INNER JOIN outbound_cutover_candidate_secrets AS cand
            ON cand.workspace_id = v.workspace_id AND cand.secret_id = v.secret_id
        WHERE cand.workspace_id = ws.workspace_id;

        INSERT INTO audit_events (
            id, occurred_at, workspace_id, actor_type, actor_id, actor_display,
            action, resource_type, resource_id, result, request_id, trace_id,
            source_ip, user_agent, changes, metadata, payload_object_id, schema_version
        ) VALUES (
            gen_random_uuid(),
            clock_timestamp(),
            ws.workspace_id,
            'SYSTEM',
            NULL,
            'Outbound identity hard cutover',
            'outbound.identity.legacy_secret.deleted',
            'WORKSPACE',
            ws.workspace_id,
            'SUCCESS',
            NULL,
            NULL,
            NULL,
            NULL,
            '{}'::JSONB,
            jsonb_build_object(
                'targetConnectionCount', audit_connection_count,
                'deletedSecretCount', audit_secret_count,
                'deletedSecretVersionCount', audit_version_count,
                'migration', '000060_outbound_identity_hard_cutover',
                'note', 'aggregate counts only; secret identifiers are not recorded'
            ),
            NULL,
            'audit.v1'
        );
    END LOOP;

    -- 8) Physical delete: clear active_version_id, delete all versions (incl. revoked), delete secrets.
    UPDATE secrets AS s
    SET active_version_id = NULL,
        updated_at = clock_timestamp(),
        lock_version = s.lock_version + 1
    FROM outbound_cutover_candidate_secrets AS cand
    WHERE cand.workspace_id = s.workspace_id
      AND cand.secret_id = s.id;

    WITH deleted AS (
        DELETE FROM secret_versions AS v
        USING outbound_cutover_candidate_secrets AS cand
        WHERE cand.workspace_id = v.workspace_id
          AND cand.secret_id = v.secret_id
        RETURNING v.id
    )
    SELECT COUNT(*) INTO deleted_version_count FROM deleted;

    WITH deleted AS (
        DELETE FROM secrets AS s
        USING outbound_cutover_candidate_secrets AS cand
        WHERE cand.workspace_id = s.workspace_id
          AND cand.secret_id = s.id
        RETURNING s.id
    )
    SELECT COUNT(*) INTO deleted_secret_count FROM deleted;

    IF deleted_secret_count <> candidate_secret_count
       OR deleted_version_count <> candidate_version_count THEN
        RAISE EXCEPTION
            'outbound identity hard cutover delete count mismatch (secrets deleted=% expected=%, versions deleted=% expected=%). Rolling back.',
            deleted_secret_count, candidate_secret_count,
            deleted_version_count, candidate_version_count
            USING ERRCODE = 'check_violation';
    END IF;

    -- 9) Post-delete proof: candidates gone from secrets, versions, and both ref tables.
    SELECT COUNT(*) INTO remaining_secret_refs
    FROM secrets AS s
    INNER JOIN outbound_cutover_candidate_secrets AS cand
        ON cand.workspace_id = s.workspace_id AND cand.secret_id = s.id;

    SELECT COUNT(*) INTO remaining_version_refs
    FROM secret_versions AS v
    INNER JOIN outbound_cutover_candidate_secrets AS cand
        ON cand.workspace_id = v.workspace_id AND cand.secret_id = v.secret_id;

    SELECT COUNT(*) INTO remaining_connection_refs
    FROM service_connections AS c
    INNER JOIN outbound_cutover_candidate_secrets AS cand
        ON cand.workspace_id = c.workspace_id AND cand.secret_id = c.credential_secret_id;

    SELECT COUNT(*) INTO remaining_model_refs
    FROM model_configs AS m
    INNER JOIN outbound_cutover_candidate_secrets AS cand
        ON cand.workspace_id = m.workspace_id AND cand.secret_id = m.credential_secret_id;

    IF remaining_secret_refs <> 0
       OR remaining_version_refs <> 0
       OR remaining_connection_refs <> 0
       OR remaining_model_refs <> 0 THEN
        RAISE EXCEPTION
            'outbound identity hard cutover post-delete proof failed (secrets=%, versions=%, connections=%, model_configs=%). Rolling back.',
            remaining_secret_refs, remaining_version_refs,
            remaining_connection_refs, remaining_model_refs
            USING ERRCODE = 'check_violation';
    END IF;

    -- Safe aggregate log (counts only).
    RAISE NOTICE
        'outbound identity hard cutover complete: target_connections=%, secrets_deleted=%, secret_versions_deleted=%',
        target_connection_count, deleted_secret_count, deleted_version_count;
END
$$;


-- ##########################################################################
-- Source: 000061_agent_prompt_preview_retention.up.sql
-- ##########################################################################

-- ZKL-69: create-preview retention, AI_ASSISTED source, and preview StoredObject kinds.
-- Expand-only: existing permanent PromptRun/StoredObject rows are not backfilled.

-- ---------------------------------------------------------------------------
-- agent_prompt_revisions: allow AI_ASSISTED (no backfill of existing rows)
-- ---------------------------------------------------------------------------
ALTER TABLE agent_prompt_revisions
    DROP CONSTRAINT agent_prompt_revisions_source_check;

ALTER TABLE agent_prompt_revisions
    ADD CONSTRAINT agent_prompt_revisions_source_check
        CHECK (source IN ('MANUAL', 'ENHANCED', 'GENERATED', 'IMPORTED', 'AI_ASSISTED'));

-- ---------------------------------------------------------------------------
-- stored_objects: preview kinds, purge tombstone/claim columns, narrow update exceptions
-- ---------------------------------------------------------------------------
ALTER TABLE stored_objects
    DROP CONSTRAINT stored_objects_kind_check;

ALTER TABLE stored_objects
    ADD CONSTRAINT stored_objects_kind_check CHECK (kind IN (
        'OPENAPI_SOURCE', 'PROMPT_RUN_INPUT', 'PROMPT_RUN_OUTPUT', 'MODEL_TURN',
        'CHAT_MESSAGE', 'TOOL_TEST_PAYLOAD', 'TOOL_INVOCATION_PAYLOAD',
        'EXECUTION_CHECKPOINT', 'AUDIT_EVENT_PAYLOAD', 'AUDIT_EXPORT',
        'PROMPT_PREVIEW_INPUT', 'PROMPT_PREVIEW_OUTPUT'
    ));

ALTER TABLE stored_objects
    ADD COLUMN body_purged_at TIMESTAMPTZ,
    ADD COLUMN purge_claim_token UUID,
    ADD COLUMN purge_claim_expires_at TIMESTAMPTZ,
    ADD COLUMN purge_attempts INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN purge_next_attempt_at TIMESTAMPTZ,
    ADD COLUMN purge_last_error_code TEXT;

ALTER TABLE stored_objects
    ADD CONSTRAINT stored_objects_purge_attempts_check CHECK (purge_attempts >= 0),
    ADD CONSTRAINT stored_objects_purge_claim_pair_check CHECK (
        (purge_claim_token IS NULL AND purge_claim_expires_at IS NULL)
        OR (purge_claim_token IS NOT NULL AND purge_claim_expires_at IS NOT NULL)
    ),
    ADD CONSTRAINT stored_objects_purge_error_code_check CHECK (
        purge_last_error_code IS NULL
        OR (
            length(btrim(purge_last_error_code)) > 0
            AND length(purge_last_error_code) <= 128
            AND purge_last_error_code ~ '^[A-Z0-9_]+$'
        )
    ),
    ADD CONSTRAINT stored_objects_body_purged_at_check CHECK (
        body_purged_at IS NULL OR body_purged_at >= created_at
    );

-- Existing permanent kinds remain permanent; preview kinds use a dedicated policy.
ALTER TABLE stored_objects
    DROP CONSTRAINT stored_objects_permanent_content_policy_check;

ALTER TABLE stored_objects
    ADD CONSTRAINT stored_objects_permanent_content_policy_check CHECK (
        kind NOT IN (
            'PROMPT_RUN_INPUT', 'PROMPT_RUN_OUTPUT', 'MODEL_TURN', 'CHAT_MESSAGE',
            'TOOL_TEST_PAYLOAD', 'TOOL_INVOCATION_PAYLOAD', 'EXECUTION_CHECKPOINT'
        )
        OR (
            classification IN ('SENSITIVE', 'RESTRICTED')
            AND retention_mode = 'PERMANENT'
            AND retention_until IS NULL
        )
    ),
    ADD CONSTRAINT stored_objects_preview_content_policy_check CHECK (
        kind NOT IN ('PROMPT_PREVIEW_INPUT', 'PROMPT_PREVIEW_OUTPUT')
        OR (
            classification IN ('SENSITIVE', 'RESTRICTED')
            AND encryption_key_id IS NOT NULL
            AND (
                (retention_mode = 'EXPIRING' AND retention_until IS NOT NULL)
                OR (retention_mode = 'PERMANENT' AND retention_until IS NULL)
            )
        )
    );

CREATE INDEX stored_objects_preview_purge_claim_idx
    ON stored_objects (purge_next_attempt_at, retention_until, id)
    WHERE retention_mode = 'EXPIRING'
      AND body_purged_at IS NULL
      AND kind IN ('PROMPT_PREVIEW_INPUT', 'PROMPT_PREVIEW_OUTPUT');

CREATE OR REPLACE FUNCTION enforce_stored_object_metadata()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    core_changed BOOLEAN;
    purge_only_changed BOOLEAN;
    is_preview BOOLEAN;
BEGIN
    is_preview := OLD.kind IN ('PROMPT_PREVIEW_INPUT', 'PROMPT_PREVIEW_OUTPUT');

    IF TG_OP = 'DELETE' THEN
        IF OLD.retention_mode = 'PERMANENT' THEN
            RAISE EXCEPTION 'permanent stored object metadata cannot be deleted'
                USING ERRCODE = '55000';
        END IF;
        IF is_preview THEN
            RAISE EXCEPTION 'prompt preview stored object metadata cannot be deleted'
                USING ERRCODE = '55000';
        END IF;
        IF OLD.retention_until > clock_timestamp() THEN
            RAISE EXCEPTION 'stored object retention has not expired'
                USING ERRCODE = '55000';
        END IF;
        RETURN OLD;
    END IF;

    -- TG_OP = UPDATE
    core_changed := ROW(
        NEW.id, NEW.workspace_id, NEW.bucket, NEW.object_key, NEW.kind,
        NEW.content_type, NEW.size_bytes, NEW.sha256, NEW.encryption_key_id,
        NEW.classification, NEW.created_by_type, NEW.created_by_id, NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.bucket, OLD.object_key, OLD.kind,
        OLD.content_type, OLD.size_bytes, OLD.sha256, OLD.encryption_key_id,
        OLD.classification, OLD.created_by_type, OLD.created_by_id, OLD.created_at
    );

    IF core_changed THEN
        RAISE EXCEPTION 'stored object metadata is immutable'
            USING ERRCODE = '55000';
    END IF;

    -- One-shot promote: EXPIRING -> PERMANENT for unpurged, unexpired preview kinds.
    IF is_preview
        AND OLD.retention_mode = 'EXPIRING'
        AND OLD.retention_until IS NOT NULL
        AND OLD.retention_until > clock_timestamp()
        AND OLD.body_purged_at IS NULL
        AND NEW.retention_mode = 'PERMANENT'
        AND NEW.retention_until IS NULL
        AND NEW.body_purged_at IS NULL
        AND NEW.purge_claim_token IS NOT DISTINCT FROM OLD.purge_claim_token
        AND NEW.purge_claim_expires_at IS NOT DISTINCT FROM OLD.purge_claim_expires_at
        AND NEW.purge_attempts IS NOT DISTINCT FROM OLD.purge_attempts
        AND NEW.purge_next_attempt_at IS NOT DISTINCT FROM OLD.purge_next_attempt_at
        AND NEW.purge_last_error_code IS NOT DISTINCT FROM OLD.purge_last_error_code
    THEN
        RETURN NEW;
    END IF;

    -- Purge claim / finalize path for preview kinds only.
    IF is_preview AND NEW.retention_mode IS NOT DISTINCT FROM OLD.retention_mode
        AND NEW.retention_until IS NOT DISTINCT FROM OLD.retention_until
    THEN
        -- body_purged_at is write-once and only after expiry (or already expired claim finalize).
        IF NEW.body_purged_at IS DISTINCT FROM OLD.body_purged_at THEN
            IF OLD.body_purged_at IS NOT NULL THEN
                RAISE EXCEPTION 'stored object body_purged_at cannot be changed once set'
                    USING ERRCODE = '55000';
            END IF;
            IF NEW.body_purged_at IS NULL THEN
                RAISE EXCEPTION 'stored object body_purged_at cannot be cleared'
                    USING ERRCODE = '55000';
            END IF;
            IF OLD.retention_mode <> 'EXPIRING'
                OR OLD.retention_until IS NULL
                OR OLD.retention_until > clock_timestamp()
            THEN
                RAISE EXCEPTION 'stored object body cannot be purged before retention expiry'
                    USING ERRCODE = '55000';
            END IF;
            -- Finalize clears claim fields.
            IF NEW.purge_claim_token IS NOT NULL OR NEW.purge_claim_expires_at IS NOT NULL THEN
                RAISE EXCEPTION 'purged stored object cannot retain a purge claim'
                    USING ERRCODE = '55000';
            END IF;
            RETURN NEW;
        END IF;

        -- After body purge, only allow no-op or leave claim fields cleared.
        IF OLD.body_purged_at IS NOT NULL THEN
            IF NEW.purge_claim_token IS DISTINCT FROM OLD.purge_claim_token
                OR NEW.purge_claim_expires_at IS DISTINCT FROM OLD.purge_claim_expires_at
                OR NEW.purge_attempts IS DISTINCT FROM OLD.purge_attempts
                OR NEW.purge_next_attempt_at IS DISTINCT FROM OLD.purge_next_attempt_at
                OR NEW.purge_last_error_code IS DISTINCT FROM OLD.purge_last_error_code
            THEN
                RAISE EXCEPTION 'purged stored object purge metadata is immutable'
                    USING ERRCODE = '55000';
            END IF;
            RETURN NEW;
        END IF;

        -- Claim / retry bookkeeping while body still present.
        IF NEW.purge_attempts < OLD.purge_attempts THEN
            RAISE EXCEPTION 'stored object purge_attempts cannot decrease'
                USING ERRCODE = '55000';
        END IF;
        RETURN NEW;
    END IF;

    RAISE EXCEPTION 'stored object metadata is immutable'
        USING ERRCODE = '55000';
END;
$$;

-- ---------------------------------------------------------------------------
-- prompt_runs: CREATE_PREVIEW + retention tombstone columns
-- ---------------------------------------------------------------------------
ALTER TABLE prompt_runs
    DROP CONSTRAINT prompt_runs_operation_check;

ALTER TABLE prompt_runs
    ADD CONSTRAINT prompt_runs_operation_check
        CHECK (operation_type IN ('ENHANCE', 'GENERATE', 'PREVIEW', 'CREATE_PREVIEW'));

ALTER TABLE prompt_runs
    ADD COLUMN expires_at TIMESTAMPTZ,
    ADD COLUMN promoted_at TIMESTAMPTZ,
    ADD COLUMN content_purged_at TIMESTAMPTZ;

ALTER TABLE prompt_runs
    ADD CONSTRAINT prompt_runs_create_preview_lifecycle_check CHECK (
        (
            operation_type = 'CREATE_PREVIEW'
            AND expires_at IS NOT NULL
            AND expires_at = created_at + INTERVAL '30 days'
            AND (
                (
                    agent_id IS NULL
                    AND accepted_revision_id IS NULL
                    AND promoted_at IS NULL
                )
                OR (
                    agent_id IS NOT NULL
                    AND accepted_revision_id IS NOT NULL
                    AND promoted_at IS NOT NULL
                    AND content_purged_at IS NULL
                )
            )
        )
        OR (
            operation_type <> 'CREATE_PREVIEW'
            AND expires_at IS NULL
            AND promoted_at IS NULL
            AND content_purged_at IS NULL
        )
    ),
    ADD CONSTRAINT prompt_runs_promoted_at_check CHECK (
        promoted_at IS NULL OR promoted_at >= created_at
    ),
    ADD CONSTRAINT prompt_runs_content_purged_at_check CHECK (
        content_purged_at IS NULL OR content_purged_at >= created_at
    );

CREATE OR REPLACE FUNCTION enforce_prompt_run_permanent_content()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'prompt runs are permanently retained and cannot be deleted'
            USING ERRCODE = '55000';
    END IF;

    -- Identity and input evidence are always immutable.
    IF ROW(
        NEW.id, NEW.workspace_id, NEW.operation_type,
        NEW.model_config_id, NEW.model_snapshot, NEW.input_object_id,
        NEW.input_sha256, NEW.input_length, NEW.trace_id, NEW.created_by,
        NEW.created_at, NEW.expires_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.operation_type,
        OLD.model_config_id, OLD.model_snapshot, OLD.input_object_id,
        OLD.input_sha256, OLD.input_length, OLD.trace_id, OLD.created_by,
        OLD.created_at, OLD.expires_at
    ) THEN
        RAISE EXCEPTION 'prompt run input evidence is immutable'
            USING ERRCODE = '55000';
    END IF;

    IF OLD.output_object_id IS NOT NULL AND ROW(
        NEW.output_object_id, NEW.output_sha256, NEW.output_length
    ) IS DISTINCT FROM ROW(
        OLD.output_object_id, OLD.output_sha256, OLD.output_length
    ) THEN
        RAISE EXCEPTION 'prompt run output evidence is immutable'
            USING ERRCODE = '55000';
    END IF;

    -- agent_id is immutable except one CREATE_PREVIEW promotion NULL -> value.
    IF NEW.agent_id IS DISTINCT FROM OLD.agent_id THEN
        IF NOT (
            OLD.operation_type = 'CREATE_PREVIEW'
            AND OLD.agent_id IS NULL
            AND NEW.agent_id IS NOT NULL
            AND OLD.accepted_revision_id IS NULL
            AND NEW.accepted_revision_id IS NOT NULL
            AND OLD.promoted_at IS NULL
            AND NEW.promoted_at IS NOT NULL
            AND OLD.content_purged_at IS NULL
            AND NEW.content_purged_at IS NULL
            AND OLD.status = 'SUCCEEDED'
            AND NEW.status = 'SUCCEEDED'
            AND OLD.expires_at IS NOT NULL
            AND OLD.expires_at > clock_timestamp()
        ) THEN
            RAISE EXCEPTION 'prompt run agent binding is immutable'
                USING ERRCODE = '55000';
        END IF;
    END IF;

    -- accepted_revision_id write-once (existing + CREATE_PREVIEW promotion).
    IF NEW.accepted_revision_id IS DISTINCT FROM OLD.accepted_revision_id THEN
        IF OLD.accepted_revision_id IS NOT NULL OR NEW.accepted_revision_id IS NULL THEN
            RAISE EXCEPTION 'prompt run accepted revision is immutable once set'
                USING ERRCODE = '55000';
        END IF;
    END IF;

    -- promoted_at write-once, only with CREATE_PREVIEW promotion.
    IF NEW.promoted_at IS DISTINCT FROM OLD.promoted_at THEN
        IF NOT (
            OLD.operation_type = 'CREATE_PREVIEW'
            AND OLD.promoted_at IS NULL
            AND NEW.promoted_at IS NOT NULL
            AND OLD.agent_id IS NULL
            AND NEW.agent_id IS NOT NULL
            AND OLD.accepted_revision_id IS NULL
            AND NEW.accepted_revision_id IS NOT NULL
            AND OLD.content_purged_at IS NULL
            AND NEW.content_purged_at IS NULL
        ) THEN
            RAISE EXCEPTION 'prompt run promoted_at is immutable once set'
                USING ERRCODE = '55000';
        END IF;
    END IF;

    -- content_purged_at write-once; only for unpromoted CREATE_PREVIEW.
    IF NEW.content_purged_at IS DISTINCT FROM OLD.content_purged_at THEN
        IF NOT (
            OLD.operation_type = 'CREATE_PREVIEW'
            AND OLD.content_purged_at IS NULL
            AND NEW.content_purged_at IS NOT NULL
            AND OLD.promoted_at IS NULL
            AND OLD.agent_id IS NULL
            AND OLD.accepted_revision_id IS NULL
        ) THEN
            RAISE EXCEPTION 'prompt run content_purged_at is invalid or immutable'
                USING ERRCODE = '55000';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;

