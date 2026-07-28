WITH ranked AS (
    SELECT id,
           row_number() OVER (
               PARTITION BY workspace_id, provider_id
               ORDER BY started_at DESC, id DESC
           ) AS position
    FROM provider_sync_runs
    WHERE status = 'RUNNING'
)
UPDATE provider_sync_runs AS runs
SET status = 'FAILED',
    error_summary = '{"code":"SUPERSEDED_CONCURRENT_SYNC"}'::JSONB,
    finished_at = GREATEST(clock_timestamp(), runs.started_at)
FROM ranked
WHERE runs.id = ranked.id AND ranked.position > 1;

CREATE UNIQUE INDEX provider_sync_runs_provider_running_key
    ON provider_sync_runs (workspace_id, provider_id)
    WHERE status = 'RUNNING';
