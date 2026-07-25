ALTER TABLE prompt_runs
    ADD COLUMN input_sha256 CHAR(64) NOT NULL,
    ADD COLUMN input_length BIGINT NOT NULL,
    ADD COLUMN output_sha256 CHAR(64),
    ADD COLUMN output_length BIGINT,
    ADD CONSTRAINT prompt_runs_input_sha256_check
        CHECK (input_sha256 ~ '^[0-9a-f]{64}$'),
    ADD CONSTRAINT prompt_runs_input_length_check CHECK (input_length > 0),
    ADD CONSTRAINT prompt_runs_output_evidence_check CHECK (
        (output_object_id IS NULL AND output_sha256 IS NULL AND output_length IS NULL)
        OR (
            output_object_id IS NOT NULL
            AND output_sha256 ~ '^[0-9a-f]{64}$'
            AND output_length > 0
        )
    ),
    ADD CONSTRAINT prompt_runs_input_object_fk
        FOREIGN KEY (workspace_id, input_object_id)
        REFERENCES stored_objects (workspace_id, id) ON DELETE RESTRICT,
    ADD CONSTRAINT prompt_runs_output_object_fk
        FOREIGN KEY (workspace_id, output_object_id)
        REFERENCES stored_objects (workspace_id, id) ON DELETE RESTRICT;

CREATE FUNCTION enforce_prompt_run_permanent_content()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'prompt runs are permanently retained and cannot be deleted'
            USING ERRCODE = '55000';
    END IF;
    IF ROW(
        NEW.id, NEW.workspace_id, NEW.agent_id, NEW.operation_type,
        NEW.model_config_id, NEW.model_snapshot, NEW.input_object_id,
        NEW.input_sha256, NEW.input_length, NEW.trace_id, NEW.created_by,
        NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.agent_id, OLD.operation_type,
        OLD.model_config_id, OLD.model_snapshot, OLD.input_object_id,
        OLD.input_sha256, OLD.input_length, OLD.trace_id, OLD.created_by,
        OLD.created_at
    ) THEN
        RAISE EXCEPTION 'prompt run input evidence is immutable'
            USING ERRCODE = '55000';
    END IF;
    IF OLD.output_object_id IS NOT NULL AND ROW(
        NEW.output_object_id, NEW.output_sha256, NEW.output_length
    ) IS DISTINCT FROM ROW(
        OLD.output_object_id, OLD.output_sha256, OLD.output_length
    ) THEN
        RAISE EXCEPTION 'prompt run output evidence is immutable'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER prompt_runs_permanent_content
BEFORE UPDATE OR DELETE ON prompt_runs
FOR EACH ROW EXECUTE FUNCTION enforce_prompt_run_permanent_content();

ALTER TABLE chat_messages
    ADD COLUMN content_length BIGINT NOT NULL,
    DROP CONSTRAINT chat_messages_content_check,
    ADD CONSTRAINT chat_messages_content_carrier_check CHECK (
        (content IS NOT NULL AND length(content) > 0 AND content_object_id IS NULL)
        OR (content IS NULL AND content_object_id IS NOT NULL)
    ),
    ADD CONSTRAINT chat_messages_content_length_check CHECK (content_length > 0),
    ADD CONSTRAINT chat_messages_content_object_fk
        FOREIGN KEY (workspace_id, content_object_id)
        REFERENCES stored_objects (workspace_id, id) ON DELETE RESTRICT;

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
        NEW.content_object_id, NEW.content_sha256, NEW.content_length,
        NEW.created_by, NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.workspace_id, OLD.session_id, OLD.role, OLD.content,
        OLD.content_object_id, OLD.content_sha256, OLD.content_length,
        OLD.created_by, OLD.created_at
    ) THEN
        RAISE EXCEPTION 'chat message original content and identity are immutable'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

ALTER TABLE agent_run_steps
    ADD COLUMN raw_sha256 CHAR(64),
    ADD COLUMN raw_length BIGINT,
    ADD CONSTRAINT agent_run_steps_raw_evidence_check CHECK (
        (raw_object_id IS NULL AND raw_sha256 IS NULL AND raw_length IS NULL)
        OR (
            raw_object_id IS NOT NULL
            AND raw_sha256 ~ '^[0-9a-f]{64}$'
            AND raw_length > 0
        )
    ),
    ADD CONSTRAINT agent_run_steps_model_turn_evidence_check CHECK (
        step_type <> 'MODEL'
        OR status NOT IN ('SUCCEEDED', 'FAILED')
        OR raw_object_id IS NOT NULL
    ),
    ADD CONSTRAINT agent_run_steps_raw_object_fk
        FOREIGN KEY (workspace_id, raw_object_id)
        REFERENCES stored_objects (workspace_id, id) ON DELETE RESTRICT;

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
    IF OLD.raw_object_id IS NOT NULL AND ROW(
        NEW.raw_object_id, NEW.raw_sha256, NEW.raw_length
    ) IS DISTINCT FROM ROW(
        OLD.raw_object_id, OLD.raw_sha256, OLD.raw_length
    ) THEN
        RAISE EXCEPTION 'agent run step raw evidence is immutable'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;
