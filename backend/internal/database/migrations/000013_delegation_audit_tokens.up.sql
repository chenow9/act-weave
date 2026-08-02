-- Delegation call audit: token usage + execution attempt/retry counts.
-- Tokens are NULL when unknown (A2A remote without usage); never invent 0.
-- attempt_count = actual agent dispatch count (idempotent replay does not increment).
-- retry_count = max(0, attempt_count-1) for execution retries only (not finalize-outbox).

ALTER TABLE agent_run_delegations
    ADD COLUMN IF NOT EXISTS input_tokens BIGINT,
    ADD COLUMN IF NOT EXISTS output_tokens BIGINT,
    ADD COLUMN IF NOT EXISTS total_tokens BIGINT,
    ADD COLUMN IF NOT EXISTS tokens_known BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS attempt_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS retry_count INTEGER NOT NULL DEFAULT 0;

ALTER TABLE agent_run_delegations
    DROP CONSTRAINT IF EXISTS agent_run_delegations_tokens_nonneg,
    DROP CONSTRAINT IF EXISTS agent_run_delegations_attempt_nonneg;

ALTER TABLE agent_run_delegations
    ADD CONSTRAINT agent_run_delegations_tokens_nonneg
        CHECK (
            (input_tokens IS NULL OR input_tokens >= 0)
            AND (output_tokens IS NULL OR output_tokens >= 0)
            AND (total_tokens IS NULL OR total_tokens >= 0)
        ),
    ADD CONSTRAINT agent_run_delegations_attempt_nonneg
        CHECK (attempt_count >= 0 AND retry_count >= 0 AND retry_count <= attempt_count);

COMMENT ON COLUMN agent_run_delegations.input_tokens IS
    'Sum of prompt/input tokens from nested MODEL turns under this delegation; NULL if unknown';
COMMENT ON COLUMN agent_run_delegations.output_tokens IS
    'Sum of completion/output tokens from nested MODEL turns; NULL if unknown';
COMMENT ON COLUMN agent_run_delegations.total_tokens IS
    'Sum of total tokens from nested MODEL turns; NULL if unknown';
COMMENT ON COLUMN agent_run_delegations.tokens_known IS
    'True only when at least one MODEL turn under this delegation reported usage';
COMMENT ON COLUMN agent_run_delegations.attempt_count IS
    'Actual agent dispatch attempts (not idempotent replay; not finalize-outbox retries)';
COMMENT ON COLUMN agent_run_delegations.retry_count IS
    'Execution retries = max(0, attempt_count-1); finalize-outbox retries are separate';
