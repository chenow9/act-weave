CREATE TABLE run_events (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    run_id UUID NOT NULL,
    sequence_no BIGINT NOT NULL,
    event_type TEXT NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::JSONB,
    terminal BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT run_events_workspace_run_id_key UNIQUE (workspace_id, run_id, id),
    CONSTRAINT run_events_run_sequence_key UNIQUE (run_id, sequence_no),
    CONSTRAINT run_events_workspace_run_fk
        FOREIGN KEY (workspace_id, run_id)
        REFERENCES agent_runs (workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT run_events_sequence_check CHECK (sequence_no > 0),
    CONSTRAINT run_events_type_check CHECK (
        event_type IN (
            'RUN_STARTED', 'STEP_STARTED', 'STEP_COMPLETED',
            'RUN_WAITING_CONFIRMATION', 'RUN_RESUMED',
            'RUN_COMPLETED', 'RUN_FAILED', 'RUN_CANCELLED'
        )
    ),
    CONSTRAINT run_events_payload_object_check CHECK (jsonb_typeof(payload) = 'object'),
    CONSTRAINT run_events_terminal_type_check CHECK (
        terminal = (event_type IN ('RUN_COMPLETED', 'RUN_FAILED', 'RUN_CANCELLED'))
    )
);

CREATE INDEX run_events_workspace_run_sequence_idx
    ON run_events (workspace_id, run_id, sequence_no, id);
CREATE UNIQUE INDEX run_events_one_terminal_per_run_key
    ON run_events (run_id) WHERE terminal;

CREATE FUNCTION enforce_run_event_fact()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    current_status TEXT;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'run events are immutable and permanently retained'
            USING ERRCODE = '55000';
    END IF;
    SELECT status INTO current_status
    FROM agent_runs
    WHERE workspace_id = NEW.workspace_id AND id = NEW.run_id;
    IF current_status IN ('SUCCEEDED', 'FAILED', 'CANCELLED') THEN
        IF NOT NEW.terminal OR
           (current_status = 'SUCCEEDED' AND NEW.event_type <> 'RUN_COMPLETED') OR
           (current_status = 'FAILED' AND NEW.event_type <> 'RUN_FAILED') OR
           (current_status = 'CANCELLED' AND NEW.event_type <> 'RUN_CANCELLED') THEN
            RAISE EXCEPTION 'terminal run only accepts its matching terminal event'
                USING ERRCODE = '23514';
        END IF;
    ELSIF NEW.terminal THEN
        RAISE EXCEPTION 'terminal event requires a terminal run state'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER run_events_fact_guard
BEFORE INSERT OR UPDATE OR DELETE ON run_events
FOR EACH ROW EXECUTE FUNCTION enforce_run_event_fact();
