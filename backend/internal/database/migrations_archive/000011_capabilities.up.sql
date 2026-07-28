CREATE TABLE capabilities (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    kind TEXT NOT NULL,
    name CITEXT NOT NULL,
    slug CITEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'ACTIVE',
    active_release_id UUID,
    created_by UUID NOT NULL,
    updated_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    lock_version BIGINT NOT NULL DEFAULT 1,
    deleted_at TIMESTAMPTZ,
    CONSTRAINT capabilities_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT capabilities_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT capabilities_created_by_fk
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT capabilities_updated_by_fk
        FOREIGN KEY (updated_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT capabilities_kind_check CHECK (kind IN ('TOOL', 'WORKFLOW')),
    CONSTRAINT capabilities_name_not_blank CHECK (length(btrim(name::TEXT)) > 0),
    CONSTRAINT capabilities_slug_check
        CHECK (slug::TEXT ~ '^[a-z][a-z0-9-]{0,62}[a-z0-9]$' OR slug::TEXT ~ '^[a-z]$'),
    CONSTRAINT capabilities_status_check CHECK (status IN ('ACTIVE', 'DISABLED', 'ERROR')),
    CONSTRAINT capabilities_lock_version_check CHECK (lock_version > 0),
    CONSTRAINT capabilities_timestamps_check CHECK (updated_at >= created_at),
    CONSTRAINT capabilities_deleted_at_check CHECK (deleted_at IS NULL OR deleted_at >= created_at)
);

CREATE UNIQUE INDEX capabilities_workspace_slug_active_key
    ON capabilities (workspace_id, slug) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX capabilities_workspace_name_active_key
    ON capabilities (workspace_id, name) WHERE deleted_at IS NULL;
CREATE INDEX capabilities_workspace_status_updated_idx
    ON capabilities (workspace_id, status, updated_at DESC, id) WHERE deleted_at IS NULL;

CREATE TABLE capability_releases (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    capability_id UUID NOT NULL,
    release_no INTEGER NOT NULL,
    source_type TEXT NOT NULL,
    source_id UUID NOT NULL,
    callable_name TEXT NOT NULL,
    callable_description TEXT NOT NULL DEFAULT '',
    input_schema JSONB NOT NULL,
    output_schema JSONB NOT NULL,
    risk_level TEXT NOT NULL,
    side_effect_level TEXT NOT NULL,
    requires_confirmation BOOLEAN NOT NULL DEFAULT FALSE,
    checksum CHAR(64) NOT NULL,
    published_by UUID NOT NULL,
    published_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    retired_at TIMESTAMPTZ,
    CONSTRAINT capability_releases_workspace_capability_id_key
        UNIQUE (workspace_id, capability_id, id),
    CONSTRAINT capability_releases_capability_release_no_key
        UNIQUE (capability_id, release_no),
    CONSTRAINT capability_releases_workspace_capability_fk
        FOREIGN KEY (workspace_id, capability_id)
        REFERENCES capabilities (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT capability_releases_published_by_fk
        FOREIGN KEY (published_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT capability_releases_release_no_check CHECK (release_no > 0),
    CONSTRAINT capability_releases_source_type_check
        CHECK (source_type IN ('TOOL_VERSION', 'WORKFLOW_REVISION')),
    CONSTRAINT capability_releases_callable_name_check
        CHECK (callable_name ~ '^[A-Za-z_][A-Za-z0-9_]{0,63}$'),
    CONSTRAINT capability_releases_input_schema_object_check
        CHECK (jsonb_typeof(input_schema) = 'object'),
    CONSTRAINT capability_releases_output_schema_object_check
        CHECK (jsonb_typeof(output_schema) = 'object'),
    CONSTRAINT capability_releases_risk_check
        CHECK (risk_level IN ('LOW', 'MEDIUM', 'HIGH', 'CRITICAL')),
    CONSTRAINT capability_releases_side_effect_check
        CHECK (side_effect_level IN ('NONE', 'READ', 'WRITE', 'IRREVERSIBLE')),
    CONSTRAINT capability_releases_checksum_check CHECK (checksum ~ '^[0-9a-f]{64}$'),
    CONSTRAINT capability_releases_retired_at_check
        CHECK (retired_at IS NULL OR retired_at >= published_at)
);

CREATE INDEX capability_releases_workspace_capability_published_idx
    ON capability_releases (workspace_id, capability_id, release_no DESC, id);
CREATE INDEX capability_releases_workspace_callable_idx
    ON capability_releases (workspace_id, lower(callable_name), id);

ALTER TABLE capabilities
    ADD CONSTRAINT capabilities_active_release_fk
    FOREIGN KEY (workspace_id, id, active_release_id)
    REFERENCES capability_releases (workspace_id, capability_id, id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

-- The legacy column did not reference the normalized Capability table.
UPDATE provider_assets
SET materialized_capability_id = NULL
WHERE materialized_capability_id IS NOT NULL;

ALTER TABLE provider_assets
    ADD CONSTRAINT provider_assets_materialized_capability_fk
    FOREIGN KEY (workspace_id, materialized_capability_id)
    REFERENCES capabilities (workspace_id, id)
    ON DELETE RESTRICT;

CREATE FUNCTION enforce_capability_active_release()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    selected_callable TEXT;
BEGIN
    IF NEW.active_release_id IS NULL THEN
        RETURN NEW;
    END IF;

    PERFORM pg_advisory_xact_lock(hashtextextended(NEW.workspace_id::TEXT, 0));
    SELECT callable_name INTO selected_callable
    FROM capability_releases
    WHERE workspace_id = NEW.workspace_id
      AND capability_id = NEW.id
      AND id = NEW.active_release_id
      AND retired_at IS NULL;
    IF selected_callable IS NULL THEN
        RAISE EXCEPTION 'active release must be a non-retired release of the same capability'
            USING ERRCODE = '23514';
    END IF;
    IF EXISTS (
        SELECT 1
        FROM capabilities AS c
        JOIN capability_releases AS r
          ON r.workspace_id = c.workspace_id
         AND r.capability_id = c.id
         AND r.id = c.active_release_id
        WHERE c.workspace_id = NEW.workspace_id
          AND c.id <> NEW.id
          AND c.deleted_at IS NULL
          AND lower(r.callable_name) = lower(selected_callable)
    ) THEN
        RAISE EXCEPTION 'active capability callable name already exists in workspace'
            USING ERRCODE = '23505';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER capabilities_active_release_integrity
BEFORE INSERT OR UPDATE OF active_release_id, workspace_id ON capabilities
FOR EACH ROW EXECUTE FUNCTION enforce_capability_active_release();

CREATE FUNCTION enforce_capability_release_immutability()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'capability releases are immutable'
            USING ERRCODE = '55000';
    END IF;
    IF ROW(
        NEW.id, NEW.workspace_id, NEW.capability_id, NEW.release_no,
        NEW.source_type, NEW.source_id, NEW.callable_name, NEW.callable_description,
        NEW.input_schema, NEW.output_schema, NEW.risk_level, NEW.side_effect_level,
        NEW.requires_confirmation, NEW.checksum, NEW.published_by, NEW.published_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.capability_id, OLD.release_no,
        OLD.source_type, OLD.source_id, OLD.callable_name, OLD.callable_description,
        OLD.input_schema, OLD.output_schema, OLD.risk_level, OLD.side_effect_level,
        OLD.requires_confirmation, OLD.checksum, OLD.published_by, OLD.published_at
    ) OR OLD.retired_at IS NOT NULL OR NEW.retired_at IS NULL THEN
        RAISE EXCEPTION 'capability releases are immutable except for one-way retirement'
            USING ERRCODE = '55000';
    END IF;
    IF EXISTS (SELECT 1 FROM capabilities WHERE active_release_id = OLD.id) THEN
        RAISE EXCEPTION 'active capability release cannot be retired'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER capability_releases_immutable
BEFORE UPDATE OR DELETE ON capability_releases
FOR EACH ROW EXECUTE FUNCTION enforce_capability_release_immutability();
