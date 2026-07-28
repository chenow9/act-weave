DROP TRIGGER IF EXISTS workflow_executions_state_guard ON workflow_executions;

DELETE FROM workflow_trial_runs
WHERE execution_id IN (
    SELECT id FROM workflow_executions WHERE compilation_id IS NOT NULL
);

DELETE FROM workflow_executions WHERE compilation_id IS NOT NULL;

DROP INDEX IF EXISTS workflow_executions_workspace_compilation_started_idx;

ALTER TABLE workflow_executions
    DROP CONSTRAINT IF EXISTS workflow_executions_exact_source_check,
    DROP CONSTRAINT IF EXISTS workflow_executions_workspace_compilation_fk,
    ALTER COLUMN revision_id SET NOT NULL,
    DROP COLUMN compilation_id;

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
        NEW.input_summary, NEW.started_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.workflow_id, OLD.revision_id, OLD.agent_run_id,
        OLD.trigger_type, OLD.triggered_by_type, OLD.triggered_by_id, OLD.trace_id,
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

CREATE TRIGGER workflow_executions_state_guard
BEFORE UPDATE OR DELETE ON workflow_executions
FOR EACH ROW EXECUTE FUNCTION enforce_workflow_execution_state();
