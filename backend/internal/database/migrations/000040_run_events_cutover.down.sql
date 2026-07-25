DROP TRIGGER IF EXISTS run_events_cutover_complete ON run_events;
DROP FUNCTION IF EXISTS reject_legacy_run_event_insert();

ALTER TABLE run_events DISABLE TRIGGER run_events_fact_guard;
INSERT INTO run_events(
    id,workspace_id,run_id,sequence_no,event_type,payload,terminal,created_at
)
SELECT
    pe.id,pe.workspace_id,pe.run_id,pe.sequence_no,
    pe.payload->'data'->>'legacyEventType',
    pe.payload->'data'->'legacyPayload',
    (pe.payload->'data'->>'legacyEventType') IN (
        'RUN_COMPLETED','RUN_FAILED','RUN_CANCELLED'
    ),
    pe.occurred_at
FROM protocol_events pe
WHERE pe.payload->'data' ? 'legacyEventType'
ON CONFLICT (id) DO NOTHING;
ALTER TABLE run_events ENABLE TRIGGER run_events_fact_guard;

DROP TRIGGER IF EXISTS protocol_events_immutable ON protocol_events;
DELETE FROM protocol_events
WHERE payload->'data' ? 'legacyEventType';
DELETE FROM protocol_event_streams pes
WHERE NOT EXISTS (
    SELECT 1 FROM protocol_events pe WHERE pe.stream_id=pes.id
)
AND EXISTS (
    SELECT 1 FROM run_events re
    WHERE re.workspace_id=pes.workspace_id AND re.run_id=pes.run_id
);
CREATE TRIGGER protocol_events_immutable
BEFORE UPDATE OR DELETE ON protocol_events
FOR EACH ROW EXECUTE FUNCTION reject_protocol_event_mutation();
