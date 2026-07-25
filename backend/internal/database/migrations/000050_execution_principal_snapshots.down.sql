DO $$
BEGIN
    IF EXISTS(SELECT 1 FROM agent_runs WHERE principal_snapshot_version='execution.principal.v1' AND triggered_by_type='SERVICE_PRINCIPAL')
       OR EXISTS(SELECT 1 FROM workflow_executions WHERE principal_snapshot_version='execution.principal.v1' AND triggered_by_type='SERVICE_PRINCIPAL')
       OR EXISTS(SELECT 1 FROM tool_invocations WHERE principal_snapshot_version='execution.principal.v1' AND actor_type='SERVICE_PRINCIPAL') THEN
        RAISE EXCEPTION 'cannot rollback Principal snapshots with external permanent execution facts'
            USING ERRCODE='55000';
    END IF;
END;
$$;

DROP INDEX tool_invocations_workspace_principal_started_idx;
DROP INDEX workflow_executions_workspace_principal_started_idx;
DROP INDEX agent_runs_workspace_principal_started_idx;
DROP TRIGGER tool_invocations_principal_snapshot_guard ON tool_invocations;
DROP TRIGGER workflow_executions_principal_snapshot_guard ON workflow_executions;
DROP TRIGGER agent_runs_principal_snapshot_guard ON agent_runs;
DROP FUNCTION validate_execution_principal_snapshot();
DROP FUNCTION execution_authorization_envelope_matches(JSONB,UUID,TEXT,UUID,TEXT,UUID,UUID,UUID,BIGINT,BIGINT);

-- Restore the version-49 state guards before dropping the added columns.
CREATE OR REPLACE FUNCTION enforce_agent_run_permanent_snapshot()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='DELETE' THEN RAISE EXCEPTION 'agent runs are permanently retained and cannot be deleted' USING ERRCODE='55000'; END IF;
    IF ROW(NEW.id,NEW.workspace_id,NEW.session_id,NEW.agent_id,NEW.trigger_type,
        NEW.triggered_by_type,NEW.triggered_by_id,NEW.trace_id,NEW.model_snapshot,
        NEW.capability_snapshot,NEW.context_policy_snapshot,NEW.authorization_snapshot,
        NEW.snapshot_schema_version,NEW.input_summary,NEW.started_at)
       IS DISTINCT FROM ROW(OLD.id,OLD.workspace_id,OLD.session_id,OLD.agent_id,OLD.trigger_type,
        OLD.triggered_by_type,OLD.triggered_by_id,OLD.trace_id,OLD.model_snapshot,
        OLD.capability_snapshot,OLD.context_policy_snapshot,OLD.authorization_snapshot,
        OLD.snapshot_schema_version,OLD.input_summary,OLD.started_at) THEN
        RAISE EXCEPTION 'agent run identity and start snapshots are immutable' USING ERRCODE='55000';
    END IF;
    IF OLD.status IN ('SUCCEEDED','FAILED','CANCELLED') THEN RAISE EXCEPTION 'terminal agent run is immutable' USING ERRCODE='55000'; END IF;
    IF NEW.lock_version<>OLD.lock_version+1 THEN RAISE EXCEPTION 'agent run update requires the next lock version' USING ERRCODE='40001'; END IF;
    IF NEW.status<>OLD.status AND NOT ((OLD.status='PENDING' AND NEW.status IN ('RUNNING','FAILED','CANCELLED')) OR (OLD.status='RUNNING' AND NEW.status IN ('WAITING_CONFIRMATION','SUCCEEDED','FAILED','CANCELLED')) OR (OLD.status='WAITING_CONFIRMATION' AND NEW.status IN ('RUNNING','FAILED','CANCELLED'))) THEN RAISE EXCEPTION 'illegal agent run status transition from % to %',OLD.status,NEW.status USING ERRCODE='55000'; END IF;
    RETURN NEW;
END; $$;

CREATE OR REPLACE FUNCTION enforce_workflow_execution_state()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='DELETE' THEN RAISE EXCEPTION 'workflow executions are permanently retained and cannot be deleted' USING ERRCODE='55000'; END IF;
    IF ROW(NEW.id,NEW.workspace_id,NEW.workflow_id,NEW.revision_id,NEW.compilation_id,
        NEW.agent_run_id,NEW.trigger_type,NEW.triggered_by_type,NEW.triggered_by_id,
        NEW.trace_id,NEW.input_summary,NEW.started_at)
       IS DISTINCT FROM ROW(OLD.id,OLD.workspace_id,OLD.workflow_id,OLD.revision_id,OLD.compilation_id,
        OLD.agent_run_id,OLD.trigger_type,OLD.triggered_by_type,OLD.triggered_by_id,
        OLD.trace_id,OLD.input_summary,OLD.started_at) THEN
        RAISE EXCEPTION 'workflow execution identity and start evidence are immutable' USING ERRCODE='55000';
    END IF;
    IF OLD.status IN ('SUCCEEDED','FAILED','CANCELLED') THEN RAISE EXCEPTION 'terminal workflow execution is immutable' USING ERRCODE='55000'; END IF;
    IF NEW.lock_version<>OLD.lock_version+1 THEN RAISE EXCEPTION 'workflow execution update requires the next lock version' USING ERRCODE='40001'; END IF;
    IF NEW.status<>OLD.status AND NOT ((OLD.status='PENDING' AND NEW.status IN ('RUNNING','FAILED','CANCELLED')) OR (OLD.status='RUNNING' AND NEW.status IN ('WAITING_CONFIRMATION','SUCCEEDED','FAILED','CANCELLED')) OR (OLD.status='WAITING_CONFIRMATION' AND NEW.status IN ('RUNNING','FAILED','CANCELLED'))) THEN RAISE EXCEPTION 'illegal workflow execution status transition from % to %',OLD.status,NEW.status USING ERRCODE='55000'; END IF;
    RETURN NEW;
END; $$;

CREATE OR REPLACE FUNCTION enforce_tool_invocation_state()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='DELETE' THEN RAISE EXCEPTION 'tool invocations are permanently retained and cannot be deleted' USING ERRCODE='55000'; END IF;
    IF ROW(NEW.id,NEW.workspace_id,NEW.tool_id,NEW.tool_version_id,NEW.capability_release_id,
        NEW.provider_id,NEW.connection_id,NEW.execution_lease_id,NEW.agent_run_id,
        NEW.workflow_execution_id,NEW.execution_step_id,NEW.actor_type,NEW.actor_id,
        NEW.trace_id,NEW.idempotency_key,NEW.input_summary,NEW.started_at)
       IS DISTINCT FROM ROW(OLD.id,OLD.workspace_id,OLD.tool_id,OLD.tool_version_id,OLD.capability_release_id,
        OLD.provider_id,OLD.connection_id,OLD.execution_lease_id,OLD.agent_run_id,
        OLD.workflow_execution_id,OLD.execution_step_id,OLD.actor_type,OLD.actor_id,
        OLD.trace_id,OLD.idempotency_key,OLD.input_summary,OLD.started_at) THEN
        RAISE EXCEPTION 'tool invocation identity and request evidence are immutable' USING ERRCODE='55000';
    END IF;
    IF OLD.status IN ('SUCCEEDED','FAILED','CANCELLED') THEN RAISE EXCEPTION 'terminal tool invocation is immutable' USING ERRCODE='55000'; END IF;
    IF NEW.status<>OLD.status AND NOT ((OLD.status='PENDING' AND NEW.status IN ('RUNNING','FAILED','CANCELLED')) OR (OLD.status='RUNNING' AND NEW.status IN ('SUCCEEDED','FAILED','CANCELLED'))) THEN RAISE EXCEPTION 'illegal tool invocation status transition from % to %',OLD.status,NEW.status USING ERRCODE='55000'; END IF;
    RETURN NEW;
END; $$;

ALTER TABLE tool_invocations
    DROP CONSTRAINT tool_invocations_authorization_snapshot_object_check,
    DROP CONSTRAINT tool_invocations_external_version_pair_check,
    DROP CONSTRAINT tool_invocations_grant_scope_fk,
    DROP CONSTRAINT tool_invocations_client_scope_fk,
    DROP CONSTRAINT tool_invocations_subject_ref_fk,
    DROP CONSTRAINT tool_invocations_subject_pair_check,
    DROP CONSTRAINT tool_invocations_principal_snapshot_version_check,
    DROP COLUMN authorization_snapshot,DROP COLUMN agent_policy_version,
    DROP COLUMN grant_version,DROP COLUMN grant_id,DROP COLUMN client_id,
    DROP COLUMN subject_id,DROP COLUMN subject_type,DROP COLUMN principal_snapshot_version;

ALTER TABLE workflow_executions
    DROP CONSTRAINT workflow_executions_external_version_pair_check,
    DROP CONSTRAINT workflow_executions_grant_scope_fk,
    DROP CONSTRAINT workflow_executions_client_scope_fk,
    DROP CONSTRAINT workflow_executions_subject_ref_fk,
    DROP CONSTRAINT workflow_executions_subject_pair_check,
    DROP CONSTRAINT workflow_executions_principal_snapshot_version_check,
    DROP COLUMN agent_policy_version,DROP COLUMN grant_version,DROP COLUMN grant_id,
    DROP COLUMN client_id,DROP COLUMN subject_id,DROP COLUMN subject_type,
    DROP COLUMN principal_snapshot_version;

ALTER TABLE agent_runs
    DROP CONSTRAINT agent_runs_external_version_pair_check,
    DROP CONSTRAINT agent_runs_grant_scope_fk,
    DROP CONSTRAINT agent_runs_client_scope_fk,
    DROP CONSTRAINT agent_runs_subject_ref_fk,
    DROP CONSTRAINT agent_runs_subject_pair_check,
    DROP CONSTRAINT agent_runs_principal_snapshot_version_check,
    DROP COLUMN agent_policy_version,DROP COLUMN grant_version,DROP COLUMN grant_id,
    DROP COLUMN client_id,DROP COLUMN subject_id,DROP COLUMN subject_type,
    DROP COLUMN principal_snapshot_version;
