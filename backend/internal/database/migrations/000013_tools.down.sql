DROP TRIGGER IF EXISTS tool_tests_immutable ON tool_tests;
DROP FUNCTION IF EXISTS reject_tool_test_mutation();
DROP TABLE IF EXISTS tool_tests;
DROP TRIGGER IF EXISTS published_tool_versions_immutable ON tool_versions;
DROP FUNCTION IF EXISTS enforce_published_tool_version_immutability();
DROP TABLE IF EXISTS tool_versions;
DROP TRIGGER IF EXISTS tools_capability_kind_integrity ON tools;
DROP FUNCTION IF EXISTS enforce_tool_capability_kind();
DROP TABLE IF EXISTS tools;
ALTER TABLE service_connections
    DROP CONSTRAINT IF EXISTS service_connections_workspace_provider_id_key;
ALTER TABLE provider_assets
    DROP CONSTRAINT IF EXISTS provider_assets_workspace_provider_id_key;
