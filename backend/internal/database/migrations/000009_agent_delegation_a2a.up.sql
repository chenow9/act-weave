-- Agent→Agent delegation bindings, A2A exposure/remote bindings, authoritative
-- agent_run_delegations audit table, and hierarchical agent_run_steps attribution.

-- ---------------------------------------------------------------------------
-- Internal delegation bindings (explicit, directed, disableable)
-- ---------------------------------------------------------------------------
CREATE TABLE agent_delegation_bindings (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    caller_agent_id UUID NOT NULL,
    target_agent_id UUID NOT NULL,
    callable_name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    mode TEXT NOT NULL DEFAULT 'INLINE',
    context_policy TEXT NOT NULL DEFAULT 'TASK_ONLY',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    version BIGINT NOT NULL DEFAULT 1,
    created_by UUID,
    updated_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ,
    CONSTRAINT agent_delegation_bindings_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT agent_delegation_bindings_workspace_id_key
        UNIQUE (workspace_id, id),
    CONSTRAINT agent_delegation_bindings_caller_fk
        FOREIGN KEY (workspace_id, caller_agent_id)
        REFERENCES agents (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT agent_delegation_bindings_target_fk
        FOREIGN KEY (workspace_id, target_agent_id)
        REFERENCES agents (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT agent_delegation_bindings_mode_check
        CHECK (mode IN ('INLINE', 'TASK')),
    -- Only TASK_ONLY is implemented; reject other policies at the DB layer.
    CONSTRAINT agent_delegation_bindings_context_policy_check
        CHECK (context_policy IN ('TASK_ONLY')),
    CONSTRAINT agent_delegation_bindings_callable_name_not_blank
        CHECK (length(btrim(callable_name)) > 0),
    CONSTRAINT agent_delegation_bindings_no_self_loop
        CHECK (caller_agent_id <> target_agent_id),
    CONSTRAINT agent_delegation_bindings_version_check
        CHECK (version > 0)
);

-- Unique callable_name per caller within a workspace among non-deleted bindings.
CREATE UNIQUE INDEX agent_delegation_bindings_caller_alias_uidx
    ON agent_delegation_bindings (workspace_id, caller_agent_id, lower(btrim(callable_name)))
    WHERE deleted_at IS NULL;

CREATE UNIQUE INDEX agent_delegation_bindings_caller_target_uidx
    ON agent_delegation_bindings (workspace_id, caller_agent_id, target_agent_id)
    WHERE deleted_at IS NULL;

CREATE INDEX agent_delegation_bindings_workspace_caller_idx
    ON agent_delegation_bindings (workspace_id, caller_agent_id, enabled)
    WHERE deleted_at IS NULL;

CREATE INDEX agent_delegation_bindings_workspace_target_idx
    ON agent_delegation_bindings (workspace_id, target_agent_id)
    WHERE deleted_at IS NULL;

-- ---------------------------------------------------------------------------
-- A2A inbound: which internal agents are exposed externally
-- ---------------------------------------------------------------------------
CREATE TABLE agent_a2a_exposures (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    public_name TEXT NOT NULL,
    public_description TEXT NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    card_overrides JSONB NOT NULL DEFAULT '{}'::JSONB,
    auth_mode TEXT NOT NULL DEFAULT 'AGENT_ACCESS',
    version BIGINT NOT NULL DEFAULT 1,
    created_by UUID,
    updated_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ,
    CONSTRAINT agent_a2a_exposures_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT agent_a2a_exposures_workspace_id_key
        UNIQUE (workspace_id, id),
    CONSTRAINT agent_a2a_exposures_agent_fk
        FOREIGN KEY (workspace_id, agent_id)
        REFERENCES agents (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT agent_a2a_exposures_auth_mode_check
        CHECK (auth_mode IN ('AGENT_ACCESS', 'NONE')),
    CONSTRAINT agent_a2a_exposures_public_name_not_blank
        CHECK (length(btrim(public_name)) > 0),
    CONSTRAINT agent_a2a_exposures_card_object_check
        CHECK (jsonb_typeof(card_overrides) = 'object'),
    CONSTRAINT agent_a2a_exposures_version_check
        CHECK (version > 0)
);

CREATE UNIQUE INDEX agent_a2a_exposures_agent_uidx
    ON agent_a2a_exposures (workspace_id, agent_id)
    WHERE deleted_at IS NULL;

CREATE INDEX agent_a2a_exposures_workspace_enabled_idx
    ON agent_a2a_exposures (workspace_id, enabled)
    WHERE deleted_at IS NULL;

-- ---------------------------------------------------------------------------
-- A2A outbound: remote agent bindings for internal agents
-- ---------------------------------------------------------------------------
CREATE TABLE agent_a2a_remote_bindings (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    caller_agent_id UUID NOT NULL,
    callable_name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    endpoint_url TEXT NOT NULL,
    agent_card_url TEXT,
    allowed_hosts JSONB NOT NULL DEFAULT '[]'::JSONB,
    auth_secret_ref TEXT,
    timeout_ms INTEGER NOT NULL DEFAULT 60000,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    version BIGINT NOT NULL DEFAULT 1,
    created_by UUID,
    updated_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ,
    CONSTRAINT agent_a2a_remote_bindings_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT agent_a2a_remote_bindings_workspace_id_key
        UNIQUE (workspace_id, id),
    CONSTRAINT agent_a2a_remote_bindings_caller_fk
        FOREIGN KEY (workspace_id, caller_agent_id)
        REFERENCES agents (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT agent_a2a_remote_bindings_callable_name_not_blank
        CHECK (length(btrim(callable_name)) > 0),
    CONSTRAINT agent_a2a_remote_bindings_endpoint_not_blank
        CHECK (length(btrim(endpoint_url)) > 0),
    CONSTRAINT agent_a2a_remote_bindings_timeout_check
        CHECK (timeout_ms > 0 AND timeout_ms <= 600000),
    CONSTRAINT agent_a2a_remote_bindings_allowed_hosts_array_check
        CHECK (jsonb_typeof(allowed_hosts) = 'array'),
    CONSTRAINT agent_a2a_remote_bindings_version_check
        CHECK (version > 0)
);

CREATE UNIQUE INDEX agent_a2a_remote_bindings_caller_alias_uidx
    ON agent_a2a_remote_bindings (workspace_id, caller_agent_id, lower(btrim(callable_name)))
    WHERE deleted_at IS NULL;

CREATE INDEX agent_a2a_remote_bindings_workspace_caller_idx
    ON agent_a2a_remote_bindings (workspace_id, caller_agent_id, enabled)
    WHERE deleted_at IS NULL;

-- ---------------------------------------------------------------------------
-- Authoritative delegation audit rows
-- ---------------------------------------------------------------------------
CREATE TABLE agent_run_delegations (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    parent_run_id UUID NOT NULL,
    child_run_id UUID,
    parent_delegation_id UUID,
    caller_agent_id UUID NOT NULL,
    target_agent_id UUID,
    external_agent_ref TEXT,
    mode TEXT NOT NULL,
    protocol TEXT NOT NULL,
    origin TEXT NOT NULL,
    depth INTEGER NOT NULL DEFAULT 1,
    binding_version BIGINT NOT NULL DEFAULT 1,
    tool_call_id TEXT NOT NULL DEFAULT '',
    idempotency_key TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'PENDING',
    input_summary JSONB NOT NULL DEFAULT '{}'::JSONB,
    input_payload JSONB NOT NULL DEFAULT '{}'::JSONB,
    output_summary JSONB NOT NULL DEFAULT '{}'::JSONB,
    output_payload JSONB NOT NULL DEFAULT '{}'::JSONB,
    error_code TEXT,
    error_message TEXT,
    remote_task_id TEXT,
    remote_context_id TEXT,
    remote_message_id TEXT,
    remote_endpoint_ref TEXT,
    protocol_status TEXT,
    latency_ms BIGINT,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT agent_run_delegations_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT agent_run_delegations_workspace_id_key
        UNIQUE (workspace_id, id),
    CONSTRAINT agent_run_delegations_parent_run_fk
        FOREIGN KEY (workspace_id, parent_run_id)
        REFERENCES agent_runs (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT agent_run_delegations_child_run_fk
        FOREIGN KEY (workspace_id, child_run_id)
        REFERENCES agent_runs (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT agent_run_delegations_parent_delegation_fk
        FOREIGN KEY (workspace_id, parent_delegation_id)
        REFERENCES agent_run_delegations (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT agent_run_delegations_mode_check
        CHECK (mode IN ('INLINE', 'TASK')),
    CONSTRAINT agent_run_delegations_protocol_check
        CHECK (protocol IN ('INTERNAL', 'A2A')),
    CONSTRAINT agent_run_delegations_origin_check
        CHECK (origin IN ('INTERNAL', 'EXTERNAL')),
    CONSTRAINT agent_run_delegations_status_check
        CHECK (status IN (
            'PENDING', 'RUNNING', 'SUCCEEDED', 'FAILED', 'CANCELLED', 'TIMED_OUT'
        )),
    CONSTRAINT agent_run_delegations_depth_check
        CHECK (depth >= 0 AND depth <= 32),
    CONSTRAINT agent_run_delegations_idempotency_not_blank
        CHECK (length(btrim(idempotency_key)) > 0),
    CONSTRAINT agent_run_delegations_input_summary_object_check
        CHECK (jsonb_typeof(input_summary) = 'object'),
    CONSTRAINT agent_run_delegations_input_payload_object_check
        CHECK (jsonb_typeof(input_payload) = 'object'),
    CONSTRAINT agent_run_delegations_output_summary_object_check
        CHECK (jsonb_typeof(output_summary) = 'object'),
    CONSTRAINT agent_run_delegations_output_payload_object_check
        CHECK (jsonb_typeof(output_payload) = 'object'),
    CONSTRAINT agent_run_delegations_finish_state_check CHECK (
        (status IN ('PENDING', 'RUNNING') AND finished_at IS NULL)
        OR (status IN ('SUCCEEDED', 'FAILED', 'CANCELLED', 'TIMED_OUT')
            AND finished_at IS NOT NULL)
    ),
    CONSTRAINT agent_run_delegations_context_policy_future_guard CHECK (true),
    CONSTRAINT agent_run_delegations_target_xor_external CHECK (
        (protocol = 'INTERNAL' AND target_agent_id IS NOT NULL)
        OR (protocol = 'A2A' AND external_agent_ref IS NOT NULL)
        OR (origin = 'EXTERNAL')
    )
);

CREATE UNIQUE INDEX agent_run_delegations_idempotency_uidx
    ON agent_run_delegations (workspace_id, idempotency_key);

CREATE INDEX agent_run_delegations_parent_run_idx
    ON agent_run_delegations (workspace_id, parent_run_id, created_at, id);

CREATE INDEX agent_run_delegations_child_run_idx
    ON agent_run_delegations (workspace_id, child_run_id)
    WHERE child_run_id IS NOT NULL;

CREATE INDEX agent_run_delegations_status_idx
    ON agent_run_delegations (workspace_id, status, created_at DESC);

CREATE INDEX agent_run_delegations_parent_delegation_idx
    ON agent_run_delegations (workspace_id, parent_delegation_id)
    WHERE parent_delegation_id IS NOT NULL;

-- Permanent evidence: no delete; identity immutable after create.
CREATE FUNCTION enforce_agent_run_delegation_permanent_evidence()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'agent run delegations are permanently retained and cannot be deleted'
            USING ERRCODE = '55000';
    END IF;
    IF ROW(
        NEW.id, NEW.workspace_id, NEW.parent_run_id, NEW.caller_agent_id,
        NEW.target_agent_id, NEW.external_agent_ref, NEW.parent_delegation_id,
        NEW.mode, NEW.protocol, NEW.origin, NEW.depth, NEW.binding_version,
        NEW.tool_call_id, NEW.idempotency_key, NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.parent_run_id, OLD.caller_agent_id,
        OLD.target_agent_id, OLD.external_agent_ref, OLD.parent_delegation_id,
        OLD.mode, OLD.protocol, OLD.origin, OLD.depth, OLD.binding_version,
        OLD.tool_call_id, OLD.idempotency_key, OLD.created_at
    ) THEN
        RAISE EXCEPTION 'agent run delegation identity is immutable'
            USING ERRCODE = '55000';
    END IF;
    -- child_run_id may be set once (NULL → value) but not reassigned.
    IF OLD.child_run_id IS NOT NULL AND NEW.child_run_id IS DISTINCT FROM OLD.child_run_id THEN
        RAISE EXCEPTION 'agent run delegation child_run_id is immutable once set'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER agent_run_delegations_permanent_evidence
BEFORE UPDATE OR DELETE ON agent_run_delegations
FOR EACH ROW EXECUTE FUNCTION enforce_agent_run_delegation_permanent_evidence();

-- ---------------------------------------------------------------------------
-- agent_runs: parent linkage + immutable graph snapshot
-- ---------------------------------------------------------------------------
ALTER TABLE agent_runs
    ADD COLUMN IF NOT EXISTS parent_run_id UUID,
    ADD COLUMN IF NOT EXISTS parent_delegation_id UUID,
    ADD COLUMN IF NOT EXISTS agent_graph_snapshot JSONB NOT NULL DEFAULT '{}'::JSONB;

ALTER TABLE agent_runs
    DROP CONSTRAINT IF EXISTS agent_runs_parent_run_fk,
    DROP CONSTRAINT IF EXISTS agent_runs_parent_delegation_fk,
    DROP CONSTRAINT IF EXISTS agent_runs_graph_snapshot_object_check;

ALTER TABLE agent_runs
    ADD CONSTRAINT agent_runs_parent_run_fk
        FOREIGN KEY (workspace_id, parent_run_id)
        REFERENCES agent_runs (workspace_id, id) ON DELETE RESTRICT,
    ADD CONSTRAINT agent_runs_parent_delegation_fk
        FOREIGN KEY (workspace_id, parent_delegation_id)
        REFERENCES agent_run_delegations (workspace_id, id) ON DELETE RESTRICT,
    ADD CONSTRAINT agent_runs_graph_snapshot_object_check
        CHECK (jsonb_typeof(agent_graph_snapshot) = 'object');

CREATE INDEX IF NOT EXISTS agent_runs_parent_run_idx
    ON agent_runs (workspace_id, parent_run_id)
    WHERE parent_run_id IS NOT NULL;

-- Expand status set with TIMED_OUT for long TASK/A2A child runs.
ALTER TABLE agent_runs DROP CONSTRAINT IF EXISTS agent_runs_status_check;
ALTER TABLE agent_runs
    ADD CONSTRAINT agent_runs_status_check CHECK (
        status IN (
            'PENDING', 'RUNNING', 'WAITING_CONFIRMATION', 'WAITING_INTERACTION',
            'SUCCEEDED', 'FAILED', 'CANCELLED', 'TIMED_OUT', 'ACCEPTED'
        )
    );

ALTER TABLE agent_runs DROP CONSTRAINT IF EXISTS agent_runs_finish_state_check;
ALTER TABLE agent_runs
    ADD CONSTRAINT agent_runs_finish_state_check CHECK (
        (status IN ('PENDING', 'RUNNING', 'WAITING_CONFIRMATION', 'WAITING_INTERACTION', 'ACCEPTED')
            AND finished_at IS NULL)
        OR (status IN ('SUCCEEDED', 'FAILED', 'CANCELLED', 'TIMED_OUT')
            AND finished_at IS NOT NULL)
    );

-- Permanent-snapshot trigger must allow RUNNING→TIMED_OUT (and treat TIMED_OUT as
-- terminal) in the same migration that expands status checks. Leaving the old
-- transition matrix until a later revision would leave version=9 DBs unable to
-- finish timed-out TASK/A2A child runs.
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

-- ---------------------------------------------------------------------------
-- agent_run_steps: hierarchical attribution
-- ---------------------------------------------------------------------------
ALTER TABLE agent_run_steps
    ADD COLUMN IF NOT EXISTS agent_id UUID,
    ADD COLUMN IF NOT EXISTS delegation_id UUID,
    ADD COLUMN IF NOT EXISTS parent_step_id UUID;

ALTER TABLE agent_run_steps
    DROP CONSTRAINT IF EXISTS agent_run_steps_delegation_fk,
    DROP CONSTRAINT IF EXISTS agent_run_steps_parent_step_fk;

ALTER TABLE agent_run_steps
    ADD CONSTRAINT agent_run_steps_delegation_fk
        FOREIGN KEY (workspace_id, delegation_id)
        REFERENCES agent_run_delegations (workspace_id, id) ON DELETE RESTRICT,
    ADD CONSTRAINT agent_run_steps_parent_step_fk
        FOREIGN KEY (workspace_id, run_id, parent_step_id)
        REFERENCES agent_run_steps (workspace_id, run_id, id) ON DELETE RESTRICT;

CREATE INDEX IF NOT EXISTS agent_run_steps_delegation_idx
    ON agent_run_steps (workspace_id, delegation_id)
    WHERE delegation_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS agent_run_steps_parent_step_idx
    ON agent_run_steps (workspace_id, run_id, parent_step_id)
    WHERE parent_step_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS agent_run_steps_agent_idx
    ON agent_run_steps (workspace_id, agent_id)
    WHERE agent_id IS NOT NULL;

-- Allow TIMED_OUT on steps (A2A/TASK timeout).
ALTER TABLE agent_run_steps DROP CONSTRAINT IF EXISTS agent_run_steps_status_check;
ALTER TABLE agent_run_steps
    ADD CONSTRAINT agent_run_steps_status_check CHECK (
        status IN (
            'QUEUED', 'RUNNING', 'WAITING_CONFIRMATION',
            'SUCCEEDED', 'FAILED', 'SKIPPED', 'CANCELLED', 'TIMED_OUT'
        )
    );

ALTER TABLE agent_run_steps DROP CONSTRAINT IF EXISTS agent_run_steps_finish_state_check;
ALTER TABLE agent_run_steps
    ADD CONSTRAINT agent_run_steps_finish_state_check CHECK (
        (status IN ('QUEUED', 'RUNNING', 'WAITING_CONFIRMATION') AND finished_at IS NULL)
        OR (status IN ('SUCCEEDED', 'FAILED', 'SKIPPED', 'CANCELLED', 'TIMED_OUT')
            AND finished_at IS NOT NULL)
    );

-- Update permanent evidence trigger function to allow new nullable attribution
-- columns to be set only when previously NULL (first write), otherwise immutable
-- identity still holds for original columns.
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
    -- Attribution columns may be set once (NULL → value) but not reassigned.
    IF OLD.agent_id IS NOT NULL AND NEW.agent_id IS DISTINCT FROM OLD.agent_id THEN
        RAISE EXCEPTION 'agent run step agent_id is immutable once set'
            USING ERRCODE = '55000';
    END IF;
    IF OLD.delegation_id IS NOT NULL AND NEW.delegation_id IS DISTINCT FROM OLD.delegation_id THEN
        RAISE EXCEPTION 'agent run step delegation_id is immutable once set'
            USING ERRCODE = '55000';
    END IF;
    IF OLD.parent_step_id IS NOT NULL AND NEW.parent_step_id IS DISTINCT FROM OLD.parent_step_id THEN
        RAISE EXCEPTION 'agent run step parent_step_id is immutable once set'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;
