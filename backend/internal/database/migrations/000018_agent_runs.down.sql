ALTER TABLE chat_messages DROP CONSTRAINT IF EXISTS chat_messages_run_fk;
ALTER TABLE chat_sessions DROP CONSTRAINT IF EXISTS chat_sessions_latest_run_fk;
UPDATE chat_messages SET run_id = NULL WHERE run_id IS NOT NULL;
UPDATE chat_sessions SET latest_run_id = NULL WHERE latest_run_id IS NOT NULL;
DROP TRIGGER IF EXISTS agent_run_steps_permanent_evidence ON agent_run_steps;
DROP FUNCTION IF EXISTS enforce_agent_run_step_permanent_evidence();
DROP TABLE IF EXISTS agent_run_steps;
DROP TRIGGER IF EXISTS agent_runs_permanent_snapshot ON agent_runs;
DROP FUNCTION IF EXISTS enforce_agent_run_permanent_snapshot();
DROP TABLE IF EXISTS agent_runs;
ALTER TABLE capability_releases
    DROP CONSTRAINT IF EXISTS capability_releases_workspace_id_key;
