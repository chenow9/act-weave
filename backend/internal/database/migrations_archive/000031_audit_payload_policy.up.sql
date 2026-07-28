ALTER TABLE stored_objects
    ADD CONSTRAINT stored_objects_audit_event_payload_policy_check CHECK (
        kind <> 'AUDIT_EVENT_PAYLOAD'
        OR (
            classification IN ('SENSITIVE', 'RESTRICTED')
            AND retention_mode = 'PERMANENT'
            AND retention_until IS NULL
        )
    );

CREATE FUNCTION enforce_audit_event_payload_reference()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.payload_object_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM stored_objects
        WHERE workspace_id = NEW.workspace_id
          AND id = NEW.payload_object_id
          AND kind = 'AUDIT_EVENT_PAYLOAD'
          AND classification IN ('SENSITIVE', 'RESTRICTED')
          AND retention_mode = 'PERMANENT'
          AND retention_until IS NULL
    ) THEN
        RAISE EXCEPTION 'audit event payload requires a permanent sensitive AUDIT_EVENT_PAYLOAD object'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER audit_events_payload_guard
BEFORE INSERT OR UPDATE OF workspace_id, payload_object_id ON audit_events
FOR EACH ROW EXECUTE FUNCTION enforce_audit_event_payload_reference();
