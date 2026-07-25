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
