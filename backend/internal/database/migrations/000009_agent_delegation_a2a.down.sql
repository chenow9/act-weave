-- Reverse 000009_agent_delegation_a2a (best-effort; permanent evidence tables keep rows).

CREATE OR REPLACE FUNCTION enforce_agent_run_step_permanent_evidence()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'agent run steps are permanently retained and cannot be deleted'
            USING ERRCODE = '55000';
    END IF;
    IF ROW(
        NEW.id, NEW.workspace_id, NEW.run_id, NEW.sequence_no, NEW.step_type,
        NEW.capability_release_id, NEW.input_summary, NEW.started_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.run_id, OLD.sequence_no, OLD.step_type,
        OLD.capability_release_id, OLD.input_summary, OLD.started_at
    ) THEN
        RAISE EXCEPTION 'agent run step identity and start evidence are immutable'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

ALTER TABLE agent_run_steps DROP CONSTRAINT IF EXISTS agent_run_steps_finish_state_check;
ALTER TABLE agent_run_steps
    ADD CONSTRAINT agent_run_steps_finish_state_check CHECK (
        (status IN ('QUEUED', 'RUNNING', 'WAITING_CONFIRMATION') AND finished_at IS NULL)
        OR (status IN ('SUCCEEDED', 'FAILED', 'SKIPPED', 'CANCELLED') AND finished_at IS NOT NULL)
    );

ALTER TABLE agent_run_steps DROP CONSTRAINT IF EXISTS agent_run_steps_status_check;
ALTER TABLE agent_run_steps
    ADD CONSTRAINT agent_run_steps_status_check CHECK (
        status IN (
            'QUEUED', 'RUNNING', 'WAITING_CONFIRMATION',
            'SUCCEEDED', 'FAILED', 'SKIPPED', 'CANCELLED'
        )
    );

DROP INDEX IF EXISTS agent_run_steps_agent_idx;
DROP INDEX IF EXISTS agent_run_steps_parent_step_idx;
DROP INDEX IF EXISTS agent_run_steps_delegation_idx;

ALTER TABLE agent_run_steps
    DROP CONSTRAINT IF EXISTS agent_run_steps_parent_step_fk,
    DROP CONSTRAINT IF EXISTS agent_run_steps_delegation_fk;

ALTER TABLE agent_run_steps
    DROP COLUMN IF EXISTS parent_step_id,
    DROP COLUMN IF EXISTS delegation_id,
    DROP COLUMN IF EXISTS agent_id;

-- Restore pre-TIMED_OUT permanent-snapshot transitions (000002 baseline matrix).
CREATE OR REPLACE FUNCTION enforce_agent_run_permanent_snapshot()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'agent runs are permanently retained and cannot be deleted'
            USING ERRCODE = '55000';
    END IF;
    IF ROW(
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
    IF OLD.status IN ('SUCCEEDED', 'FAILED', 'CANCELLED') THEN
        RAISE EXCEPTION 'terminal agent run is immutable' USING ERRCODE = '55000';
    END IF;
    IF NEW.lock_version <> OLD.lock_version + 1 THEN
        RAISE EXCEPTION 'agent run update requires the next lock version' USING ERRCODE = '40001';
    END IF;
    IF NEW.status <> OLD.status AND NOT (
        (OLD.status = 'PENDING' AND NEW.status IN ('RUNNING', 'FAILED', 'CANCELLED')) OR
        (OLD.status = 'RUNNING' AND NEW.status IN ('WAITING_CONFIRMATION', 'SUCCEEDED', 'FAILED', 'CANCELLED')) OR
        (OLD.status = 'WAITING_CONFIRMATION' AND NEW.status IN ('RUNNING', 'FAILED', 'CANCELLED'))
    ) THEN
        RAISE EXCEPTION 'illegal agent run status transition from % to %', OLD.status, NEW.status
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

ALTER TABLE agent_runs DROP CONSTRAINT IF EXISTS agent_runs_finish_state_check;
ALTER TABLE agent_runs
    ADD CONSTRAINT agent_runs_finish_state_check CHECK (
        (status IN ('PENDING', 'RUNNING', 'WAITING_CONFIRMATION') AND finished_at IS NULL)
        OR (status IN ('SUCCEEDED', 'FAILED', 'CANCELLED') AND finished_at IS NOT NULL)
    );

ALTER TABLE agent_runs DROP CONSTRAINT IF EXISTS agent_runs_status_check;
ALTER TABLE agent_runs
    ADD CONSTRAINT agent_runs_status_check CHECK (
        status IN (
            'PENDING', 'RUNNING', 'WAITING_CONFIRMATION',
            'SUCCEEDED', 'FAILED', 'CANCELLED'
        )
    );

DROP INDEX IF EXISTS agent_runs_parent_run_idx;

ALTER TABLE agent_runs
    DROP CONSTRAINT IF EXISTS agent_runs_graph_snapshot_object_check,
    DROP CONSTRAINT IF EXISTS agent_runs_parent_delegation_fk,
    DROP CONSTRAINT IF EXISTS agent_runs_parent_run_fk;

ALTER TABLE agent_runs
    DROP COLUMN IF EXISTS agent_graph_snapshot,
    DROP COLUMN IF EXISTS parent_delegation_id,
    DROP COLUMN IF EXISTS parent_run_id;

DROP TRIGGER IF EXISTS agent_run_delegations_permanent_evidence ON agent_run_delegations;
DROP FUNCTION IF EXISTS enforce_agent_run_delegation_permanent_evidence();

DROP TABLE IF EXISTS agent_run_delegations;
DROP TABLE IF EXISTS agent_a2a_remote_bindings;
DROP TABLE IF EXISTS agent_a2a_exposures;
DROP TABLE IF EXISTS agent_delegation_bindings;
