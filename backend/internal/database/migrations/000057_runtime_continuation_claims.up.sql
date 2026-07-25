-- Durable multi-replica lease for post-SUCCEEDED runtime continue (EnqueueContinue).
-- Terminal confirmation_resume_checkpoints rows are immutable, so the lease lives
-- in a sibling table keyed by confirmation_id.
CREATE TABLE runtime_continuation_claims (
    confirmation_id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    run_id UUID NOT NULL,
    claim_id UUID,
    claim_expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    lock_version BIGINT NOT NULL DEFAULT 1,
    CONSTRAINT runtime_continuation_claims_workspace_id_key
        UNIQUE (workspace_id, confirmation_id),
    CONSTRAINT runtime_continuation_claims_confirmation_fk
        FOREIGN KEY (workspace_id, confirmation_id)
        REFERENCES execution_confirmations (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT runtime_continuation_claims_run_fk
        FOREIGN KEY (workspace_id, run_id)
        REFERENCES agent_runs (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT runtime_continuation_claims_claim_pair_check
        CHECK ((claim_id IS NULL) = (claim_expires_at IS NULL)),
    CONSTRAINT runtime_continuation_claims_claim_window_check
        CHECK (
            claim_expires_at IS NULL
            OR claim_id IS NOT NULL
        ),
    CONSTRAINT runtime_continuation_claims_lock_check CHECK (lock_version > 0)
);

CREATE INDEX runtime_continuation_claims_reclaim_idx
    ON runtime_continuation_claims (claim_expires_at, confirmation_id)
    WHERE claim_id IS NOT NULL;
