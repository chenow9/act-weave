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
