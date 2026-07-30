-- ZKL-81 / IC-01: LLM compact expand migration.
-- Expand-only: generation_method, workspace-scoped FKs, READY LLM lookup index,
-- and permanent sensitive policy for CHAT_CONTEXT_SUMMARY. No runtime enablement.

-- ---------------------------------------------------------------------------
-- generation_method: legacy rows stay LEGACY_EXTRACTIVE; new LLM rows write LLM.
-- ---------------------------------------------------------------------------
ALTER TABLE chat_context_summaries
    ADD COLUMN IF NOT EXISTS generation_method TEXT NOT NULL DEFAULT 'LEGACY_EXTRACTIVE';

ALTER TABLE chat_context_summaries
    DROP CONSTRAINT IF EXISTS chat_context_summaries_generation_method_check;

ALTER TABLE chat_context_summaries
    ADD CONSTRAINT chat_context_summaries_generation_method_check
        CHECK (generation_method IN ('LEGACY_EXTRACTIVE', 'LLM'));

-- ---------------------------------------------------------------------------
-- Workspace-scoped FKs (NOT VALID so existing legacy rows are not blocked;
-- new writes are still enforced by PostgreSQL).
-- ---------------------------------------------------------------------------
ALTER TABLE chat_context_summaries
    DROP CONSTRAINT IF EXISTS chat_context_summaries_content_object_fk;
ALTER TABLE chat_context_summaries
    ADD CONSTRAINT chat_context_summaries_content_object_fk
        FOREIGN KEY (workspace_id, content_object_id)
        REFERENCES stored_objects (workspace_id, id)
        ON DELETE RESTRICT
        NOT VALID;

ALTER TABLE chat_context_summaries
    DROP CONSTRAINT IF EXISTS chat_context_summaries_parent_summary_fk;
ALTER TABLE chat_context_summaries
    ADD CONSTRAINT chat_context_summaries_parent_summary_fk
        FOREIGN KEY (workspace_id, parent_summary_id)
        REFERENCES chat_context_summaries (workspace_id, id)
        ON DELETE RESTRICT
        NOT VALID;

ALTER TABLE chat_context_summaries
    DROP CONSTRAINT IF EXISTS chat_context_summaries_coverage_start_fk;
ALTER TABLE chat_context_summaries
    ADD CONSTRAINT chat_context_summaries_coverage_start_fk
        FOREIGN KEY (workspace_id, session_id, coverage_start_message_id)
        REFERENCES chat_messages (workspace_id, session_id, id)
        ON DELETE RESTRICT
        NOT VALID;

ALTER TABLE chat_context_summaries
    DROP CONSTRAINT IF EXISTS chat_context_summaries_coverage_end_fk;
ALTER TABLE chat_context_summaries
    ADD CONSTRAINT chat_context_summaries_coverage_end_fk
        FOREIGN KEY (workspace_id, session_id, coverage_end_message_id)
        REFERENCES chat_messages (workspace_id, session_id, id)
        ON DELETE RESTRICT
        NOT VALID;

-- ---------------------------------------------------------------------------
-- Latest READY LLM lookup (partial index).
-- ---------------------------------------------------------------------------
CREATE INDEX IF NOT EXISTS chat_context_summaries_ready_llm_lookup_idx
    ON chat_context_summaries (
        workspace_id, session_id, coverage_end_message_id, ready_at DESC, id
    )
    WHERE status = 'READY' AND generation_method = 'LLM';

-- ---------------------------------------------------------------------------
-- Force CHAT_CONTEXT_SUMMARY permanent + sensitive/restricted at DB boundary.
-- ---------------------------------------------------------------------------
ALTER TABLE stored_objects
    DROP CONSTRAINT IF EXISTS stored_objects_permanent_content_policy_check;

ALTER TABLE stored_objects
    ADD CONSTRAINT stored_objects_permanent_content_policy_check CHECK (
        kind NOT IN (
            'PROMPT_RUN_INPUT', 'PROMPT_RUN_OUTPUT', 'MODEL_TURN', 'CHAT_MESSAGE',
            'TOOL_TEST_PAYLOAD', 'TOOL_INVOCATION_PAYLOAD', 'EXECUTION_CHECKPOINT',
            'CHAT_CONTEXT_SUMMARY'
        )
        OR (
            classification IN ('SENSITIVE', 'RESTRICTED')
            AND retention_mode = 'PERMANENT'
            AND retention_until IS NULL
        )
    );
