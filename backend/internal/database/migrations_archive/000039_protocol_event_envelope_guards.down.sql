DROP TRIGGER IF EXISTS protocol_events_immutable ON protocol_events;
DROP FUNCTION IF EXISTS reject_protocol_event_mutation();

DROP TRIGGER IF EXISTS protocol_events_validate_envelope ON protocol_events;
DROP FUNCTION IF EXISTS validate_protocol_event_envelope();
