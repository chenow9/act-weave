-- Durable production workflow idempotency across restarts and replicas.
-- execution_id intentionally has no FK so the claim remains as historical
-- idempotency evidence if execution retention is introduced later. The claim
-- and initial workflow_executions row are created in one transaction.

CREATE TABLE workflow_production_idempotency (
    workspace_id   UUID NOT NULL,
    actor_id       UUID NOT NULL,
    idempotency_key TEXT NOT NULL,
    request_hash   TEXT NOT NULL,
    execution_id   UUID NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (workspace_id, actor_id, idempotency_key),
    CONSTRAINT workflow_production_idempotency_execution_key
        UNIQUE (workspace_id, execution_id),
    CONSTRAINT workflow_production_idempotency_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT workflow_production_idempotency_actor_fk
        FOREIGN KEY (actor_id) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT workflow_production_idempotency_key_check
        CHECK (length(btrim(idempotency_key)) BETWEEN 1 AND 255),
    CONSTRAINT workflow_production_idempotency_request_hash_check
        CHECK (request_hash ~ '^[0-9a-f]{64}$')
);

COMMENT ON TABLE workflow_production_idempotency IS
    'Atomic production workflow execute claims; survives process restart and multi-replica routing.';
