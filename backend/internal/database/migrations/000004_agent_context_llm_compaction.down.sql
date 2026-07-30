-- ZKL-81 / IC-01 down: reverse expand-only objects.
-- Production rollback does not run destructive down; this exists for clean local
-- / test round-trips only.

ALTER TABLE stored_objects
    DROP CONSTRAINT IF EXISTS stored_objects_permanent_content_policy_check;

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
    );

DROP INDEX IF EXISTS chat_context_summaries_ready_llm_lookup_idx;

ALTER TABLE chat_context_summaries
    DROP CONSTRAINT IF EXISTS chat_context_summaries_coverage_end_fk;
ALTER TABLE chat_context_summaries
    DROP CONSTRAINT IF EXISTS chat_context_summaries_coverage_start_fk;
ALTER TABLE chat_context_summaries
    DROP CONSTRAINT IF EXISTS chat_context_summaries_parent_summary_fk;
ALTER TABLE chat_context_summaries
    DROP CONSTRAINT IF EXISTS chat_context_summaries_content_object_fk;

ALTER TABLE chat_context_summaries
    DROP CONSTRAINT IF EXISTS chat_context_summaries_generation_method_check;

ALTER TABLE chat_context_summaries
    DROP COLUMN IF EXISTS generation_method;
