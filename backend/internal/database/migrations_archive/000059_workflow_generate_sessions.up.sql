-- SmartGenerateSession storage for multi-turn intelligent orchestration (D15).
-- Console-only generate context; not ChatSession / AAP Conversation.
-- Application generates UUIDv7 for entity ids (no random UUID defaults).

CREATE TABLE workflow_generate_sessions (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    workflow_id UUID,
    model_config_id UUID NOT NULL,
    status TEXT NOT NULL DEFAULT 'OPEN',
    prompt_id TEXT,
    prompt_hash CHAR(64),
    constraints JSONB NOT NULL DEFAULT '{}'::JSONB,
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    closed_at TIMESTAMPTZ,
    lock_version BIGINT NOT NULL DEFAULT 1,
    CONSTRAINT workflow_generate_sessions_workspace_id_id_key
        UNIQUE (workspace_id, id),
    CONSTRAINT workflow_generate_sessions_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT workflow_generate_sessions_workspace_agent_fk
        FOREIGN KEY (workspace_id, agent_id)
        REFERENCES agents (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT workflow_generate_sessions_workspace_model_config_fk
        FOREIGN KEY (workspace_id, model_config_id)
        REFERENCES model_configs (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT workflow_generate_sessions_workspace_workflow_fk
        FOREIGN KEY (workspace_id, workflow_id)
        REFERENCES workflows (workspace_id, capability_id) ON DELETE RESTRICT,
    CONSTRAINT workflow_generate_sessions_created_by_fk
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT workflow_generate_sessions_status_check
        CHECK (status IN ('OPEN', 'CLOSED')),
    CONSTRAINT workflow_generate_sessions_closed_state_check CHECK (
        (status = 'OPEN' AND closed_at IS NULL)
        OR (status = 'CLOSED' AND closed_at IS NOT NULL)
    ),
    CONSTRAINT workflow_generate_sessions_prompt_hash_check CHECK (
        prompt_hash IS NULL OR prompt_hash ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT workflow_generate_sessions_constraints_object_check
        CHECK (jsonb_typeof(constraints) = 'object'),
    CONSTRAINT workflow_generate_sessions_lock_version_check CHECK (lock_version > 0),
    CONSTRAINT workflow_generate_sessions_timestamps_check CHECK (updated_at >= created_at),
    CONSTRAINT workflow_generate_sessions_closed_at_check
        CHECK (closed_at IS NULL OR closed_at >= created_at)
);

CREATE INDEX workflow_generate_sessions_workspace_status_updated_idx
    ON workflow_generate_sessions (workspace_id, status, updated_at DESC, id);

CREATE INDEX workflow_generate_sessions_workspace_agent_updated_idx
    ON workflow_generate_sessions (workspace_id, agent_id, updated_at DESC, id);

CREATE INDEX workflow_generate_sessions_workspace_workflow_idx
    ON workflow_generate_sessions (workspace_id, workflow_id, id)
    WHERE workflow_id IS NOT NULL;

CREATE TABLE workflow_generate_turns (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    session_id UUID NOT NULL,
    turn_index INTEGER NOT NULL,
    user_message TEXT NOT NULL,
    assistant_message TEXT,
    generation_id UUID NOT NULL,
    guard_ok BOOLEAN NOT NULL DEFAULT FALSE,
    guard_report JSONB NOT NULL DEFAULT '{}'::JSONB,
    draft_version BIGINT,
    status TEXT NOT NULL,
    error_code TEXT,
    prompt_id TEXT,
    prompt_hash CHAR(64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT workflow_generate_turns_workspace_session_id_key
        UNIQUE (workspace_id, session_id, id),
    CONSTRAINT workflow_generate_turns_session_turn_index_key
        UNIQUE (session_id, turn_index),
    CONSTRAINT workflow_generate_turns_generation_id_key
        UNIQUE (generation_id),
    CONSTRAINT workflow_generate_turns_workspace_session_fk
        FOREIGN KEY (workspace_id, session_id)
        REFERENCES workflow_generate_sessions (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT workflow_generate_turns_turn_index_check CHECK (turn_index > 0),
    CONSTRAINT workflow_generate_turns_user_message_not_blank
        CHECK (length(btrim(user_message)) > 0),
    CONSTRAINT workflow_generate_turns_status_check
        CHECK (status IN ('SUCCEEDED', 'GUARD_REJECTED', 'FAILED')),
    CONSTRAINT workflow_generate_turns_guard_report_object_check
        CHECK (jsonb_typeof(guard_report) = 'object'),
    CONSTRAINT workflow_generate_turns_draft_version_check
        CHECK (draft_version IS NULL OR draft_version > 0),
    CONSTRAINT workflow_generate_turns_prompt_hash_check CHECK (
        prompt_hash IS NULL OR prompt_hash ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT workflow_generate_turns_success_guard_check CHECK (
        (status = 'SUCCEEDED' AND guard_ok = TRUE AND draft_version IS NOT NULL)
        OR (status <> 'SUCCEEDED')
    ),
    CONSTRAINT workflow_generate_turns_failed_guard_check CHECK (
        (status = 'GUARD_REJECTED' AND guard_ok = FALSE)
        OR (status <> 'GUARD_REJECTED')
    )
);

CREATE INDEX workflow_generate_turns_workspace_session_index_idx
    ON workflow_generate_turns (workspace_id, session_id, turn_index ASC, id);

CREATE INDEX workflow_generate_turns_workspace_created_idx
    ON workflow_generate_turns (workspace_id, created_at DESC, id);
