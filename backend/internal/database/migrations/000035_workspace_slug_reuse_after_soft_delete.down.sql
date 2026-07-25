DROP INDEX IF EXISTS workspaces_slug_active_key;

ALTER TABLE workspaces
    ADD CONSTRAINT workspaces_slug_key UNIQUE (slug);
