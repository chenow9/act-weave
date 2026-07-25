CREATE TABLE stored_objects (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    bucket TEXT NOT NULL,
    object_key TEXT NOT NULL,
    kind TEXT NOT NULL,
    content_type TEXT NOT NULL,
    size_bytes BIGINT NOT NULL,
    sha256 CHAR(64) NOT NULL,
    encryption_key_id TEXT,
    classification TEXT NOT NULL,
    retention_mode TEXT NOT NULL,
    retention_until TIMESTAMPTZ,
    created_by_type TEXT NOT NULL,
    created_by_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT stored_objects_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT stored_objects_bucket_key_key UNIQUE (bucket, object_key),
    CONSTRAINT stored_objects_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT stored_objects_bucket_not_blank
        CHECK (length(btrim(bucket)) BETWEEN 3 AND 63),
    CONSTRAINT stored_objects_key_not_blank
        CHECK (length(btrim(object_key)) BETWEEN 1 AND 1024),
    CONSTRAINT stored_objects_kind_check CHECK (kind IN (
        'OPENAPI_SOURCE', 'PROMPT_RUN_INPUT', 'PROMPT_RUN_OUTPUT', 'MODEL_TURN',
        'CHAT_MESSAGE', 'TOOL_TEST_PAYLOAD', 'TOOL_INVOCATION_PAYLOAD',
        'EXECUTION_CHECKPOINT', 'AUDIT_EVENT_PAYLOAD', 'AUDIT_EXPORT'
    )),
    CONSTRAINT stored_objects_content_type_not_blank
        CHECK (length(btrim(content_type)) BETWEEN 1 AND 255),
    CONSTRAINT stored_objects_size_check CHECK (size_bytes >= 0),
    CONSTRAINT stored_objects_sha256_check CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT stored_objects_encryption_key_not_blank
        CHECK (encryption_key_id IS NULL OR length(btrim(encryption_key_id)) > 0),
    CONSTRAINT stored_objects_classification_check
        CHECK (classification IN ('PUBLIC', 'INTERNAL', 'SENSITIVE', 'RESTRICTED')),
    CONSTRAINT stored_objects_retention_mode_check
        CHECK (retention_mode IN ('PERMANENT', 'EXPIRING')),
    CONSTRAINT stored_objects_retention_check CHECK (
        (retention_mode = 'PERMANENT' AND retention_until IS NULL)
        OR (retention_mode = 'EXPIRING' AND retention_until > created_at)
    ),
    CONSTRAINT stored_objects_created_by_type_check
        CHECK (created_by_type IN ('USER', 'SERVICE_PRINCIPAL', 'SYSTEM'))
);

CREATE INDEX stored_objects_workspace_kind_created_idx
    ON stored_objects (workspace_id, kind, created_at DESC, id);
CREATE INDEX stored_objects_workspace_classification_created_idx
    ON stored_objects (workspace_id, classification, created_at DESC, id);
CREATE INDEX stored_objects_expiring_retention_idx
    ON stored_objects (retention_until, id)
    WHERE retention_mode = 'EXPIRING';

CREATE FUNCTION enforce_stored_object_metadata()
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

CREATE TRIGGER stored_objects_metadata_guard
BEFORE UPDATE OR DELETE ON stored_objects
FOR EACH ROW EXECUTE FUNCTION enforce_stored_object_metadata();
