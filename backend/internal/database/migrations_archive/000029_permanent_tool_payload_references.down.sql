DROP TRIGGER IF EXISTS tool_invocations_permanent_payload ON tool_invocations;
DROP TRIGGER IF EXISTS tool_tests_permanent_payload ON tool_tests;
DROP FUNCTION IF EXISTS enforce_permanent_tool_payload_reference();

ALTER TABLE tool_invocations
    DROP CONSTRAINT IF EXISTS tool_invocations_terminal_raw_object_check,
    DROP CONSTRAINT IF EXISTS tool_invocations_raw_object_fk;

ALTER TABLE tool_tests
    DROP CONSTRAINT IF EXISTS tool_tests_raw_object_fk,
    ALTER COLUMN raw_object_id DROP NOT NULL;
