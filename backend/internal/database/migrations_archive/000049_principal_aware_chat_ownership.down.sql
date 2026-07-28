-- A rollback cannot losslessly represent external Chat facts in the old User
-- FK model. Fail before changing schema instead of deleting permanent data or
-- manufacturing a User.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM chat_sessions WHERE actor_type <> 'USER')
       OR EXISTS (SELECT 1 FROM chat_messages WHERE actor_type='SERVICE_PRINCIPAL') THEN
        RAISE EXCEPTION 'cannot rollback Principal-aware Chat with external facts'
            USING ERRCODE='55000';
    END IF;
END;
$$;

DROP INDEX chat_sessions_workspace_client_agent_updated_idx;
DROP INDEX chat_sessions_workspace_owner_updated_idx;
CREATE INDEX chat_sessions_workspace_creator_updated_idx
    ON chat_sessions(workspace_id,created_by,status,updated_at DESC,id);

DROP TRIGGER chat_messages_principal_ownership_guard ON chat_messages;
DROP FUNCTION validate_chat_message_principal_ownership();
DROP TRIGGER chat_sessions_ownership_immutable_guard ON chat_sessions;
DROP FUNCTION reject_chat_session_ownership_mutation();
DROP TRIGGER chat_sessions_principal_ownership_guard ON chat_sessions;
DROP FUNCTION validate_chat_session_principal_ownership();

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
        NEW.created_by,NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.id,OLD.workspace_id,OLD.session_id,OLD.role,OLD.content,
        OLD.content_object_id,OLD.content_sha256,OLD.content_length,
        OLD.created_by,OLD.created_at
    ) THEN
        RAISE EXCEPTION 'chat message original content and identity are immutable'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

ALTER TABLE chat_messages
    DROP CONSTRAINT chat_messages_ownership_policy_version_check,
    DROP CONSTRAINT chat_messages_ownership_mode_check,
    DROP CONSTRAINT chat_messages_subject_pair_check,
    DROP CONSTRAINT chat_messages_client_scope_fk,
    DROP CONSTRAINT chat_messages_subject_ref_fk,
    DROP CONSTRAINT chat_messages_actor_ref_fk,
    ADD CONSTRAINT chat_messages_user_actor_check
        CHECK(role <> 'USER' OR created_by IS NOT NULL),
    DROP COLUMN ownership_policy_version,
    DROP COLUMN ownership_mode,
    DROP COLUMN client_id,
    DROP COLUMN subject_id,
    DROP COLUMN subject_type,
    DROP COLUMN actor_id,
    DROP COLUMN actor_type;

ALTER TABLE chat_sessions
    DROP CONSTRAINT chat_sessions_ownership_policy_version_check,
    DROP CONSTRAINT chat_sessions_ownership_mode_check,
    DROP CONSTRAINT chat_sessions_subject_pair_check,
    DROP CONSTRAINT chat_sessions_client_scope_fk,
    DROP CONSTRAINT chat_sessions_subject_ref_fk,
    DROP CONSTRAINT chat_sessions_actor_ref_fk,
    ALTER COLUMN created_by SET NOT NULL,
    DROP COLUMN ownership_policy_version,
    DROP COLUMN ownership_mode,
    DROP COLUMN client_id,
    DROP COLUMN subject_id,
    DROP COLUMN subject_type,
    DROP COLUMN actor_id,
    DROP COLUMN actor_type;

CREATE OR REPLACE FUNCTION register_directory_principal_ref()
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

DROP TRIGGER principal_refs_immutable_guard ON principal_refs;
DELETE FROM principal_refs
WHERE principal_type='SYSTEM'
  AND principal_id='00000000-0000-0000-0000-000000000001'::UUID
  AND system_key='actweave:chat-runtime';
CREATE TRIGGER principal_refs_immutable_guard
BEFORE UPDATE OR DELETE ON principal_refs
FOR EACH ROW EXECUTE FUNCTION reject_principal_ref_mutation();
