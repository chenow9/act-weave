DROP TRIGGER IF EXISTS agent_a2a_inbound_task_aliases_immutable_trg
    ON agent_a2a_inbound_task_aliases;
DROP FUNCTION IF EXISTS enforce_agent_a2a_inbound_task_aliases_immutable();
DROP TABLE IF EXISTS agent_a2a_inbound_task_aliases;
ALTER TABLE agent_a2a_inbound_tasks
    DROP CONSTRAINT IF EXISTS agent_a2a_inbound_tasks_workspace_exposure_id_key;
