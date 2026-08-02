-- Unique key so aliases can FK (workspace_id, exposure_id, inbound_task_id)
-- and prove the alias exposure always matches the authority task exposure.
ALTER TABLE agent_a2a_inbound_tasks
    DROP CONSTRAINT IF EXISTS agent_a2a_inbound_tasks_workspace_exposure_id_key;
ALTER TABLE agent_a2a_inbound_tasks
    ADD CONSTRAINT agent_a2a_inbound_tasks_workspace_exposure_id_key
        UNIQUE (workspace_id, exposure_id, id);

-- Durable external TaskID aliases: multiple a2asrv replicas may mint different
-- TaskIDs for the same contextId+messageId retry; each observed TaskID maps to
-- one authoritative inbound task (workspace+exposure isolated).
-- Permanent evidence: ON DELETE RESTRICT + no UPDATE/DELETE of alias rows.
CREATE TABLE IF NOT EXISTS agent_a2a_inbound_task_aliases (
    workspace_id      UUID NOT NULL,
    exposure_id       UUID NOT NULL,
    external_task_id  TEXT NOT NULL,
    inbound_task_id   UUID NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (workspace_id, exposure_id, external_task_id),
    CONSTRAINT agent_a2a_inbound_task_aliases_external_task_id_not_blank
        CHECK (length(btrim(external_task_id)) > 0),
    -- Composite FK enforces workspace + exposure + task identity together
    -- (cannot attach an alias to a task under a different exposure).
    CONSTRAINT agent_a2a_inbound_task_aliases_task_fk
        FOREIGN KEY (workspace_id, exposure_id, inbound_task_id)
        REFERENCES agent_a2a_inbound_tasks (workspace_id, exposure_id, id)
        ON DELETE RESTRICT
        ON UPDATE RESTRICT
);

CREATE INDEX IF NOT EXISTS agent_a2a_inbound_task_aliases_inbound_idx
    ON agent_a2a_inbound_task_aliases (workspace_id, exposure_id, inbound_task_id);

COMMENT ON TABLE agent_a2a_inbound_task_aliases IS
    'Maps every observed A2A TaskID (per exposure) to one authoritative inbound task; permanent, non-rebindable evidence.';

-- Alias rows are permanent evidence: block UPDATE (rebind/retarget) and DELETE.
CREATE OR REPLACE FUNCTION enforce_agent_a2a_inbound_task_aliases_immutable()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'agent_a2a_inbound_task_aliases rows are permanent evidence (DELETE denied)'
            USING ERRCODE = '55000';
    END IF;
    IF TG_OP = 'UPDATE' THEN
        -- No field may change after insert (including inbound_task_id rebind).
        IF ROW(
            NEW.workspace_id, NEW.exposure_id, NEW.external_task_id,
            NEW.inbound_task_id, NEW.created_at
        ) IS DISTINCT FROM ROW(
            OLD.workspace_id, OLD.exposure_id, OLD.external_task_id,
            OLD.inbound_task_id, OLD.created_at
        ) THEN
            RAISE EXCEPTION 'agent_a2a_inbound_task_aliases rows are permanent evidence (UPDATE denied)'
                USING ERRCODE = '55000';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS agent_a2a_inbound_task_aliases_immutable_trg
    ON agent_a2a_inbound_task_aliases;
CREATE TRIGGER agent_a2a_inbound_task_aliases_immutable_trg
    BEFORE UPDATE OR DELETE ON agent_a2a_inbound_task_aliases
    FOR EACH ROW
    EXECUTE FUNCTION enforce_agent_a2a_inbound_task_aliases_immutable();

-- TIMED_OUT transitions were introduced in 000009 together with status expansion.
-- Re-apply the same permanent-snapshot matrix here for idempotency when upgrading
-- from intermediate revisions that only had status checks without trigger updates.
CREATE OR REPLACE FUNCTION enforce_agent_run_permanent_snapshot()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND ROW(
        NEW.id, NEW.workspace_id, NEW.session_id, NEW.agent_id, NEW.trigger_type,
        NEW.triggered_by_type, NEW.triggered_by_id, NEW.trace_id, NEW.model_snapshot,
        NEW.capability_snapshot, NEW.context_policy_snapshot, NEW.agent_snapshot,
        NEW.authorization_snapshot, NEW.snapshot_schema_version, NEW.input_summary,
        NEW.started_at, NEW.principal_snapshot_version, NEW.subject_type, NEW.subject_id,
        NEW.client_id, NEW.grant_id, NEW.grant_version, NEW.agent_policy_version
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.session_id, OLD.agent_id, OLD.trigger_type,
        OLD.triggered_by_type, OLD.triggered_by_id, OLD.trace_id, OLD.model_snapshot,
        OLD.capability_snapshot, OLD.context_policy_snapshot, OLD.agent_snapshot,
        OLD.authorization_snapshot, OLD.snapshot_schema_version, OLD.input_summary,
        OLD.started_at, OLD.principal_snapshot_version, OLD.subject_type, OLD.subject_id,
        OLD.client_id, OLD.grant_id, OLD.grant_version, OLD.agent_policy_version
    ) THEN
        RAISE EXCEPTION 'agent run identity and start snapshots are immutable'
            USING ERRCODE = '55000';
    END IF;
    IF OLD.status IN ('SUCCEEDED', 'FAILED', 'CANCELLED', 'TIMED_OUT') THEN
        RAISE EXCEPTION 'terminal agent run is immutable' USING ERRCODE = '55000';
    END IF;
    IF NEW.lock_version <> OLD.lock_version + 1 THEN
        RAISE EXCEPTION 'agent run update requires the next lock version' USING ERRCODE = '40001';
    END IF;
    IF NEW.status <> OLD.status AND NOT (
        (OLD.status = 'PENDING' AND NEW.status IN ('RUNNING', 'FAILED', 'CANCELLED', 'TIMED_OUT')) OR
        (OLD.status = 'RUNNING' AND NEW.status IN (
            'WAITING_CONFIRMATION', 'WAITING_INTERACTION', 'SUCCEEDED', 'FAILED', 'CANCELLED', 'TIMED_OUT'
        )) OR
        (OLD.status = 'WAITING_CONFIRMATION' AND NEW.status IN ('RUNNING', 'FAILED', 'CANCELLED', 'TIMED_OUT')) OR
        (OLD.status = 'WAITING_INTERACTION' AND NEW.status IN ('RUNNING', 'FAILED', 'CANCELLED', 'TIMED_OUT'))
    ) THEN
        RAISE EXCEPTION 'illegal agent run status transition from % to %', OLD.status, NEW.status
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;
