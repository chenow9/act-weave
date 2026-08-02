ALTER TABLE agent_run_delegations
    DROP CONSTRAINT IF EXISTS agent_run_delegations_tokens_nonneg,
    DROP CONSTRAINT IF EXISTS agent_run_delegations_attempt_nonneg;

ALTER TABLE agent_run_delegations
    DROP COLUMN IF EXISTS input_tokens,
    DROP COLUMN IF EXISTS output_tokens,
    DROP COLUMN IF EXISTS total_tokens,
    DROP COLUMN IF EXISTS tokens_known,
    DROP COLUMN IF EXISTS attempt_count,
    DROP COLUMN IF EXISTS retry_count;
