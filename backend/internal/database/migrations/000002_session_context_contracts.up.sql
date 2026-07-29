-- ZKL-74 / IC-01: expand-only session context window contracts.
-- Defaults keep old binaries and existing rows readable; no runtime gate is
-- enabled by this migration. Assembly manifests store IDs/hashes/budgets only.

ALTER TABLE model_configs
    ADD COLUMN runtime_capabilities JSONB NOT NULL DEFAULT '{}'::JSONB;

ALTER TABLE model_configs
    ADD CONSTRAINT model_configs_runtime_capabilities_object_check
        CHECK (jsonb_typeof(runtime_capabilities) = 'object');

ALTER TABLE workspaces
    ADD COLUMN context_policy JSONB NOT NULL DEFAULT '{}'::JSONB;

ALTER TABLE workspaces
    ADD CONSTRAINT workspaces_context_policy_object_check
        CHECK (jsonb_typeof(context_policy) = 'object');

ALTER TABLE agents
    ADD COLUMN context_policy JSONB NOT NULL DEFAULT '{}'::JSONB;

ALTER TABLE agents
    ADD CONSTRAINT agents_context_policy_object_check
        CHECK (jsonb_typeof(context_policy) = 'object');

ALTER TABLE agent_runs
    ADD COLUMN agent_snapshot JSONB NOT NULL DEFAULT '{}'::JSONB;

ALTER TABLE agent_runs
    ADD CONSTRAINT agent_runs_agent_snapshot_object_check
        CHECK (jsonb_typeof(agent_snapshot) = 'object');

-- Extend the current permanent start-evidence immutability set with agent_snapshot.
-- This replaces the baseline final definition (authorization/principal/lock/status
-- guards) and must not drop those protections.
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

CREATE TABLE agent_run_context_assemblies (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    run_id UUID NOT NULL,
    session_id UUID,
    mode TEXT NOT NULL,
    policy_snapshot_hash CHAR(64) NOT NULL,
    model_snapshot_hash CHAR(64) NOT NULL,
    capability_snapshot_hash CHAR(64) NOT NULL,
    agent_snapshot_hash CHAR(64) NOT NULL,
    estimator_profile TEXT NOT NULL,
    estimator_version TEXT NOT NULL,
    hard_input_ceiling_tokens BIGINT NOT NULL,
    output_reserve_tokens BIGINT NOT NULL,
    safety_margin_tokens BIGINT NOT NULL,
    tools_overhead_tokens BIGINT NOT NULL,
    system_prompt_revision_id UUID,
    system_prompt_hash CHAR(64) NOT NULL,
    included_segments JSONB NOT NULL DEFAULT '[]'::JSONB,
    omitted_prefix_start_message_id UUID,
    omitted_prefix_end_message_id UUID,
    omitted_prefix_count INTEGER NOT NULL DEFAULT 0,
    summary_id UUID,
    summary_hash CHAR(64),
    summary_coverage JSONB,
    assembly_digest CHAR(64) NOT NULL,
    estimated_total_tokens BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT agent_run_context_assemblies_workspace_run_key
        UNIQUE (workspace_id, run_id),
    CONSTRAINT agent_run_context_assemblies_workspace_id_id_key
        UNIQUE (workspace_id, id),
    CONSTRAINT agent_run_context_assemblies_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT agent_run_context_assemblies_workspace_run_fk
        FOREIGN KEY (workspace_id, run_id)
        REFERENCES agent_runs (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT agent_run_context_assemblies_workspace_session_fk
        FOREIGN KEY (workspace_id, session_id)
        REFERENCES chat_sessions (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT agent_run_context_assemblies_mode_not_blank
        CHECK (length(btrim(mode)) > 0),
    CONSTRAINT agent_run_context_assemblies_estimator_profile_not_blank
        CHECK (length(btrim(estimator_profile)) > 0),
    CONSTRAINT agent_run_context_assemblies_estimator_version_not_blank
        CHECK (length(btrim(estimator_version)) > 0),
    CONSTRAINT agent_run_context_assemblies_policy_hash_check
        CHECK (policy_snapshot_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT agent_run_context_assemblies_model_hash_check
        CHECK (model_snapshot_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT agent_run_context_assemblies_capability_hash_check
        CHECK (capability_snapshot_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT agent_run_context_assemblies_agent_hash_check
        CHECK (agent_snapshot_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT agent_run_context_assemblies_system_hash_check
        CHECK (system_prompt_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT agent_run_context_assemblies_digest_check
        CHECK (assembly_digest ~ '^[0-9a-f]{64}$'),
    CONSTRAINT agent_run_context_assemblies_summary_hash_check
        CHECK (summary_hash IS NULL OR summary_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT agent_run_context_assemblies_budget_non_negative_check
        CHECK (
            hard_input_ceiling_tokens >= 0
            AND output_reserve_tokens >= 0
            AND safety_margin_tokens >= 0
            AND tools_overhead_tokens >= 0
            AND estimated_total_tokens >= 0
            AND omitted_prefix_count >= 0
        ),
    CONSTRAINT agent_run_context_assemblies_included_segments_array_check
        CHECK (jsonb_typeof(included_segments) = 'array'),
    CONSTRAINT agent_run_context_assemblies_summary_coverage_object_check
        CHECK (summary_coverage IS NULL OR jsonb_typeof(summary_coverage) = 'object'),
    CONSTRAINT agent_run_context_assemblies_summary_pair_check
        CHECK (
            (summary_id IS NULL AND summary_hash IS NULL AND summary_coverage IS NULL)
            OR (summary_id IS NOT NULL AND summary_hash IS NOT NULL)
        ),
    CONSTRAINT agent_run_context_assemblies_omitted_boundary_pair_check
        CHECK (
            (omitted_prefix_start_message_id IS NULL)
            = (omitted_prefix_end_message_id IS NULL)
        )
);

CREATE INDEX agent_run_context_assemblies_workspace_created_idx
    ON agent_run_context_assemblies (workspace_id, created_at DESC, id);
CREATE INDEX agent_run_context_assemblies_workspace_session_created_idx
    ON agent_run_context_assemblies (workspace_id, session_id, created_at DESC, id)
    WHERE session_id IS NOT NULL;

CREATE FUNCTION enforce_agent_run_context_assembly_immutable()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'agent run context assemblies are permanently retained and cannot be deleted'
            USING ERRCODE = '55000';
    END IF;
    RAISE EXCEPTION 'agent run context assemblies are immutable'
        USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER agent_run_context_assemblies_immutable
BEFORE UPDATE OR DELETE ON agent_run_context_assemblies
FOR EACH ROW EXECUTE FUNCTION enforce_agent_run_context_assembly_immutable();
