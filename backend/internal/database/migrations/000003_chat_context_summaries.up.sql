-- ZKL-74 / IC-11: chat context summary metadata + CHAT_CONTEXT_SUMMARY object kind.
-- Rolling generation remains gate-closed; this migration is expand-only storage.

ALTER TABLE stored_objects
    DROP CONSTRAINT IF EXISTS stored_objects_kind_check;

ALTER TABLE stored_objects
    ADD CONSTRAINT stored_objects_kind_check CHECK (kind IN (
        'OPENAPI_SOURCE', 'PROMPT_RUN_INPUT', 'PROMPT_RUN_OUTPUT', 'MODEL_TURN',
        'CHAT_MESSAGE', 'TOOL_TEST_PAYLOAD', 'TOOL_INVOCATION_PAYLOAD',
        'EXECUTION_CHECKPOINT', 'AUDIT_EVENT_PAYLOAD', 'AUDIT_EXPORT',
        'PROMPT_PREVIEW_INPUT', 'PROMPT_PREVIEW_OUTPUT',
        'CHAT_CONTEXT_SUMMARY'
    ));

CREATE TABLE chat_context_summaries (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    session_id UUID NOT NULL,
    status TEXT NOT NULL DEFAULT 'BUILDING',
    owner_token UUID,
    lease_expires_at TIMESTAMPTZ,
    coverage_start_message_id UUID,
    coverage_end_message_id UUID,
    source_message_count INTEGER NOT NULL DEFAULT 0,
    source_digest CHAR(64) NOT NULL,
    parent_summary_id UUID,
    parent_summary_digest CHAR(64),
    policy_fingerprint CHAR(64) NOT NULL,
    summarizer_snapshot JSONB NOT NULL DEFAULT '{}'::JSONB,
    prompt_template_version TEXT NOT NULL DEFAULT '',
    prompt_template_hash CHAR(64) NOT NULL,
    content_object_id UUID,
    content_sha256 CHAR(64),
    content_length BIGINT,
    estimated_input_tokens BIGINT NOT NULL DEFAULT 0,
    estimated_output_tokens BIGINT NOT NULL DEFAULT 0,
    estimator_version TEXT NOT NULL DEFAULT '',
    attempt_count INTEGER NOT NULL DEFAULT 0,
    next_retry_at TIMESTAMPTZ,
    failure_code TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    ready_at TIMESTAMPTZ,
    CONSTRAINT chat_context_summaries_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT chat_context_summaries_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT chat_context_summaries_workspace_session_fk
        FOREIGN KEY (workspace_id, session_id)
        REFERENCES chat_sessions (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT chat_context_summaries_status_check
        CHECK (status IN ('BUILDING', 'READY', 'FAILED')),
    CONSTRAINT chat_context_summaries_source_digest_check
        CHECK (source_digest ~ '^[0-9a-f]{64}$'),
    CONSTRAINT chat_context_summaries_policy_fp_check
        CHECK (policy_fingerprint ~ '^[0-9a-f]{64}$'),
    CONSTRAINT chat_context_summaries_template_hash_check
        CHECK (prompt_template_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT chat_context_summaries_parent_digest_check
        CHECK (parent_summary_digest IS NULL OR parent_summary_digest ~ '^[0-9a-f]{64}$'),
    CONSTRAINT chat_context_summaries_content_sha_check
        CHECK (content_sha256 IS NULL OR content_sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT chat_context_summaries_summarizer_object_check
        CHECK (jsonb_typeof(summarizer_snapshot) = 'object'),
    CONSTRAINT chat_context_summaries_source_count_check
        CHECK (source_message_count >= 0),
    CONSTRAINT chat_context_summaries_tokens_check
        CHECK (estimated_input_tokens >= 0 AND estimated_output_tokens >= 0 AND attempt_count >= 0),
    CONSTRAINT chat_context_summaries_ready_state_check CHECK (
        (status = 'BUILDING' AND ready_at IS NULL AND content_object_id IS NULL AND failure_code IS NULL)
        OR (status = 'READY' AND ready_at IS NOT NULL AND content_object_id IS NOT NULL
            AND content_sha256 IS NOT NULL AND failure_code IS NULL)
        OR (status = 'FAILED' AND failure_code IS NOT NULL AND content_object_id IS NULL)
    ),
    CONSTRAINT chat_context_summaries_lease_pair_check
        CHECK ((owner_token IS NULL) = (lease_expires_at IS NULL)),
    CONSTRAINT chat_context_summaries_idempotency_key UNIQUE (
        workspace_id, session_id, coverage_end_message_id, source_digest,
        policy_fingerprint, prompt_template_hash
    )
);

CREATE INDEX chat_context_summaries_workspace_session_created_idx
    ON chat_context_summaries (workspace_id, session_id, created_at DESC, id);
CREATE INDEX chat_context_summaries_building_lease_idx
    ON chat_context_summaries (lease_expires_at, id)
    WHERE status = 'BUILDING';

CREATE FUNCTION enforce_chat_context_summary_ready_immutable()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'chat context summaries are permanently retained'
            USING ERRCODE = '55000';
    END IF;
    IF OLD.status = 'READY' THEN
        RAISE EXCEPTION 'READY chat context summaries are immutable'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER chat_context_summaries_ready_immutable
BEFORE UPDATE OR DELETE ON chat_context_summaries
FOR EACH ROW EXECUTE FUNCTION enforce_chat_context_summary_ready_immutable();
