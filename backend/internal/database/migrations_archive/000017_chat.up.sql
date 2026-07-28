CREATE TABLE chat_sessions (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'ACTIVE',
    created_by UUID NOT NULL,
    latest_run_id UUID,
    pending_confirmation_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    lock_version BIGINT NOT NULL DEFAULT 1,
    CONSTRAINT chat_sessions_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT chat_sessions_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT chat_sessions_workspace_agent_fk
        FOREIGN KEY (workspace_id, agent_id)
        REFERENCES agents (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT chat_sessions_created_by_fk
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT chat_sessions_status_check CHECK (status IN ('ACTIVE', 'ARCHIVED')),
    CONSTRAINT chat_sessions_lock_version_check CHECK (lock_version > 0),
    CONSTRAINT chat_sessions_timestamps_check CHECK (updated_at >= created_at)
);

CREATE INDEX chat_sessions_workspace_creator_updated_idx
    ON chat_sessions (workspace_id, created_by, status, updated_at DESC, id);
CREATE INDEX chat_sessions_workspace_agent_updated_idx
    ON chat_sessions (workspace_id, agent_id, updated_at DESC, id);

CREATE FUNCTION reject_chat_session_delete()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'chat sessions are permanently retained and cannot be deleted'
        USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER chat_sessions_no_delete
BEFORE DELETE ON chat_sessions
FOR EACH ROW EXECUTE FUNCTION reject_chat_session_delete();

CREATE TABLE chat_messages (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    session_id UUID NOT NULL,
    role TEXT NOT NULL,
    content TEXT,
    content_object_id UUID,
    content_sha256 CHAR(64) NOT NULL,
    status TEXT NOT NULL,
    run_id UUID,
    confirmation_id UUID,
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT chat_messages_workspace_session_id_key UNIQUE (workspace_id, session_id, id),
    CONSTRAINT chat_messages_workspace_session_fk
        FOREIGN KEY (workspace_id, session_id)
        REFERENCES chat_sessions (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT chat_messages_created_by_fk
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT chat_messages_role_check
        CHECK (role IN ('USER', 'ASSISTANT', 'SYSTEM', 'TOOL')),
    CONSTRAINT chat_messages_content_check CHECK (
        (content IS NOT NULL AND length(content) > 0) OR content_object_id IS NOT NULL
    ),
    CONSTRAINT chat_messages_sha256_check
        CHECK (content_sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT chat_messages_status_check CHECK (
        status IN (
            'RECEIVED', 'PROCESSING', 'NEEDS_INPUT', 'MATCHED_NONE',
            'PENDING_CONFIRMATION', 'EXECUTED', 'FAILED'
        )
    ),
    CONSTRAINT chat_messages_user_actor_check
        CHECK (role <> 'USER' OR created_by IS NOT NULL),
    CONSTRAINT chat_messages_confirmation_status_check CHECK (
        status <> 'PENDING_CONFIRMATION' OR confirmation_id IS NOT NULL
    )
);

CREATE INDEX chat_messages_workspace_session_created_idx
    ON chat_messages (workspace_id, session_id, created_at, id);
CREATE INDEX chat_messages_workspace_run_created_idx
    ON chat_messages (workspace_id, run_id, created_at, id)
    WHERE run_id IS NOT NULL;
CREATE INDEX chat_messages_workspace_confirmation_idx
    ON chat_messages (workspace_id, confirmation_id, id)
    WHERE confirmation_id IS NOT NULL;

CREATE FUNCTION enforce_chat_message_permanent_retention()
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

CREATE TRIGGER chat_messages_permanent_retention
BEFORE UPDATE OR DELETE ON chat_messages
FOR EACH ROW EXECUTE FUNCTION enforce_chat_message_permanent_retention();
