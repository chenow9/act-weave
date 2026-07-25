ALTER TABLE capability_releases
    ADD CONSTRAINT capability_releases_workspace_id_key UNIQUE (workspace_id, id);

CREATE TABLE agent_runs (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    session_id UUID,
    agent_id UUID NOT NULL,
    status TEXT NOT NULL DEFAULT 'PENDING',
    trigger_type TEXT NOT NULL,
    triggered_by_type TEXT NOT NULL,
    triggered_by_id UUID NOT NULL,
    trace_id TEXT NOT NULL,
    model_snapshot JSONB NOT NULL,
    capability_snapshot JSONB NOT NULL,
    context_policy_snapshot JSONB NOT NULL DEFAULT '{}'::JSONB,
    input_summary JSONB NOT NULL DEFAULT '{}'::JSONB,
    output_summary JSONB NOT NULL DEFAULT '{}'::JSONB,
    error_code TEXT,
    started_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at TIMESTAMPTZ,
    lock_version BIGINT NOT NULL DEFAULT 1,
    CONSTRAINT agent_runs_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT agent_runs_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT agent_runs_workspace_session_fk
        FOREIGN KEY (workspace_id, session_id)
        REFERENCES chat_sessions (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT agent_runs_workspace_agent_fk
        FOREIGN KEY (workspace_id, agent_id)
        REFERENCES agents (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT agent_runs_status_check CHECK (
        status IN (
            'PENDING', 'RUNNING', 'WAITING_CONFIRMATION',
            'SUCCEEDED', 'FAILED', 'CANCELLED'
        )
    ),
    CONSTRAINT agent_runs_trigger_type_not_blank CHECK (length(btrim(trigger_type)) > 0),
    CONSTRAINT agent_runs_triggered_by_type_check
        CHECK (triggered_by_type IN ('USER', 'SERVICE_PRINCIPAL', 'SYSTEM')),
    CONSTRAINT agent_runs_trace_id_not_blank CHECK (length(btrim(trace_id)) > 0),
    CONSTRAINT agent_runs_model_snapshot_object_check
        CHECK (jsonb_typeof(model_snapshot) = 'object'),
    CONSTRAINT agent_runs_capability_snapshot_object_check
        CHECK (jsonb_typeof(capability_snapshot) = 'object'),
    CONSTRAINT agent_runs_context_snapshot_object_check
        CHECK (jsonb_typeof(context_policy_snapshot) = 'object'),
    CONSTRAINT agent_runs_input_summary_object_check
        CHECK (jsonb_typeof(input_summary) = 'object'),
    CONSTRAINT agent_runs_output_summary_object_check
        CHECK (jsonb_typeof(output_summary) = 'object'),
    CONSTRAINT agent_runs_finish_state_check CHECK (
        (status IN ('PENDING', 'RUNNING', 'WAITING_CONFIRMATION') AND finished_at IS NULL)
        OR (status IN ('SUCCEEDED', 'FAILED', 'CANCELLED') AND finished_at IS NOT NULL)
    ),
    CONSTRAINT agent_runs_finished_at_check
        CHECK (finished_at IS NULL OR finished_at >= started_at),
    CONSTRAINT agent_runs_error_state_check CHECK (
        (status = 'FAILED' AND error_code IS NOT NULL)
        OR (status <> 'FAILED' AND error_code IS NULL)
    ),
    CONSTRAINT agent_runs_lock_version_check CHECK (lock_version > 0)
);

CREATE INDEX agent_runs_workspace_started_idx
    ON agent_runs (workspace_id, started_at DESC, id);
CREATE INDEX agent_runs_workspace_status_started_idx
    ON agent_runs (workspace_id, status, started_at DESC, id);
CREATE INDEX agent_runs_workspace_session_started_idx
    ON agent_runs (workspace_id, session_id, started_at DESC, id)
    WHERE session_id IS NOT NULL;
CREATE INDEX agent_runs_trace_idx ON agent_runs (trace_id, id);

CREATE FUNCTION enforce_agent_run_permanent_snapshot()
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
        NEW.input_summary, NEW.started_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.session_id, OLD.agent_id,
        OLD.trigger_type, OLD.triggered_by_type, OLD.triggered_by_id, OLD.trace_id,
        OLD.model_snapshot, OLD.capability_snapshot, OLD.context_policy_snapshot,
        OLD.input_summary, OLD.started_at
    ) THEN
        RAISE EXCEPTION 'agent run identity and start snapshots are immutable'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER agent_runs_permanent_snapshot
BEFORE UPDATE OR DELETE ON agent_runs
FOR EACH ROW EXECUTE FUNCTION enforce_agent_run_permanent_snapshot();

CREATE TABLE agent_run_steps (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    run_id UUID NOT NULL,
    sequence_no INTEGER NOT NULL,
    step_type TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'QUEUED',
    capability_release_id UUID,
    input_summary JSONB NOT NULL DEFAULT '{}'::JSONB,
    output_summary JSONB NOT NULL DEFAULT '{}'::JSONB,
    raw_object_id UUID,
    started_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at TIMESTAMPTZ,
    error_code TEXT,
    CONSTRAINT agent_run_steps_workspace_run_id_key UNIQUE (workspace_id, run_id, id),
    CONSTRAINT agent_run_steps_run_sequence_key UNIQUE (run_id, sequence_no),
    CONSTRAINT agent_run_steps_workspace_run_fk
        FOREIGN KEY (workspace_id, run_id)
        REFERENCES agent_runs (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT agent_run_steps_workspace_release_fk
        FOREIGN KEY (workspace_id, capability_release_id)
        REFERENCES capability_releases (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT agent_run_steps_sequence_check CHECK (sequence_no > 0),
    CONSTRAINT agent_run_steps_type_not_blank CHECK (length(btrim(step_type)) > 0),
    CONSTRAINT agent_run_steps_status_check CHECK (
        status IN (
            'QUEUED', 'RUNNING', 'WAITING_CONFIRMATION',
            'SUCCEEDED', 'FAILED', 'SKIPPED', 'CANCELLED'
        )
    ),
    CONSTRAINT agent_run_steps_input_summary_object_check
        CHECK (jsonb_typeof(input_summary) = 'object'),
    CONSTRAINT agent_run_steps_output_summary_object_check
        CHECK (jsonb_typeof(output_summary) = 'object'),
    CONSTRAINT agent_run_steps_finish_state_check CHECK (
        (status IN ('QUEUED', 'RUNNING', 'WAITING_CONFIRMATION') AND finished_at IS NULL)
        OR (status IN ('SUCCEEDED', 'FAILED', 'SKIPPED', 'CANCELLED') AND finished_at IS NOT NULL)
    ),
    CONSTRAINT agent_run_steps_finished_at_check
        CHECK (finished_at IS NULL OR finished_at >= started_at),
    CONSTRAINT agent_run_steps_error_state_check CHECK (
        (status = 'FAILED' AND error_code IS NOT NULL)
        OR (status <> 'FAILED' AND error_code IS NULL)
    )
);

CREATE INDEX agent_run_steps_workspace_run_sequence_idx
    ON agent_run_steps (workspace_id, run_id, sequence_no, id);
CREATE INDEX agent_run_steps_workspace_status_started_idx
    ON agent_run_steps (workspace_id, status, started_at DESC, id);

CREATE FUNCTION enforce_agent_run_step_permanent_evidence()
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

CREATE TRIGGER agent_run_steps_permanent_evidence
BEFORE UPDATE OR DELETE ON agent_run_steps
FOR EACH ROW EXECUTE FUNCTION enforce_agent_run_step_permanent_evidence();

ALTER TABLE chat_sessions
    ADD CONSTRAINT chat_sessions_latest_run_fk
        FOREIGN KEY (workspace_id, latest_run_id)
        REFERENCES agent_runs (workspace_id, id)
        ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE chat_messages
    ADD CONSTRAINT chat_messages_run_fk
        FOREIGN KEY (workspace_id, run_id)
        REFERENCES agent_runs (workspace_id, id) ON DELETE RESTRICT;
