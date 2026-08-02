ALTER TABLE agent_run_delegations
    DROP CONSTRAINT IF EXISTS agent_run_delegations_attempt_nonneg;

ALTER TABLE agent_run_delegations
    ADD CONSTRAINT agent_run_delegations_attempt_nonneg
        CHECK (attempt_count >= 0 AND retry_count >= 0 AND retry_count <= attempt_count);
