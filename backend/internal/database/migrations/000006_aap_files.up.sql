-- AAP file domain tables + stored_objects kind expand (IC-02 / KD-1).
-- Expand-only: aap_files*, download tokens, workspace processors, kind CHECK.
-- No HTTP, no files.enabled default change, no destructive rewrite.

-- ---------------------------------------------------------------------------
-- stored_objects: allow permanent AAP file kinds (not forced PERMANENT).
-- ---------------------------------------------------------------------------
ALTER TABLE stored_objects
    DROP CONSTRAINT IF EXISTS stored_objects_kind_check;

ALTER TABLE stored_objects
    ADD CONSTRAINT stored_objects_kind_check CHECK (kind IN (
        'OPENAPI_SOURCE', 'PROMPT_RUN_INPUT', 'PROMPT_RUN_OUTPUT', 'MODEL_TURN',
        'CHAT_MESSAGE', 'TOOL_TEST_PAYLOAD', 'TOOL_INVOCATION_PAYLOAD',
        'EXECUTION_CHECKPOINT', 'AUDIT_EVENT_PAYLOAD', 'AUDIT_EXPORT',
        'PROMPT_PREVIEW_INPUT', 'PROMPT_PREVIEW_OUTPUT',
        'CHAT_CONTEXT_SUMMARY',
        'AAP_FILE', 'AAP_FILE_DERIVED'
    ));

-- ---------------------------------------------------------------------------
-- aap_files: domain fact source for upload intent / lifecycle / ownership.
-- ---------------------------------------------------------------------------
CREATE TABLE aap_files (
    id                       UUID PRIMARY KEY,
    workspace_id             UUID NOT NULL,
    agent_id                 UUID NOT NULL,
    actor_type               TEXT NOT NULL,
    actor_id                 UUID NOT NULL,
    client_id                UUID NOT NULL,
    subject_type             TEXT,
    subject_id               UUID,
    ownership_mode           TEXT NOT NULL,
    ownership_policy_version BIGINT NOT NULL DEFAULT 1,
    status                   TEXT NOT NULL,
    filename                 TEXT,
    declared_media_type      TEXT NOT NULL,
    detected_media_type      TEXT,
    size_bytes               BIGINT NOT NULL,
    sha256                   CHAR(64),
    staging_bucket           TEXT NOT NULL,
    staging_object_key       TEXT,
    staging_expires_at       TIMESTAMPTZ NOT NULL,
    staging_deleted_at       TIMESTAMPTZ,
    stored_object_id         UUID,
    purpose                  TEXT NOT NULL DEFAULT 'GENERAL',
    error_code               TEXT,
    error_message            TEXT,
    processing_version       BIGINT NOT NULL DEFAULT 1,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    ready_at                 TIMESTAMPTZ,
    retention_until          TIMESTAMPTZ,
    CONSTRAINT aap_files_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT aap_files_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT aap_files_size_check CHECK (size_bytes > 0),
    CONSTRAINT aap_files_status_check CHECK (status IN (
        'PENDING_UPLOAD', 'UPLOADED', 'PROCESSING', 'READY', 'FAILED', 'EXPIRED'
    )),
    CONSTRAINT aap_files_ownership_mode_check
        CHECK (ownership_mode IN ('SUBJECT_OWNED', 'POLICY_SHARED')),
    CONSTRAINT aap_files_actor_type_check
        CHECK (actor_type = 'SERVICE_PRINCIPAL'),
    CONSTRAINT aap_files_purpose_check
        CHECK (purpose IN ('GENERAL', 'VISION', 'DOCUMENT', 'TOOL_INPUT')),
    CONSTRAINT aap_files_sha256_check
        CHECK (sha256 IS NULL OR sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT aap_files_subject_pair_check CHECK (
        (subject_type IS NULL AND subject_id IS NULL)
        OR (subject_type IS NOT NULL AND subject_id IS NOT NULL)
    ),
    CONSTRAINT aap_files_subject_type_check CHECK (
        subject_type IS NULL OR subject_type = 'EXTERNAL_SUBJECT'
    ),
    CONSTRAINT aap_files_policy_version_check CHECK (ownership_policy_version >= 1),
    CONSTRAINT aap_files_processing_version_check CHECK (processing_version >= 1),
    CONSTRAINT aap_files_staging_bucket_not_blank
        CHECK (length(btrim(staging_bucket)) BETWEEN 3 AND 63)
);

CREATE INDEX aap_files_staging_gc_idx
    ON aap_files (staging_expires_at)
    WHERE staging_object_key IS NOT NULL AND staging_deleted_at IS NULL;

CREATE INDEX aap_files_owner_idx
    ON aap_files (workspace_id, agent_id, client_id, created_at DESC);

-- ---------------------------------------------------------------------------
-- aap_file_artifacts: derived pipeline products (KD-8).
-- ---------------------------------------------------------------------------
CREATE TABLE aap_file_artifacts (
    id               UUID PRIMARY KEY,
    workspace_id     UUID NOT NULL,
    file_id          UUID NOT NULL,
    kind             TEXT NOT NULL,
    media_type       TEXT NOT NULL,
    stored_object_id UUID NOT NULL,
    processor_id     TEXT NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT aap_file_artifacts_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT aap_file_artifacts_file_fk
        FOREIGN KEY (workspace_id, file_id)
        REFERENCES aap_files (workspace_id, id)
);

CREATE INDEX aap_file_artifacts_file_idx
    ON aap_file_artifacts (workspace_id, file_id, created_at DESC);

-- ---------------------------------------------------------------------------
-- aap_file_processing_jobs: one row per (file, stage); retries UPDATE same row.
-- ---------------------------------------------------------------------------
CREATE TABLE aap_file_processing_jobs (
    id               UUID PRIMARY KEY,
    workspace_id     UUID NOT NULL,
    file_id          UUID NOT NULL,
    stage            TEXT NOT NULL,
    status           TEXT NOT NULL,
    attempt          INT NOT NULL DEFAULT 0,
    claim_token      UUID,
    claim_expires_at TIMESTAMPTZ,
    available_at     TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    deadline_at      TIMESTAMPTZ,
    delivery_id      UUID,
    last_error_code  TEXT,
    result           JSONB NOT NULL DEFAULT '{}'::JSONB,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT aap_file_processing_jobs_workspace_file_stage_key
        UNIQUE (workspace_id, file_id, stage),
    CONSTRAINT aap_file_processing_jobs_file_fk
        FOREIGN KEY (workspace_id, file_id)
        REFERENCES aap_files (workspace_id, id),
    CONSTRAINT aap_file_processing_jobs_status_check CHECK (status IN (
        'PENDING', 'RUNNING', 'DELIVERED', 'SUCCEEDED', 'FAILED', 'SKIPPED', 'TIMED_OUT'
    )),
    CONSTRAINT aap_file_processing_jobs_attempt_check CHECK (attempt >= 0),
    CONSTRAINT aap_file_processing_jobs_stage_not_blank
        CHECK (length(btrim(stage)) > 0),
    CONSTRAINT aap_file_processing_jobs_result_object_check
        CHECK (jsonb_typeof(result) = 'object')
);

CREATE INDEX aap_file_jobs_claim_idx
    ON aap_file_processing_jobs (status, available_at)
    WHERE status IN ('PENDING', 'DELIVERED');

-- ---------------------------------------------------------------------------
-- aap_file_download_tokens: opaque DB tokens (KD-13), not JWTs.
-- ---------------------------------------------------------------------------
CREATE TABLE aap_file_download_tokens (
    id            UUID PRIMARY KEY,
    workspace_id  UUID NOT NULL,
    file_id       UUID NOT NULL,
    purpose       TEXT NOT NULL,
    jti           UUID NOT NULL,
    single_use    BOOLEAN NOT NULL DEFAULT false,
    consumed_at   TIMESTAMPTZ,
    max_bytes     BIGINT,
    expires_at    TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    created_by    TEXT NOT NULL,
    CONSTRAINT aap_file_download_tokens_jti_key UNIQUE (jti),
    CONSTRAINT aap_file_download_tokens_file_fk
        FOREIGN KEY (workspace_id, file_id)
        REFERENCES aap_files (workspace_id, id),
    CONSTRAINT aap_file_download_tokens_purpose_check CHECK (purpose IN (
        'client_content', 'tool_invoke', 'processor_delivery'
    )),
    CONSTRAINT aap_file_download_tokens_created_by_not_blank
        CHECK (length(btrim(created_by)) > 0),
    CONSTRAINT aap_file_download_tokens_max_bytes_check
        CHECK (max_bytes IS NULL OR max_bytes > 0)
);

CREATE INDEX aap_file_tokens_expiry_idx
    ON aap_file_download_tokens (expires_at)
    WHERE consumed_at IS NULL;

-- ---------------------------------------------------------------------------
-- aap_workspace_file_processors: workspace webhook processor config (KD-7).
-- ---------------------------------------------------------------------------
CREATE TABLE aap_workspace_file_processors (
    id            UUID PRIMARY KEY,
    workspace_id  UUID NOT NULL,
    processor_id  TEXT NOT NULL,
    type          TEXT NOT NULL,
    url           TEXT NOT NULL,
    secret_ref    TEXT NOT NULL,
    timeout_ms    INT NOT NULL DEFAULT 10000,
    required      BOOLEAN NOT NULL DEFAULT false,
    enabled       BOOLEAN NOT NULL DEFAULT true,
    events        TEXT[] NOT NULL DEFAULT ARRAY['file.uploaded']::TEXT[],
    created_at    TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT aap_workspace_file_processors_workspace_processor_key
        UNIQUE (workspace_id, processor_id),
    CONSTRAINT aap_workspace_file_processors_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT aap_workspace_file_processors_type_check
        CHECK (type = 'webhook'),
    CONSTRAINT aap_workspace_file_processors_timeout_check
        CHECK (timeout_ms > 0),
    CONSTRAINT aap_workspace_file_processors_processor_id_not_blank
        CHECK (length(btrim(processor_id)) > 0),
    CONSTRAINT aap_workspace_file_processors_url_not_blank
        CHECK (length(btrim(url)) > 0),
    CONSTRAINT aap_workspace_file_processors_secret_ref_not_blank
        CHECK (length(btrim(secret_ref)) > 0)
);
