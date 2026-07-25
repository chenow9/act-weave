CREATE TABLE agent_capability_bindings (
    workspace_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    capability_id UUID NOT NULL,
    version_policy TEXT NOT NULL,
    pinned_release_id UUID,
    connection_id UUID,
    execution_policy_id UUID,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    config_overrides JSONB NOT NULL DEFAULT '{}'::JSONB,
    bound_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    lock_version BIGINT NOT NULL DEFAULT 1,
    PRIMARY KEY (agent_id, capability_id),
    CONSTRAINT agent_capability_bindings_workspace_agent_capability_key
        UNIQUE (workspace_id, agent_id, capability_id),
    CONSTRAINT agent_capability_bindings_workspace_agent_fk
        FOREIGN KEY (workspace_id, agent_id)
        REFERENCES agents (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT agent_capability_bindings_workspace_capability_fk
        FOREIGN KEY (workspace_id, capability_id)
        REFERENCES capabilities (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT agent_capability_bindings_pinned_release_fk
        FOREIGN KEY (workspace_id, capability_id, pinned_release_id)
        REFERENCES capability_releases (workspace_id, capability_id, id) ON DELETE RESTRICT,
    CONSTRAINT agent_capability_bindings_connection_fk
        FOREIGN KEY (workspace_id, connection_id)
        REFERENCES service_connections (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT agent_capability_bindings_bound_by_fk
        FOREIGN KEY (bound_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT agent_capability_bindings_version_policy_check
        CHECK (version_policy IN ('FOLLOW_ACTIVE', 'PINNED')),
    CONSTRAINT agent_capability_bindings_pinned_policy_check CHECK (
        (version_policy = 'FOLLOW_ACTIVE' AND pinned_release_id IS NULL)
        OR (version_policy = 'PINNED' AND pinned_release_id IS NOT NULL)
    ),
    CONSTRAINT agent_capability_bindings_config_object_check
        CHECK (jsonb_typeof(config_overrides) = 'object'),
    CONSTRAINT agent_capability_bindings_timestamps_check CHECK (updated_at >= created_at),
    CONSTRAINT agent_capability_bindings_lock_version_check CHECK (lock_version > 0)
);

CREATE INDEX agent_capability_bindings_workspace_agent_enabled_idx
    ON agent_capability_bindings (workspace_id, agent_id, enabled, capability_id);
CREATE INDEX agent_capability_bindings_workspace_capability_idx
    ON agent_capability_bindings (workspace_id, capability_id, agent_id);
CREATE INDEX agent_capability_bindings_workspace_connection_idx
    ON agent_capability_bindings (workspace_id, connection_id, agent_id)
    WHERE connection_id IS NOT NULL;
