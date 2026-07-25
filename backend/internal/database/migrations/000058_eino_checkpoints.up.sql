-- Eino CheckPointStore persistence (adk / compose gob payloads).
-- checkpoint_id is the full multi-tenant key: ws/{workspaceID}/...
-- workspace_id is a redundant column parsed from the ID and validated on write.
-- expires_at aligns with confirmation TTL (PR3 wires write/renew + cleanup job).
CREATE TABLE eino_checkpoints (
    checkpoint_id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    owner_id TEXT NOT NULL,
    payload BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT eino_checkpoints_checkpoint_id_not_blank
        CHECK (length(btrim(checkpoint_id)) > 0),
    CONSTRAINT eino_checkpoints_workspace_id_not_blank
        CHECK (length(btrim(workspace_id)) > 0),
    CONSTRAINT eino_checkpoints_owner_id_not_blank
        CHECK (length(btrim(owner_id)) > 0),
    CONSTRAINT eino_checkpoints_kind_check
        CHECK (kind IN ('agent_run', 'workflow_execution'))
);

CREATE INDEX eino_checkpoints_workspace_id_idx
    ON eino_checkpoints (workspace_id);

CREATE INDEX eino_checkpoints_expires_at_idx
    ON eino_checkpoints (expires_at);

CREATE INDEX eino_checkpoints_owner_idx
    ON eino_checkpoints (kind, owner_id);
