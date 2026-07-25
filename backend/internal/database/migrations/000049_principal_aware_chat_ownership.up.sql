-- Chat facts preserve the transport Actor separately from the represented
-- Subject. created_by remains a compatibility projection for internal Users;
-- external callers never manufacture a User row.
ALTER TABLE chat_sessions
    ADD COLUMN actor_type TEXT,
    ADD COLUMN actor_id UUID,
    ADD COLUMN subject_type TEXT,
    ADD COLUMN subject_id UUID,
    ADD COLUMN client_id UUID,
    ADD COLUMN ownership_mode TEXT,
    ADD COLUMN ownership_policy_version BIGINT;

ALTER TABLE chat_messages
    ADD COLUMN actor_type TEXT,
    ADD COLUMN actor_id UUID,
    ADD COLUMN subject_type TEXT,
    ADD COLUMN subject_id UUID,
    ADD COLUMN client_id UUID,
    ADD COLUMN ownership_mode TEXT,
    ADD COLUMN ownership_policy_version BIGINT;

-- A stable runtime Principal represents assistant/system-produced Chat
-- messages. The same typed UUID is safe across Workspaces because Principal
-- Refs are Workspace scoped.
INSERT INTO principal_refs(
    workspace_id,principal_type,principal_id,system_key,origin,created_at
)
SELECT id,'SYSTEM','00000000-0000-0000-0000-000000000001'::UUID,
       'actweave:chat-runtime','SYSTEM',created_at
FROM workspaces
ON CONFLICT DO NOTHING;

-- Future Workspaces receive the same explicit runtime Ref together with the
-- existing owner User Ref.
CREATE OR REPLACE FUNCTION register_directory_principal_ref()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_TABLE_NAME = 'workspaces' THEN
        INSERT INTO principal_refs(workspace_id,principal_type,principal_id,origin,created_at)
        VALUES(NEW.id,'USER',NEW.owner_user_id,'DIRECTORY',NEW.created_at)
        ON CONFLICT DO NOTHING;
        INSERT INTO principal_refs(
            workspace_id,principal_type,principal_id,system_key,origin,created_at
        ) VALUES(
            NEW.id,'SYSTEM','00000000-0000-0000-0000-000000000001'::UUID,
            'actweave:chat-runtime','SYSTEM',NEW.created_at
        ) ON CONFLICT DO NOTHING;
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

UPDATE chat_sessions
SET actor_type='USER',actor_id=created_by,
    subject_type='USER',subject_id=created_by,
    ownership_mode='SUBJECT_OWNED',ownership_policy_version=1;

UPDATE chat_messages cm
SET actor_type=CASE WHEN cm.created_by IS NULL THEN 'SYSTEM' ELSE 'USER' END,
    actor_id=coalesce(
        cm.created_by,'00000000-0000-0000-0000-000000000001'::UUID
    ),
    subject_type=cs.subject_type,subject_id=cs.subject_id,
    client_id=cs.client_id,ownership_mode=cs.ownership_mode,
    ownership_policy_version=cs.ownership_policy_version
FROM chat_sessions cs
WHERE cs.workspace_id=cm.workspace_id AND cs.id=cm.session_id;

ALTER TABLE chat_sessions ALTER COLUMN created_by DROP NOT NULL;

ALTER TABLE chat_sessions
    ALTER COLUMN actor_type SET NOT NULL,
    ALTER COLUMN actor_id SET NOT NULL,
    ALTER COLUMN ownership_mode SET NOT NULL,
    ALTER COLUMN ownership_policy_version SET NOT NULL,
    ADD CONSTRAINT chat_sessions_actor_ref_fk
        FOREIGN KEY (workspace_id,actor_type,actor_id)
        REFERENCES principal_refs(workspace_id,principal_type,principal_id)
        ON DELETE RESTRICT,
    ADD CONSTRAINT chat_sessions_subject_ref_fk
        FOREIGN KEY (workspace_id,subject_type,subject_id)
        REFERENCES principal_refs(workspace_id,principal_type,principal_id)
        ON DELETE RESTRICT,
    ADD CONSTRAINT chat_sessions_client_scope_fk
        FOREIGN KEY (workspace_id,client_id)
        REFERENCES agent_access_clients(workspace_id,id) ON DELETE RESTRICT,
    ADD CONSTRAINT chat_sessions_subject_pair_check CHECK (
        (subject_type IS NULL) = (subject_id IS NULL)
    ),
    ADD CONSTRAINT chat_sessions_ownership_mode_check CHECK (
        ownership_mode IN ('SUBJECT_OWNED','POLICY_SHARED')
    ),
    ADD CONSTRAINT chat_sessions_ownership_policy_version_check
        CHECK (ownership_policy_version > 0);

ALTER TABLE chat_messages
    ALTER COLUMN actor_type SET NOT NULL,
    ALTER COLUMN actor_id SET NOT NULL,
    ALTER COLUMN ownership_mode SET NOT NULL,
    ALTER COLUMN ownership_policy_version SET NOT NULL,
    ADD CONSTRAINT chat_messages_actor_ref_fk
        FOREIGN KEY (workspace_id,actor_type,actor_id)
        REFERENCES principal_refs(workspace_id,principal_type,principal_id)
        ON DELETE RESTRICT,
    ADD CONSTRAINT chat_messages_subject_ref_fk
        FOREIGN KEY (workspace_id,subject_type,subject_id)
        REFERENCES principal_refs(workspace_id,principal_type,principal_id)
        ON DELETE RESTRICT,
    ADD CONSTRAINT chat_messages_client_scope_fk
        FOREIGN KEY (workspace_id,client_id)
        REFERENCES agent_access_clients(workspace_id,id) ON DELETE RESTRICT,
    ADD CONSTRAINT chat_messages_subject_pair_check CHECK (
        (subject_type IS NULL) = (subject_id IS NULL)
    ),
    ADD CONSTRAINT chat_messages_ownership_mode_check CHECK (
        ownership_mode IN ('SUBJECT_OWNED','POLICY_SHARED')
    ),
    ADD CONSTRAINT chat_messages_ownership_policy_version_check
        CHECK (ownership_policy_version > 0);

ALTER TABLE chat_messages DROP CONSTRAINT chat_messages_user_actor_check;

CREATE FUNCTION validate_chat_session_principal_ownership()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.actor_type IS NULL AND NEW.actor_id IS NULL AND NEW.created_by IS NOT NULL THEN
        NEW.actor_type := 'USER';
        NEW.actor_id := NEW.created_by;
        NEW.subject_type := 'USER';
        NEW.subject_id := NEW.created_by;
    END IF;
    NEW.ownership_mode := coalesce(NEW.ownership_mode,'SUBJECT_OWNED');
    NEW.ownership_policy_version := coalesce(NEW.ownership_policy_version,1);

    IF NEW.actor_type = 'USER' THEN
        IF NEW.created_by IS DISTINCT FROM NEW.actor_id
           OR NEW.subject_type IS DISTINCT FROM 'USER'
           OR NEW.subject_id IS DISTINCT FROM NEW.actor_id
           OR NEW.client_id IS NOT NULL
           OR NEW.ownership_mode <> 'SUBJECT_OWNED' THEN
            RAISE EXCEPTION 'Internal User Chat ownership is inconsistent'
                USING ERRCODE='23514',CONSTRAINT='chat_sessions_user_ownership_check';
        END IF;
    ELSIF NEW.actor_type = 'SERVICE_PRINCIPAL' THEN
        IF NEW.created_by IS NOT NULL OR NEW.client_id IS NULL OR NOT EXISTS (
            SELECT 1 FROM agent_access_clients c
            WHERE c.workspace_id=NEW.workspace_id AND c.id=NEW.client_id
              AND c.service_principal_id=NEW.actor_id
        ) THEN
            RAISE EXCEPTION 'Service Principal Chat Client binding is invalid'
                USING ERRCODE='23514',CONSTRAINT='chat_sessions_client_actor_check';
        END IF;
        IF NEW.subject_id IS NOT NULL AND (
            NEW.subject_type <> 'EXTERNAL_SUBJECT' OR NOT EXISTS (
                SELECT 1 FROM external_subjects s
                WHERE s.workspace_id=NEW.workspace_id AND s.id=NEW.subject_id
                  AND s.client_id=NEW.client_id
            )
        ) THEN
            RAISE EXCEPTION 'External Subject Chat binding is invalid'
                USING ERRCODE='23514',CONSTRAINT='chat_sessions_external_subject_check';
        END IF;
        IF NEW.subject_id IS NOT NULL AND NEW.ownership_mode <> 'SUBJECT_OWNED' THEN
            RAISE EXCEPTION 'Subject-owned Chat cannot be policy shared'
                USING ERRCODE='23514',CONSTRAINT='chat_sessions_subject_private_check';
        END IF;
    ELSE
        RAISE EXCEPTION 'Chat Session Actor must be User or Service Principal'
            USING ERRCODE='23514',CONSTRAINT='chat_sessions_actor_type_check';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER chat_sessions_principal_ownership_guard
BEFORE INSERT ON chat_sessions
FOR EACH ROW EXECUTE FUNCTION validate_chat_session_principal_ownership();

CREATE FUNCTION reject_chat_session_ownership_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF ROW(
        NEW.workspace_id,NEW.agent_id,NEW.created_by,NEW.actor_type,NEW.actor_id,
        NEW.subject_type,NEW.subject_id,NEW.client_id,NEW.ownership_mode,
        NEW.ownership_policy_version,NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.workspace_id,OLD.agent_id,OLD.created_by,OLD.actor_type,OLD.actor_id,
        OLD.subject_type,OLD.subject_id,OLD.client_id,OLD.ownership_mode,
        OLD.ownership_policy_version,OLD.created_at
    ) THEN
        RAISE EXCEPTION 'Chat Session ownership and identity are immutable'
            USING ERRCODE='55000',CONSTRAINT='chat_sessions_ownership_immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER chat_sessions_ownership_immutable_guard
BEFORE UPDATE ON chat_sessions
FOR EACH ROW EXECUTE FUNCTION reject_chat_session_ownership_mutation();

CREATE FUNCTION validate_chat_message_principal_ownership()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    session_actor_type TEXT;
    session_actor_id UUID;
    session_subject_type TEXT;
    session_subject_id UUID;
    session_client_id UUID;
    session_mode TEXT;
    session_policy_version BIGINT;
BEGIN
    SELECT actor_type,actor_id,subject_type,subject_id,client_id,
           ownership_mode,ownership_policy_version
    INTO STRICT session_actor_type,session_actor_id,session_subject_type,
        session_subject_id,session_client_id,session_mode,session_policy_version
    FROM chat_sessions
    WHERE workspace_id=NEW.workspace_id AND id=NEW.session_id;

    IF NEW.actor_type IS NULL AND NEW.actor_id IS NULL THEN
        IF NEW.created_by IS NOT NULL THEN
            NEW.actor_type := 'USER';
            NEW.actor_id := NEW.created_by;
        ELSE
            NEW.actor_type := 'SYSTEM';
            NEW.actor_id := '00000000-0000-0000-0000-000000000001'::UUID;
        END IF;
    END IF;
    IF NEW.subject_type IS NULL AND NEW.subject_id IS NULL THEN
        NEW.subject_type := session_subject_type;
        NEW.subject_id := session_subject_id;
    END IF;
    NEW.client_id := coalesce(NEW.client_id,session_client_id);
    NEW.ownership_mode := coalesce(NEW.ownership_mode,session_mode);
    NEW.ownership_policy_version := coalesce(
        NEW.ownership_policy_version,session_policy_version
    );

    IF ROW(NEW.client_id,NEW.ownership_mode,NEW.ownership_policy_version)
       IS DISTINCT FROM ROW(session_client_id,session_mode,session_policy_version) THEN
        RAISE EXCEPTION 'Chat Message ownership must match its Session'
            USING ERRCODE='23514',CONSTRAINT='chat_messages_session_ownership_check';
    END IF;
    IF session_mode='SUBJECT_OWNED' AND
       ROW(NEW.subject_type,NEW.subject_id) IS DISTINCT FROM
       ROW(session_subject_type,session_subject_id) THEN
        RAISE EXCEPTION 'Subject-owned Message must retain its Session Subject'
            USING ERRCODE='23514',CONSTRAINT='chat_messages_subject_owner_check';
    END IF;
    IF session_mode='POLICY_SHARED' AND NEW.subject_id IS NOT NULL AND (
        NEW.subject_type <> 'EXTERNAL_SUBJECT' OR NOT EXISTS (
            SELECT 1 FROM external_subjects s
            WHERE s.workspace_id=NEW.workspace_id AND s.id=NEW.subject_id
              AND s.client_id=session_client_id
        )
    ) THEN
        RAISE EXCEPTION 'Policy-shared Message Subject is outside its Client'
            USING ERRCODE='23514',CONSTRAINT='chat_messages_shared_subject_check';
    END IF;
    IF NEW.actor_type = 'USER' THEN
        IF NEW.created_by IS DISTINCT FROM NEW.actor_id THEN
            RAISE EXCEPTION 'User Message actor projection is inconsistent'
                USING ERRCODE='23514',CONSTRAINT='chat_messages_user_actor_check';
        END IF;
    ELSIF NEW.actor_type IN ('SERVICE_PRINCIPAL','SYSTEM') THEN
        IF NEW.created_by IS NOT NULL THEN
            RAISE EXCEPTION 'Machine Message must not reference a User creator'
                USING ERRCODE='23514',CONSTRAINT='chat_messages_machine_actor_check';
        END IF;
    ELSE
        RAISE EXCEPTION 'External Subject cannot be a Message transport Actor'
            USING ERRCODE='23514',CONSTRAINT='chat_messages_actor_type_check';
    END IF;
    IF NEW.role = 'USER' AND (
        NEW.actor_type IS DISTINCT FROM session_actor_type
        OR NEW.actor_id IS DISTINCT FROM session_actor_id
    ) THEN
        RAISE EXCEPTION 'User Message Actor must match its Session Actor'
            USING ERRCODE='23514',CONSTRAINT='chat_messages_user_session_actor_check';
    END IF;
    RETURN NEW;
EXCEPTION
    WHEN NO_DATA_FOUND THEN
        RAISE EXCEPTION 'Chat Message Session does not exist'
            USING ERRCODE='23503',CONSTRAINT='chat_messages_workspace_session_fk';
END;
$$;

CREATE TRIGGER chat_messages_principal_ownership_guard
BEFORE INSERT ON chat_messages
FOR EACH ROW EXECUTE FUNCTION validate_chat_message_principal_ownership();

-- Extend the permanent-retention invariant to every newly persisted identity
-- and ownership field. Mutable delivery/execution status remains unchanged.
CREATE OR REPLACE FUNCTION enforce_chat_message_permanent_retention()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'chat messages are permanently retained and cannot be deleted'
            USING ERRCODE = '55000';
    END IF;
    IF ROW(
        NEW.id,NEW.workspace_id,NEW.session_id,NEW.role,NEW.content,
        NEW.content_object_id,NEW.content_sha256,NEW.content_length,
        NEW.created_by,NEW.created_at,
        NEW.actor_type,NEW.actor_id,NEW.subject_type,NEW.subject_id,NEW.client_id,
        NEW.ownership_mode,NEW.ownership_policy_version
    ) IS DISTINCT FROM ROW(
        OLD.id,OLD.workspace_id,OLD.session_id,OLD.role,OLD.content,
        OLD.content_object_id,OLD.content_sha256,OLD.content_length,
        OLD.created_by,OLD.created_at,
        OLD.actor_type,OLD.actor_id,OLD.subject_type,OLD.subject_id,OLD.client_id,
        OLD.ownership_mode,OLD.ownership_policy_version
    ) THEN
        RAISE EXCEPTION 'chat message original content and identity are immutable'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

DROP INDEX chat_sessions_workspace_creator_updated_idx;
CREATE INDEX chat_sessions_workspace_owner_updated_idx ON chat_sessions(
    workspace_id,client_id,subject_type,subject_id,actor_type,actor_id,
    ownership_mode,status,updated_at DESC,id
);
CREATE INDEX chat_sessions_workspace_client_agent_updated_idx ON chat_sessions(
    workspace_id,client_id,agent_id,updated_at DESC,id
) WHERE client_id IS NOT NULL;
