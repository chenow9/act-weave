ALTER TABLE workspaces
    DROP CONSTRAINT workspaces_slug_key;

CREATE UNIQUE INDEX workspaces_slug_active_key
    ON workspaces (slug)
    WHERE deleted_at IS NULL;
