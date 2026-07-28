CREATE TABLE workflow_executions (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    workflow_id UUID NOT NULL,
    revision_id UUID NOT NULL,
    agent_run_id UUID,
    trigger_type TEXT NOT NULL,
    triggered_by_type TEXT NOT NULL,
    triggered_by_id UUID NOT NULL,
    trace_id TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'PENDING',
    input_summary JSONB NOT NULL DEFAULT '{}'::JSONB,
    output_summary JSONB NOT NULL DEFAULT '{}'::JSONB,
    error_code TEXT,
    started_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at TIMESTAMPTZ,
    lock_version BIGINT NOT NULL DEFAULT 1,
    CONSTRAINT workflow_executions_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT workflow_executions_workspace_workflow_fk
        FOREIGN KEY (workspace_id, workflow_id)
        REFERENCES workflows (workspace_id, capability_id) ON DELETE RESTRICT,
    CONSTRAINT workflow_executions_workspace_revision_fk
        FOREIGN KEY (workspace_id, workflow_id, revision_id)
        REFERENCES workflow_revisions (workspace_id, capability_id, id) ON DELETE RESTRICT,
    CONSTRAINT workflow_executions_workspace_agent_run_fk
        FOREIGN KEY (workspace_id, agent_run_id)
        REFERENCES agent_runs (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT workflow_executions_trigger_type_not_blank
        CHECK (length(btrim(trigger_type)) > 0),
    CONSTRAINT workflow_executions_triggered_by_type_check
        CHECK (triggered_by_type IN ('USER', 'SERVICE_PRINCIPAL', 'SYSTEM')),
    CONSTRAINT workflow_executions_trace_id_not_blank CHECK (length(btrim(trace_id)) > 0),
    CONSTRAINT workflow_executions_status_check CHECK (
        status IN (
            'PENDING', 'RUNNING', 'WAITING_CONFIRMATION',
            'SUCCEEDED', 'FAILED', 'CANCELLED'
        )
    ),
    CONSTRAINT workflow_executions_input_summary_object_check
        CHECK (jsonb_typeof(input_summary) = 'object'),
    CONSTRAINT workflow_executions_output_summary_object_check
        CHECK (jsonb_typeof(output_summary) = 'object'),
    CONSTRAINT workflow_executions_finish_state_check CHECK (
        (status IN ('PENDING', 'RUNNING', 'WAITING_CONFIRMATION') AND finished_at IS NULL)
        OR (status IN ('SUCCEEDED', 'FAILED', 'CANCELLED') AND finished_at IS NOT NULL)
    ),
    CONSTRAINT workflow_executions_finished_at_check
        CHECK (finished_at IS NULL OR finished_at >= started_at),
    CONSTRAINT workflow_executions_error_state_check CHECK (
        (status = 'FAILED' AND error_code IS NOT NULL)
        OR (status <> 'FAILED' AND error_code IS NULL)
    ),
    CONSTRAINT workflow_executions_lock_version_check CHECK (lock_version > 0)
);

CREATE INDEX workflow_executions_workspace_started_idx
    ON workflow_executions (workspace_id, started_at DESC, id);
CREATE INDEX workflow_executions_workspace_status_started_idx
    ON workflow_executions (workspace_id, status, started_at DESC, id);
CREATE INDEX workflow_executions_workspace_workflow_started_idx
    ON workflow_executions (workspace_id, workflow_id, started_at DESC, id);
CREATE INDEX workflow_executions_workspace_agent_run_started_idx
    ON workflow_executions (workspace_id, agent_run_id, started_at DESC, id)
    WHERE agent_run_id IS NOT NULL;
CREATE INDEX workflow_executions_trace_idx ON workflow_executions (trace_id, id);

CREATE FUNCTION enforce_workflow_execution_state()
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

CREATE TABLE execution_steps (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    execution_id UUID NOT NULL,
    node_id TEXT NOT NULL,
    node_type TEXT NOT NULL,
    sequence_no INTEGER NOT NULL,
    status TEXT NOT NULL DEFAULT 'QUEUED',
    input_summary JSONB NOT NULL DEFAULT '{}'::JSONB,
    output_summary JSONB NOT NULL DEFAULT '{}'::JSONB,
    raw_object_id UUID,
    started_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at TIMESTAMPTZ,
    error_code TEXT,
    CONSTRAINT execution_steps_workspace_execution_id_key
        UNIQUE (workspace_id, execution_id, id),
    CONSTRAINT execution_steps_execution_sequence_key UNIQUE (execution_id, sequence_no),
    CONSTRAINT execution_steps_workspace_execution_fk
        FOREIGN KEY (workspace_id, execution_id)
        REFERENCES workflow_executions (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT execution_steps_sequence_check CHECK (sequence_no > 0),
    CONSTRAINT execution_steps_status_check CHECK (
        status IN (
            'QUEUED', 'RUNNING', 'WAITING_CONFIRMATION',
            'SUCCEEDED', 'FAILED', 'SKIPPED', 'CANCELLED'
        )
    ),
    CONSTRAINT execution_steps_input_summary_object_check
        CHECK (jsonb_typeof(input_summary) = 'object'),
    CONSTRAINT execution_steps_output_summary_object_check
        CHECK (jsonb_typeof(output_summary) = 'object'),
    CONSTRAINT execution_steps_finish_state_check CHECK (
        (status IN ('QUEUED', 'RUNNING', 'WAITING_CONFIRMATION') AND finished_at IS NULL)
        OR (status IN ('SUCCEEDED', 'FAILED', 'SKIPPED', 'CANCELLED') AND finished_at IS NOT NULL)
    ),
    CONSTRAINT execution_steps_finished_at_check
        CHECK (finished_at IS NULL OR finished_at >= started_at),
    CONSTRAINT execution_steps_error_state_check CHECK (
        (status = 'FAILED' AND error_code IS NOT NULL)
        OR (status <> 'FAILED' AND error_code IS NULL)
    )
);

CREATE INDEX execution_steps_execution_sequence_idx
    ON execution_steps (execution_id, sequence_no, id);
CREATE INDEX execution_steps_workspace_status_started_idx
    ON execution_steps (workspace_id, status, started_at DESC, id);

CREATE FUNCTION enforce_execution_step_state()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'workflow execution steps are permanently retained and cannot be deleted'
            USING ERRCODE = '55000';
    END IF;
    IF ROW(
        NEW.id, NEW.workspace_id, NEW.execution_id, NEW.node_id, NEW.node_type,
        NEW.sequence_no, NEW.input_summary, NEW.started_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.execution_id, OLD.node_id, OLD.node_type,
        OLD.sequence_no, OLD.input_summary, OLD.started_at
    ) THEN
        RAISE EXCEPTION 'workflow execution step identity and start evidence are immutable'
            USING ERRCODE = '55000';
    END IF;
    IF OLD.status IN ('SUCCEEDED', 'FAILED', 'SKIPPED', 'CANCELLED') THEN
        RAISE EXCEPTION 'terminal workflow execution step is immutable'
            USING ERRCODE = '55000';
    END IF;
    IF NEW.status <> OLD.status AND NOT (
        (OLD.status = 'QUEUED' AND NEW.status IN ('RUNNING', 'FAILED', 'SKIPPED', 'CANCELLED'))
        OR (OLD.status = 'RUNNING' AND NEW.status IN (
            'WAITING_CONFIRMATION', 'SUCCEEDED', 'FAILED', 'SKIPPED', 'CANCELLED'
        ))
        OR (OLD.status = 'WAITING_CONFIRMATION' AND NEW.status IN (
            'RUNNING', 'FAILED', 'CANCELLED'
        ))
    ) THEN
        RAISE EXCEPTION 'illegal workflow execution step status transition from % to %',
            OLD.status, NEW.status USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER execution_steps_state_guard
BEFORE UPDATE OR DELETE ON execution_steps
FOR EACH ROW EXECUTE FUNCTION enforce_execution_step_state();

-- Existing trial rows used opaque UUIDs before workflow_executions existed and
-- cannot be linked safely. Old data is explicitly disposable for this refactor.
DELETE FROM workflow_trial_runs
WHERE NOT EXISTS (
    SELECT 1
    FROM workflow_executions
    WHERE workflow_executions.workspace_id = workflow_trial_runs.workspace_id
      AND workflow_executions.id = workflow_trial_runs.execution_id
);

ALTER TABLE workflow_trial_runs
    ADD CONSTRAINT workflow_trial_runs_execution_fk
        FOREIGN KEY (workspace_id, execution_id)
        REFERENCES workflow_executions (workspace_id, id) ON DELETE RESTRICT;
