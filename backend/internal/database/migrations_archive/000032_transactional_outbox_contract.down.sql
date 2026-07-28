ALTER TABLE outbox_events
    DROP CONSTRAINT IF EXISTS outbox_events_payload_schema_version_check,
    DROP CONSTRAINT outbox_events_timestamps_check,
    ALTER COLUMN created_at SET DEFAULT CURRENT_TIMESTAMP;

UPDATE outbox_events
SET created_at = occurred_at
WHERE created_at < occurred_at;

ALTER TABLE outbox_events
    ADD CONSTRAINT outbox_events_timestamps_check CHECK (
        available_at >= occurred_at
        AND created_at >= occurred_at
        AND (published_at IS NULL OR published_at >= occurred_at)
    );
