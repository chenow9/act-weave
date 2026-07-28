-- ZKL-69 down: fail closed when any preview retention data exists.
-- Application rollback keeps this schema once CREATE_PREVIEW or preview objects exist.

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM stored_objects
        WHERE kind IN ('PROMPT_PREVIEW_INPUT', 'PROMPT_PREVIEW_OUTPUT')
    ) THEN
        RAISE EXCEPTION
            'cannot down-migrate 000061: PROMPT_PREVIEW_* stored objects exist (expand-only after use)'
            USING ERRCODE = '55000';
    END IF;

    IF EXISTS (
        SELECT 1 FROM prompt_runs WHERE operation_type = 'CREATE_PREVIEW'
    ) THEN
        RAISE EXCEPTION
            'cannot down-migrate 000061: CREATE_PREVIEW prompt runs exist (expand-only after use)'
            USING ERRCODE = '55000';
    END IF;

    IF EXISTS (
        SELECT 1 FROM agent_prompt_revisions WHERE source = 'AI_ASSISTED'
    ) THEN
        RAISE EXCEPTION
            'cannot down-migrate 000061: AI_ASSISTED prompt revisions exist (expand-only after use)'
            USING ERRCODE = '55000';
    END IF;

    IF EXISTS (
        SELECT 1 FROM prompt_runs
        WHERE expires_at IS NOT NULL
           OR promoted_at IS NOT NULL
           OR content_purged_at IS NOT NULL
    ) THEN
        RAISE EXCEPTION
            'cannot down-migrate 000061: prompt_runs retention columns are populated'
            USING ERRCODE = '55000';
    END IF;

    IF EXISTS (
        SELECT 1 FROM stored_objects
        WHERE body_purged_at IS NOT NULL
           OR purge_claim_token IS NOT NULL
           OR purge_attempts <> 0
           OR purge_next_attempt_at IS NOT NULL
           OR purge_last_error_code IS NOT NULL
    ) THEN
        RAISE EXCEPTION
            'cannot down-migrate 000061: stored_objects purge columns are populated'
            USING ERRCODE = '55000';
    END IF;
END $$;

CREATE OR REPLACE FUNCTION enforce_prompt_run_permanent_content()
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

ALTER TABLE prompt_runs
    DROP CONSTRAINT IF EXISTS prompt_runs_content_purged_at_check,
    DROP CONSTRAINT IF EXISTS prompt_runs_promoted_at_check,
    DROP CONSTRAINT IF EXISTS prompt_runs_create_preview_lifecycle_check,
    DROP CONSTRAINT IF EXISTS prompt_runs_operation_check;

ALTER TABLE prompt_runs
    DROP COLUMN IF EXISTS content_purged_at,
    DROP COLUMN IF EXISTS promoted_at,
    DROP COLUMN IF EXISTS expires_at;

ALTER TABLE prompt_runs
    ADD CONSTRAINT prompt_runs_operation_check
        CHECK (operation_type IN ('ENHANCE', 'GENERATE', 'PREVIEW'));

CREATE OR REPLACE FUNCTION enforce_stored_object_metadata()
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

DROP INDEX IF EXISTS stored_objects_preview_purge_claim_idx;

ALTER TABLE stored_objects
    DROP CONSTRAINT IF EXISTS stored_objects_preview_content_policy_check,
    DROP CONSTRAINT IF EXISTS stored_objects_permanent_content_policy_check,
    DROP CONSTRAINT IF EXISTS stored_objects_body_purged_at_check,
    DROP CONSTRAINT IF EXISTS stored_objects_purge_error_code_check,
    DROP CONSTRAINT IF EXISTS stored_objects_purge_claim_pair_check,
    DROP CONSTRAINT IF EXISTS stored_objects_purge_attempts_check,
    DROP CONSTRAINT IF EXISTS stored_objects_kind_check;

ALTER TABLE stored_objects
    DROP COLUMN IF EXISTS purge_last_error_code,
    DROP COLUMN IF EXISTS purge_next_attempt_at,
    DROP COLUMN IF EXISTS purge_attempts,
    DROP COLUMN IF EXISTS purge_claim_expires_at,
    DROP COLUMN IF EXISTS purge_claim_token,
    DROP COLUMN IF EXISTS body_purged_at;

ALTER TABLE stored_objects
    ADD CONSTRAINT stored_objects_kind_check CHECK (kind IN (
        'OPENAPI_SOURCE', 'PROMPT_RUN_INPUT', 'PROMPT_RUN_OUTPUT', 'MODEL_TURN',
        'CHAT_MESSAGE', 'TOOL_TEST_PAYLOAD', 'TOOL_INVOCATION_PAYLOAD',
        'EXECUTION_CHECKPOINT', 'AUDIT_EVENT_PAYLOAD', 'AUDIT_EXPORT'
    )),
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
    );

ALTER TABLE agent_prompt_revisions
    DROP CONSTRAINT IF EXISTS agent_prompt_revisions_source_check;

ALTER TABLE agent_prompt_revisions
    ADD CONSTRAINT agent_prompt_revisions_source_check
        CHECK (source IN ('MANUAL', 'ENHANCED', 'GENERATED', 'IMPORTED'));
