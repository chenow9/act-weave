-- Reverse 000018.

DROP TRIGGER IF EXISTS agent_run_steps_terminal_delegation_immutable_trg
    ON agent_run_steps;
DROP FUNCTION IF EXISTS enforce_agent_run_step_terminal_delegation_immutable();
