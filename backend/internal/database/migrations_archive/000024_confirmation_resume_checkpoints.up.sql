CREATE TABLE confirmation_resume_checkpoints (
    confirmation_id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    kind TEXT NOT NULL,
    run_id UUID,
    execution_id UUID,
    agent_run_step_id UUID,
    execution_step_id UUID,
    node_id TEXT NOT NULL,
    run_wait_lock_version BIGINT,
    execution_wait_lock_version BIGINT,
    status TEXT NOT NULL DEFAULT 'PENDING',
    snapshot_schema_version TEXT NOT NULL,
    request_snapshot JSONB NOT NULL,
    resolved_snapshot JSONB NOT NULL,
    input_payload JSONB NOT NULL,
    input_hash CHAR(64) NOT NULL,
    plan_hash CHAR(64),
    terminal_on_success BOOLEAN NOT NULL DEFAULT FALSE,
    result_snapshot JSONB NOT NULL DEFAULT '{}'::JSONB,
    error_code TEXT,
    claim_id UUID,
    claim_expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    lock_version BIGINT NOT NULL DEFAULT 1,
    CONSTRAINT confirmation_resume_checkpoints_workspace_id_key
        UNIQUE (workspace_id, confirmation_id),
    CONSTRAINT confirmation_resume_checkpoints_confirmation_fk
        FOREIGN KEY (workspace_id, confirmation_id)
        REFERENCES execution_confirmations (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT confirmation_resume_checkpoints_run_fk
        FOREIGN KEY (workspace_id, run_id)
        REFERENCES agent_runs (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT confirmation_resume_checkpoints_execution_fk
        FOREIGN KEY (workspace_id, execution_id)
        REFERENCES workflow_executions (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT confirmation_resume_checkpoints_agent_step_fk
        FOREIGN KEY (workspace_id, run_id, agent_run_step_id)
        REFERENCES agent_run_steps (workspace_id, run_id, id) ON DELETE RESTRICT,
    CONSTRAINT confirmation_resume_checkpoints_execution_step_fk
        FOREIGN KEY (workspace_id, execution_id, execution_step_id)
        REFERENCES execution_steps (workspace_id, execution_id, id) ON DELETE RESTRICT,
    CONSTRAINT confirmation_resume_checkpoints_kind_check
        CHECK (kind IN ('TOOL', 'WORKFLOW')),
    CONSTRAINT confirmation_resume_checkpoints_target_check
        CHECK (run_id IS NOT NULL OR execution_id IS NOT NULL),
    CONSTRAINT confirmation_resume_checkpoints_agent_step_parent_check
        CHECK (agent_run_step_id IS NULL OR run_id IS NOT NULL),
    CONSTRAINT confirmation_resume_checkpoints_execution_step_parent_check
        CHECK (execution_step_id IS NULL OR execution_id IS NOT NULL),
    CONSTRAINT confirmation_resume_checkpoints_node_not_blank
        CHECK (length(btrim(node_id)) > 0),
    CONSTRAINT confirmation_resume_checkpoints_run_lock_check
        CHECK ((run_id IS NULL) = (run_wait_lock_version IS NULL)
            AND (run_wait_lock_version IS NULL OR run_wait_lock_version > 1)),
    CONSTRAINT confirmation_resume_checkpoints_execution_lock_check
        CHECK ((execution_id IS NULL) = (execution_wait_lock_version IS NULL)
            AND (execution_wait_lock_version IS NULL OR execution_wait_lock_version > 1)),
    CONSTRAINT confirmation_resume_checkpoints_status_check
        CHECK (status IN ('PENDING', 'CLAIMED', 'EXECUTING', 'SUCCEEDED', 'FAILED', 'CANCELLED')),
    CONSTRAINT confirmation_resume_checkpoints_snapshot_version_not_blank
        CHECK (length(btrim(snapshot_schema_version)) > 0),
    CONSTRAINT confirmation_resume_checkpoints_request_object_check
        CHECK (jsonb_typeof(request_snapshot) = 'object'),
    CONSTRAINT confirmation_resume_checkpoints_resolved_object_check
        CHECK (jsonb_typeof(resolved_snapshot) = 'object'),
    CONSTRAINT confirmation_resume_checkpoints_input_object_check
        CHECK (jsonb_typeof(input_payload) = 'object'),
    CONSTRAINT confirmation_resume_checkpoints_result_object_check
        CHECK (jsonb_typeof(result_snapshot) = 'object'),
    CONSTRAINT confirmation_resume_checkpoints_input_hash_check
        CHECK (input_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT confirmation_resume_checkpoints_plan_hash_check
        CHECK (plan_hash IS NULL OR plan_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT confirmation_resume_checkpoints_claim_pair_check
        CHECK ((claim_id IS NULL) = (claim_expires_at IS NULL)),
    CONSTRAINT confirmation_resume_checkpoints_state_check CHECK (
        (status = 'PENDING' AND claim_id IS NULL AND started_at IS NULL
            AND completed_at IS NULL AND error_code IS NULL)
        OR (status = 'CLAIMED' AND claim_id IS NOT NULL AND started_at IS NULL
            AND completed_at IS NULL AND error_code IS NULL)
        OR (status = 'EXECUTING' AND claim_id IS NOT NULL AND started_at IS NOT NULL
            AND completed_at IS NULL AND error_code IS NULL)
        OR (status = 'SUCCEEDED' AND started_at IS NOT NULL
            AND completed_at IS NOT NULL AND error_code IS NULL)
        OR (status = 'FAILED' AND started_at IS NOT NULL
            AND completed_at IS NOT NULL AND error_code IS NOT NULL)
        OR (status = 'CANCELLED' AND completed_at IS NOT NULL AND error_code IS NULL)
    ),
    CONSTRAINT confirmation_resume_checkpoints_times_check CHECK (
        (started_at IS NULL OR started_at >= created_at)
        AND (completed_at IS NULL OR completed_at >= created_at)
        AND (completed_at IS NULL OR started_at IS NULL OR completed_at >= started_at)
    ),
    CONSTRAINT confirmation_resume_checkpoints_lock_check CHECK (lock_version > 0)
);

CREATE INDEX confirmation_resume_checkpoints_workspace_status_created_idx
    ON confirmation_resume_checkpoints (workspace_id, status, created_at DESC, confirmation_id);
CREATE INDEX confirmation_resume_checkpoints_reclaim_idx
    ON confirmation_resume_checkpoints (claim_expires_at, confirmation_id)
    WHERE status = 'CLAIMED';

CREATE FUNCTION enforce_confirmation_resume_checkpoint()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'confirmation resume checkpoints are permanently retained'
            USING ERRCODE = '55000';
    END IF;
    IF ROW(
        NEW.confirmation_id, NEW.workspace_id, NEW.kind, NEW.run_id,
        NEW.execution_id, NEW.agent_run_step_id, NEW.execution_step_id,
        NEW.node_id, NEW.run_wait_lock_version, NEW.execution_wait_lock_version,
        NEW.snapshot_schema_version, NEW.request_snapshot,
        NEW.resolved_snapshot, NEW.input_payload, NEW.input_hash,
        NEW.plan_hash, NEW.terminal_on_success, NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.confirmation_id, OLD.workspace_id, OLD.kind, OLD.run_id,
        OLD.execution_id, OLD.agent_run_step_id, OLD.execution_step_id,
        OLD.node_id, OLD.run_wait_lock_version, OLD.execution_wait_lock_version,
        OLD.snapshot_schema_version, OLD.request_snapshot,
        OLD.resolved_snapshot, OLD.input_payload, OLD.input_hash,
        OLD.plan_hash, OLD.terminal_on_success, OLD.created_at
    ) THEN
        RAISE EXCEPTION 'confirmation resume checkpoint facts are immutable'
            USING ERRCODE = '55000';
    END IF;
    IF OLD.status IN ('SUCCEEDED', 'FAILED', 'CANCELLED') THEN
        RAISE EXCEPTION 'terminal confirmation resume checkpoint is immutable'
            USING ERRCODE = '55000';
    END IF;
    IF NEW.lock_version <> OLD.lock_version + 1 THEN
        RAISE EXCEPTION 'confirmation resume checkpoint requires next lock version'
            USING ERRCODE = '40001';
    END IF;
    IF NEW.status <> OLD.status AND NOT (
        (OLD.status = 'PENDING' AND NEW.status IN ('CLAIMED', 'CANCELLED'))
        OR (OLD.status = 'CLAIMED' AND NEW.status IN ('EXECUTING', 'CANCELLED'))
        OR (OLD.status = 'EXECUTING' AND NEW.status IN ('SUCCEEDED', 'FAILED'))
    ) THEN
        RAISE EXCEPTION 'illegal confirmation resume checkpoint transition from % to %',
            OLD.status, NEW.status USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER confirmation_resume_checkpoints_fact_guard
BEFORE UPDATE OR DELETE ON confirmation_resume_checkpoints
FOR EACH ROW EXECUTE FUNCTION enforce_confirmation_resume_checkpoint();
