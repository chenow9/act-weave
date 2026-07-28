-- Reverse of the squashed baseline (historical downs 000061 → 000001).
-- Intended for test isolation / local rollback only, not production restore.


-- ##########################################################################
-- Source: 000061_agent_prompt_preview_retention.down.sql
-- ##########################################################################

-- ZKL-69 down: fail closed when any preview retention data exists.
-- Application rollback keeps this schema once CREATE_PREVIEW or preview objects exist.

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM stored_objects
        WHERE kind IN ('PROMPT_PREVIEW_INPUT', 'PROMPT_PREVIEW_OUTPUT')
    ) THEN
        RAISE EXCEPTION
            'cannot down-migrate 000061: PROMPT_PREVIEW_* stored objects exist (expand-only after use)'
            USING ERRCODE = '55000';
    END IF;

    IF EXISTS (
        SELECT 1 FROM prompt_runs WHERE operation_type = 'CREATE_PREVIEW'
    ) THEN
        RAISE EXCEPTION
            'cannot down-migrate 000061: CREATE_PREVIEW prompt runs exist (expand-only after use)'
            USING ERRCODE = '55000';
    END IF;

    IF EXISTS (
        SELECT 1 FROM agent_prompt_revisions WHERE source = 'AI_ASSISTED'
    ) THEN
        RAISE EXCEPTION
            'cannot down-migrate 000061: AI_ASSISTED prompt revisions exist (expand-only after use)'
            USING ERRCODE = '55000';
    END IF;

    IF EXISTS (
        SELECT 1 FROM prompt_runs
        WHERE expires_at IS NOT NULL
           OR promoted_at IS NOT NULL
           OR content_purged_at IS NOT NULL
    ) THEN
        RAISE EXCEPTION
            'cannot down-migrate 000061: prompt_runs retention columns are populated'
            USING ERRCODE = '55000';
    END IF;

    IF EXISTS (
        SELECT 1 FROM stored_objects
        WHERE body_purged_at IS NOT NULL
           OR purge_claim_token IS NOT NULL
           OR purge_attempts <> 0
           OR purge_next_attempt_at IS NOT NULL
           OR purge_last_error_code IS NOT NULL
    ) THEN
        RAISE EXCEPTION
            'cannot down-migrate 000061: stored_objects purge columns are populated'
            USING ERRCODE = '55000';
    END IF;
END $$;

CREATE OR REPLACE FUNCTION enforce_prompt_run_permanent_content()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'prompt runs are permanently retained and cannot be deleted'
            USING ERRCODE = '55000';
    END IF;
    IF ROW(
        NEW.id, NEW.workspace_id, NEW.agent_id, NEW.operation_type,
        NEW.model_config_id, NEW.model_snapshot, NEW.input_object_id,
        NEW.input_sha256, NEW.input_length, NEW.trace_id, NEW.created_by,
        NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.agent_id, OLD.operation_type,
        OLD.model_config_id, OLD.model_snapshot, OLD.input_object_id,
        OLD.input_sha256, OLD.input_length, OLD.trace_id, OLD.created_by,
        OLD.created_at
    ) THEN
        RAISE EXCEPTION 'prompt run input evidence is immutable'
            USING ERRCODE = '55000';
    END IF;
    IF OLD.output_object_id IS NOT NULL AND ROW(
        NEW.output_object_id, NEW.output_sha256, NEW.output_length
    ) IS DISTINCT FROM ROW(
        OLD.output_object_id, OLD.output_sha256, OLD.output_length
    ) THEN
        RAISE EXCEPTION 'prompt run output evidence is immutable'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

ALTER TABLE prompt_runs
    DROP CONSTRAINT IF EXISTS prompt_runs_content_purged_at_check,
    DROP CONSTRAINT IF EXISTS prompt_runs_promoted_at_check,
    DROP CONSTRAINT IF EXISTS prompt_runs_create_preview_lifecycle_check,
    DROP CONSTRAINT IF EXISTS prompt_runs_operation_check;

ALTER TABLE prompt_runs
    DROP COLUMN IF EXISTS content_purged_at,
    DROP COLUMN IF EXISTS promoted_at,
    DROP COLUMN IF EXISTS expires_at;

ALTER TABLE prompt_runs
    ADD CONSTRAINT prompt_runs_operation_check
        CHECK (operation_type IN ('ENHANCE', 'GENERATE', 'PREVIEW'));

CREATE OR REPLACE FUNCTION enforce_stored_object_metadata()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        RAISE EXCEPTION 'stored object metadata is immutable'
            USING ERRCODE = '55000';
    END IF;
    IF TG_OP = 'DELETE' AND OLD.retention_mode = 'PERMANENT' THEN
        RAISE EXCEPTION 'permanent stored object metadata cannot be deleted'
            USING ERRCODE = '55000';
    END IF;
    IF TG_OP = 'DELETE' AND OLD.retention_until > clock_timestamp() THEN
        RAISE EXCEPTION 'stored object retention has not expired'
            USING ERRCODE = '55000';
    END IF;
    RETURN OLD;
END;
$$;

DROP INDEX IF EXISTS stored_objects_preview_purge_claim_idx;

ALTER TABLE stored_objects
    DROP CONSTRAINT IF EXISTS stored_objects_preview_content_policy_check,
    DROP CONSTRAINT IF EXISTS stored_objects_permanent_content_policy_check,
    DROP CONSTRAINT IF EXISTS stored_objects_body_purged_at_check,
    DROP CONSTRAINT IF EXISTS stored_objects_purge_error_code_check,
    DROP CONSTRAINT IF EXISTS stored_objects_purge_claim_pair_check,
    DROP CONSTRAINT IF EXISTS stored_objects_purge_attempts_check,
    DROP CONSTRAINT IF EXISTS stored_objects_kind_check;

ALTER TABLE stored_objects
    DROP COLUMN IF EXISTS purge_last_error_code,
    DROP COLUMN IF EXISTS purge_next_attempt_at,
    DROP COLUMN IF EXISTS purge_attempts,
    DROP COLUMN IF EXISTS purge_claim_expires_at,
    DROP COLUMN IF EXISTS purge_claim_token,
    DROP COLUMN IF EXISTS body_purged_at;

ALTER TABLE stored_objects
    ADD CONSTRAINT stored_objects_kind_check CHECK (kind IN (
        'OPENAPI_SOURCE', 'PROMPT_RUN_INPUT', 'PROMPT_RUN_OUTPUT', 'MODEL_TURN',
        'CHAT_MESSAGE', 'TOOL_TEST_PAYLOAD', 'TOOL_INVOCATION_PAYLOAD',
        'EXECUTION_CHECKPOINT', 'AUDIT_EVENT_PAYLOAD', 'AUDIT_EXPORT'
    )),
    ADD CONSTRAINT stored_objects_permanent_content_policy_check CHECK (
        kind NOT IN (
            'PROMPT_RUN_INPUT', 'PROMPT_RUN_OUTPUT', 'MODEL_TURN', 'CHAT_MESSAGE',
            'TOOL_TEST_PAYLOAD', 'TOOL_INVOCATION_PAYLOAD', 'EXECUTION_CHECKPOINT'
        )
        OR (
            classification IN ('SENSITIVE', 'RESTRICTED')
            AND retention_mode = 'PERMANENT'
            AND retention_until IS NULL
        )
    );

ALTER TABLE agent_prompt_revisions
    DROP CONSTRAINT IF EXISTS agent_prompt_revisions_source_check;

ALTER TABLE agent_prompt_revisions
    ADD CONSTRAINT agent_prompt_revisions_source_check
        CHECK (source IN ('MANUAL', 'ENHANCED', 'GENERATED', 'IMPORTED'));


-- ##########################################################################
-- Source: 000060_outbound_identity_hard_cutover.down.sql
-- ##########################################################################

-- 000060 down: reversible SCHEMA only.
-- Intentionally does NOT restore:
--   - deleted secrets / secret_versions / ciphertext / nonces / key references
--   - cleared credential_secret_id values on target connections
--   - pre-cutover connection status or migration_state
-- Production must never rely on this down path after a successful up commit.
-- Disaster recovery that restores a pre-cutover infrastructure snapshot must
-- re-run 000060 and pass delete proofs before reopening traffic.

DROP TABLE IF EXISTS outbound_runtime_affinities;
DROP TABLE IF EXISTS outbound_runtime_instances;

DROP INDEX IF EXISTS service_connections_machine_credential_secret_idx;
DROP INDEX IF EXISTS service_connections_workspace_migration_state_idx;

ALTER TABLE service_connections
    DROP CONSTRAINT IF EXISTS service_connections_machine_credential_secret_fk,
    DROP CONSTRAINT IF EXISTS service_connections_migration_state_check,
    DROP CONSTRAINT IF EXISTS service_connections_outbound_identity_policy_version_check,
    DROP CONSTRAINT IF EXISTS service_connections_outbound_identity_object_check;

ALTER TABLE service_connections
    DROP COLUMN IF EXISTS machine_credential_secret_id,
    DROP COLUMN IF EXISTS migration_state,
    DROP COLUMN IF EXISTS outbound_identity_policy_version,
    DROP COLUMN IF EXISTS outbound_identity;

ALTER TABLE capability_providers
    DROP CONSTRAINT IF EXISTS capability_providers_outbound_identity_policy_version_check;

ALTER TABLE capability_providers
    DROP COLUMN IF EXISTS outbound_identity_policy_version;


-- ##########################################################################
-- Source: 000059_workflow_generate_sessions.down.sql
-- ##########################################################################

DROP INDEX IF EXISTS workflow_generate_turns_workspace_created_idx;
DROP INDEX IF EXISTS workflow_generate_turns_workspace_session_index_idx;
DROP TABLE IF EXISTS workflow_generate_turns;

DROP INDEX IF EXISTS workflow_generate_sessions_workspace_workflow_idx;
DROP INDEX IF EXISTS workflow_generate_sessions_workspace_agent_updated_idx;
DROP INDEX IF EXISTS workflow_generate_sessions_workspace_status_updated_idx;
DROP TABLE IF EXISTS workflow_generate_sessions;


-- ##########################################################################
-- Source: 000058_eino_checkpoints.down.sql
-- ##########################################################################

DROP INDEX IF EXISTS eino_checkpoints_owner_idx;
DROP INDEX IF EXISTS eino_checkpoints_expires_at_idx;
DROP INDEX IF EXISTS eino_checkpoints_workspace_id_idx;
DROP TABLE IF EXISTS eino_checkpoints;


-- ##########################################################################
-- Source: 000057_runtime_continuation_claims.down.sql
-- ##########################################################################

DROP INDEX IF EXISTS runtime_continuation_claims_reclaim_idx;
DROP TABLE IF EXISTS runtime_continuation_claims;


-- ##########################################################################
-- Source: 000056_subject_token_jtis.down.sql
-- ##########################################################################

DROP TRIGGER IF EXISTS agent_access_subject_token_jtis_immutable ON agent_access_subject_token_jtis;
DROP FUNCTION IF EXISTS enforce_agent_access_subject_token_jti_immutable();
DROP TABLE IF EXISTS agent_access_subject_token_jtis;


-- ##########################################################################
-- Source: 000055_trusted_subject_issuer.down.sql
-- ##########################################################################

ALTER TABLE agent_access_clients
    DROP CONSTRAINT IF EXISTS agent_access_clients_subject_claim_policy_check;

ALTER TABLE agent_access_clients
    DROP CONSTRAINT IF EXISTS agent_access_clients_subject_algorithms_check;

ALTER TABLE agent_access_clients
    DROP CONSTRAINT IF EXISTS agent_access_clients_subject_inline_jwks_check;

ALTER TABLE agent_access_clients
    DROP CONSTRAINT IF EXISTS agent_access_clients_subject_jwks_uri_check;

ALTER TABLE agent_access_clients
    DROP CONSTRAINT IF EXISTS agent_access_clients_subject_audience_check;

ALTER TABLE agent_access_clients
    DROP CONSTRAINT IF EXISTS agent_access_clients_subject_issuer_check;

ALTER TABLE agent_access_clients
    DROP CONSTRAINT IF EXISTS agent_access_clients_subject_trust_presence_check;

ALTER TABLE agent_access_clients
    DROP COLUMN IF EXISTS trusted_subject_claim_policy,
    DROP COLUMN IF EXISTS trusted_subject_algorithms,
    DROP COLUMN IF EXISTS trusted_subject_inline_jwks,
    DROP COLUMN IF EXISTS trusted_subject_audience;

-- Restore migration 41 pair constraints. Any expanded M9 trust config is cleared
-- on downgrade because audience/algorithms/claim policy cannot be represented.
UPDATE agent_access_clients
SET
    trusted_subject_issuer = NULL,
    trusted_subject_jwks_uri = NULL
WHERE trusted_subject_issuer IS NOT NULL
   OR trusted_subject_jwks_uri IS NOT NULL;

ALTER TABLE agent_access_clients
    ADD CONSTRAINT agent_access_clients_subject_trust_pair_check CHECK (
        (trusted_subject_issuer IS NULL) = (trusted_subject_jwks_uri IS NULL)
    );

ALTER TABLE agent_access_clients
    ADD CONSTRAINT agent_access_clients_subject_issuer_check CHECK (
        trusted_subject_issuer IS NULL OR (
            length(trusted_subject_issuer) <= 2048
            AND btrim(trusted_subject_issuer) = trusted_subject_issuer
            AND trusted_subject_issuer ~ '^https://[^[:space:]?#]+/?$'
        )
    );

ALTER TABLE agent_access_clients
    ADD CONSTRAINT agent_access_clients_subject_jwks_uri_check CHECK (
        trusted_subject_jwks_uri IS NULL OR (
            length(trusted_subject_jwks_uri) <= 2048
            AND btrim(trusted_subject_jwks_uri) = trusted_subject_jwks_uri
            AND trusted_subject_jwks_uri ~ '^https://[^[:space:]#]+$'
        )
    );

DROP FUNCTION IF EXISTS agent_access_subject_inline_jwks_valid(JSONB);
DROP FUNCTION IF EXISTS agent_access_subject_claim_policy_valid(JSONB);
DROP FUNCTION IF EXISTS agent_access_subject_algorithms_valid(JSONB);


-- ##########################################################################
-- Source: 000054_agent_access_data_commands.down.sql
-- ##########################################################################

DROP TABLE IF EXISTS agent_access_data_commands;


-- ##########################################################################
-- Source: 000053_interaction_decision_binding.down.sql
-- ##########################################################################

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM interaction_decision_commands)
       OR EXISTS (SELECT 1 FROM execution_confirmations WHERE target_item_id IS NOT NULL)
       OR EXISTS (SELECT 1 FROM confirmation_resume_checkpoints WHERE target_item_id IS NOT NULL) THEN
        RAISE EXCEPTION 'cannot remove interaction decision binding while bound interactions exist'
            USING ERRCODE = '55000';
    END IF;
END;
$$;

DROP TRIGGER IF EXISTS interaction_decision_commands_fact_guard
    ON interaction_decision_commands;
DROP FUNCTION IF EXISTS enforce_interaction_decision_command_fact();
DROP TABLE IF EXISTS interaction_decision_commands;

DROP TRIGGER IF EXISTS confirmation_resume_interaction_binding_guard
    ON confirmation_resume_checkpoints;
DROP FUNCTION IF EXISTS enforce_confirmation_resume_interaction_binding();
ALTER TABLE confirmation_resume_checkpoints
    DROP CONSTRAINT IF EXISTS confirmation_resume_interaction_binding_hash_check,
    DROP CONSTRAINT IF EXISTS confirmation_resume_interaction_binding_pair_check,
    DROP COLUMN IF EXISTS interaction_binding_hash,
    DROP COLUMN IF EXISTS target_item_id;

DROP TRIGGER IF EXISTS execution_confirmations_interaction_binding_guard
    ON execution_confirmations;
DROP FUNCTION IF EXISTS enforce_interaction_confirmation_binding();
ALTER TABLE execution_confirmations
    DROP CONSTRAINT IF EXISTS execution_confirmations_interaction_binding_hash_check,
    DROP CONSTRAINT IF EXISTS execution_confirmations_interaction_binding_pair_check,
    DROP COLUMN IF EXISTS interaction_binding_hash,
    DROP COLUMN IF EXISTS target_item_id;


-- ##########################################################################
-- Source: 000052_subject_ownership_policy.down.sql
-- ##########################################################################

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM agent_access_grants WHERE policy ? 'subjectSharing'
    ) THEN
        RAISE EXCEPTION 'cannot remove Subject Sharing policy while Grants use it'
            USING ERRCODE='23514',CONSTRAINT='agent_access_subject_sharing_rollback_blocked';
    END IF;
END;
$$;

CREATE OR REPLACE FUNCTION agent_access_grant_policy_valid(policy JSONB)
RETURNS BOOLEAN
LANGUAGE plpgsql
IMMUTABLE
PARALLEL SAFE
STRICT
AS $$
DECLARE
    service_decision JSONB;
BEGIN
    IF jsonb_typeof(policy) <> 'object' OR EXISTS (
        SELECT 1 FROM jsonb_object_keys(policy) AS key
        WHERE key <> 'serviceDecision'
    ) THEN
        RETURN FALSE;
    END IF;
    IF NOT policy ? 'serviceDecision' THEN
        RETURN TRUE;
    END IF;
    service_decision := policy->'serviceDecision';
    IF jsonb_typeof(service_decision) <> 'object'
       OR NOT service_decision ? 'enabled'
       OR jsonb_typeof(service_decision->'enabled') <> 'boolean'
       OR EXISTS (
            SELECT 1 FROM jsonb_object_keys(service_decision) AS key
            WHERE key NOT IN ('enabled', 'maxRisk')
       ) THEN
        RETURN FALSE;
    END IF;
    IF (service_decision->>'enabled')::BOOLEAN THEN
        RETURN service_decision->>'maxRisk' IN ('low', 'medium')
               AND jsonb_typeof(service_decision->'maxRisk') = 'string';
    END IF;
    RETURN NOT service_decision ? 'maxRisk';
END;
$$;



-- ##########################################################################
-- Source: 000051_principal_aware_confirmations.down.sql
-- ##########################################################################

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM execution_confirmations
        WHERE request_principal_snapshot_version<>'legacy.v1'
    ) THEN
        RAISE EXCEPTION 'cannot remove Principal-aware confirmation columns after modern confirmation facts exist'
            USING ERRCODE='23514',CONSTRAINT='execution_confirmation_principal_rollback_blocked';
    END IF;
END;
$$;

DROP TRIGGER IF EXISTS execution_confirmations_chat_projection_sync ON execution_confirmations;
DROP FUNCTION IF EXISTS synchronize_chat_confirmation_projection();
DROP TRIGGER IF EXISTS chat_confirmations_projection_guard ON chat_confirmations;
DROP FUNCTION IF EXISTS enforce_chat_confirmation_projection();
DROP TRIGGER IF EXISTS execution_confirmations_fact_guard ON execution_confirmations;
DROP FUNCTION IF EXISTS validate_execution_confirmation_principal();
DROP INDEX IF EXISTS execution_confirmations_workspace_decision_principal_idx;
DROP INDEX IF EXISTS execution_confirmations_workspace_request_principal_idx;

ALTER TABLE chat_confirmations
    DROP CONSTRAINT chat_confirmations_confirmed_state_check,
    ADD CONSTRAINT chat_confirmations_confirmed_state_check CHECK (
        (status='CONFIRMED' AND confirmed_by IS NOT NULL AND confirmed_at IS NOT NULL)
        OR (status<>'CONFIRMED' AND confirmed_by IS NULL AND confirmed_at IS NULL)
    );

ALTER TABLE execution_confirmations
    DROP CONSTRAINT execution_confirmations_decision_grant_fk,
    DROP CONSTRAINT execution_confirmations_decision_client_fk,
    DROP CONSTRAINT execution_confirmations_decision_subject_ref_fk,
    DROP CONSTRAINT execution_confirmations_decision_actor_ref_fk,
    DROP CONSTRAINT execution_confirmations_request_grant_fk,
    DROP CONSTRAINT execution_confirmations_request_client_fk,
    DROP CONSTRAINT execution_confirmations_request_subject_ref_fk,
    DROP CONSTRAINT execution_confirmations_request_actor_ref_fk,
    DROP CONSTRAINT execution_confirmations_principal_state_check,
    DROP CONSTRAINT execution_confirmations_confirmed_by_projection_check,
    DROP CONSTRAINT execution_confirmations_requested_by_projection_check,
    DROP CONSTRAINT execution_confirmations_decision_policy_object_check,
    DROP CONSTRAINT execution_confirmations_decision_snapshot_pair_check,
    DROP CONSTRAINT execution_confirmations_request_external_pair_check,
    DROP CONSTRAINT execution_confirmations_request_subject_pair_check,
    DROP CONSTRAINT execution_confirmations_request_snapshot_version_check,
    DROP COLUMN decision_policy_snapshot,
    DROP COLUMN decision_agent_policy_version,
    DROP COLUMN decision_grant_version,
    DROP COLUMN decision_grant_id,
    DROP COLUMN decision_client_id,
    DROP COLUMN decision_subject_id,
    DROP COLUMN decision_subject_type,
    DROP COLUMN decision_actor_id,
    DROP COLUMN decision_actor_type,
    DROP COLUMN decision_principal_snapshot_version,
    DROP COLUMN request_agent_policy_version,
    DROP COLUMN request_grant_version,
    DROP COLUMN request_grant_id,
    DROP COLUMN request_client_id,
    DROP COLUMN request_subject_id,
    DROP COLUMN request_subject_type,
    DROP COLUMN request_actor_id,
    DROP COLUMN request_actor_type,
    DROP COLUMN request_principal_snapshot_version,
    ALTER COLUMN requested_by SET NOT NULL,
    ADD CONSTRAINT execution_confirmations_requester_check
        CHECK(confirmed_by IS NULL OR confirmed_by=requested_by),
    ADD CONSTRAINT execution_confirmations_state_check CHECK (
        (status='PENDING' AND confirmed_by IS NULL AND confirmed_at IS NULL AND cancelled_at IS NULL)
        OR (status='CONFIRMED' AND confirmed_by=requested_by
            AND confirmed_at IS NOT NULL AND cancelled_at IS NULL)
        OR (status='CANCELLED' AND confirmed_by IS NULL
            AND confirmed_at IS NULL AND cancelled_at IS NOT NULL)
        OR (status='EXPIRED' AND confirmed_by IS NULL
            AND confirmed_at IS NULL AND cancelled_at IS NULL)
    );

CREATE FUNCTION enforce_execution_confirmation_fact()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    parent_run UUID;
BEGIN
    IF TG_OP='DELETE' THEN
        RAISE EXCEPTION 'execution confirmations are permanently retained' USING ERRCODE='55000';
    END IF;
    IF TG_OP='UPDATE' AND ROW(
        NEW.id,NEW.workspace_id,NEW.execution_id,NEW.run_id,NEW.node_id,
        NEW.reason,NEW.risk_reasons,NEW.scope_snapshot,NEW.release_id,
        NEW.input_hash,NEW.connection_id,NEW.plan_hash,NEW.resume_token_hash,
        NEW.requested_by,NEW.created_at,NEW.expires_at
    ) IS DISTINCT FROM ROW(
        OLD.id,OLD.workspace_id,OLD.execution_id,OLD.run_id,OLD.node_id,
        OLD.reason,OLD.risk_reasons,OLD.scope_snapshot,OLD.release_id,
        OLD.input_hash,OLD.connection_id,OLD.plan_hash,OLD.resume_token_hash,
        OLD.requested_by,OLD.created_at,OLD.expires_at
    ) THEN
        RAISE EXCEPTION 'execution confirmation request snapshot is immutable' USING ERRCODE='55000';
    END IF;
    IF NEW.execution_id IS NOT NULL AND NEW.run_id IS NOT NULL THEN
        SELECT agent_run_id INTO parent_run FROM workflow_executions
        WHERE workspace_id=NEW.workspace_id AND id=NEW.execution_id;
        IF parent_run IS DISTINCT FROM NEW.run_id THEN
            RAISE EXCEPTION 'confirmation execution and run chain mismatch' USING ERRCODE='23514';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER execution_confirmations_fact_guard
BEFORE INSERT OR UPDATE OR DELETE ON execution_confirmations
FOR EACH ROW EXECUTE FUNCTION enforce_execution_confirmation_fact();

CREATE FUNCTION enforce_chat_confirmation_projection()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    execution_row execution_confirmations%ROWTYPE;
BEGIN
    IF TG_OP='DELETE' THEN
        RAISE EXCEPTION 'chat confirmations are permanently retained' USING ERRCODE='55000';
    END IF;
    SELECT * INTO execution_row FROM execution_confirmations
    WHERE workspace_id=NEW.workspace_id AND id=NEW.execution_confirmation_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'chat confirmation execution target not found' USING ERRCODE='23503';
    END IF;
    IF NEW.run_id IS DISTINCT FROM execution_row.run_id
       OR NEW.target_release_id IS DISTINCT FROM execution_row.release_id THEN
        RAISE EXCEPTION 'chat confirmation target differs from execution confirmation' USING ERRCODE='23514';
    END IF;
    IF NEW.status IS DISTINCT FROM execution_row.status
       OR NEW.confirmed_by IS DISTINCT FROM execution_row.confirmed_by
       OR NEW.confirmed_at IS DISTINCT FROM execution_row.confirmed_at THEN
        RAISE EXCEPTION 'chat confirmation state is derived from execution confirmation' USING ERRCODE='55000';
    END IF;
    IF TG_OP='UPDATE' AND ROW(
        NEW.id,NEW.workspace_id,NEW.session_id,NEW.run_id,
        NEW.execution_confirmation_id,NEW.target_type,NEW.target_release_id,
        NEW.risk_level,NEW.risk_reasons,NEW.input_summary,NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.id,OLD.workspace_id,OLD.session_id,OLD.run_id,
        OLD.execution_confirmation_id,OLD.target_type,OLD.target_release_id,
        OLD.risk_level,OLD.risk_reasons,OLD.input_summary,OLD.created_at
    ) THEN
        RAISE EXCEPTION 'chat confirmation display mapping is immutable' USING ERRCODE='55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER chat_confirmations_projection_guard
BEFORE INSERT OR UPDATE OR DELETE ON chat_confirmations
FOR EACH ROW EXECUTE FUNCTION enforce_chat_confirmation_projection();

CREATE FUNCTION synchronize_chat_confirmation_projection()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    chat_confirmation_id UUID;
BEGIN
    UPDATE chat_confirmations
    SET status=NEW.status,confirmed_by=NEW.confirmed_by,confirmed_at=NEW.confirmed_at
    WHERE workspace_id=NEW.workspace_id AND execution_confirmation_id=NEW.id
    RETURNING id INTO chat_confirmation_id;
    IF chat_confirmation_id IS NOT NULL AND NEW.status<>'PENDING' THEN
        UPDATE chat_sessions SET pending_confirmation_id=NULL,
            updated_at=clock_timestamp(),lock_version=lock_version+1
        WHERE workspace_id=NEW.workspace_id AND pending_confirmation_id=chat_confirmation_id;
        UPDATE chat_messages
        SET status=CASE WHEN NEW.status='CONFIRMED' THEN 'PROCESSING' ELSE 'FAILED' END
        WHERE workspace_id=NEW.workspace_id AND confirmation_id=chat_confirmation_id
          AND status='PENDING_CONFIRMATION';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER execution_confirmations_chat_projection_sync
AFTER UPDATE OF status,confirmed_by,confirmed_at ON execution_confirmations
FOR EACH ROW WHEN (OLD.status IS DISTINCT FROM NEW.status)
EXECUTE FUNCTION synchronize_chat_confirmation_projection();


-- ##########################################################################
-- Source: 000050_execution_principal_snapshots.down.sql
-- ##########################################################################

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


-- ##########################################################################
-- Source: 000049_principal_aware_chat_ownership.down.sql
-- ##########################################################################

-- A rollback cannot losslessly represent external Chat facts in the old User
-- FK model. Fail before changing schema instead of deleting permanent data or
-- manufacturing a User.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM chat_sessions WHERE actor_type <> 'USER')
       OR EXISTS (SELECT 1 FROM chat_messages WHERE actor_type='SERVICE_PRINCIPAL') THEN
        RAISE EXCEPTION 'cannot rollback Principal-aware Chat with external facts'
            USING ERRCODE='55000';
    END IF;
END;
$$;

DROP INDEX chat_sessions_workspace_client_agent_updated_idx;
DROP INDEX chat_sessions_workspace_owner_updated_idx;
CREATE INDEX chat_sessions_workspace_creator_updated_idx
    ON chat_sessions(workspace_id,created_by,status,updated_at DESC,id);

DROP TRIGGER chat_messages_principal_ownership_guard ON chat_messages;
DROP FUNCTION validate_chat_message_principal_ownership();
DROP TRIGGER chat_sessions_ownership_immutable_guard ON chat_sessions;
DROP FUNCTION reject_chat_session_ownership_mutation();
DROP TRIGGER chat_sessions_principal_ownership_guard ON chat_sessions;
DROP FUNCTION validate_chat_session_principal_ownership();

CREATE OR REPLACE FUNCTION enforce_chat_message_permanent_retention()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'chat messages are permanently retained and cannot be deleted'
            USING ERRCODE = '55000';
    END IF;
    IF ROW(
        NEW.id,NEW.workspace_id,NEW.session_id,NEW.role,NEW.content,
        NEW.content_object_id,NEW.content_sha256,NEW.content_length,
        NEW.created_by,NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.id,OLD.workspace_id,OLD.session_id,OLD.role,OLD.content,
        OLD.content_object_id,OLD.content_sha256,OLD.content_length,
        OLD.created_by,OLD.created_at
    ) THEN
        RAISE EXCEPTION 'chat message original content and identity are immutable'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

ALTER TABLE chat_messages
    DROP CONSTRAINT chat_messages_ownership_policy_version_check,
    DROP CONSTRAINT chat_messages_ownership_mode_check,
    DROP CONSTRAINT chat_messages_subject_pair_check,
    DROP CONSTRAINT chat_messages_client_scope_fk,
    DROP CONSTRAINT chat_messages_subject_ref_fk,
    DROP CONSTRAINT chat_messages_actor_ref_fk,
    ADD CONSTRAINT chat_messages_user_actor_check
        CHECK(role <> 'USER' OR created_by IS NOT NULL),
    DROP COLUMN ownership_policy_version,
    DROP COLUMN ownership_mode,
    DROP COLUMN client_id,
    DROP COLUMN subject_id,
    DROP COLUMN subject_type,
    DROP COLUMN actor_id,
    DROP COLUMN actor_type;

ALTER TABLE chat_sessions
    DROP CONSTRAINT chat_sessions_ownership_policy_version_check,
    DROP CONSTRAINT chat_sessions_ownership_mode_check,
    DROP CONSTRAINT chat_sessions_subject_pair_check,
    DROP CONSTRAINT chat_sessions_client_scope_fk,
    DROP CONSTRAINT chat_sessions_subject_ref_fk,
    DROP CONSTRAINT chat_sessions_actor_ref_fk,
    ALTER COLUMN created_by SET NOT NULL,
    DROP COLUMN ownership_policy_version,
    DROP COLUMN ownership_mode,
    DROP COLUMN client_id,
    DROP COLUMN subject_id,
    DROP COLUMN subject_type,
    DROP COLUMN actor_id,
    DROP COLUMN actor_type;

CREATE OR REPLACE FUNCTION register_directory_principal_ref()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_TABLE_NAME = 'workspaces' THEN
        INSERT INTO principal_refs(workspace_id,principal_type,principal_id,origin,created_at)
        VALUES(NEW.id,'USER',NEW.owner_user_id,'DIRECTORY',NEW.created_at)
        ON CONFLICT DO NOTHING;
    ELSIF TG_TABLE_NAME = 'workspace_members' THEN
        INSERT INTO principal_refs(workspace_id,principal_type,principal_id,origin,created_at)
        VALUES(NEW.workspace_id,'USER',NEW.user_id,'DIRECTORY',NEW.joined_at)
        ON CONFLICT DO NOTHING;
    ELSIF TG_TABLE_NAME = 'service_principals' THEN
        INSERT INTO principal_refs(workspace_id,principal_type,principal_id,origin,created_at)
        VALUES(NEW.workspace_id,'SERVICE_PRINCIPAL',NEW.id,'DIRECTORY',NEW.created_at)
        ON CONFLICT DO NOTHING;
    ELSIF TG_TABLE_NAME = 'external_subjects' THEN
        INSERT INTO principal_refs(workspace_id,principal_type,principal_id,origin,created_at)
        VALUES(NEW.workspace_id,'EXTERNAL_SUBJECT',NEW.id,'DIRECTORY',NEW.created_at)
        ON CONFLICT DO NOTHING;
    ELSE
        RAISE EXCEPTION 'Unsupported Principal directory source'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER principal_refs_immutable_guard ON principal_refs;
DELETE FROM principal_refs
WHERE principal_type='SYSTEM'
  AND principal_id='00000000-0000-0000-0000-000000000001'::UUID
  AND system_key='actweave:chat-runtime';
CREATE TRIGGER principal_refs_immutable_guard
BEFORE UPDATE OR DELETE ON principal_refs
FOR EACH ROW EXECUTE FUNCTION reject_principal_ref_mutation();


-- ##########################################################################
-- Source: 000048_principal_refs.down.sql
-- ##########################################################################

ALTER TABLE tool_invocations
    DROP CONSTRAINT tool_invocations_principal_ref_fk;
ALTER TABLE workflow_executions
    DROP CONSTRAINT workflow_executions_principal_ref_fk;
ALTER TABLE agent_runs
    DROP CONSTRAINT agent_runs_principal_ref_fk;

DROP TRIGGER external_subjects_principal_ref ON external_subjects;
DROP TRIGGER service_principals_principal_ref ON service_principals;
DROP TRIGGER workspace_members_principal_ref ON workspace_members;
DROP TRIGGER workspaces_owner_principal_ref ON workspaces;
DROP FUNCTION register_directory_principal_ref();

DROP TRIGGER principal_refs_immutable_guard ON principal_refs;
DROP FUNCTION reject_principal_ref_mutation();
DROP TRIGGER principal_refs_target_guard ON principal_refs;
DROP FUNCTION validate_principal_ref_target();

DROP TABLE principal_refs;


-- ##########################################################################
-- Source: 000047_agent_access_token_ttl.down.sql
-- ##########################################################################

ALTER TABLE agent_access_clients
    DROP CONSTRAINT agent_access_clients_token_ttl_check;

ALTER TABLE agent_access_clients
    ADD CONSTRAINT agent_access_clients_token_ttl_check
    CHECK (token_ttl_seconds BETWEEN 60 AND 900);


-- ##########################################################################
-- Source: 000046_agent_access_client_assertion_jtis.down.sql
-- ##########################################################################

DROP TRIGGER IF EXISTS agent_access_client_assertion_jtis_immutable
    ON agent_access_client_assertion_jtis;
DROP FUNCTION IF EXISTS enforce_agent_access_client_assertion_jti_immutable();
DROP TABLE IF EXISTS agent_access_client_assertion_jtis;



-- ##########################################################################
-- Source: 000045_agent_access_management_commands.down.sql
-- ##########################################################################

DROP TABLE IF EXISTS agent_access_management_commands;
DROP FUNCTION IF EXISTS enforce_agent_access_management_command_lifecycle();


-- ##########################################################################
-- Source: 000044_external_subjects.down.sql
-- ##########################################################################

DROP TABLE IF EXISTS external_subjects;
DROP FUNCTION IF EXISTS enforce_external_subject_identity();



-- ##########################################################################
-- Source: 000043_agent_access_grants.down.sql
-- ##########################################################################

DROP TABLE IF EXISTS agent_access_grants;
DROP FUNCTION IF EXISTS enforce_agent_access_grant_window();
DROP FUNCTION IF EXISTS agent_access_grant_policy_valid(JSONB);
DROP FUNCTION IF EXISTS agent_access_grant_scopes_valid(JSONB);



-- ##########################################################################
-- Source: 000042_agent_access_credentials.down.sql
-- ##########################################################################

DROP TABLE IF EXISTS agent_access_credentials;
DROP FUNCTION IF EXISTS enforce_agent_access_credential_evidence();



-- ##########################################################################
-- Source: 000041_agent_access_clients.down.sql
-- ##########################################################################

DROP TABLE IF EXISTS agent_access_clients;
DROP TABLE IF EXISTS service_principals;
DROP FUNCTION IF EXISTS agent_access_cors_origins_valid(JSONB);



-- ##########################################################################
-- Source: 000040_run_events_cutover.down.sql
-- ##########################################################################

DROP TRIGGER IF EXISTS run_events_cutover_complete ON run_events;
DROP FUNCTION IF EXISTS reject_legacy_run_event_insert();

ALTER TABLE run_events DISABLE TRIGGER run_events_fact_guard;
INSERT INTO run_events(
    id,workspace_id,run_id,sequence_no,event_type,payload,terminal,created_at
)
SELECT
    pe.id,pe.workspace_id,pe.run_id,pe.sequence_no,
    pe.payload->'data'->>'legacyEventType',
    pe.payload->'data'->'legacyPayload',
    (pe.payload->'data'->>'legacyEventType') IN (
        'RUN_COMPLETED','RUN_FAILED','RUN_CANCELLED'
    ),
    pe.occurred_at
FROM protocol_events pe
WHERE pe.payload->'data' ? 'legacyEventType'
ON CONFLICT (id) DO NOTHING;
ALTER TABLE run_events ENABLE TRIGGER run_events_fact_guard;

DROP TRIGGER IF EXISTS protocol_events_immutable ON protocol_events;
DELETE FROM protocol_events
WHERE payload->'data' ? 'legacyEventType';
DELETE FROM protocol_event_streams pes
WHERE NOT EXISTS (
    SELECT 1 FROM protocol_events pe WHERE pe.stream_id=pes.id
)
AND EXISTS (
    SELECT 1 FROM run_events re
    WHERE re.workspace_id=pes.workspace_id AND re.run_id=pes.run_id
);
CREATE TRIGGER protocol_events_immutable
BEFORE UPDATE OR DELETE ON protocol_events
FOR EACH ROW EXECUTE FUNCTION reject_protocol_event_mutation();


-- ##########################################################################
-- Source: 000039_protocol_event_envelope_guards.down.sql
-- ##########################################################################

DROP TRIGGER IF EXISTS protocol_events_immutable ON protocol_events;
DROP FUNCTION IF EXISTS reject_protocol_event_mutation();

DROP TRIGGER IF EXISTS protocol_events_validate_envelope ON protocol_events;
DROP FUNCTION IF EXISTS validate_protocol_event_envelope();


-- ##########################################################################
-- Source: 000038_run_items.down.sql
-- ##########################################################################

DROP TABLE IF EXISTS run_items;

ALTER TABLE agent_runs
    DROP CONSTRAINT IF EXISTS agent_runs_workspace_agent_id_key;


-- ##########################################################################
-- Source: 000037_protocol_events.down.sql
-- ##########################################################################

DROP TABLE IF EXISTS protocol_events;
DROP TABLE IF EXISTS protocol_event_streams;

ALTER TABLE agent_runs
    DROP CONSTRAINT IF EXISTS agent_runs_workspace_agent_session_id_key;
ALTER TABLE chat_sessions
    DROP CONSTRAINT IF EXISTS chat_sessions_workspace_agent_id_key;


-- ##########################################################################
-- Source: 000036_provider_sync_running_uniqueness.down.sql
-- ##########################################################################

DROP INDEX IF EXISTS provider_sync_runs_provider_running_key;


-- ##########################################################################
-- Source: 000035_workspace_slug_reuse_after_soft_delete.down.sql
-- ##########################################################################

DROP INDEX IF EXISTS workspaces_slug_active_key;

ALTER TABLE workspaces
    ADD CONSTRAINT workspaces_slug_key UNIQUE (slug);


-- ##########################################################################
-- Source: 000034_workflow_trial_execution_sources.down.sql
-- ##########################################################################

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


-- ##########################################################################
-- Source: 000033_outbox_claim_lease.down.sql
-- ##########################################################################

DROP INDEX IF EXISTS outbox_events_claimable_idx;

ALTER TABLE outbox_events
    DROP CONSTRAINT IF EXISTS outbox_events_claim_lease_check,
    DROP COLUMN IF EXISTS claim_expires_at,
    DROP COLUMN IF EXISTS claimed_at,
    DROP COLUMN IF EXISTS claim_token;


-- ##########################################################################
-- Source: 000032_transactional_outbox_contract.down.sql
-- ##########################################################################

ALTER TABLE outbox_events
    DROP CONSTRAINT IF EXISTS outbox_events_payload_schema_version_check,
    DROP CONSTRAINT outbox_events_timestamps_check,
    ALTER COLUMN created_at SET DEFAULT CURRENT_TIMESTAMP;

UPDATE outbox_events
SET created_at = occurred_at
WHERE created_at < occurred_at;

ALTER TABLE outbox_events
    ADD CONSTRAINT outbox_events_timestamps_check CHECK (
        available_at >= occurred_at
        AND created_at >= occurred_at
        AND (published_at IS NULL OR published_at >= occurred_at)
    );


-- ##########################################################################
-- Source: 000031_audit_payload_policy.down.sql
-- ##########################################################################

DROP TRIGGER IF EXISTS audit_events_payload_guard ON audit_events;
DROP FUNCTION IF EXISTS enforce_audit_event_payload_reference();

ALTER TABLE stored_objects
    DROP CONSTRAINT IF EXISTS stored_objects_audit_event_payload_policy_check;


-- ##########################################################################
-- Source: 000030_audit_outbox_exports.down.sql
-- ##########################################################################

DROP TRIGGER IF EXISTS audit_exports_object_guard ON audit_exports;
DROP FUNCTION IF EXISTS enforce_audit_export_object();
DROP TABLE IF EXISTS audit_exports;
DROP TABLE IF EXISTS outbox_events;
DROP TABLE IF EXISTS audit_events CASCADE;
DROP FUNCTION IF EXISTS reject_audit_event_mutation();


-- ##########################################################################
-- Source: 000029_permanent_tool_payload_references.down.sql
-- ##########################################################################

DROP TRIGGER IF EXISTS tool_invocations_permanent_payload ON tool_invocations;
DROP TRIGGER IF EXISTS tool_tests_permanent_payload ON tool_tests;
DROP FUNCTION IF EXISTS enforce_permanent_tool_payload_reference();

ALTER TABLE tool_invocations
    DROP CONSTRAINT IF EXISTS tool_invocations_terminal_raw_object_check,
    DROP CONSTRAINT IF EXISTS tool_invocations_raw_object_fk;

ALTER TABLE tool_tests
    DROP CONSTRAINT IF EXISTS tool_tests_raw_object_fk,
    ALTER COLUMN raw_object_id DROP NOT NULL;


-- ##########################################################################
-- Source: 000028_permanent_content_references.down.sql
-- ##########################################################################

ALTER TABLE agent_run_steps
    DROP CONSTRAINT IF EXISTS agent_run_steps_raw_object_fk,
    DROP CONSTRAINT IF EXISTS agent_run_steps_model_turn_evidence_check,
    DROP CONSTRAINT IF EXISTS agent_run_steps_raw_evidence_check,
    DROP COLUMN IF EXISTS raw_length,
    DROP COLUMN IF EXISTS raw_sha256;

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
    RETURN NEW;
END;
$$;

ALTER TABLE chat_messages
    DROP CONSTRAINT IF EXISTS chat_messages_content_object_fk,
    DROP CONSTRAINT IF EXISTS chat_messages_content_length_check,
    DROP CONSTRAINT IF EXISTS chat_messages_content_carrier_check,
    DROP COLUMN IF EXISTS content_length,
    ADD CONSTRAINT chat_messages_content_check CHECK (
        (content IS NOT NULL AND length(content) > 0) OR content_object_id IS NOT NULL
    );

CREATE OR REPLACE FUNCTION enforce_chat_message_permanent_retention()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'chat messages are permanently retained and cannot be deleted'
            USING ERRCODE = '55000';
    END IF;
    IF ROW(
        NEW.id, NEW.workspace_id, NEW.session_id, NEW.role, NEW.content,
        NEW.content_object_id, NEW.content_sha256, NEW.created_by, NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.session_id, OLD.role, OLD.content,
        OLD.content_object_id, OLD.content_sha256, OLD.created_by, OLD.created_at
    ) THEN
        RAISE EXCEPTION 'chat message original content and identity are immutable'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS prompt_runs_permanent_content ON prompt_runs;
DROP FUNCTION IF EXISTS enforce_prompt_run_permanent_content();

ALTER TABLE prompt_runs
    DROP CONSTRAINT IF EXISTS prompt_runs_output_object_fk,
    DROP CONSTRAINT IF EXISTS prompt_runs_input_object_fk,
    DROP CONSTRAINT IF EXISTS prompt_runs_output_evidence_check,
    DROP CONSTRAINT IF EXISTS prompt_runs_input_length_check,
    DROP CONSTRAINT IF EXISTS prompt_runs_input_sha256_check,
    DROP COLUMN IF EXISTS output_length,
    DROP COLUMN IF EXISTS output_sha256,
    DROP COLUMN IF EXISTS input_length,
    DROP COLUMN IF EXISTS input_sha256;


-- ##########################################################################
-- Source: 000027_stored_object_security_policy.down.sql
-- ##########################################################################

ALTER TABLE stored_objects
    DROP CONSTRAINT IF EXISTS stored_objects_audit_export_retention_check,
    DROP CONSTRAINT IF EXISTS stored_objects_openapi_retention_check,
    DROP CONSTRAINT IF EXISTS stored_objects_permanent_content_policy_check,
    DROP CONSTRAINT IF EXISTS stored_objects_classification_encryption_check;


-- ##########################################################################
-- Source: 000026_stored_objects.down.sql
-- ##########################################################################

DROP TRIGGER IF EXISTS stored_objects_metadata_guard ON stored_objects;
DROP FUNCTION IF EXISTS enforce_stored_object_metadata();
DROP TABLE IF EXISTS stored_objects;


-- ##########################################################################
-- Source: 000025_chat_confirmation_projection.down.sql
-- ##########################################################################

DROP TRIGGER IF EXISTS execution_confirmations_chat_projection_sync
    ON execution_confirmations;
DROP FUNCTION IF EXISTS synchronize_chat_confirmation_projection();

DROP TRIGGER IF EXISTS chat_confirmations_projection_guard
    ON chat_confirmations;
DROP FUNCTION IF EXISTS enforce_chat_confirmation_projection();

CREATE TRIGGER chat_confirmations_no_delete
BEFORE DELETE ON chat_confirmations
FOR EACH ROW EXECUTE FUNCTION reject_chat_confirmation_delete();

DROP INDEX IF EXISTS chat_confirmations_one_pending_per_session_idx;


-- ##########################################################################
-- Source: 000024_confirmation_resume_checkpoints.down.sql
-- ##########################################################################

DROP TRIGGER IF EXISTS confirmation_resume_checkpoints_fact_guard
    ON confirmation_resume_checkpoints;
DROP FUNCTION IF EXISTS enforce_confirmation_resume_checkpoint();
DROP TABLE IF EXISTS confirmation_resume_checkpoints;



-- ##########################################################################
-- Source: 000023_execution_confirmations.down.sql
-- ##########################################################################

ALTER TABLE chat_messages DROP CONSTRAINT IF EXISTS chat_messages_confirmation_fk;
ALTER TABLE chat_sessions DROP CONSTRAINT IF EXISTS chat_sessions_pending_confirmation_fk;
UPDATE chat_messages
SET status = 'FAILED', confirmation_id = NULL
WHERE confirmation_id IS NOT NULL;
UPDATE chat_sessions SET pending_confirmation_id = NULL
WHERE pending_confirmation_id IS NOT NULL;
DROP TRIGGER IF EXISTS chat_confirmations_no_delete ON chat_confirmations;
DROP FUNCTION IF EXISTS reject_chat_confirmation_delete();
DROP TABLE IF EXISTS chat_confirmations;
DROP TRIGGER IF EXISTS execution_confirmations_fact_guard ON execution_confirmations;
DROP FUNCTION IF EXISTS enforce_execution_confirmation_fact();
DROP TABLE IF EXISTS execution_confirmations;


-- ##########################################################################
-- Source: 000022_run_events.down.sql
-- ##########################################################################

DROP TRIGGER IF EXISTS run_events_fact_guard ON run_events;
DROP FUNCTION IF EXISTS enforce_run_event_fact();
DROP TABLE IF EXISTS run_events;


-- ##########################################################################
-- Source: 000021_run_state_snapshots.down.sql
-- ##########################################################################

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

ALTER TABLE workflow_executions
    DROP CONSTRAINT IF EXISTS workflow_executions_authorization_snapshot_object_check,
    DROP CONSTRAINT IF EXISTS workflow_executions_snapshot_schema_version_not_blank,
    DROP COLUMN IF EXISTS authorization_snapshot,
    DROP COLUMN IF EXISTS snapshot_schema_version;

ALTER TABLE agent_runs
    DROP CONSTRAINT IF EXISTS agent_runs_authorization_snapshot_object_check,
    DROP CONSTRAINT IF EXISTS agent_runs_snapshot_schema_version_not_blank,
    DROP COLUMN IF EXISTS authorization_snapshot,
    DROP COLUMN IF EXISTS snapshot_schema_version;


-- ##########################################################################
-- Source: 000020_tool_invocations.down.sql
-- ##########################################################################

DROP TRIGGER IF EXISTS tool_invocations_state_guard ON tool_invocations;
DROP FUNCTION IF EXISTS enforce_tool_invocation_state();
DROP TRIGGER IF EXISTS tool_invocations_integrity ON tool_invocations;
DROP FUNCTION IF EXISTS enforce_tool_invocation_integrity();
DROP TABLE IF EXISTS tool_invocations;
ALTER TABLE tool_versions
    DROP CONSTRAINT IF EXISTS tool_versions_workspace_capability_version_provider_key;


-- ##########################################################################
-- Source: 000019_workflow_executions.down.sql
-- ##########################################################################

ALTER TABLE workflow_trial_runs
    DROP CONSTRAINT IF EXISTS workflow_trial_runs_execution_fk;
DELETE FROM workflow_trial_runs
WHERE EXISTS (
    SELECT 1
    FROM workflow_executions
    WHERE workflow_executions.workspace_id = workflow_trial_runs.workspace_id
      AND workflow_executions.id = workflow_trial_runs.execution_id
);
DROP TRIGGER IF EXISTS execution_steps_state_guard ON execution_steps;
DROP FUNCTION IF EXISTS enforce_execution_step_state();
DROP TABLE IF EXISTS execution_steps;
DROP TRIGGER IF EXISTS workflow_executions_state_guard ON workflow_executions;
DROP FUNCTION IF EXISTS enforce_workflow_execution_state();
DROP TABLE IF EXISTS workflow_executions;


-- ##########################################################################
-- Source: 000018_agent_runs.down.sql
-- ##########################################################################

ALTER TABLE chat_messages DROP CONSTRAINT IF EXISTS chat_messages_run_fk;
ALTER TABLE chat_sessions DROP CONSTRAINT IF EXISTS chat_sessions_latest_run_fk;
UPDATE chat_messages SET run_id = NULL WHERE run_id IS NOT NULL;
UPDATE chat_sessions SET latest_run_id = NULL WHERE latest_run_id IS NOT NULL;
DROP TRIGGER IF EXISTS agent_run_steps_permanent_evidence ON agent_run_steps;
DROP FUNCTION IF EXISTS enforce_agent_run_step_permanent_evidence();
DROP TABLE IF EXISTS agent_run_steps;
DROP TRIGGER IF EXISTS agent_runs_permanent_snapshot ON agent_runs;
DROP FUNCTION IF EXISTS enforce_agent_run_permanent_snapshot();
DROP TABLE IF EXISTS agent_runs;
ALTER TABLE capability_releases
    DROP CONSTRAINT IF EXISTS capability_releases_workspace_id_key;


-- ##########################################################################
-- Source: 000017_chat.down.sql
-- ##########################################################################

DROP TRIGGER IF EXISTS chat_messages_permanent_retention ON chat_messages;
DROP FUNCTION IF EXISTS enforce_chat_message_permanent_retention();
DROP TABLE IF EXISTS chat_messages;
DROP TRIGGER IF EXISTS chat_sessions_no_delete ON chat_sessions;
DROP FUNCTION IF EXISTS reject_chat_session_delete();
DROP TABLE IF EXISTS chat_sessions;


-- ##########################################################################
-- Source: 000016_workflows.down.sql
-- ##########################################################################

DROP TRIGGER IF EXISTS workflows_active_revision_integrity ON workflows;
DROP FUNCTION IF EXISTS enforce_workflow_active_revision();
ALTER TABLE workflows
    DROP CONSTRAINT IF EXISTS workflows_latest_compilation_fk,
    DROP CONSTRAINT IF EXISTS workflows_active_revision_fk,
    DROP CONSTRAINT IF EXISTS workflows_current_draft_fk;
DROP TABLE IF EXISTS workflow_trial_runs;
DROP TRIGGER IF EXISTS workflow_revisions_immutable ON workflow_revisions;
DROP FUNCTION IF EXISTS reject_workflow_revision_mutation();
DROP TABLE IF EXISTS workflow_revisions;
DROP TRIGGER IF EXISTS workflow_compilations_immutable ON workflow_compilations;
DROP FUNCTION IF EXISTS reject_workflow_compilation_mutation();
DROP TABLE IF EXISTS workflow_compilations;
DROP TABLE IF EXISTS workflow_drafts;
DROP TRIGGER IF EXISTS workflows_capability_kind_integrity ON workflows;
DROP FUNCTION IF EXISTS enforce_workflow_capability_kind();
DROP TABLE IF EXISTS workflows;


-- ##########################################################################
-- Source: 000015_openapi_source_revision.down.sql
-- ##########################################################################

DROP INDEX IF EXISTS openapi_imports_workspace_provider_revision_idx;
ALTER TABLE openapi_imports
    DROP CONSTRAINT IF EXISTS openapi_imports_source_revision_not_blank,
    DROP COLUMN IF EXISTS source_revision;


-- ##########################################################################
-- Source: 000014_openapi_imports.down.sql
-- ##########################################################################

ALTER TABLE tools
    DROP CONSTRAINT IF EXISTS tools_source_endpoint_fk;
DROP TABLE IF EXISTS openapi_endpoints;
DROP TABLE IF EXISTS openapi_imports;


-- ##########################################################################
-- Source: 000013_tools.down.sql
-- ##########################################################################

DROP TRIGGER IF EXISTS tool_tests_immutable ON tool_tests;
DROP FUNCTION IF EXISTS reject_tool_test_mutation();
DROP TABLE IF EXISTS tool_tests;
DROP TRIGGER IF EXISTS published_tool_versions_immutable ON tool_versions;
DROP FUNCTION IF EXISTS enforce_published_tool_version_immutability();
DROP TABLE IF EXISTS tool_versions;
DROP TRIGGER IF EXISTS tools_capability_kind_integrity ON tools;
DROP FUNCTION IF EXISTS enforce_tool_capability_kind();
DROP TABLE IF EXISTS tools;
ALTER TABLE service_connections
    DROP CONSTRAINT IF EXISTS service_connections_workspace_provider_id_key;
ALTER TABLE provider_assets
    DROP CONSTRAINT IF EXISTS provider_assets_workspace_provider_id_key;


-- ##########################################################################
-- Source: 000012_agent_capability_bindings.down.sql
-- ##########################################################################

DROP TABLE IF EXISTS agent_capability_bindings;


-- ##########################################################################
-- Source: 000011_capabilities.down.sql
-- ##########################################################################

DROP TRIGGER IF EXISTS capability_releases_immutable ON capability_releases;
DROP FUNCTION IF EXISTS enforce_capability_release_immutability();
DROP TRIGGER IF EXISTS capabilities_active_release_integrity ON capabilities;
DROP FUNCTION IF EXISTS enforce_capability_active_release();
ALTER TABLE provider_assets DROP CONSTRAINT IF EXISTS provider_assets_materialized_capability_fk;
ALTER TABLE capabilities DROP CONSTRAINT IF EXISTS capabilities_active_release_fk;
DROP TABLE IF EXISTS capability_releases;
DROP TABLE IF EXISTS capabilities;


-- ##########################################################################
-- Source: 000010_agents.down.sql
-- ##########################################################################

DROP TRIGGER IF EXISTS agent_prompt_revisions_immutable ON agent_prompt_revisions;
DROP FUNCTION IF EXISTS reject_agent_prompt_revision_mutation();
DROP TABLE IF EXISTS prompt_runs;
ALTER TABLE workspaces DROP CONSTRAINT IF EXISTS workspaces_default_agent_fk;
ALTER TABLE agents DROP CONSTRAINT IF EXISTS agents_current_prompt_revision_fk;
DROP TABLE IF EXISTS agent_prompt_revisions;
DROP TABLE IF EXISTS agents;


-- ##########################################################################
-- Source: 000009_connections.down.sql
-- ##########################################################################

DROP TABLE IF EXISTS connection_verifications;
DROP TABLE IF EXISTS service_connections;


-- ##########################################################################
-- Source: 000008_providers.down.sql
-- ##########################################################################

DROP TABLE IF EXISTS provider_sync_runs;
DROP TABLE IF EXISTS provider_assets;
DROP TABLE IF EXISTS capability_providers;


-- ##########################################################################
-- Source: 000007_model_configs.down.sql
-- ##########################################################################

ALTER TABLE workspaces DROP CONSTRAINT IF EXISTS workspaces_default_model_config_fk;
UPDATE workspaces SET default_model_config_id = NULL WHERE default_model_config_id IS NOT NULL;
DROP TABLE IF EXISTS model_configs;


-- ##########################################################################
-- Source: 000006_secrets.down.sql
-- ##########################################################################

ALTER TABLE secrets DROP CONSTRAINT IF EXISTS secrets_active_version_fk;
DROP TABLE IF EXISTS secret_versions;
DROP TABLE IF EXISTS secrets;


-- ##########################################################################
-- Source: 000005_workspace_rbac.down.sql
-- ##########################################################################

DROP TABLE IF EXISTS workspace_members;
DROP TABLE IF EXISTS workspaces;


-- ##########################################################################
-- Source: 000004_user_lock_version.down.sql
-- ##########################################################################

ALTER TABLE users
    DROP CONSTRAINT IF EXISTS users_lock_version_check;

ALTER TABLE users
    DROP COLUMN IF EXISTS lock_version;


-- ##########################################################################
-- Source: 000003_identity.down.sql
-- ##########################################################################

DROP TABLE IF EXISTS auth_sessions;
DROP TABLE IF EXISTS user_credentials;
DROP TABLE IF EXISTS users;


-- ##########################################################################
-- Source: 000002_postgres_baseline.down.sql
-- ##########################################################################

DO $$
BEGIN
    EXECUTE format(
        'ALTER DATABASE %I RESET timezone',
        current_database()
    );
END
$$;

DROP EXTENSION IF EXISTS citext;


-- ##########################################################################
-- Source: 000001_migration_tooling.down.sql
-- ##########################################################################

-- Migration infrastructure marker. Business schema starts in later migrations.
SELECT 1;

