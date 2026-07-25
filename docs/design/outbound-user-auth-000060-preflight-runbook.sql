-- Read-only production preflight for 000060_outbound_identity_hard_cutover.
-- Run inside the maintenance window AFTER legacy HTTP execution is drained and
-- BEFORE applying migration 000060. Do not mutate data with this script.
--
-- Safe output only: workspace-level aggregate counts. Never SELECT secret names,
-- IDs into operator tickets that will be logged broadly if avoidable; if IDs
-- must be inspected for unblock work, do so in an isolated session and do not
-- paste ciphertext/fingerprints into tickets.

-- 1) Target connection inventory (HTTP_OPENAPI, including soft-deleted)
SELECT
    c.workspace_id,
    COUNT(*) FILTER (WHERE c.deleted_at IS NULL) AS active_target_connections,
    COUNT(*) FILTER (WHERE c.deleted_at IS NOT NULL) AS soft_deleted_target_connections,
    COUNT(*) FILTER (
        WHERE c.credential_secret_id IS NOT NULL
    ) AS target_connections_with_legacy_secret
FROM service_connections AS c
INNER JOIN capability_providers AS p
    ON p.workspace_id = c.workspace_id AND p.id = c.provider_id
WHERE p.provider_kind = 'HTTP_OPENAPI'
GROUP BY c.workspace_id
ORDER BY c.workspace_id;

-- 2) Candidate secret aggregate counts (distinct secrets referenced by targets)
WITH candidates AS (
    SELECT DISTINCT c.workspace_id, c.credential_secret_id AS secret_id
    FROM service_connections AS c
    INNER JOIN capability_providers AS p
        ON p.workspace_id = c.workspace_id AND p.id = c.provider_id
    WHERE p.provider_kind = 'HTTP_OPENAPI'
      AND c.credential_secret_id IS NOT NULL
)
SELECT
    cand.workspace_id,
    COUNT(*) AS candidate_secret_count,
    (
        SELECT COUNT(*)
        FROM secret_versions AS v
        INNER JOIN candidates AS c2
            ON c2.workspace_id = v.workspace_id AND c2.secret_id = v.secret_id
        WHERE c2.workspace_id = cand.workspace_id
    ) AS candidate_secret_version_count
FROM candidates AS cand
GROUP BY cand.workspace_id
ORDER BY cand.workspace_id;

-- 3) BLOCKERS: candidate secrets also referenced by model_configs
WITH candidates AS (
    SELECT DISTINCT c.workspace_id, c.credential_secret_id AS secret_id
    FROM service_connections AS c
    INNER JOIN capability_providers AS p
        ON p.workspace_id = c.workspace_id AND p.id = c.provider_id
    WHERE p.provider_kind = 'HTTP_OPENAPI'
      AND c.credential_secret_id IS NOT NULL
)
SELECT
    m.workspace_id,
    COUNT(*) AS model_config_shared_ref_count
FROM model_configs AS m
INNER JOIN candidates AS cand
    ON cand.workspace_id = m.workspace_id AND cand.secret_id = m.credential_secret_id
GROUP BY m.workspace_id
ORDER BY m.workspace_id;
-- Expect zero rows. Any row blocks 000060.

-- 4) BLOCKERS: candidate secrets also referenced by non-HTTP_OPENAPI connections
WITH candidates AS (
    SELECT DISTINCT c.workspace_id, c.credential_secret_id AS secret_id
    FROM service_connections AS c
    INNER JOIN capability_providers AS p
        ON p.workspace_id = c.workspace_id AND p.id = c.provider_id
    WHERE p.provider_kind = 'HTTP_OPENAPI'
      AND c.credential_secret_id IS NOT NULL
)
SELECT
    c.workspace_id,
    COUNT(*) AS non_target_connection_shared_ref_count
FROM service_connections AS c
INNER JOIN candidates AS cand
    ON cand.workspace_id = c.workspace_id AND cand.secret_id = c.credential_secret_id
INNER JOIN capability_providers AS p
    ON p.workspace_id = c.workspace_id AND p.id = c.provider_id
WHERE p.provider_kind <> 'HTTP_OPENAPI'
GROUP BY c.workspace_id
ORDER BY c.workspace_id;
-- Expect zero rows. Any row blocks 000060.

-- 5) Schema / version readiness (informational)
SELECT version, dirty FROM schema_migrations;
-- Expect version = 59, dirty = false before applying 000060.
