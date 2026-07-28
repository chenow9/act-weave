ALTER TABLE outbox_events
    DROP CONSTRAINT outbox_events_timestamps_check,
    ALTER COLUMN created_at SET DEFAULT clock_timestamp(),
    ADD CONSTRAINT outbox_events_timestamps_check CHECK (
        available_at >= occurred_at
        AND (published_at IS NULL OR published_at >= occurred_at)
    ),
    ADD CONSTRAINT outbox_events_payload_schema_version_check CHECK (
        payload ? 'schemaVersion'
        AND jsonb_typeof(payload->'schemaVersion') = 'string'
        AND payload->>'schemaVersion' = schema_version
    );
