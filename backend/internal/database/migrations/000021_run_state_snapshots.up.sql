ALTER TABLE agent_runs
    ADD COLUMN snapshot_schema_version TEXT NOT NULL DEFAULT 'run.v1',
    ADD COLUMN authorization_snapshot JSONB NOT NULL DEFAULT '{}'::JSONB,
    ADD CONSTRAINT agent_runs_snapshot_schema_version_not_blank
        CHECK (length(btrim(snapshot_schema_version)) > 0),
    ADD CONSTRAINT agent_runs_authorization_snapshot_object_check
        CHECK (jsonb_typeof(authorization_snapshot) = 'object');

ALTER TABLE workflow_executions
    ADD COLUMN snapshot_schema_version TEXT NOT NULL DEFAULT 'run.v1',
    ADD COLUMN authorization_snapshot JSONB NOT NULL DEFAULT '{}'::JSONB,
    ADD CONSTRAINT workflow_executions_snapshot_schema_version_not_blank
        CHECK (length(btrim(snapshot_schema_version)) > 0),
    ADD CONSTRAINT workflow_executions_authorization_snapshot_object_check
        CHECK (jsonb_typeof(authorization_snapshot) = 'object');

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
        NEW.id, NEW.workspace_id, NEW.session_id, NEW.agent_id,
        NEW.trigger_type, NEW.triggered_by_type, NEW.triggered_by_id, NEW.trace_id,
        NEW.model_snapshot, NEW.capability_snapshot, NEW.context_policy_snapshot,
        NEW.authorization_snapshot, NEW.snapshot_schema_version,
        NEW.input_summary, NEW.started_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.session_id, OLD.agent_id,
        OLD.trigger_type, OLD.triggered_by_type, OLD.triggered_by_id, OLD.trace_id,
        OLD.model_snapshot, OLD.capability_snapshot, OLD.context_policy_snapshot,
        OLD.authorization_snapshot, OLD.snapshot_schema_version,
        OLD.input_summary, OLD.started_at
    ) THEN
        RAISE EXCEPTION 'agent run identity and start snapshots are immutable'
            USING ERRCODE = '55000';
    END IF;
    IF OLD.status IN ('SUCCEEDED', 'FAILED', 'CANCELLED') THEN
        RAISE EXCEPTION 'terminal agent run is immutable'
            USING ERRCODE = '55000';
    END IF;
    IF NEW.lock_version <> OLD.lock_version + 1 THEN
        RAISE EXCEPTION 'agent run update requires the next lock version'
            USING ERRCODE = '40001';
    END IF;
    IF NEW.status <> OLD.status AND NOT (
        (OLD.status = 'PENDING' AND NEW.status IN ('RUNNING', 'FAILED', 'CANCELLED'))
        OR (OLD.status = 'RUNNING' AND NEW.status IN (
            'WAITING_CONFIRMATION', 'SUCCEEDED', 'FAILED', 'CANCELLED'
        ))
        OR (OLD.status = 'WAITING_CONFIRMATION' AND NEW.status IN (
            'RUNNING', 'FAILED', 'CANCELLED'
        ))
    ) THEN
        RAISE EXCEPTION 'illegal agent run status transition from % to %',
            OLD.status, NEW.status USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION enforce_workflow_execution_state()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'workflow executions are permanently retained and cannot be deleted'
            USING ERRCODE = '55000';
    END IF;
    IF ROW(
        NEW.id, NEW.workspace_id, NEW.workflow_id, NEW.revision_id, NEW.agent_run_id,
        NEW.trigger_type, NEW.triggered_by_type, NEW.triggered_by_id, NEW.trace_id,
        NEW.snapshot_schema_version, NEW.authorization_snapshot,
        NEW.input_summary, NEW.started_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.workflow_id, OLD.revision_id, OLD.agent_run_id,
        OLD.trigger_type, OLD.triggered_by_type, OLD.triggered_by_id, OLD.trace_id,
        OLD.snapshot_schema_version, OLD.authorization_snapshot,
        OLD.input_summary, OLD.started_at
    ) THEN
        RAISE EXCEPTION 'workflow execution identity and start evidence are immutable'
            USING ERRCODE = '55000';
    END IF;
    IF OLD.status IN ('SUCCEEDED', 'FAILED', 'CANCELLED') THEN
        RAISE EXCEPTION 'terminal workflow execution is immutable'
            USING ERRCODE = '55000';
    END IF;
    IF NEW.lock_version <> OLD.lock_version + 1 THEN
        RAISE EXCEPTION 'workflow execution update requires the next lock version'
            USING ERRCODE = '40001';
    END IF;
    IF NEW.status <> OLD.status AND NOT (
        (OLD.status = 'PENDING' AND NEW.status IN ('RUNNING', 'FAILED', 'CANCELLED'))
        OR (OLD.status = 'RUNNING' AND NEW.status IN (
            'WAITING_CONFIRMATION', 'SUCCEEDED', 'FAILED', 'CANCELLED'
        ))
        OR (OLD.status = 'WAITING_CONFIRMATION' AND NEW.status IN (
            'RUNNING', 'FAILED', 'CANCELLED'
        ))
    ) THEN
        RAISE EXCEPTION 'illegal workflow execution status transition from % to %',
            OLD.status, NEW.status USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;
