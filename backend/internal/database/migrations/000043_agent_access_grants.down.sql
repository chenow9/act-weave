DROP TABLE IF EXISTS agent_access_grants;
DROP FUNCTION IF EXISTS enforce_agent_access_grant_window();
DROP FUNCTION IF EXISTS agent_access_grant_policy_valid(JSONB);
DROP FUNCTION IF EXISTS agent_access_grant_scopes_valid(JSONB);

