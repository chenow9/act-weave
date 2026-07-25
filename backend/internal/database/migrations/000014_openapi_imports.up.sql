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
