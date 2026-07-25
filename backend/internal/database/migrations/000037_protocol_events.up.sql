ALTER TABLE chat_sessions
    ADD CONSTRAINT chat_sessions_workspace_agent_id_key
        UNIQUE (workspace_id, agent_id, id);

ALTER TABLE agent_runs
    ADD CONSTRAINT agent_runs_workspace_agent_session_id_key
        UNIQUE (workspace_id, agent_id, session_id, id);

CREATE TABLE protocol_event_streams (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    conversation_id UUID NOT NULL,
    run_id UUID NOT NULL,
    next_sequence BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT protocol_event_streams_workspace_run_key
        UNIQUE (workspace_id, run_id),
    CONSTRAINT protocol_event_streams_scope_id_key
        UNIQUE (workspace_id, agent_id, conversation_id, run_id, id),
    CONSTRAINT protocol_event_streams_workspace_fk
        FOREIGN KEY (workspace_id)
        REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT protocol_event_streams_workspace_agent_fk
        FOREIGN KEY (workspace_id, agent_id)
        REFERENCES agents (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT protocol_event_streams_conversation_fk
        FOREIGN KEY (workspace_id, agent_id, conversation_id)
        REFERENCES chat_sessions (workspace_id, agent_id, id) ON DELETE RESTRICT,
    CONSTRAINT protocol_event_streams_run_scope_fk
        FOREIGN KEY (workspace_id, agent_id, conversation_id, run_id)
        REFERENCES agent_runs (workspace_id, agent_id, session_id, id) ON DELETE RESTRICT,
    CONSTRAINT protocol_event_streams_next_sequence_check
        CHECK (next_sequence > 0)
);

CREATE INDEX protocol_event_streams_scope_created_idx
    ON protocol_event_streams (
        workspace_id, agent_id, conversation_id, created_at DESC, id
    );

CREATE TABLE protocol_events (
    global_position BIGINT GENERATED ALWAYS AS IDENTITY,
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    conversation_id UUID NOT NULL,
    run_id UUID NOT NULL,
    stream_id UUID NOT NULL,
    sequence_no BIGINT NOT NULL,
    event_type TEXT NOT NULL,
    spec_version TEXT NOT NULL,
    item_id UUID,
    interaction_id UUID,
    payload JSONB NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT protocol_events_global_position_key UNIQUE (global_position),
    CONSTRAINT protocol_events_stream_sequence_key UNIQUE (stream_id, sequence_no),
    CONSTRAINT protocol_events_workspace_run_id_key UNIQUE (workspace_id, run_id, id),
    CONSTRAINT protocol_events_stream_scope_fk
        FOREIGN KEY (workspace_id, agent_id, conversation_id, run_id, stream_id)
        REFERENCES protocol_event_streams (
            workspace_id, agent_id, conversation_id, run_id, id
        ) ON DELETE RESTRICT,
    CONSTRAINT protocol_events_sequence_check CHECK (sequence_no > 0),
    CONSTRAINT protocol_events_type_check CHECK (
        event_type ~ '^[a-z][a-z_]*\.[a-z][a-z_]*$'
    ),
    CONSTRAINT protocol_events_spec_version_not_blank CHECK (
        length(btrim(spec_version)) > 0 AND length(spec_version) <= 32
    ),
    CONSTRAINT protocol_events_payload_object_check CHECK (
        jsonb_typeof(payload) = 'object'
    )
);

CREATE INDEX protocol_events_scope_sequence_idx
    ON protocol_events (
        workspace_id, agent_id, conversation_id, run_id, sequence_no
    );
CREATE INDEX protocol_events_global_delivery_idx
    ON protocol_events (global_position, id);
CREATE INDEX protocol_events_item_sequence_idx
    ON protocol_events (workspace_id, run_id, item_id, sequence_no)
    WHERE item_id IS NOT NULL;
CREATE INDEX protocol_events_interaction_sequence_idx
    ON protocol_events (workspace_id, run_id, interaction_id, sequence_no)
    WHERE interaction_id IS NOT NULL;
