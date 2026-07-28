DROP TRIGGER IF EXISTS execution_confirmations_chat_projection_sync ON execution_confirmations;
DROP FUNCTION IF EXISTS synchronize_chat_confirmation_projection();
DROP TRIGGER IF EXISTS chat_confirmations_projection_guard ON chat_confirmations;
DROP FUNCTION IF EXISTS enforce_chat_confirmation_projection();
DROP TRIGGER IF EXISTS execution_confirmations_fact_guard ON execution_confirmations;
DROP FUNCTION IF EXISTS enforce_execution_confirmation_fact();

ALTER TABLE execution_confirmations
    DROP CONSTRAINT execution_confirmations_requested_by_fk,
    DROP CONSTRAINT execution_confirmations_confirmed_by_fk,
    DROP CONSTRAINT execution_confirmations_requester_check,
    DROP CONSTRAINT execution_confirmations_state_check,
    ALTER COLUMN requested_by DROP NOT NULL,
    ADD COLUMN request_principal_snapshot_version TEXT,
    ADD COLUMN request_actor_type TEXT,
    ADD COLUMN request_actor_id UUID,
    ADD COLUMN request_subject_type TEXT,
    ADD COLUMN request_subject_id UUID,
    ADD COLUMN request_client_id UUID,
    ADD COLUMN request_grant_id UUID,
    ADD COLUMN request_grant_version BIGINT,
    ADD COLUMN request_agent_policy_version BIGINT,
    ADD COLUMN decision_principal_snapshot_version TEXT,
    ADD COLUMN decision_actor_type TEXT,
    ADD COLUMN decision_actor_id UUID,
    ADD COLUMN decision_subject_type TEXT,
    ADD COLUMN decision_subject_id UUID,
    ADD COLUMN decision_client_id UUID,
    ADD COLUMN decision_grant_id UUID,
    ADD COLUMN decision_grant_version BIGINT,
    ADD COLUMN decision_agent_policy_version BIGINT,
    ADD COLUMN decision_policy_snapshot JSONB NOT NULL DEFAULT '{}'::JSONB;

-- Historical requests identify a real User, but cancelled rows never stored
-- who cancelled them. Mark the record legacy instead of inventing evidence.
UPDATE execution_confirmations SET
    request_principal_snapshot_version='legacy.v1',
    request_actor_type='USER',request_actor_id=requested_by,
    request_subject_type='USER',request_subject_id=requested_by,
    decision_principal_snapshot_version=CASE WHEN status='CONFIRMED' THEN 'legacy.v1' END,
    decision_actor_type=CASE WHEN status='CONFIRMED' THEN 'USER' END,
    decision_actor_id=CASE WHEN status='CONFIRMED' THEN confirmed_by END,
    decision_subject_type=CASE WHEN status='CONFIRMED' THEN 'USER' END,
    decision_subject_id=CASE WHEN status='CONFIRMED' THEN confirmed_by END,
    decision_policy_snapshot=CASE WHEN status='CONFIRMED'
        THEN '{"mode":"actweave_user","legacy":true}'::JSONB ELSE '{}'::JSONB END;

ALTER TABLE execution_confirmations
    ALTER COLUMN request_principal_snapshot_version SET NOT NULL,
    ALTER COLUMN request_actor_type SET NOT NULL,
    ALTER COLUMN request_actor_id SET NOT NULL,
    ADD CONSTRAINT execution_confirmations_request_snapshot_version_check CHECK (
        request_principal_snapshot_version IN ('legacy.v1','execution.principal.v1')
    ),
    ADD CONSTRAINT execution_confirmations_request_subject_pair_check
        CHECK((request_subject_type IS NULL)=(request_subject_id IS NULL)),
    ADD CONSTRAINT execution_confirmations_request_external_pair_check CHECK (
        (request_client_id IS NULL AND request_grant_id IS NULL
         AND request_grant_version IS NULL AND request_agent_policy_version IS NULL)
        OR
        (request_client_id IS NOT NULL AND request_grant_id IS NOT NULL
         AND request_grant_version > 0 AND request_agent_policy_version > 0)
    ),
    ADD CONSTRAINT execution_confirmations_decision_snapshot_pair_check CHECK (
        (decision_principal_snapshot_version IS NULL AND decision_actor_type IS NULL
         AND decision_actor_id IS NULL AND decision_subject_type IS NULL
         AND decision_subject_id IS NULL AND decision_client_id IS NULL
         AND decision_grant_id IS NULL AND decision_grant_version IS NULL
         AND decision_agent_policy_version IS NULL)
        OR
        (decision_principal_snapshot_version IN ('legacy.v1','execution.principal.v1')
         AND decision_actor_type IS NOT NULL AND decision_actor_id IS NOT NULL
         AND (decision_subject_type IS NULL)=(decision_subject_id IS NULL)
         AND ((decision_client_id IS NULL AND decision_grant_id IS NULL
               AND decision_grant_version IS NULL AND decision_agent_policy_version IS NULL)
              OR
              (decision_client_id IS NOT NULL AND decision_grant_id IS NOT NULL
               AND decision_grant_version > 0 AND decision_agent_policy_version > 0)))
    ),
    ADD CONSTRAINT execution_confirmations_decision_policy_object_check
        CHECK(jsonb_typeof(decision_policy_snapshot)='object'),
    ADD CONSTRAINT execution_confirmations_requested_by_projection_check CHECK (
        (request_actor_type='USER' AND requested_by=request_actor_id)
        OR (request_actor_type<>'USER' AND requested_by IS NULL)
    ),
    ADD CONSTRAINT execution_confirmations_confirmed_by_projection_check CHECK (
        (status='CONFIRMED' AND decision_actor_type='USER' AND confirmed_by=decision_actor_id)
        OR ((status<>'CONFIRMED' OR decision_actor_type IS DISTINCT FROM 'USER')
            AND confirmed_by IS NULL)
    ),
    ADD CONSTRAINT execution_confirmations_requested_by_fk
        FOREIGN KEY(requested_by) REFERENCES users(id) ON DELETE RESTRICT,
    ADD CONSTRAINT execution_confirmations_confirmed_by_fk
        FOREIGN KEY(confirmed_by) REFERENCES users(id) ON DELETE RESTRICT,
    ADD CONSTRAINT execution_confirmations_principal_state_check CHECK (
        (status='PENDING' AND decision_principal_snapshot_version IS NULL
         AND confirmed_at IS NULL AND cancelled_at IS NULL)
        OR
        (status='CONFIRMED' AND decision_principal_snapshot_version IS NOT NULL
         AND confirmed_at IS NOT NULL AND cancelled_at IS NULL)
        OR
        (status='CANCELLED' AND confirmed_at IS NULL AND cancelled_at IS NOT NULL
         AND (decision_principal_snapshot_version IS NOT NULL
              OR request_principal_snapshot_version='legacy.v1'))
        OR
        (status='EXPIRED' AND decision_principal_snapshot_version IS NULL
         AND confirmed_at IS NULL AND cancelled_at IS NULL)
    ),
    ADD CONSTRAINT execution_confirmations_request_actor_ref_fk
        FOREIGN KEY(workspace_id,request_actor_type,request_actor_id)
        REFERENCES principal_refs(workspace_id,principal_type,principal_id) ON DELETE RESTRICT,
    ADD CONSTRAINT execution_confirmations_request_subject_ref_fk
        FOREIGN KEY(workspace_id,request_subject_type,request_subject_id)
        REFERENCES principal_refs(workspace_id,principal_type,principal_id) ON DELETE RESTRICT,
    ADD CONSTRAINT execution_confirmations_request_client_fk
        FOREIGN KEY(workspace_id,request_client_id)
        REFERENCES agent_access_clients(workspace_id,id) ON DELETE RESTRICT,
    ADD CONSTRAINT execution_confirmations_request_grant_fk
        FOREIGN KEY(workspace_id,request_grant_id)
        REFERENCES agent_access_grants(workspace_id,id) ON DELETE RESTRICT,
    ADD CONSTRAINT execution_confirmations_decision_actor_ref_fk
        FOREIGN KEY(workspace_id,decision_actor_type,decision_actor_id)
        REFERENCES principal_refs(workspace_id,principal_type,principal_id) ON DELETE RESTRICT,
    ADD CONSTRAINT execution_confirmations_decision_subject_ref_fk
        FOREIGN KEY(workspace_id,decision_subject_type,decision_subject_id)
        REFERENCES principal_refs(workspace_id,principal_type,principal_id) ON DELETE RESTRICT,
    ADD CONSTRAINT execution_confirmations_decision_client_fk
        FOREIGN KEY(workspace_id,decision_client_id)
        REFERENCES agent_access_clients(workspace_id,id) ON DELETE RESTRICT,
    ADD CONSTRAINT execution_confirmations_decision_grant_fk
        FOREIGN KEY(workspace_id,decision_grant_id)
        REFERENCES agent_access_grants(workspace_id,id) ON DELETE RESTRICT;

CREATE FUNCTION validate_execution_confirmation_principal()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    parent_version TEXT;
    parent_actor_type TEXT;
    parent_actor_id UUID;
    parent_subject_type TEXT;
    parent_subject_id UUID;
    parent_client_id UUID;
    parent_grant_id UUID;
    parent_grant_version BIGINT;
    parent_policy_version BIGINT;
    parent_run UUID;
    decision_mode TEXT;
    max_risk TEXT;
    release_risk TEXT;
    side_effect TEXT;
    mandatory BOOLEAN;
BEGIN
    IF TG_OP='DELETE' THEN
        RAISE EXCEPTION 'execution confirmations are permanently retained'
            USING ERRCODE='55000';
    END IF;

    IF TG_OP='INSERT' THEN
        IF NEW.status<>'PENDING' OR NEW.request_principal_snapshot_version<>'execution.principal.v1' THEN
            RAISE EXCEPTION 'new confirmation requires a modern pending Principal snapshot'
                USING ERRCODE='23514',CONSTRAINT='execution_confirmation_modern_request_check';
        END IF;
    ELSE
        IF ROW(
            NEW.id,NEW.workspace_id,NEW.execution_id,NEW.run_id,NEW.node_id,
            NEW.reason,NEW.risk_reasons,NEW.scope_snapshot,NEW.release_id,
            NEW.input_hash,NEW.connection_id,NEW.plan_hash,NEW.resume_token_hash,
            NEW.requested_by,NEW.created_at,NEW.expires_at,
            NEW.request_principal_snapshot_version,NEW.request_actor_type,
            NEW.request_actor_id,NEW.request_subject_type,NEW.request_subject_id,
            NEW.request_client_id,NEW.request_grant_id,NEW.request_grant_version,
            NEW.request_agent_policy_version
        ) IS DISTINCT FROM ROW(
            OLD.id,OLD.workspace_id,OLD.execution_id,OLD.run_id,OLD.node_id,
            OLD.reason,OLD.risk_reasons,OLD.scope_snapshot,OLD.release_id,
            OLD.input_hash,OLD.connection_id,OLD.plan_hash,OLD.resume_token_hash,
            OLD.requested_by,OLD.created_at,OLD.expires_at,
            OLD.request_principal_snapshot_version,OLD.request_actor_type,
            OLD.request_actor_id,OLD.request_subject_type,OLD.request_subject_id,
            OLD.request_client_id,OLD.request_grant_id,OLD.request_grant_version,
            OLD.request_agent_policy_version
        ) THEN
            RAISE EXCEPTION 'execution confirmation request snapshot is immutable'
                USING ERRCODE='55000';
        END IF;
        IF OLD.status IN ('CONFIRMED','CANCELLED','EXPIRED') THEN
            RAISE EXCEPTION 'terminal execution confirmation is immutable' USING ERRCODE='55000';
        END IF;
        IF NEW.lock_version<>OLD.lock_version+1 THEN
            RAISE EXCEPTION 'execution confirmation requires next lock version' USING ERRCODE='40001';
        END IF;
        IF NEW.status NOT IN ('CONFIRMED','CANCELLED','EXPIRED') THEN
            RAISE EXCEPTION 'illegal execution confirmation transition' USING ERRCODE='55000';
        END IF;
    END IF;

    IF NEW.request_actor_type='USER' THEN
        IF NEW.request_subject_type IS DISTINCT FROM 'USER'
           OR NEW.request_subject_id IS DISTINCT FROM NEW.request_actor_id
           OR NEW.request_client_id IS NOT NULL THEN
            RAISE EXCEPTION 'User confirmation request identity is invalid'
                USING ERRCODE='23514',CONSTRAINT='execution_confirmation_request_identity_check';
        END IF;
    ELSIF NEW.request_actor_type='SERVICE_PRINCIPAL' THEN
        IF NEW.request_client_id IS NULL OR NEW.request_grant_id IS NULL
           OR NOT EXISTS (
             SELECT 1 FROM agent_access_clients c
             WHERE c.workspace_id=NEW.workspace_id AND c.id=NEW.request_client_id
               AND c.service_principal_id=NEW.request_actor_id
           ) OR NOT EXISTS (
             SELECT 1 FROM agent_access_grants g
             WHERE g.workspace_id=NEW.workspace_id AND g.id=NEW.request_grant_id
               AND g.client_id=NEW.request_client_id
           ) OR (NEW.request_subject_id IS NOT NULL AND (
             NEW.request_subject_type<>'EXTERNAL_SUBJECT' OR NOT EXISTS (
               SELECT 1 FROM external_subjects s
               WHERE s.workspace_id=NEW.workspace_id AND s.id=NEW.request_subject_id
                 AND s.client_id=NEW.request_client_id
             )
           )) THEN
            RAISE EXCEPTION 'external confirmation request identity is invalid'
                USING ERRCODE='23514',CONSTRAINT='execution_confirmation_request_identity_check';
        END IF;
    ELSE
        RAISE EXCEPTION 'confirmation requester must be User or Service Principal'
            USING ERRCODE='23514',CONSTRAINT='execution_confirmation_request_identity_check';
    END IF;

    IF NEW.execution_id IS NOT NULL THEN
        SELECT principal_snapshot_version,triggered_by_type,triggered_by_id,
               subject_type,subject_id,client_id,grant_id,grant_version,
               agent_policy_version,agent_run_id
        INTO STRICT parent_version,parent_actor_type,parent_actor_id,parent_subject_type,
             parent_subject_id,parent_client_id,parent_grant_id,parent_grant_version,
             parent_policy_version,parent_run
        FROM workflow_executions
        WHERE workspace_id=NEW.workspace_id AND id=NEW.execution_id;
        IF NEW.run_id IS NOT NULL AND parent_run IS DISTINCT FROM NEW.run_id THEN
            RAISE EXCEPTION 'confirmation execution and run chain mismatch'
                USING ERRCODE='23514',CONSTRAINT='execution_confirmation_parent_chain_check';
        END IF;
    ELSE
        SELECT principal_snapshot_version,triggered_by_type,triggered_by_id,
               subject_type,subject_id,client_id,grant_id,grant_version,
               agent_policy_version
        INTO STRICT parent_version,parent_actor_type,parent_actor_id,parent_subject_type,
             parent_subject_id,parent_client_id,parent_grant_id,parent_grant_version,
             parent_policy_version
        FROM agent_runs WHERE workspace_id=NEW.workspace_id AND id=NEW.run_id;
    END IF;
    IF NOT (
        NEW.request_principal_snapshot_version=parent_version
        OR (parent_version='legacy.v1'
            AND NEW.request_principal_snapshot_version='execution.principal.v1'
            AND NEW.request_actor_type='USER')
    ) OR ROW(
        NEW.request_actor_type,NEW.request_actor_id,
        NEW.request_subject_type,NEW.request_subject_id,NEW.request_client_id,
        NEW.request_grant_id,NEW.request_grant_version,NEW.request_agent_policy_version
    ) IS DISTINCT FROM ROW(
        parent_actor_type,parent_actor_id,parent_subject_type,parent_subject_id,
        parent_client_id,parent_grant_id,parent_grant_version,parent_policy_version
    ) THEN
        RAISE EXCEPTION 'confirmation requester differs from parent execution snapshot'
            USING ERRCODE='23514',CONSTRAINT='execution_confirmation_parent_principal_check';
    END IF;

    IF NEW.decision_principal_snapshot_version IS NOT NULL THEN
        IF NEW.decision_principal_snapshot_version<>'execution.principal.v1'
           AND NEW.request_principal_snapshot_version<>'legacy.v1' THEN
            RAISE EXCEPTION 'new decision requires modern Principal snapshot'
                USING ERRCODE='23514',CONSTRAINT='execution_confirmation_decision_identity_check';
        END IF;
        IF ROW(
            NEW.decision_actor_type,NEW.decision_actor_id,
            NEW.decision_subject_type,NEW.decision_subject_id,
            NEW.decision_client_id,NEW.decision_grant_id
        ) IS DISTINCT FROM ROW(
            NEW.request_actor_type,NEW.request_actor_id,
            NEW.request_subject_type,NEW.request_subject_id,
            NEW.request_client_id,NEW.request_grant_id
        ) THEN
            RAISE EXCEPTION 'confirmation decision Principal differs from requester'
                USING ERRCODE='23514',CONSTRAINT='execution_confirmation_decision_identity_check';
        END IF;
        decision_mode := NEW.decision_policy_snapshot->>'mode';
        IF NEW.decision_actor_type='USER' THEN
            IF decision_mode<>'actweave_user' THEN
                RAISE EXCEPTION 'User decision policy evidence is invalid'
                    USING ERRCODE='23514',CONSTRAINT='execution_confirmation_decision_identity_check';
            END IF;
        ELSIF NEW.decision_subject_id IS NOT NULL THEN
            IF NEW.decision_subject_type<>'EXTERNAL_SUBJECT'
               OR decision_mode<>'external_subject' THEN
                RAISE EXCEPTION 'External Subject decision evidence is invalid'
                    USING ERRCODE='23514',CONSTRAINT='execution_confirmation_decision_identity_check';
            END IF;
        ELSE
            max_risk := NEW.decision_policy_snapshot->>'maxRisk';
            release_risk := lower(COALESCE(NEW.scope_snapshot#>>'{release,riskLevel}',''));
            side_effect := upper(COALESCE(NEW.scope_snapshot#>>'{release,sideEffectLevel}',''));
            mandatory := COALESCE((NEW.scope_snapshot#>>'{decision,mandatory}')::BOOLEAN,FALSE);
            IF decision_mode<>'service_principal'
               OR NEW.decision_policy_snapshot->>'enabled'<>'true'
               OR max_risk NOT IN ('low','medium')
               OR release_risk NOT IN ('low','medium')
               OR (max_risk='low' AND release_risk<>'low')
               OR mandatory OR side_effect='IRREVERSIBLE'
               OR NOT EXISTS (
                 SELECT 1 FROM agent_access_grants g
                 JOIN agents a ON a.workspace_id=g.workspace_id AND a.id=g.agent_id
                 WHERE g.workspace_id=NEW.workspace_id AND g.id=NEW.decision_grant_id
                   AND g.client_id=NEW.decision_client_id
                   AND g.lock_version=NEW.decision_grant_version
                   AND a.lock_version=NEW.decision_agent_policy_version
                   AND g.status='ACTIVE' AND g.valid_from<=clock_timestamp()
                   AND (g.expires_at IS NULL OR g.expires_at>clock_timestamp())
                   AND g.scopes ? 'interaction:decide'
                   AND g.policy#>>'{serviceDecision,enabled}'='true'
                   AND g.policy#>>'{serviceDecision,maxRisk}'=max_risk
               ) THEN
                RAISE EXCEPTION 'Service Principal is not allowed to decide this confirmation'
                    USING ERRCODE='23514',CONSTRAINT='execution_confirmation_service_decision_check';
            END IF;
        END IF;
    END IF;
    RETURN NEW;
EXCEPTION
    WHEN NO_DATA_FOUND THEN
        RAISE EXCEPTION 'confirmation parent execution does not exist'
            USING ERRCODE='23503',CONSTRAINT='execution_confirmation_parent_fk';
END;
$$;

CREATE TRIGGER execution_confirmations_fact_guard
BEFORE INSERT OR UPDATE OR DELETE ON execution_confirmations
FOR EACH ROW EXECUTE FUNCTION validate_execution_confirmation_principal();

ALTER TABLE chat_confirmations
    DROP CONSTRAINT chat_confirmations_confirmed_state_check,
    ADD CONSTRAINT chat_confirmations_confirmed_state_check CHECK (
        (status='CONFIRMED' AND confirmed_at IS NOT NULL)
        OR (status<>'CONFIRMED' AND confirmed_by IS NULL AND confirmed_at IS NULL)
    );

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
        RAISE EXCEPTION 'chat confirmation target differs from execution confirmation'
            USING ERRCODE='23514';
    END IF;
    IF NEW.status IS DISTINCT FROM execution_row.status
       OR NEW.confirmed_by IS DISTINCT FROM execution_row.confirmed_by
       OR NEW.confirmed_at IS DISTINCT FROM execution_row.confirmed_at THEN
        RAISE EXCEPTION 'chat confirmation state is derived from execution confirmation'
            USING ERRCODE='55000';
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

CREATE INDEX execution_confirmations_workspace_request_principal_idx
    ON execution_confirmations(workspace_id,request_client_id,request_subject_type,
        request_subject_id,created_at DESC,id);
CREATE INDEX execution_confirmations_workspace_decision_principal_idx
    ON execution_confirmations(workspace_id,decision_client_id,decision_subject_type,
        decision_subject_id,created_at DESC,id)
    WHERE decision_principal_snapshot_version IS NOT NULL;
