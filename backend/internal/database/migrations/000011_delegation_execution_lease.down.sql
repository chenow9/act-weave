DROP INDEX IF EXISTS agent_run_delegation_finalize_outbox_claim_idx;

ALTER TABLE agent_run_delegation_finalize_outbox
    DROP COLUMN IF EXISTS claimed_until,
    DROP COLUMN IF EXISTS claimed_by;

ALTER TABLE agent_a2a_inbound_tasks
    DROP CONSTRAINT IF EXISTS agent_a2a_inbound_tasks_execute_generation_check;

ALTER TABLE agent_a2a_inbound_tasks
    DROP COLUMN IF EXISTS execute_finished_at,
    DROP COLUMN IF EXISTS execute_started_at,
    DROP COLUMN IF EXISTS execute_generation;
