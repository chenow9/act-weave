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
