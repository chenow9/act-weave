ALTER TABLE stored_objects
    ADD CONSTRAINT stored_objects_classification_encryption_check CHECK (
        (classification IN ('SENSITIVE', 'RESTRICTED') AND encryption_key_id IS NOT NULL)
        OR (classification IN ('PUBLIC', 'INTERNAL') AND encryption_key_id IS NULL)
    ),
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
    ADD CONSTRAINT stored_objects_openapi_retention_check CHECK (
        kind <> 'OPENAPI_SOURCE'
        OR (
            classification <> 'PUBLIC'
            AND retention_mode = 'PERMANENT'
            AND retention_until IS NULL
        )
    ),
    ADD CONSTRAINT stored_objects_audit_export_retention_check CHECK (
        kind <> 'AUDIT_EXPORT'
        OR (retention_mode = 'EXPIRING' AND retention_until IS NOT NULL)
    );
