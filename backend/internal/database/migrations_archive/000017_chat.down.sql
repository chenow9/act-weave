DROP TRIGGER IF EXISTS chat_messages_permanent_retention ON chat_messages;
DROP FUNCTION IF EXISTS enforce_chat_message_permanent_retention();
DROP TABLE IF EXISTS chat_messages;
DROP TRIGGER IF EXISTS chat_sessions_no_delete ON chat_sessions;
DROP FUNCTION IF EXISTS reject_chat_session_delete();
DROP TABLE IF EXISTS chat_sessions;
