-- Recoverable inbound execution lease + outbox claim-token safety.

ALTER TABLE agent_a2a_inbound_tasks
    ADD COLUMN IF NOT EXISTS execute_owner TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS execute_token TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS execute_lease_until TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS agent_a2a_inbound_tasks_lease_idx
    ON agent_a2a_inbound_tasks (workspace_id, execute_lease_until)
    WHERE status = 'RUNNING' AND execute_generation > 0;

ALTER TABLE agent_run_delegation_finalize_outbox
    ADD COLUMN IF NOT EXISTS claim_token TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS claim_generation BIGINT NOT NULL DEFAULT 0;

-- Soft-disable must not hide rows: ensure list paths use enabled flag only.
-- (deleted_at remains for true deletion; SoftDisable APIs set enabled=false only.)
