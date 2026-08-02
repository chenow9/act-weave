-- Inbound execution ownership (prevent double model dispatch) + outbox claim lease.

ALTER TABLE agent_a2a_inbound_tasks
    ADD COLUMN IF NOT EXISTS execute_generation BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS execute_started_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS execute_finished_at TIMESTAMPTZ;

ALTER TABLE agent_a2a_inbound_tasks
    DROP CONSTRAINT IF EXISTS agent_a2a_inbound_tasks_execute_generation_check;
ALTER TABLE agent_a2a_inbound_tasks
    ADD CONSTRAINT agent_a2a_inbound_tasks_execute_generation_check
        CHECK (execute_generation >= 0);

ALTER TABLE agent_run_delegation_finalize_outbox
    ADD COLUMN IF NOT EXISTS claimed_until TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS claimed_by TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS agent_run_delegation_finalize_outbox_claim_idx
    ON agent_run_delegation_finalize_outbox (next_attempt_at, claimed_until)
    WHERE attempts < 32;
