CREATE TABLE execution_confirmations (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    execution_id UUID,
    run_id UUID,
    node_id TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'PENDING',
    reason TEXT NOT NULL,
    risk_reasons JSONB NOT NULL DEFAULT '[]'::JSONB,
    scope_snapshot JSONB NOT NULL,
    release_id UUID NOT NULL,
    input_hash CHAR(64) NOT NULL,
    connection_id UUID,
    plan_hash CHAR(64),
    resume_token_hash CHAR(64) NOT NULL,
    requested_by UUID NOT NULL,
    confirmed_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMPTZ NOT NULL,
    confirmed_at TIMESTAMPTZ,
    cancelled_at TIMESTAMPTZ,
    lock_version BIGINT NOT NULL DEFAULT 1,
    CONSTRAINT execution_confirmations_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT execution_confirmations_workspace_execution_fk
        FOREIGN KEY (workspace_id, execution_id)
        REFERENCES workflow_executions (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT execution_confirmations_workspace_run_fk
        FOREIGN KEY (workspace_id, run_id)
        REFERENCES agent_runs (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT execution_confirmations_workspace_release_fk
        FOREIGN KEY (workspace_id, release_id)
        REFERENCES capability_releases (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT execution_confirmations_workspace_connection_fk
        FOREIGN KEY (workspace_id, connection_id)
        REFERENCES service_connections (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT execution_confirmations_requested_by_fk
        FOREIGN KEY (requested_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT execution_confirmations_confirmed_by_fk
        FOREIGN KEY (confirmed_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT execution_confirmations_target_check
        CHECK (execution_id IS NOT NULL OR run_id IS NOT NULL),
    CONSTRAINT execution_confirmations_node_id_not_blank CHECK (length(btrim(node_id)) > 0),
    CONSTRAINT execution_confirmations_status_check
        CHECK (status IN ('PENDING', 'CONFIRMED', 'CANCELLED', 'EXPIRED')),
    CONSTRAINT execution_confirmations_reason_not_blank CHECK (length(btrim(reason)) > 0),
    CONSTRAINT execution_confirmations_risk_reasons_array_check
        CHECK (jsonb_typeof(risk_reasons) = 'array'),
    CONSTRAINT execution_confirmations_scope_snapshot_object_check
        CHECK (jsonb_typeof(scope_snapshot) = 'object'),
    CONSTRAINT execution_confirmations_input_hash_check
        CHECK (input_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT execution_confirmations_plan_hash_check
        CHECK (plan_hash IS NULL OR plan_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT execution_confirmations_resume_token_hash_check
        CHECK (resume_token_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT execution_confirmations_requester_check
        CHECK (confirmed_by IS NULL OR confirmed_by = requested_by),
    CONSTRAINT execution_confirmations_state_check CHECK (
        (status = 'PENDING' AND confirmed_by IS NULL AND confirmed_at IS NULL AND cancelled_at IS NULL)
        OR (status = 'CONFIRMED' AND confirmed_by = requested_by
            AND confirmed_at IS NOT NULL AND cancelled_at IS NULL)
        OR (status = 'CANCELLED' AND confirmed_by IS NULL
            AND confirmed_at IS NULL AND cancelled_at IS NOT NULL)
        OR (status = 'EXPIRED' AND confirmed_by IS NULL
            AND confirmed_at IS NULL AND cancelled_at IS NULL)
    ),
    CONSTRAINT execution_confirmations_expiry_check CHECK (expires_at > created_at),
    CONSTRAINT execution_confirmations_confirmed_at_check
        CHECK (confirmed_at IS NULL OR (confirmed_at >= created_at AND confirmed_at <= expires_at)),
    CONSTRAINT execution_confirmations_cancelled_at_check
        CHECK (cancelled_at IS NULL OR cancelled_at >= created_at),
    CONSTRAINT execution_confirmations_lock_version_check CHECK (lock_version > 0)
);

CREATE INDEX execution_confirmations_workspace_status_created_idx
    ON execution_confirmations (workspace_id, status, created_at DESC, id);
CREATE INDEX execution_confirmations_workspace_run_created_idx
    ON execution_confirmations (workspace_id, run_id, created_at DESC, id)
    WHERE run_id IS NOT NULL;
CREATE INDEX execution_confirmations_workspace_execution_created_idx
    ON execution_confirmations (workspace_id, execution_id, created_at DESC, id)
    WHERE execution_id IS NOT NULL;
CREATE INDEX execution_confirmations_pending_expiry_idx
    ON execution_confirmations (expires_at, id) WHERE status = 'PENDING';

CREATE FUNCTION enforce_execution_confirmation_fact()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    parent_run UUID;
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'execution confirmations are permanently retained'
            USING ERRCODE = '55000';
    END IF;
    IF TG_OP = 'UPDATE' AND ROW(
        NEW.id, NEW.workspace_id, NEW.execution_id, NEW.run_id, NEW.node_id,
        NEW.reason, NEW.risk_reasons, NEW.scope_snapshot, NEW.release_id,
        NEW.input_hash, NEW.connection_id, NEW.plan_hash, NEW.resume_token_hash,
        NEW.requested_by, NEW.created_at, NEW.expires_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.execution_id, OLD.run_id, OLD.node_id,
        OLD.reason, OLD.risk_reasons, OLD.scope_snapshot, OLD.release_id,
        OLD.input_hash, OLD.connection_id, OLD.plan_hash, OLD.resume_token_hash,
        OLD.requested_by, OLD.created_at, OLD.expires_at
    ) THEN
        RAISE EXCEPTION 'execution confirmation request snapshot is immutable'
            USING ERRCODE = '55000';
    END IF;
    IF NEW.execution_id IS NOT NULL AND NEW.run_id IS NOT NULL THEN
        SELECT agent_run_id INTO parent_run FROM workflow_executions
        WHERE workspace_id = NEW.workspace_id AND id = NEW.execution_id;
        IF parent_run IS DISTINCT FROM NEW.run_id THEN
            RAISE EXCEPTION 'confirmation execution and run chain mismatch'
                USING ERRCODE = '23514';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER execution_confirmations_fact_guard
BEFORE INSERT OR UPDATE OR DELETE ON execution_confirmations
FOR EACH ROW EXECUTE FUNCTION enforce_execution_confirmation_fact();

CREATE TABLE chat_confirmations (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    session_id UUID NOT NULL,
    run_id UUID NOT NULL,
    execution_confirmation_id UUID NOT NULL UNIQUE,
    target_type TEXT NOT NULL,
    target_release_id UUID NOT NULL,
    risk_level TEXT NOT NULL,
    risk_reasons JSONB NOT NULL DEFAULT '[]'::JSONB,
    input_summary JSONB NOT NULL DEFAULT '{}'::JSONB,
    status TEXT NOT NULL DEFAULT 'PENDING',
    confirmed_by UUID,
    confirmed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT chat_confirmations_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT chat_confirmations_workspace_session_fk
        FOREIGN KEY (workspace_id, session_id)
        REFERENCES chat_sessions (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT chat_confirmations_workspace_run_fk
        FOREIGN KEY (workspace_id, run_id)
        REFERENCES agent_runs (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT chat_confirmations_workspace_execution_confirmation_fk
        FOREIGN KEY (workspace_id, execution_confirmation_id)
        REFERENCES execution_confirmations (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT chat_confirmations_workspace_release_fk
        FOREIGN KEY (workspace_id, target_release_id)
        REFERENCES capability_releases (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT chat_confirmations_confirmed_by_fk
        FOREIGN KEY (confirmed_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT chat_confirmations_target_type_check CHECK (target_type IN ('TOOL', 'WORKFLOW')),
    CONSTRAINT chat_confirmations_risk_level_check
        CHECK (risk_level IN ('LOW', 'MEDIUM', 'HIGH', 'CRITICAL')),
    CONSTRAINT chat_confirmations_risk_reasons_array_check
        CHECK (jsonb_typeof(risk_reasons) = 'array'),
    CONSTRAINT chat_confirmations_input_summary_object_check
        CHECK (jsonb_typeof(input_summary) = 'object'),
    CONSTRAINT chat_confirmations_status_check
        CHECK (status IN ('PENDING', 'CONFIRMED', 'CANCELLED', 'EXPIRED')),
    CONSTRAINT chat_confirmations_confirmed_state_check CHECK (
        (status = 'CONFIRMED' AND confirmed_by IS NOT NULL AND confirmed_at IS NOT NULL)
        OR (status <> 'CONFIRMED' AND confirmed_by IS NULL AND confirmed_at IS NULL)
    )
);

CREATE INDEX chat_confirmations_workspace_session_created_idx
    ON chat_confirmations (workspace_id, session_id, created_at DESC, id);
CREATE INDEX chat_confirmations_workspace_run_created_idx
    ON chat_confirmations (workspace_id, run_id, created_at DESC, id);

CREATE FUNCTION reject_chat_confirmation_delete()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'chat confirmations are permanently retained'
        USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER chat_confirmations_no_delete
BEFORE DELETE ON chat_confirmations
FOR EACH ROW EXECUTE FUNCTION reject_chat_confirmation_delete();

ALTER TABLE chat_sessions
    ADD CONSTRAINT chat_sessions_pending_confirmation_fk
        FOREIGN KEY (workspace_id, pending_confirmation_id)
        REFERENCES chat_confirmations (workspace_id, id)
        ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE chat_messages
    ADD CONSTRAINT chat_messages_confirmation_fk
        FOREIGN KEY (workspace_id, confirmation_id)
        REFERENCES chat_confirmations (workspace_id, id) ON DELETE RESTRICT;
