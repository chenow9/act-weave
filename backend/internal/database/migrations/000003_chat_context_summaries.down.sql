-- Reverse 000003. Production rollback must not rely on destructive down.

DROP TRIGGER IF EXISTS chat_context_summaries_ready_immutable ON chat_context_summaries;
DROP FUNCTION IF EXISTS enforce_chat_context_summary_ready_immutable();
DROP TABLE IF EXISTS chat_context_summaries;

ALTER TABLE stored_objects
    DROP CONSTRAINT IF EXISTS stored_objects_kind_check;

ALTER TABLE stored_objects
    ADD CONSTRAINT stored_objects_kind_check CHECK (kind IN (
        'OPENAPI_SOURCE', 'PROMPT_RUN_INPUT', 'PROMPT_RUN_OUTPUT', 'MODEL_TURN',
        'CHAT_MESSAGE', 'TOOL_TEST_PAYLOAD', 'TOOL_INVOCATION_PAYLOAD',
        'EXECUTION_CHECKPOINT', 'AUDIT_EVENT_PAYLOAD', 'AUDIT_EXPORT',
        'PROMPT_PREVIEW_INPUT', 'PROMPT_PREVIEW_OUTPUT'
    ));
