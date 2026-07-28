CREATE TABLE agents (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    name CITEXT NOT NULL,
    role_description TEXT NOT NULL DEFAULT '',
    current_prompt_revision_id UUID,
    model_config_id UUID NOT NULL,
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    status TEXT NOT NULL DEFAULT 'ACTIVE',
    created_by UUID NOT NULL,
    updated_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    lock_version BIGINT NOT NULL DEFAULT 1,
    deleted_at TIMESTAMPTZ,
    CONSTRAINT agents_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT agents_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT agents_workspace_model_config_fk
        FOREIGN KEY (workspace_id, model_config_id)
        REFERENCES model_configs (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT agents_created_by_fk
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT agents_updated_by_fk
        FOREIGN KEY (updated_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT agents_name_not_blank CHECK (length(btrim(name::TEXT)) > 0),
    CONSTRAINT agents_status_check CHECK (status IN ('ACTIVE', 'DISABLED', 'ERROR')),
    CONSTRAINT agents_lock_version_check CHECK (lock_version > 0),
    CONSTRAINT agents_timestamps_check CHECK (updated_at >= created_at),
    CONSTRAINT agents_deleted_at_check CHECK (deleted_at IS NULL OR deleted_at >= created_at)
);

CREATE UNIQUE INDEX agents_workspace_name_active_key
    ON agents (workspace_id, name) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX agents_workspace_default_active_key
    ON agents (workspace_id) WHERE is_default AND deleted_at IS NULL;
CREATE INDEX agents_workspace_status_updated_idx
    ON agents (workspace_id, status, updated_at DESC, id) WHERE deleted_at IS NULL;

CREATE TABLE agent_prompt_revisions (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    revision_no INTEGER NOT NULL,
    system_prompt TEXT NOT NULL,
    source TEXT NOT NULL,
    content_sha256 CHAR(64) NOT NULL,
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT agent_prompt_revisions_workspace_agent_id_key
        UNIQUE (workspace_id, agent_id, id),
    CONSTRAINT agent_prompt_revisions_agent_revision_key
        UNIQUE (agent_id, revision_no),
    CONSTRAINT agent_prompt_revisions_workspace_agent_fk
        FOREIGN KEY (workspace_id, agent_id)
        REFERENCES agents (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT agent_prompt_revisions_created_by_fk
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT agent_prompt_revisions_revision_no_check CHECK (revision_no > 0),
    CONSTRAINT agent_prompt_revisions_prompt_not_blank CHECK (length(btrim(system_prompt)) > 0),
    CONSTRAINT agent_prompt_revisions_source_check
        CHECK (source IN ('MANUAL', 'ENHANCED', 'GENERATED', 'IMPORTED')),
    CONSTRAINT agent_prompt_revisions_sha256_check
        CHECK (content_sha256 ~ '^[0-9a-f]{64}$')
);

CREATE INDEX agent_prompt_revisions_workspace_agent_revision_idx
    ON agent_prompt_revisions (workspace_id, agent_id, revision_no DESC, id);

ALTER TABLE agents
    ADD CONSTRAINT agents_current_prompt_revision_fk
    FOREIGN KEY (workspace_id, id, current_prompt_revision_id)
    REFERENCES agent_prompt_revisions (workspace_id, agent_id, id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

-- The pre-normalization column had no referential target. Legacy values are
-- intentionally discarded; this phase does not migrate the old state model.
UPDATE workspaces SET default_agent_id = NULL WHERE default_agent_id IS NOT NULL;

ALTER TABLE workspaces
    ADD CONSTRAINT workspaces_default_agent_fk
    FOREIGN KEY (id, default_agent_id)
    REFERENCES agents (workspace_id, id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE prompt_runs (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    agent_id UUID,
    operation_type TEXT NOT NULL,
    model_config_id UUID NOT NULL,
    model_snapshot JSONB NOT NULL,
    input_object_id UUID NOT NULL,
    output_object_id UUID,
    status TEXT NOT NULL DEFAULT 'PENDING',
    accepted_revision_id UUID,
    trace_id TEXT NOT NULL,
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at TIMESTAMPTZ,
    error_code TEXT,
    CONSTRAINT prompt_runs_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT prompt_runs_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT prompt_runs_workspace_agent_fk
        FOREIGN KEY (workspace_id, agent_id)
        REFERENCES agents (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT prompt_runs_workspace_model_config_fk
        FOREIGN KEY (workspace_id, model_config_id)
        REFERENCES model_configs (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT prompt_runs_accepted_revision_fk
        FOREIGN KEY (workspace_id, agent_id, accepted_revision_id)
        REFERENCES agent_prompt_revisions (workspace_id, agent_id, id) ON DELETE RESTRICT,
    CONSTRAINT prompt_runs_created_by_fk
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT prompt_runs_operation_check
        CHECK (operation_type IN ('ENHANCE', 'GENERATE', 'PREVIEW')),
    CONSTRAINT prompt_runs_model_snapshot_object_check
        CHECK (jsonb_typeof(model_snapshot) = 'object'),
    CONSTRAINT prompt_runs_status_check
        CHECK (status IN ('PENDING', 'RUNNING', 'SUCCEEDED', 'FAILED')),
    CONSTRAINT prompt_runs_trace_not_blank CHECK (length(btrim(trace_id)) > 0),
    CONSTRAINT prompt_runs_finished_status_check CHECK (
        (status IN ('PENDING', 'RUNNING') AND finished_at IS NULL)
        OR (status IN ('SUCCEEDED', 'FAILED') AND finished_at IS NOT NULL)
    ),
    CONSTRAINT prompt_runs_output_status_check CHECK (
        output_object_id IS NULL OR status = 'SUCCEEDED'
    ),
    CONSTRAINT prompt_runs_acceptance_check CHECK (
        accepted_revision_id IS NULL
        OR (agent_id IS NOT NULL AND status = 'SUCCEEDED' AND output_object_id IS NOT NULL)
    ),
    CONSTRAINT prompt_runs_error_status_check CHECK (
        (status = 'FAILED' AND error_code IS NOT NULL)
        OR (status <> 'FAILED' AND error_code IS NULL)
    )
);

CREATE INDEX prompt_runs_workspace_created_idx
    ON prompt_runs (workspace_id, created_at DESC, id);
CREATE INDEX prompt_runs_workspace_agent_created_idx
    ON prompt_runs (workspace_id, agent_id, created_at DESC, id)
    WHERE agent_id IS NOT NULL;
CREATE INDEX prompt_runs_trace_idx ON prompt_runs (trace_id, id);

CREATE FUNCTION reject_agent_prompt_revision_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'agent prompt revisions are immutable and permanently retained'
        USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER agent_prompt_revisions_immutable
BEFORE UPDATE OR DELETE ON agent_prompt_revisions
FOR EACH ROW EXECUTE FUNCTION reject_agent_prompt_revision_mutation();
