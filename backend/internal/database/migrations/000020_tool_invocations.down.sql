DROP TRIGGER IF EXISTS tool_invocations_state_guard ON tool_invocations;
DROP FUNCTION IF EXISTS enforce_tool_invocation_state();
DROP TRIGGER IF EXISTS tool_invocations_integrity ON tool_invocations;
DROP FUNCTION IF EXISTS enforce_tool_invocation_integrity();
DROP TABLE IF EXISTS tool_invocations;
ALTER TABLE tool_versions
    DROP CONSTRAINT IF EXISTS tool_versions_workspace_capability_version_provider_key;
