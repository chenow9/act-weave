CREATE TABLE principal_refs (
    workspace_id UUID NOT NULL,
    principal_type TEXT NOT NULL,
    principal_id UUID NOT NULL,
    system_key TEXT,
    origin TEXT NOT NULL DEFAULT 'DIRECTORY',
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (workspace_id, principal_type, principal_id),
    CONSTRAINT principal_refs_workspace_fk
        FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT,
    CONSTRAINT principal_refs_type_check CHECK (
        principal_type IN ('USER', 'SERVICE_PRINCIPAL', 'EXTERNAL_SUBJECT', 'SYSTEM')
    ),
    CONSTRAINT principal_refs_origin_check CHECK (
        origin IN ('DIRECTORY', 'SYSTEM', 'LEGACY_EXECUTION')
    ),
    CONSTRAINT principal_refs_system_key_check CHECK (
        (principal_type = 'SYSTEM'
            AND system_key IS NOT NULL
            AND length(btrim(system_key)) BETWEEN 1 AND 120
            AND system_key = lower(system_key)
            AND system_key ~ '^[a-z][a-z0-9._:-]{0,119}$'
            AND origin IN ('SYSTEM', 'LEGACY_EXECUTION'))
        OR
        (principal_type <> 'SYSTEM' AND system_key IS NULL AND origin <> 'SYSTEM')
    )
);

CREATE UNIQUE INDEX principal_refs_workspace_system_key
    ON principal_refs (workspace_id, system_key)
    WHERE principal_type = 'SYSTEM';
CREATE INDEX principal_refs_identity_idx
    ON principal_refs (principal_type, principal_id, workspace_id);

-- Directory identities receive one stable reference per Workspace. A User can
-- legitimately have different references in multiple Workspaces while the
-- underlying User ID and all existing User-owned records remain unchanged.
INSERT INTO principal_refs(workspace_id,principal_type,principal_id,origin,created_at)
SELECT workspace_id,'USER',user_id,'DIRECTORY',joined_at
FROM workspace_members
ON CONFLICT DO NOTHING;

INSERT INTO principal_refs(workspace_id,principal_type,principal_id,origin,created_at)
SELECT id,'USER',owner_user_id,'DIRECTORY',created_at
FROM workspaces
ON CONFLICT DO NOTHING;

INSERT INTO principal_refs(workspace_id,principal_type,principal_id,origin,created_at)
SELECT workspace_id,'SERVICE_PRINCIPAL',id,'DIRECTORY',created_at
FROM service_principals
ON CONFLICT DO NOTHING;

INSERT INTO principal_refs(workspace_id,principal_type,principal_id,origin,created_at)
SELECT workspace_id,'EXTERNAL_SUBJECT',id,'DIRECTORY',created_at
FROM external_subjects
ON CONFLICT DO NOTHING;

-- Old execution rows had a typed UUID but no referential target. Preserve
-- their exact historical meaning without manufacturing a User. Resolved
-- status remains false when the referenced directory identity never existed.
INSERT INTO principal_refs(
    workspace_id,principal_type,principal_id,system_key,origin,created_at
)
SELECT workspace_id,principal_type,principal_id,
       CASE WHEN principal_type='SYSTEM' THEN 'legacy:' || principal_id::TEXT ELSE NULL END,
       'LEGACY_EXECUTION',min(occurred_at)
FROM (
    SELECT workspace_id,triggered_by_type AS principal_type,
           triggered_by_id AS principal_id,started_at AS occurred_at
    FROM agent_runs
    UNION ALL
    SELECT workspace_id,triggered_by_type,triggered_by_id,started_at
    FROM workflow_executions
    UNION ALL
    SELECT workspace_id,actor_type,actor_id,started_at
    FROM tool_invocations
) legacy
GROUP BY workspace_id,principal_type,principal_id
ON CONFLICT DO NOTHING;

CREATE FUNCTION validate_principal_ref_target()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.origin = 'LEGACY_EXECUTION' THEN
        RAISE EXCEPTION 'Legacy Principal Ref origin is reserved for migration'
            USING ERRCODE = '23514', CONSTRAINT = 'principal_refs_legacy_origin_reserved';
    END IF;
    CASE NEW.principal_type
        WHEN 'USER' THEN
            IF NEW.origin <> 'DIRECTORY' OR NOT EXISTS (
                SELECT 1 FROM workspaces
                WHERE id=NEW.workspace_id AND owner_user_id=NEW.principal_id
                UNION ALL
                SELECT 1 FROM workspace_members
                WHERE workspace_id=NEW.workspace_id AND user_id=NEW.principal_id
            ) THEN
                RAISE EXCEPTION 'User Principal does not belong to Workspace'
                    USING ERRCODE = '23503', CONSTRAINT = 'principal_refs_user_target_fk';
            END IF;
        WHEN 'SERVICE_PRINCIPAL' THEN
            IF NEW.origin <> 'DIRECTORY' OR NOT EXISTS (
                SELECT 1 FROM service_principals
                WHERE workspace_id=NEW.workspace_id AND id=NEW.principal_id
            ) THEN
                RAISE EXCEPTION 'Service Principal does not belong to Workspace'
                    USING ERRCODE = '23503', CONSTRAINT = 'principal_refs_service_target_fk';
            END IF;
        WHEN 'EXTERNAL_SUBJECT' THEN
            IF NEW.origin <> 'DIRECTORY' OR NOT EXISTS (
                SELECT 1 FROM external_subjects
                WHERE workspace_id=NEW.workspace_id AND id=NEW.principal_id
            ) THEN
                RAISE EXCEPTION 'External Subject does not belong to Workspace'
                    USING ERRCODE = '23503', CONSTRAINT = 'principal_refs_subject_target_fk';
            END IF;
        WHEN 'SYSTEM' THEN
            IF NEW.origin <> 'SYSTEM' THEN
                RAISE EXCEPTION 'System Principal requires explicit SYSTEM origin'
                    USING ERRCODE = '23514', CONSTRAINT = 'principal_refs_system_origin';
            END IF;
        ELSE
            RAISE EXCEPTION 'Unknown Principal type'
                USING ERRCODE = '23514', CONSTRAINT = 'principal_refs_type_check';
    END CASE;
    RETURN NEW;
END;
$$;

CREATE TRIGGER principal_refs_target_guard
BEFORE INSERT ON principal_refs
FOR EACH ROW EXECUTE FUNCTION validate_principal_ref_target();

CREATE FUNCTION reject_principal_ref_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'Principal Refs are immutable and permanently retained'
        USING ERRCODE = '55000', CONSTRAINT = 'principal_refs_immutable';
END;
$$;

CREATE TRIGGER principal_refs_immutable_guard
BEFORE UPDATE OR DELETE ON principal_refs
FOR EACH ROW EXECUTE FUNCTION reject_principal_ref_mutation();

CREATE FUNCTION register_directory_principal_ref()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_TABLE_NAME = 'workspaces' THEN
        INSERT INTO principal_refs(workspace_id,principal_type,principal_id,origin,created_at)
        VALUES(NEW.id,'USER',NEW.owner_user_id,'DIRECTORY',NEW.created_at)
        ON CONFLICT DO NOTHING;
    ELSIF TG_TABLE_NAME = 'workspace_members' THEN
        INSERT INTO principal_refs(workspace_id,principal_type,principal_id,origin,created_at)
        VALUES(NEW.workspace_id,'USER',NEW.user_id,'DIRECTORY',NEW.joined_at)
        ON CONFLICT DO NOTHING;
    ELSIF TG_TABLE_NAME = 'service_principals' THEN
        INSERT INTO principal_refs(workspace_id,principal_type,principal_id,origin,created_at)
        VALUES(NEW.workspace_id,'SERVICE_PRINCIPAL',NEW.id,'DIRECTORY',NEW.created_at)
        ON CONFLICT DO NOTHING;
    ELSIF TG_TABLE_NAME = 'external_subjects' THEN
        INSERT INTO principal_refs(workspace_id,principal_type,principal_id,origin,created_at)
        VALUES(NEW.workspace_id,'EXTERNAL_SUBJECT',NEW.id,'DIRECTORY',NEW.created_at)
        ON CONFLICT DO NOTHING;
    ELSE
        RAISE EXCEPTION 'Unsupported Principal directory source'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER workspaces_owner_principal_ref
AFTER INSERT ON workspaces
FOR EACH ROW EXECUTE FUNCTION register_directory_principal_ref();
CREATE TRIGGER workspace_members_principal_ref
AFTER INSERT ON workspace_members
FOR EACH ROW EXECUTE FUNCTION register_directory_principal_ref();
CREATE TRIGGER service_principals_principal_ref
AFTER INSERT ON service_principals
FOR EACH ROW EXECUTE FUNCTION register_directory_principal_ref();
CREATE TRIGGER external_subjects_principal_ref
AFTER INSERT ON external_subjects
FOR EACH ROW EXECUTE FUNCTION register_directory_principal_ref();

ALTER TABLE agent_runs
    ADD CONSTRAINT agent_runs_principal_ref_fk
    FOREIGN KEY (workspace_id,triggered_by_type,triggered_by_id)
    REFERENCES principal_refs (workspace_id,principal_type,principal_id)
    ON DELETE RESTRICT;

ALTER TABLE workflow_executions
    ADD CONSTRAINT workflow_executions_principal_ref_fk
    FOREIGN KEY (workspace_id,triggered_by_type,triggered_by_id)
    REFERENCES principal_refs (workspace_id,principal_type,principal_id)
    ON DELETE RESTRICT;

ALTER TABLE tool_invocations
    ADD CONSTRAINT tool_invocations_principal_ref_fk
    FOREIGN KEY (workspace_id,actor_type,actor_id)
    REFERENCES principal_refs (workspace_id,principal_type,principal_id)
    ON DELETE RESTRICT;
