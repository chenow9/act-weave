DROP TRIGGER IF EXISTS agent_run_delegations_sticky_terminal_trg ON agent_run_delegations;
DROP FUNCTION IF EXISTS agent_run_delegations_sticky_terminal();

ALTER TABLE agent_a2a_inbound_tasks
    DROP COLUMN IF EXISTS request_hash;
