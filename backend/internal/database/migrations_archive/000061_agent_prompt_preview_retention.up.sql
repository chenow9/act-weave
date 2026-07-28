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
