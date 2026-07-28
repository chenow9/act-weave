CREATE TABLE agent_access_data_commands (
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    client_id UUID NOT NULL,
    service_principal_id UUID NOT NULL,
    subject_id UUID NOT NULL,
    operation TEXT NOT NULL,
    idempotency_key UUID NOT NULL,
    request_hash BYTEA NOT NULL CHECK (octet_length(request_hash) = 32),
    resource_type TEXT,
    resource_id UUID,
    response_version BIGINT CHECK (response_version IS NULL OR response_version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (
        workspace_id, agent_id, client_id, service_principal_id,
        subject_id, operation, idempotency_key
    ),
    CHECK (operation IN (
        'conversation.create', 'run.create', 'run.cancel', 'interaction.decide'
    )),
    CHECK ((resource_type IS NULL) = (resource_id IS NULL)),
    CHECK (resource_type IS NULL OR resource_type IN ('CONVERSATION', 'RUN', 'INTERACTION')),
    CHECK (expires_at >= created_at + INTERVAL '24 hours')
);

CREATE INDEX agent_access_data_commands_expiry_idx
    ON agent_access_data_commands (expires_at, workspace_id, agent_id);

CREATE INDEX agent_access_data_commands_resource_idx
    ON agent_access_data_commands (workspace_id, resource_type, resource_id)
    WHERE resource_id IS NOT NULL;

COMMENT ON TABLE agent_access_data_commands IS
    'Unified durable data-plane command receipts; request hashes only, never raw tokens or command bodies';
