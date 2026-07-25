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
