ALTER TABLE tool_versions
    ADD CONSTRAINT tool_versions_workspace_capability_version_provider_key
        UNIQUE (workspace_id, capability_id, id, provider_id);

CREATE TABLE tool_invocations (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    tool_id UUID NOT NULL,
    tool_version_id UUID NOT NULL,
    capability_release_id UUID NOT NULL,
    provider_id UUID NOT NULL,
    connection_id UUID,
    execution_lease_id UUID,
    provider_request_id TEXT,
    agent_run_id UUID,
    workflow_execution_id UUID,
    execution_step_id UUID,
    actor_type TEXT NOT NULL,
    actor_id UUID NOT NULL,
    trace_id TEXT NOT NULL,
    idempotency_key TEXT,
    status TEXT NOT NULL DEFAULT 'PENDING',
    input_summary JSONB NOT NULL DEFAULT '{}'::JSONB,
    output_summary JSONB NOT NULL DEFAULT '{}'::JSONB,
    raw_object_id UUID,
    latency_ms BIGINT,
    error_code TEXT,
    started_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at TIMESTAMPTZ,
    CONSTRAINT tool_invocations_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT tool_invocations_workspace_tool_fk
        FOREIGN KEY (workspace_id, tool_id)
        REFERENCES tools (workspace_id, capability_id) ON DELETE RESTRICT,
    CONSTRAINT tool_invocations_workspace_version_provider_fk
        FOREIGN KEY (workspace_id, tool_id, tool_version_id, provider_id)
        REFERENCES tool_versions (
            workspace_id, capability_id, id, provider_id
        ) ON DELETE RESTRICT,
    CONSTRAINT tool_invocations_workspace_release_fk
        FOREIGN KEY (workspace_id, tool_id, capability_release_id)
        REFERENCES capability_releases (workspace_id, capability_id, id) ON DELETE RESTRICT,
    CONSTRAINT tool_invocations_workspace_provider_fk
        FOREIGN KEY (workspace_id, provider_id)
        REFERENCES capability_providers (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT tool_invocations_workspace_connection_fk
        FOREIGN KEY (workspace_id, provider_id, connection_id)
        REFERENCES service_connections (workspace_id, provider_id, id) ON DELETE RESTRICT,
    CONSTRAINT tool_invocations_workspace_agent_run_fk
        FOREIGN KEY (workspace_id, agent_run_id)
        REFERENCES agent_runs (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT tool_invocations_workspace_workflow_execution_fk
        FOREIGN KEY (workspace_id, workflow_execution_id)
        REFERENCES workflow_executions (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT tool_invocations_workspace_execution_step_fk
        FOREIGN KEY (workspace_id, workflow_execution_id, execution_step_id)
        REFERENCES execution_steps (workspace_id, execution_id, id) ON DELETE RESTRICT,
    CONSTRAINT tool_invocations_execution_step_parent_check
        CHECK (execution_step_id IS NULL OR workflow_execution_id IS NOT NULL),
    CONSTRAINT tool_invocations_actor_type_check
        CHECK (actor_type IN ('USER', 'SERVICE_PRINCIPAL', 'SYSTEM')),
    CONSTRAINT tool_invocations_trace_id_not_blank CHECK (length(btrim(trace_id)) > 0),
    CONSTRAINT tool_invocations_idempotency_key_check CHECK (
        idempotency_key IS NULL
        OR (length(btrim(idempotency_key)) > 0 AND length(idempotency_key) <= 255)
    ),
    CONSTRAINT tool_invocations_provider_request_id_check
        CHECK (provider_request_id IS NULL OR length(btrim(provider_request_id)) > 0),
    CONSTRAINT tool_invocations_status_check
        CHECK (status IN ('PENDING', 'RUNNING', 'SUCCEEDED', 'FAILED', 'CANCELLED')),
    CONSTRAINT tool_invocations_input_summary_object_check
        CHECK (jsonb_typeof(input_summary) = 'object'),
    CONSTRAINT tool_invocations_output_summary_object_check
        CHECK (jsonb_typeof(output_summary) = 'object'),
    CONSTRAINT tool_invocations_finish_state_check CHECK (
        (status IN ('PENDING', 'RUNNING') AND finished_at IS NULL AND latency_ms IS NULL)
        OR (status IN ('SUCCEEDED', 'FAILED', 'CANCELLED')
            AND finished_at IS NOT NULL AND latency_ms IS NOT NULL)
    ),
    CONSTRAINT tool_invocations_finished_at_check
        CHECK (finished_at IS NULL OR finished_at >= started_at),
    CONSTRAINT tool_invocations_latency_check CHECK (latency_ms IS NULL OR latency_ms >= 0),
    CONSTRAINT tool_invocations_error_state_check CHECK (
        (status = 'FAILED' AND error_code IS NOT NULL AND length(btrim(error_code)) > 0)
        OR (status <> 'FAILED' AND error_code IS NULL)
    )
);

CREATE UNIQUE INDEX tool_invocations_idempotency_key
    ON tool_invocations (workspace_id, tool_version_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
CREATE INDEX tool_invocations_workspace_started_idx
    ON tool_invocations (workspace_id, started_at DESC, id);
CREATE INDEX tool_invocations_workspace_status_started_idx
    ON tool_invocations (workspace_id, status, started_at DESC, id);
CREATE INDEX tool_invocations_workspace_agent_run_started_idx
    ON tool_invocations (workspace_id, agent_run_id, started_at DESC, id)
    WHERE agent_run_id IS NOT NULL;
CREATE INDEX tool_invocations_workspace_workflow_started_idx
    ON tool_invocations (workspace_id, workflow_execution_id, started_at DESC, id)
    WHERE workflow_execution_id IS NOT NULL;
CREATE INDEX tool_invocations_trace_idx ON tool_invocations (trace_id, id);

CREATE FUNCTION enforce_tool_invocation_integrity()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    parent_agent_run UUID;
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM capability_releases
        WHERE workspace_id = NEW.workspace_id
          AND capability_id = NEW.tool_id
          AND id = NEW.capability_release_id
          AND source_type = 'TOOL_VERSION'
          AND source_id = NEW.tool_version_id
          AND retired_at IS NULL
    ) THEN
        RAISE EXCEPTION 'tool invocation release must resolve to its immutable tool version'
            USING ERRCODE = '23514';
    END IF;
    IF NOT EXISTS (
        SELECT 1
        FROM tool_versions
        WHERE workspace_id = NEW.workspace_id
          AND capability_id = NEW.tool_id
          AND id = NEW.tool_version_id
          AND provider_id = NEW.provider_id
          AND lifecycle_status = 'PUBLISHED'
    ) THEN
        RAISE EXCEPTION 'tool invocation requires a published tool version'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.workflow_execution_id IS NOT NULL AND NEW.agent_run_id IS NOT NULL THEN
        SELECT agent_run_id INTO parent_agent_run
        FROM workflow_executions
        WHERE workspace_id = NEW.workspace_id
          AND id = NEW.workflow_execution_id;
        IF parent_agent_run IS DISTINCT FROM NEW.agent_run_id THEN
            RAISE EXCEPTION 'tool invocation run chain does not match workflow execution parent'
                USING ERRCODE = '23514';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER tool_invocations_integrity
BEFORE INSERT OR UPDATE OF
    workspace_id, tool_id, tool_version_id, capability_release_id, provider_id,
    connection_id, agent_run_id, workflow_execution_id, execution_step_id
ON tool_invocations
FOR EACH ROW EXECUTE FUNCTION enforce_tool_invocation_integrity();

CREATE FUNCTION enforce_tool_invocation_state()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'tool invocations are permanently retained and cannot be deleted'
            USING ERRCODE = '55000';
    END IF;
    IF ROW(
        NEW.id, NEW.workspace_id, NEW.tool_id, NEW.tool_version_id,
        NEW.capability_release_id, NEW.provider_id, NEW.connection_id,
        NEW.execution_lease_id, NEW.agent_run_id, NEW.workflow_execution_id,
        NEW.execution_step_id, NEW.actor_type, NEW.actor_id, NEW.trace_id,
        NEW.idempotency_key, NEW.input_summary, NEW.started_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.tool_id, OLD.tool_version_id,
        OLD.capability_release_id, OLD.provider_id, OLD.connection_id,
        OLD.execution_lease_id, OLD.agent_run_id, OLD.workflow_execution_id,
        OLD.execution_step_id, OLD.actor_type, OLD.actor_id, OLD.trace_id,
        OLD.idempotency_key, OLD.input_summary, OLD.started_at
    ) THEN
        RAISE EXCEPTION 'tool invocation identity and request evidence are immutable'
            USING ERRCODE = '55000';
    END IF;
    IF OLD.status IN ('SUCCEEDED', 'FAILED', 'CANCELLED') THEN
        RAISE EXCEPTION 'terminal tool invocation is immutable'
            USING ERRCODE = '55000';
    END IF;
    IF NEW.status <> OLD.status AND NOT (
        (OLD.status = 'PENDING' AND NEW.status IN ('RUNNING', 'FAILED', 'CANCELLED'))
        OR (OLD.status = 'RUNNING' AND NEW.status IN ('SUCCEEDED', 'FAILED', 'CANCELLED'))
    ) THEN
        RAISE EXCEPTION 'illegal tool invocation status transition from % to %',
            OLD.status, NEW.status USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER tool_invocations_state_guard
BEFORE UPDATE OR DELETE ON tool_invocations
FOR EACH ROW EXECUTE FUNCTION enforce_tool_invocation_state();
