ALTER TABLE tool_tests
    ALTER COLUMN raw_object_id SET NOT NULL,
    ADD CONSTRAINT tool_tests_raw_object_fk
        FOREIGN KEY (workspace_id, raw_object_id)
        REFERENCES stored_objects (workspace_id, id) ON DELETE RESTRICT;

ALTER TABLE tool_invocations
    ADD CONSTRAINT tool_invocations_raw_object_fk
        FOREIGN KEY (workspace_id, raw_object_id)
        REFERENCES stored_objects (workspace_id, id) ON DELETE RESTRICT,
    ADD CONSTRAINT tool_invocations_terminal_raw_object_check CHECK (
        (status IN ('PENDING', 'RUNNING') AND raw_object_id IS NULL)
        OR (status IN ('SUCCEEDED', 'FAILED', 'CANCELLED') AND raw_object_id IS NOT NULL)
    );

CREATE FUNCTION enforce_permanent_tool_payload_reference()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    expected_kind TEXT;
BEGIN
    IF TG_TABLE_NAME = 'tool_tests' THEN
        expected_kind := 'TOOL_TEST_PAYLOAD';
    ELSE
        expected_kind := 'TOOL_INVOCATION_PAYLOAD';
    END IF;
    IF NEW.raw_object_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM stored_objects
        WHERE workspace_id = NEW.workspace_id
          AND id = NEW.raw_object_id
          AND kind = expected_kind
          AND classification IN ('SENSITIVE', 'RESTRICTED')
          AND retention_mode = 'PERMANENT'
          AND retention_until IS NULL
    ) THEN
        RAISE EXCEPTION '% requires a permanent % object in the same workspace',
            TG_TABLE_NAME, expected_kind USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER tool_tests_permanent_payload
BEFORE INSERT OR UPDATE OF workspace_id, raw_object_id ON tool_tests
FOR EACH ROW EXECUTE FUNCTION enforce_permanent_tool_payload_reference();

CREATE TRIGGER tool_invocations_permanent_payload
BEFORE INSERT OR UPDATE OF workspace_id, raw_object_id ON tool_invocations
FOR EACH ROW EXECUTE FUNCTION enforce_permanent_tool_payload_reference();
