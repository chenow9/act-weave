CREATE TABLE workflows (
    capability_id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    current_draft_id UUID NOT NULL,
    active_revision_id UUID,
    latest_compilation_id UUID,
    CONSTRAINT workflows_workspace_capability_key UNIQUE (workspace_id, capability_id),
    CONSTRAINT workflows_workspace_capability_fk
        FOREIGN KEY (workspace_id, capability_id)
        REFERENCES capabilities (workspace_id, id) ON DELETE RESTRICT
);

CREATE FUNCTION enforce_workflow_capability_kind()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM capabilities
        WHERE workspace_id = NEW.workspace_id
          AND id = NEW.capability_id
          AND kind = 'WORKFLOW'
          AND deleted_at IS NULL
    ) THEN
        RAISE EXCEPTION 'workflow specialization requires an active WORKFLOW capability identity'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER workflows_capability_kind_integrity
BEFORE INSERT OR UPDATE OF workspace_id, capability_id ON workflows
FOR EACH ROW EXECUTE FUNCTION enforce_workflow_capability_kind();

CREATE TABLE workflow_drafts (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    capability_id UUID NOT NULL,
    draft_version BIGINT NOT NULL DEFAULT 1,
    schema_version TEXT NOT NULL,
    graph JSONB NOT NULL,
    graph_hash CHAR(64) NOT NULL,
    updated_by UUID NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    lock_version BIGINT NOT NULL DEFAULT 1,
    CONSTRAINT workflow_drafts_workspace_capability_id_key
        UNIQUE (workspace_id, capability_id, id),
    CONSTRAINT workflow_drafts_capability_key UNIQUE (capability_id),
    CONSTRAINT workflow_drafts_workflow_fk
        FOREIGN KEY (workspace_id, capability_id)
        REFERENCES workflows (workspace_id, capability_id) ON DELETE CASCADE,
    CONSTRAINT workflow_drafts_updated_by_fk
        FOREIGN KEY (updated_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT workflow_drafts_version_check CHECK (draft_version > 0),
    CONSTRAINT workflow_drafts_schema_version_not_blank
        CHECK (length(btrim(schema_version)) > 0),
    CONSTRAINT workflow_drafts_graph_object_check CHECK (jsonb_typeof(graph) = 'object'),
    CONSTRAINT workflow_drafts_graph_hash_check CHECK (graph_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT workflow_drafts_lock_version_check CHECK (lock_version > 0)
);

CREATE INDEX workflow_drafts_workspace_updated_idx
    ON workflow_drafts (workspace_id, updated_at DESC, id);

CREATE TABLE workflow_compilations (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    capability_id UUID NOT NULL,
    draft_id UUID NOT NULL,
    draft_version BIGINT NOT NULL,
    graph_hash CHAR(64) NOT NULL,
    compiler_version TEXT NOT NULL,
    status TEXT NOT NULL,
    spec JSONB NOT NULL,
    plan JSONB NOT NULL,
    issues JSONB NOT NULL DEFAULT '[]'::JSONB,
    plan_hash CHAR(64) NOT NULL,
    compiled_by UUID NOT NULL,
    compiled_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT workflow_compilations_workspace_capability_id_key
        UNIQUE (workspace_id, capability_id, id),
    CONSTRAINT workflow_compilations_workspace_draft_fk
        FOREIGN KEY (workspace_id, capability_id, draft_id)
        REFERENCES workflow_drafts (workspace_id, capability_id, id) ON DELETE RESTRICT,
    CONSTRAINT workflow_compilations_compiled_by_fk
        FOREIGN KEY (compiled_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT workflow_compilations_draft_version_check CHECK (draft_version > 0),
    CONSTRAINT workflow_compilations_graph_hash_check CHECK (graph_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT workflow_compilations_compiler_version_not_blank
        CHECK (length(btrim(compiler_version)) > 0),
    CONSTRAINT workflow_compilations_status_check
        CHECK (status IN ('VALID', 'INVALID', 'FAILED')),
    CONSTRAINT workflow_compilations_spec_object_check CHECK (jsonb_typeof(spec) = 'object'),
    CONSTRAINT workflow_compilations_plan_object_check CHECK (jsonb_typeof(plan) = 'object'),
    CONSTRAINT workflow_compilations_issues_array_check CHECK (jsonb_typeof(issues) = 'array'),
    CONSTRAINT workflow_compilations_plan_hash_check CHECK (plan_hash ~ '^[0-9a-f]{64}$')
);

CREATE INDEX workflow_compilations_workspace_capability_compiled_idx
    ON workflow_compilations (workspace_id, capability_id, compiled_at DESC, id);
CREATE INDEX workflow_compilations_workspace_status_compiled_idx
    ON workflow_compilations (workspace_id, status, compiled_at DESC, id);

CREATE FUNCTION reject_workflow_compilation_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'workflow compilations are immutable'
        USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER workflow_compilations_immutable
BEFORE UPDATE OR DELETE ON workflow_compilations
FOR EACH ROW EXECUTE FUNCTION reject_workflow_compilation_mutation();

CREATE TABLE workflow_revisions (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    capability_id UUID NOT NULL,
    revision_no INTEGER NOT NULL,
    source_compilation_id UUID NOT NULL,
    draft_snapshot JSONB NOT NULL,
    spec_snapshot JSONB NOT NULL,
    plan_snapshot JSONB NOT NULL,
    plan_hash CHAR(64) NOT NULL,
    status TEXT NOT NULL DEFAULT 'PUBLISHED',
    publish_note TEXT NOT NULL DEFAULT '',
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    activated_at TIMESTAMPTZ,
    retired_at TIMESTAMPTZ,
    CONSTRAINT workflow_revisions_workspace_capability_id_key
        UNIQUE (workspace_id, capability_id, id),
    CONSTRAINT workflow_revisions_capability_revision_key
        UNIQUE (capability_id, revision_no),
    CONSTRAINT workflow_revisions_source_compilation_fk
        FOREIGN KEY (workspace_id, capability_id, source_compilation_id)
        REFERENCES workflow_compilations (workspace_id, capability_id, id) ON DELETE RESTRICT,
    CONSTRAINT workflow_revisions_created_by_fk
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT workflow_revisions_revision_no_check CHECK (revision_no > 0),
    CONSTRAINT workflow_revisions_draft_snapshot_object_check
        CHECK (jsonb_typeof(draft_snapshot) = 'object'),
    CONSTRAINT workflow_revisions_spec_snapshot_object_check
        CHECK (jsonb_typeof(spec_snapshot) = 'object'),
    CONSTRAINT workflow_revisions_plan_snapshot_object_check
        CHECK (jsonb_typeof(plan_snapshot) = 'object'),
    CONSTRAINT workflow_revisions_plan_hash_check CHECK (plan_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT workflow_revisions_status_check CHECK (status IN ('PUBLISHED', 'RETIRED')),
    CONSTRAINT workflow_revisions_retirement_state_check CHECK (
        (status = 'PUBLISHED' AND retired_at IS NULL)
        OR (status = 'RETIRED' AND retired_at IS NOT NULL)
    ),
    CONSTRAINT workflow_revisions_activated_at_check
        CHECK (activated_at IS NULL OR activated_at >= created_at),
    CONSTRAINT workflow_revisions_retired_at_check
        CHECK (retired_at IS NULL OR retired_at >= created_at)
);

CREATE INDEX workflow_revisions_workspace_capability_revision_idx
    ON workflow_revisions (workspace_id, capability_id, revision_no DESC, id);

CREATE FUNCTION reject_workflow_revision_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'workflow revisions are immutable'
        USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER workflow_revisions_immutable
BEFORE UPDATE OR DELETE ON workflow_revisions
FOR EACH ROW EXECUTE FUNCTION reject_workflow_revision_mutation();

CREATE TABLE workflow_trial_runs (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    capability_id UUID NOT NULL,
    compilation_id UUID NOT NULL,
    execution_id UUID NOT NULL,
    status TEXT NOT NULL DEFAULT 'PENDING',
    input_hash CHAR(64) NOT NULL,
    started_by UUID NOT NULL,
    started_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at TIMESTAMPTZ,
    CONSTRAINT workflow_trial_runs_workspace_compilation_fk
        FOREIGN KEY (workspace_id, capability_id, compilation_id)
        REFERENCES workflow_compilations (workspace_id, capability_id, id) ON DELETE RESTRICT,
    CONSTRAINT workflow_trial_runs_started_by_fk
        FOREIGN KEY (started_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT workflow_trial_runs_status_check
        CHECK (status IN ('PENDING', 'RUNNING', 'SUCCEEDED', 'FAILED', 'CANCELLED')),
    CONSTRAINT workflow_trial_runs_input_hash_check CHECK (input_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT workflow_trial_runs_finish_state_check CHECK (
        (status IN ('PENDING', 'RUNNING') AND finished_at IS NULL)
        OR (status IN ('SUCCEEDED', 'FAILED', 'CANCELLED') AND finished_at IS NOT NULL)
    ),
    CONSTRAINT workflow_trial_runs_finished_at_check
        CHECK (finished_at IS NULL OR finished_at >= started_at)
);

CREATE INDEX workflow_trial_runs_workspace_capability_started_idx
    ON workflow_trial_runs (workspace_id, capability_id, started_at DESC, id);
CREATE INDEX workflow_trial_runs_workspace_status_started_idx
    ON workflow_trial_runs (workspace_id, status, started_at DESC, id);

ALTER TABLE workflows
    ADD CONSTRAINT workflows_current_draft_fk
        FOREIGN KEY (workspace_id, capability_id, current_draft_id)
        REFERENCES workflow_drafts (workspace_id, capability_id, id)
        ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
    ADD CONSTRAINT workflows_active_revision_fk
        FOREIGN KEY (workspace_id, capability_id, active_revision_id)
        REFERENCES workflow_revisions (workspace_id, capability_id, id)
        ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
    ADD CONSTRAINT workflows_latest_compilation_fk
        FOREIGN KEY (workspace_id, capability_id, latest_compilation_id)
        REFERENCES workflow_compilations (workspace_id, capability_id, id)
        ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED;

CREATE FUNCTION enforce_workflow_active_revision()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.active_revision_id IS NULL THEN
        RETURN NEW;
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM workflow_revisions
        WHERE workspace_id = NEW.workspace_id
          AND capability_id = NEW.capability_id
          AND id = NEW.active_revision_id
          AND status = 'PUBLISHED'
          AND retired_at IS NULL
    ) THEN
        RAISE EXCEPTION 'active workflow revision must be a published revision of the same workflow'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER workflows_active_revision_integrity
BEFORE INSERT OR UPDATE OF active_revision_id, workspace_id, capability_id ON workflows
FOR EACH ROW EXECUTE FUNCTION enforce_workflow_active_revision();
