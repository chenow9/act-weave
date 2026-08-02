-- Hardening: immutable agent_runs linkage/graph snapshot, inbound A2A durable
-- idempotency, and deferred finalize outbox for fail-closed terminal writes.

-- ---------------------------------------------------------------------------
-- agent_runs: freeze parent linkage + non-empty graph snapshot after set
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION enforce_agent_run_delegation_linkage_immutable()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    -- parent_run_id / parent_delegation_id set at insert only (never reassigned).
    IF OLD.parent_run_id IS DISTINCT FROM NEW.parent_run_id THEN
        RAISE EXCEPTION 'agent_runs.parent_run_id is immutable'
            USING ERRCODE = '55000';
    END IF;
    IF OLD.parent_delegation_id IS DISTINCT FROM NEW.parent_delegation_id THEN
        RAISE EXCEPTION 'agent_runs.parent_delegation_id is immutable'
            USING ERRCODE = '55000';
    END IF;
    -- agent_graph_snapshot: empty → value allowed once; thereafter immutable.
    IF OLD.agent_graph_snapshot IS NOT NULL
       AND OLD.agent_graph_snapshot <> '{}'::jsonb
       AND NEW.agent_graph_snapshot IS DISTINCT FROM OLD.agent_graph_snapshot THEN
        RAISE EXCEPTION 'agent_runs.agent_graph_snapshot is immutable once frozen'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS agent_runs_delegation_linkage_immutable ON agent_runs;
CREATE TRIGGER agent_runs_delegation_linkage_immutable
BEFORE UPDATE ON agent_runs
FOR EACH ROW EXECUTE FUNCTION enforce_agent_run_delegation_linkage_immutable();

-- ---------------------------------------------------------------------------
-- Durable inbound A2A idempotency (survives restart; not memory maps)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS agent_a2a_inbound_tasks (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    exposure_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    external_key TEXT NOT NULL,
    external_task_id TEXT NOT NULL DEFAULT '',
    external_context_id TEXT NOT NULL DEFAULT '',
    external_message_id TEXT NOT NULL DEFAULT '',
    run_id UUID NOT NULL,
    delegation_id UUID,
    status TEXT NOT NULL DEFAULT 'RUNNING',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT agent_a2a_inbound_tasks_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT agent_a2a_inbound_tasks_workspace_id_key
        UNIQUE (workspace_id, id),
    CONSTRAINT agent_a2a_inbound_tasks_exposure_fk
        FOREIGN KEY (workspace_id, exposure_id)
        REFERENCES agent_a2a_exposures (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT agent_a2a_inbound_tasks_run_fk
        FOREIGN KEY (workspace_id, run_id)
        REFERENCES agent_runs (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT agent_a2a_inbound_tasks_external_key_not_blank
        CHECK (length(btrim(external_key)) > 0),
    CONSTRAINT agent_a2a_inbound_tasks_status_check
        CHECK (status IN ('RUNNING', 'SUCCEEDED', 'FAILED', 'CANCELLED', 'TIMED_OUT'))
);

CREATE UNIQUE INDEX IF NOT EXISTS agent_a2a_inbound_tasks_idempotency_uidx
    ON agent_a2a_inbound_tasks (workspace_id, exposure_id, external_key);

CREATE INDEX IF NOT EXISTS agent_a2a_inbound_tasks_run_idx
    ON agent_a2a_inbound_tasks (workspace_id, run_id);

CREATE INDEX IF NOT EXISTS agent_a2a_inbound_tasks_task_idx
    ON agent_a2a_inbound_tasks (workspace_id, external_task_id)
    WHERE length(btrim(external_task_id)) > 0;

-- ---------------------------------------------------------------------------
-- Finalize outbox: recoverable terminal writes (never silent RUNNING leftovers)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS agent_run_delegation_finalize_outbox (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    delegation_id UUID NOT NULL,
    step_id UUID,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT agent_run_delegation_finalize_outbox_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT agent_run_delegation_finalize_outbox_delegation_fk
        FOREIGN KEY (workspace_id, delegation_id)
        REFERENCES agent_run_delegations (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT agent_run_delegation_finalize_outbox_payload_object
        CHECK (jsonb_typeof(payload) = 'object'),
    CONSTRAINT agent_run_delegation_finalize_outbox_attempts_check
        CHECK (attempts >= 0)
);

CREATE UNIQUE INDEX IF NOT EXISTS agent_run_delegation_finalize_outbox_del_uidx
    ON agent_run_delegation_finalize_outbox (workspace_id, delegation_id);

CREATE INDEX IF NOT EXISTS agent_run_delegation_finalize_outbox_due_idx
    ON agent_run_delegation_finalize_outbox (next_attempt_at, created_at);
