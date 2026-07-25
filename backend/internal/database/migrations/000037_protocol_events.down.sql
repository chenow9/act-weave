DROP TABLE IF EXISTS protocol_events;
DROP TABLE IF EXISTS protocol_event_streams;

ALTER TABLE agent_runs
    DROP CONSTRAINT IF EXISTS agent_runs_workspace_agent_session_id_key;
ALTER TABLE chat_sessions
    DROP CONSTRAINT IF EXISTS chat_sessions_workspace_agent_id_key;
