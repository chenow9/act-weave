DROP TABLE IF EXISTS run_items;

ALTER TABLE agent_runs
    DROP CONSTRAINT IF EXISTS agent_runs_workspace_agent_id_key;
