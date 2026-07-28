CREATE FUNCTION validate_protocol_event_envelope()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NOT (
        NEW.payload ?& ARRAY[
            'specVersion', 'type', 'eventId', 'streamId', 'sequence',
            'occurredAt', 'workspaceId', 'agentId', 'conversationId',
            'runId', 'traceId', 'data'
        ]
        AND NEW.event_type <> 'stream.error'
        AND NEW.payload->>'type' <> 'stream.error'
        AND lower(NEW.payload->>'eventId') = NEW.id::TEXT
        AND NEW.payload->>'type' = NEW.event_type
        AND NEW.payload->>'specVersion' = NEW.spec_version
        AND jsonb_typeof(NEW.payload->'sequence') = 'number'
        AND NEW.payload->>'sequence' ~ '^[1-9][0-9]*$'
        AND NEW.payload->>'sequence' = NEW.sequence_no::TEXT
        AND lower(NEW.payload->>'workspaceId') = NEW.workspace_id::TEXT
        AND lower(NEW.payload->>'agentId') = NEW.agent_id::TEXT
        AND lower(NEW.payload->>'conversationId') = NEW.conversation_id::TEXT
        AND lower(NEW.payload->>'runId') = NEW.run_id::TEXT
        AND lower(NEW.payload->>'streamId') = 'run:' || NEW.run_id::TEXT
        AND length(btrim(NEW.payload->>'occurredAt')) > 0
        AND length(btrim(NEW.payload->>'traceId')) > 0
        AND jsonb_typeof(NEW.payload->'data') = 'object'
    ) THEN
        RAISE EXCEPTION 'protocol event envelope does not match persisted columns'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'protocol_events_envelope_consistency';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER protocol_events_validate_envelope
BEFORE INSERT ON protocol_events
FOR EACH ROW EXECUTE FUNCTION validate_protocol_event_envelope();

CREATE FUNCTION reject_protocol_event_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'protocol events are immutable and permanently retained'
        USING ERRCODE = '55000',
              CONSTRAINT = 'protocol_events_immutable';
END;
$$;

CREATE TRIGGER protocol_events_immutable
BEFORE UPDATE OR DELETE ON protocol_events
FOR EACH ROW EXECUTE FUNCTION reject_protocol_event_mutation();
