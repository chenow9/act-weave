-- Reverse IC-02 expand-only aap file domain tables and kind CHECK.
-- Production rollback prefers files.enabled=false; this exists for clean local
-- / test round-trips only.

DROP TABLE IF EXISTS aap_workspace_file_processors;
DROP TABLE IF EXISTS aap_file_download_tokens;
DROP TABLE IF EXISTS aap_file_processing_jobs;
DROP TABLE IF EXISTS aap_file_artifacts;
DROP TABLE IF EXISTS aap_files;

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
