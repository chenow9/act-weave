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
