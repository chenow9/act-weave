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
