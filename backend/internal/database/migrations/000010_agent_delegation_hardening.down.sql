DROP INDEX IF EXISTS agent_run_delegation_finalize_outbox_due_idx;
DROP INDEX IF EXISTS agent_run_delegation_finalize_outbox_del_uidx;
DROP TABLE IF EXISTS agent_run_delegation_finalize_outbox;

DROP INDEX IF EXISTS agent_a2a_inbound_tasks_task_idx;
DROP INDEX IF EXISTS agent_a2a_inbound_tasks_run_idx;
DROP INDEX IF EXISTS agent_a2a_inbound_tasks_idempotency_uidx;
DROP TABLE IF EXISTS agent_a2a_inbound_tasks;

DROP TRIGGER IF EXISTS agent_runs_delegation_linkage_immutable ON agent_runs;
DROP FUNCTION IF EXISTS enforce_agent_run_delegation_linkage_immutable();
