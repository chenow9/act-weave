CREATE TABLE workspaces (
    id UUID PRIMARY KEY,
    slug CITEXT NOT NULL,
    display_name VARCHAR(120) NOT NULL,
    mode TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'ACTIVE',
    owner_user_id UUID NOT NULL,
    default_agent_id UUID,
    default_model_config_id UUID,
    settings JSONB NOT NULL DEFAULT '{}'::JSONB,
    created_by UUID NOT NULL,
    updated_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    lock_version BIGINT NOT NULL DEFAULT 1,
    deleted_at TIMESTAMPTZ,
    CONSTRAINT workspaces_slug_key UNIQUE (slug),
    CONSTRAINT workspaces_owner_user_fk
        FOREIGN KEY (owner_user_id) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT workspaces_created_by_fk
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT workspaces_updated_by_fk
        FOREIGN KEY (updated_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT workspaces_slug_not_blank CHECK (length(btrim(slug::TEXT)) > 0),
    CONSTRAINT workspaces_display_name_not_blank CHECK (length(btrim(display_name)) > 0),
    CONSTRAINT workspaces_mode_check CHECK (mode IN ('PRODUCTION', 'SANDBOX')),
    CONSTRAINT workspaces_status_check CHECK (status IN ('ACTIVE', 'DISABLED')),
    CONSTRAINT workspaces_settings_object_check CHECK (jsonb_typeof(settings) = 'object'),
    CONSTRAINT workspaces_lock_version_check CHECK (lock_version > 0),
    CONSTRAINT workspaces_timestamps_check CHECK (updated_at >= created_at),
    CONSTRAINT workspaces_deleted_at_check CHECK (deleted_at IS NULL OR deleted_at >= created_at)
);

CREATE INDEX workspaces_status_updated_idx
    ON workspaces (status, updated_at DESC, id)
    WHERE deleted_at IS NULL;

CREATE INDEX workspaces_owner_user_idx
    ON workspaces (owner_user_id, id)
    WHERE deleted_at IS NULL;

CREATE TABLE workspace_members (
    workspace_id UUID NOT NULL,
    user_id UUID NOT NULL,
    role TEXT NOT NULL,
    invited_by UUID,
    joined_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    disabled_at TIMESTAMPTZ,
    PRIMARY KEY (workspace_id, user_id),
    CONSTRAINT workspace_members_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT workspace_members_user_fk
        FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT workspace_members_invited_by_fk
        FOREIGN KEY (invited_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT workspace_members_role_check
        CHECK (role IN ('OWNER', 'ADMIN', 'EDITOR', 'OPERATOR', 'VIEWER')),
    CONSTRAINT workspace_members_disabled_at_check
        CHECK (disabled_at IS NULL OR disabled_at >= joined_at)
);

CREATE INDEX workspace_members_user_active_idx
    ON workspace_members (user_id, workspace_id)
    WHERE disabled_at IS NULL;

CREATE INDEX workspace_members_workspace_role_idx
    ON workspace_members (workspace_id, role, user_id)
    WHERE disabled_at IS NULL;
