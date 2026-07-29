-- Reverses 000002_session_context_contracts. Production rollback must not rely
-- on this destructive path; keep expand-only schema and close rollout gates.

DROP TRIGGER IF EXISTS agent_run_context_assemblies_immutable
    ON agent_run_context_assemblies;
DROP FUNCTION IF EXISTS enforce_agent_run_context_assembly_immutable();
DROP TABLE IF EXISTS agent_run_context_assemblies;

-- Restore the baseline final permanent snapshot function (without agent_snapshot).
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
        NEW.capability_snapshot, NEW.context_policy_snapshot, NEW.authorization_snapshot,
        NEW.snapshot_schema_version, NEW.input_summary, NEW.started_at,
        NEW.principal_snapshot_version, NEW.subject_type, NEW.subject_id, NEW.client_id,
        NEW.grant_id, NEW.grant_version, NEW.agent_policy_version
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.session_id, OLD.agent_id, OLD.trigger_type,
        OLD.triggered_by_type, OLD.triggered_by_id, OLD.trace_id, OLD.model_snapshot,
        OLD.capability_snapshot, OLD.context_policy_snapshot, OLD.authorization_snapshot,
        OLD.snapshot_schema_version, OLD.input_summary, OLD.started_at,
        OLD.principal_snapshot_version, OLD.subject_type, OLD.subject_id, OLD.client_id,
        OLD.grant_id, OLD.grant_version, OLD.agent_policy_version
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

ALTER TABLE agent_runs
    DROP CONSTRAINT IF EXISTS agent_runs_agent_snapshot_object_check;
ALTER TABLE agent_runs
    DROP COLUMN IF EXISTS agent_snapshot;

ALTER TABLE agents
    DROP CONSTRAINT IF EXISTS agents_context_policy_object_check;
ALTER TABLE agents
    DROP COLUMN IF EXISTS context_policy;

ALTER TABLE workspaces
    DROP CONSTRAINT IF EXISTS workspaces_context_policy_object_check;
ALTER TABLE workspaces
    DROP COLUMN IF EXISTS context_policy;

ALTER TABLE model_configs
    DROP CONSTRAINT IF EXISTS model_configs_runtime_capabilities_object_check;
ALTER TABLE model_configs
    DROP COLUMN IF EXISTS runtime_capabilities;
