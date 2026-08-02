DROP INDEX IF EXISTS agent_a2a_inbound_tasks_lease_idx;

ALTER TABLE agent_a2a_inbound_tasks
    DROP COLUMN IF EXISTS execute_owner,
    DROP COLUMN IF EXISTS execute_token,
    DROP COLUMN IF EXISTS execute_lease_until;

ALTER TABLE agent_run_delegation_finalize_outbox
    DROP COLUMN IF EXISTS claim_token,
    DROP COLUMN IF EXISTS claim_generation;
