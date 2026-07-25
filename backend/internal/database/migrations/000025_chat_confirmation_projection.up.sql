CREATE UNIQUE INDEX chat_confirmations_one_pending_per_session_idx
    ON chat_confirmations (workspace_id, session_id)
    WHERE status = 'PENDING';

CREATE FUNCTION enforce_chat_confirmation_projection()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    execution_row execution_confirmations%ROWTYPE;
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'chat confirmations are permanently retained'
            USING ERRCODE = '55000';
    END IF;

    SELECT * INTO execution_row
    FROM execution_confirmations
    WHERE workspace_id = NEW.workspace_id
      AND id = NEW.execution_confirmation_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'chat confirmation execution target not found'
            USING ERRCODE = '23503';
    END IF;
    IF NEW.run_id IS DISTINCT FROM execution_row.run_id
       OR NEW.target_release_id IS DISTINCT FROM execution_row.release_id THEN
        RAISE EXCEPTION 'chat confirmation target differs from execution confirmation'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.status IS DISTINCT FROM execution_row.status
       OR NEW.confirmed_by IS DISTINCT FROM execution_row.confirmed_by
       OR NEW.confirmed_at IS DISTINCT FROM execution_row.confirmed_at THEN
        RAISE EXCEPTION 'chat confirmation state is derived from execution confirmation'
            USING ERRCODE = '55000';
    END IF;

    IF TG_OP = 'UPDATE' AND ROW(
        NEW.id, NEW.workspace_id, NEW.session_id, NEW.run_id,
        NEW.execution_confirmation_id, NEW.target_type, NEW.target_release_id,
        NEW.risk_level, NEW.risk_reasons, NEW.input_summary, NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.session_id, OLD.run_id,
        OLD.execution_confirmation_id, OLD.target_type, OLD.target_release_id,
        OLD.risk_level, OLD.risk_reasons, OLD.input_summary, OLD.created_at
    ) THEN
        RAISE EXCEPTION 'chat confirmation display mapping is immutable'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER chat_confirmations_no_delete ON chat_confirmations;
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
    SET status = NEW.status,
        confirmed_by = NEW.confirmed_by,
        confirmed_at = NEW.confirmed_at
    WHERE workspace_id = NEW.workspace_id
      AND execution_confirmation_id = NEW.id
    RETURNING id INTO chat_confirmation_id;

    IF chat_confirmation_id IS NOT NULL AND NEW.status <> 'PENDING' THEN
        UPDATE chat_sessions
        SET pending_confirmation_id = NULL,
            updated_at = clock_timestamp(),
            lock_version = lock_version + 1
        WHERE workspace_id = NEW.workspace_id
          AND pending_confirmation_id = chat_confirmation_id;

        UPDATE chat_messages
        SET status = CASE WHEN NEW.status = 'CONFIRMED' THEN 'PROCESSING' ELSE 'FAILED' END
        WHERE workspace_id = NEW.workspace_id
          AND confirmation_id = chat_confirmation_id
          AND status = 'PENDING_CONFIRMATION';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER execution_confirmations_chat_projection_sync
AFTER UPDATE OF status, confirmed_by, confirmed_at ON execution_confirmations
FOR EACH ROW
WHEN (OLD.status IS DISTINCT FROM NEW.status)
EXECUTE FUNCTION synchronize_chat_confirmation_projection();
