DROP TRIGGER IF EXISTS agent_prompt_revisions_immutable ON agent_prompt_revisions;
DROP FUNCTION IF EXISTS reject_agent_prompt_revision_mutation();
DROP TABLE IF EXISTS prompt_runs;
ALTER TABLE workspaces DROP CONSTRAINT IF EXISTS workspaces_default_agent_fk;
ALTER TABLE agents DROP CONSTRAINT IF EXISTS agents_current_prompt_revision_fk;
DROP TABLE IF EXISTS agent_prompt_revisions;
DROP TABLE IF EXISTS agents;
