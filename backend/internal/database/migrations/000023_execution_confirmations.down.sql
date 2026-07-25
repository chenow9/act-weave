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
