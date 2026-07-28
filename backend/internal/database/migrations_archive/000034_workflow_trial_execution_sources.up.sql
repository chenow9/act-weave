ALTER TABLE workflow_executions
    ADD COLUMN compilation_id UUID,
    ALTER COLUMN revision_id DROP NOT NULL,
    ADD CONSTRAINT workflow_executions_workspace_compilation_fk
        FOREIGN KEY (workspace_id, workflow_id, compilation_id)
        REFERENCES workflow_compilations (workspace_id, capability_id, id)
        ON DELETE RESTRICT,
    ADD CONSTRAINT workflow_executions_exact_source_check CHECK (
        (revision_id IS NOT NULL AND compilation_id IS NULL)
        OR (revision_id IS NULL AND compilation_id IS NOT NULL)
    );

CREATE INDEX workflow_executions_workspace_compilation_started_idx
    ON workflow_executions (workspace_id, compilation_id, started_at DESC, id)
    WHERE compilation_id IS NOT NULL;

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
        NEW.id, NEW.workspace_id, NEW.workflow_id, NEW.revision_id, NEW.compilation_id,
        NEW.agent_run_id, NEW.trigger_type, NEW.triggered_by_type, NEW.triggered_by_id,
        NEW.trace_id, NEW.input_summary, NEW.started_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.workflow_id, OLD.revision_id, OLD.compilation_id,
        OLD.agent_run_id, OLD.trigger_type, OLD.triggered_by_type, OLD.triggered_by_id,
        OLD.trace_id, OLD.input_summary, OLD.started_at
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
