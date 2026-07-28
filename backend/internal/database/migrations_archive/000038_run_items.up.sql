ALTER TABLE agent_runs
    ADD CONSTRAINT agent_runs_workspace_agent_id_key
        UNIQUE (workspace_id, agent_id, id);

CREATE TABLE run_items (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    run_id UUID NOT NULL,
    ordinal INTEGER NOT NULL,
    item_type TEXT NOT NULL,
    status TEXT NOT NULL,
    source_type TEXT NOT NULL,
    source_id UUID,
    snapshot JSONB NOT NULL,
    started_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    completed_at TIMESTAMPTZ,
    CONSTRAINT run_items_workspace_run_id_key
        UNIQUE (workspace_id, run_id, id),
    CONSTRAINT run_items_run_ordinal_key UNIQUE (run_id, ordinal),
    CONSTRAINT run_items_run_scope_fk
        FOREIGN KEY (workspace_id, agent_id, run_id)
        REFERENCES agent_runs (workspace_id, agent_id, id) ON DELETE RESTRICT,
    CONSTRAINT run_items_ordinal_check CHECK (ordinal > 0),
    CONSTRAINT run_items_type_check CHECK (
        item_type ~ '^[a-z][a-z0-9_]{0,63}$'
    ),
    CONSTRAINT run_items_status_check CHECK (
        status IN (
            'in_progress', 'waiting', 'completed', 'failed',
            'declined', 'cancelled', 'unknown'
        )
    ),
    CONSTRAINT run_items_source_type_check CHECK (
        source_type IN (
            'CHAT_MESSAGE', 'MODEL_RESPONSE', 'TOOL_INVOCATION',
            'WORKFLOW_EXECUTION', 'WORKFLOW_STEP', 'EXECUTION_CONFIRMATION',
            'STORED_OBJECT', 'RUNTIME', 'UNKNOWN'
        )
    ),
    CONSTRAINT run_items_snapshot_object_check CHECK (
        jsonb_typeof(snapshot) = 'object'
    ),
    CONSTRAINT run_items_completion_state_check CHECK (
        (status IN ('in_progress', 'waiting', 'unknown') AND completed_at IS NULL)
        OR
        (status IN ('completed', 'failed', 'declined', 'cancelled') AND completed_at IS NOT NULL)
    ),
    CONSTRAINT run_items_timestamps_check CHECK (
        completed_at IS NULL OR completed_at >= started_at
    )
);

CREATE INDEX run_items_scope_ordinal_idx
    ON run_items (workspace_id, agent_id, run_id, ordinal, id);
CREATE INDEX run_items_scope_status_started_idx
    ON run_items (workspace_id, agent_id, status, started_at DESC, id);
CREATE INDEX run_items_source_ref_idx
    ON run_items (workspace_id, source_type, source_id, id)
    WHERE source_id IS NOT NULL;
