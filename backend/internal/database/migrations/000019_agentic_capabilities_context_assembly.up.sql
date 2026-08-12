-- Task 3: expand-only AgenticCapabilities on model_configs and deferred-aware
-- assembly manifest fields on agent_run_context_assemblies.
-- Defaults keep old rows readable; no backfill of guessed Agentic capability
-- or tool-search mode. Existing immutability trigger remains effective.
--
-- Pre-Agentic model_configs used GET-/models-only verification and carry no
-- agentic_capabilities document. After adding the column (default {}), strict
-- application reads enforce status/evidence/caps cross-invariants. Migration
-- therefore normalizes EVERY legacy row that would be unreadable:
--
--   1) VERIFIED → UNVERIFIED, clear all evidence, caps {}, bump lock.
--      (Pre-Agentic VERIFIED has no Agentic capability document.)
--   2) UNVERIFIED / DISABLED carrying any legacy verification evidence
--      (last_verified_at / last_latency_ms / last_error_code non-null) →
--      preserve status, clear all evidence, caps {}, bump lock.
--   3) ERROR with complete evidence AND last_error_code in the exact new
--      stable allowlist → preserve ERROR + evidence, force caps {}, bump lock
--      only when agentic_capabilities is not already {}.
--      ERROR incomplete/unknown-code → UNVERIFIED, clear evidence/caps, bump.
--
-- Bump lock_version/updated_at for every row whose persisted state changes so
-- stale pre-migration CAS snapshots cannot survive. Never invent times,
-- latency, or error codes. Old discarded evidence is NOT restored on down
-- (non-restorable). Never invent capability documents.

ALTER TABLE model_configs
    ADD COLUMN agentic_capabilities JSONB NOT NULL DEFAULT '{}'::JSONB;

ALTER TABLE model_configs
    ADD CONSTRAINT model_configs_agentic_capabilities_object_check
        CHECK (jsonb_typeof(agentic_capabilities) = 'object');

-- Exact stable verification error-code allowlist (must match application
-- validVerificationErrorCode / modelconfig ErrorCode* constants).
-- Used only to decide whether a pre-Agentic ERROR row may keep its evidence.

-- 1) Pre-Agentic VERIFIED → UNVERIFIED + clear evidence + caps {} + bump lock.
UPDATE model_configs
SET
    status = 'UNVERIFIED',
    last_verified_at = NULL,
    last_latency_ms = NULL,
    last_error_code = NULL,
    agentic_capabilities = '{}'::jsonb,
    lock_version = lock_version + 1,
    updated_at = clock_timestamp()
WHERE status = 'VERIFIED';

-- 2) UNVERIFIED / DISABLED with any legacy verification evidence → preserve
--    status, clear evidence + caps {}, bump lock. Already-clean rows unchanged.
UPDATE model_configs
SET
    last_verified_at = NULL,
    last_latency_ms = NULL,
    last_error_code = NULL,
    agentic_capabilities = '{}'::jsonb,
    lock_version = lock_version + 1,
    updated_at = clock_timestamp()
WHERE status IN ('UNVERIFIED', 'DISABLED')
  AND (
      last_verified_at IS NOT NULL
      OR last_latency_ms IS NOT NULL
      OR last_error_code IS NOT NULL
      OR agentic_capabilities IS DISTINCT FROM '{}'::jsonb
  );

-- 3a) ERROR with complete evidence and allowlisted code: preserve ERROR/evidence,
--     force caps {}. After ADD COLUMN the default is already {}; bump lock only
--     when agentic_capabilities is not already {} (actual persisted change).
--     Never invent/alter times, latency, or codes on keep-ERROR rows.
UPDATE model_configs
SET
    agentic_capabilities = '{}'::jsonb,
    lock_version = lock_version + 1,
    updated_at = clock_timestamp()
WHERE status = 'ERROR'
  AND last_verified_at IS NOT NULL
  AND last_latency_ms IS NOT NULL
  AND last_latency_ms >= 0
  AND last_error_code IS NOT NULL
  AND last_error_code IN (
      'MODEL_CONFIG_VERIFICATION_TIMEOUT',
      'MODEL_CONFIG_NETWORK_ERROR',
      'MODEL_CONFIG_AUTHENTICATION_FAILED',
      'MODEL_CONFIG_UPSTREAM_ERROR',
      'MODEL_CONFIG_RESPONSES_UNSUPPORTED',
      'MODEL_CONFIG_TOOL_SEARCH_UNSUPPORTED',
      'MODEL_CONFIG_AGENTIC_STREAM_INVALID',
      'MODEL_CONFIG_AGENTIC_USAGE_INVALID'
  )
  AND agentic_capabilities IS DISTINCT FROM '{}'::jsonb;

-- 3b) ERROR incomplete or non-allowlisted code → UNVERIFIED, clear evidence/caps,
--     bump lock. Never invent replacement codes/times.
UPDATE model_configs
SET
    status = 'UNVERIFIED',
    last_verified_at = NULL,
    last_latency_ms = NULL,
    last_error_code = NULL,
    agentic_capabilities = '{}'::jsonb,
    lock_version = lock_version + 1,
    updated_at = clock_timestamp()
WHERE status = 'ERROR'
  AND NOT (
      last_verified_at IS NOT NULL
      AND last_latency_ms IS NOT NULL
      AND last_latency_ms >= 0
      AND last_error_code IS NOT NULL
      AND last_error_code IN (
          'MODEL_CONFIG_VERIFICATION_TIMEOUT',
          'MODEL_CONFIG_NETWORK_ERROR',
          'MODEL_CONFIG_AUTHENTICATION_FAILED',
          'MODEL_CONFIG_UPSTREAM_ERROR',
          'MODEL_CONFIG_RESPONSES_UNSUPPORTED',
          'MODEL_CONFIG_TOOL_SEARCH_UNSUPPORTED',
          'MODEL_CONFIG_AGENTIC_STREAM_INVALID',
          'MODEL_CONFIG_AGENTIC_USAGE_INVALID'
      )
  );

ALTER TABLE agent_run_context_assemblies
    ADD COLUMN tool_search_mode TEXT NOT NULL DEFAULT 'none',
    ADD COLUMN tool_catalog_digest CHAR(64),
    ADD COLUMN immediate_tool_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN deferred_tool_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN max_loaded_tool_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN immediate_tools_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN deferred_metadata_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN dynamic_tool_load_reserve_tokens BIGINT NOT NULL DEFAULT 0;

ALTER TABLE agent_run_context_assemblies
    ADD CONSTRAINT agent_run_context_assemblies_tool_search_mode_check
        CHECK (tool_search_mode IN ('none', 'client_bounded')),
    ADD CONSTRAINT agent_run_context_assemblies_tool_catalog_digest_check
        CHECK (tool_catalog_digest IS NULL OR tool_catalog_digest ~ '^[0-9a-f]{64}$'),
    ADD CONSTRAINT agent_run_context_assemblies_agentic_counts_non_negative_check
        CHECK (
            immediate_tool_count >= 0
            AND deferred_tool_count >= 0
            AND max_loaded_tool_count >= 0
            AND max_loaded_tool_count <= 40
            AND immediate_tools_tokens >= 0
            AND deferred_metadata_tokens >= 0
            AND dynamic_tool_load_reserve_tokens >= 0
        ),
    -- Mode/digest coupling and structural max-loaded bound:
    -- none => null digest + all agentic counts/tokens at defaults;
    -- client_bounded => non-null lowercase 64-hex digest,
    -- max_loaded_tool_count = LEAST(deferred_tool_count, 40),
    -- exact agentic estimator version, and tools_overhead identity via
    -- overflow-safe NUMERIC casts:
    -- tools_overhead_tokens = immediate + metadata + reserve.
    ADD CONSTRAINT agent_run_context_assemblies_agentic_mode_digest_coupling_check
        CHECK (
            (
                tool_search_mode = 'none'
                AND tool_catalog_digest IS NULL
                AND immediate_tool_count = 0
                AND deferred_tool_count = 0
                AND max_loaded_tool_count = 0
                AND immediate_tools_tokens = 0
                AND deferred_metadata_tokens = 0
                AND dynamic_tool_load_reserve_tokens = 0
            )
            OR
            (
                tool_search_mode = 'client_bounded'
                AND tool_catalog_digest IS NOT NULL
                AND tool_catalog_digest ~ '^[0-9a-f]{64}$'
                AND max_loaded_tool_count = LEAST(deferred_tool_count, 40)
                AND estimator_version = 'contextwindow-estimator.agentic-openai-responses.v1'
                AND tools_overhead_tokens::numeric =
                    (immediate_tools_tokens::numeric
                     + deferred_metadata_tokens::numeric
                     + dynamic_tool_load_reserve_tokens::numeric)
            )
        );
