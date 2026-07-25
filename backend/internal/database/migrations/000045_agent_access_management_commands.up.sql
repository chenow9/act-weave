CREATE TABLE agent_access_management_commands (
    workspace_id UUID NOT NULL,
    actor_id UUID NOT NULL,
    idempotency_key UUID NOT NULL,
    operation VARCHAR(255) NOT NULL,
    request_hash BYTEA NOT NULL,
    state TEXT NOT NULL DEFAULT 'PENDING',
    response_status INTEGER,
    response_body JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    completed_at TIMESTAMPTZ,
    PRIMARY KEY (workspace_id, actor_id, idempotency_key),
    CONSTRAINT agent_access_management_commands_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT agent_access_management_commands_actor_fk
        FOREIGN KEY (actor_id) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT agent_access_management_commands_operation_check CHECK (
        operation ~ '^[a-z0-9][a-z0-9:-]{0,254}$'
    ),
    CONSTRAINT agent_access_management_commands_request_hash_check
        CHECK (octet_length(request_hash) = 32),
    CONSTRAINT agent_access_management_commands_state_check
        CHECK (state IN ('PENDING', 'COMPLETED')),
    CONSTRAINT agent_access_management_commands_response_check CHECK (
        (state = 'PENDING' AND response_status IS NULL
            AND response_body IS NULL AND completed_at IS NULL)
        OR
        (state = 'COMPLETED' AND response_status BETWEEN 200 AND 299
            AND response_body IS NOT NULL AND completed_at IS NOT NULL
            AND jsonb_typeof(response_body) = 'object')
    ),
    CONSTRAINT agent_access_management_commands_completed_at_check
        CHECK (completed_at IS NULL OR completed_at >= created_at)
);

CREATE INDEX agent_access_management_commands_created_idx
    ON agent_access_management_commands (workspace_id, created_at DESC);

CREATE FUNCTION enforce_agent_access_management_command_lifecycle()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF ROW(
        NEW.workspace_id, NEW.actor_id, NEW.idempotency_key,
        NEW.operation, NEW.request_hash, NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.workspace_id, OLD.actor_id, OLD.idempotency_key,
        OLD.operation, OLD.request_hash, OLD.created_at
    ) THEN
        RAISE EXCEPTION 'Agent Access management command identity is immutable'
            USING ERRCODE = '55000',
                  CONSTRAINT = 'agent_access_management_commands_immutable_identity';
    END IF;
    IF OLD.state <> 'PENDING' OR NEW.state <> 'COMPLETED' THEN
        RAISE EXCEPTION 'Agent Access management command transition is invalid'
            USING ERRCODE = '55000',
                  CONSTRAINT = 'agent_access_management_commands_lifecycle';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER agent_access_management_commands_lifecycle_guard
BEFORE UPDATE ON agent_access_management_commands
FOR EACH ROW EXECUTE FUNCTION enforce_agent_access_management_command_lifecycle();
