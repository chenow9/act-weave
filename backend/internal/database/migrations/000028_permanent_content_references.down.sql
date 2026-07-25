ALTER TABLE agent_run_steps
    DROP CONSTRAINT IF EXISTS agent_run_steps_raw_object_fk,
    DROP CONSTRAINT IF EXISTS agent_run_steps_model_turn_evidence_check,
    DROP CONSTRAINT IF EXISTS agent_run_steps_raw_evidence_check,
    DROP COLUMN IF EXISTS raw_length,
    DROP COLUMN IF EXISTS raw_sha256;

CREATE OR REPLACE FUNCTION enforce_agent_run_step_permanent_evidence()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'agent run steps are permanently retained and cannot be deleted'
            USING ERRCODE = '55000';
    END IF;
    IF ROW(
        NEW.id, NEW.workspace_id, NEW.run_id, NEW.sequence_no, NEW.step_type,
        NEW.capability_release_id, NEW.input_summary, NEW.started_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.run_id, OLD.sequence_no, OLD.step_type,
        OLD.capability_release_id, OLD.input_summary, OLD.started_at
    ) THEN
        RAISE EXCEPTION 'agent run step identity and start evidence are immutable'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

ALTER TABLE chat_messages
    DROP CONSTRAINT IF EXISTS chat_messages_content_object_fk,
    DROP CONSTRAINT IF EXISTS chat_messages_content_length_check,
    DROP CONSTRAINT IF EXISTS chat_messages_content_carrier_check,
    DROP COLUMN IF EXISTS content_length,
    ADD CONSTRAINT chat_messages_content_check CHECK (
        (content IS NOT NULL AND length(content) > 0) OR content_object_id IS NOT NULL
    );

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
        NEW.id, NEW.workspace_id, NEW.session_id, NEW.role, NEW.content,
        NEW.content_object_id, NEW.content_sha256, NEW.created_by, NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.session_id, OLD.role, OLD.content,
        OLD.content_object_id, OLD.content_sha256, OLD.created_by, OLD.created_at
    ) THEN
        RAISE EXCEPTION 'chat message original content and identity are immutable'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS prompt_runs_permanent_content ON prompt_runs;
DROP FUNCTION IF EXISTS enforce_prompt_run_permanent_content();

ALTER TABLE prompt_runs
    DROP CONSTRAINT IF EXISTS prompt_runs_output_object_fk,
    DROP CONSTRAINT IF EXISTS prompt_runs_input_object_fk,
    DROP CONSTRAINT IF EXISTS prompt_runs_output_evidence_check,
    DROP CONSTRAINT IF EXISTS prompt_runs_input_length_check,
    DROP CONSTRAINT IF EXISTS prompt_runs_input_sha256_check,
    DROP COLUMN IF EXISTS output_length,
    DROP COLUMN IF EXISTS output_sha256,
    DROP COLUMN IF EXISTS input_length,
    DROP COLUMN IF EXISTS input_sha256;
