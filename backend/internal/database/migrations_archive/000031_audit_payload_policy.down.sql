DROP TRIGGER IF EXISTS audit_events_payload_guard ON audit_events;
DROP FUNCTION IF EXISTS enforce_audit_event_payload_reference();

ALTER TABLE stored_objects
    DROP CONSTRAINT IF EXISTS stored_objects_audit_event_payload_policy_check;
