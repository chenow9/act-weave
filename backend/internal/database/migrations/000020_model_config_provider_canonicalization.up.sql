-- Task 4A R11-2: canonicalize model_configs.provider to the closed canonical set
-- ('openai', 'openai-compatible').
--
-- Why: provider is part of the wire identity. It is persisted verbatim, copied
-- into frozen model snapshots, hashed into modelconfig.WireConfigDigest, and
-- matched exactly (no case fold, no alias map) by
-- chatruntimebridge.agenticSupportedProviders. Three spellings of the same
-- provider were live at once — 'OPENAI_COMPATIBLE' in database rows,
-- 'OpenAI Compatible' from the console, 'openai-compatible' in the agentic
-- allowlist — so every stored config was rejected by the Agentic initial path.
-- modelconfig.CanonicalProvider now fails closed on writes; this migration
-- brings existing rows to the same canonical form.
--
-- Alias set (must stay identical to modelconfig.canonicalProviderAliases):
--   openai            <- 'openai', 'OPENAI', 'OpenAI'
--   openai-compatible <- 'openai-compatible', 'OPENAI-COMPATIBLE',
--                        'openai_compatible', 'OPENAI_COMPATIBLE',
--                        'OpenAI Compatible'
-- Matching is on btrim(provider), so a stored ' openai ' is canonicalized too:
-- chatruntimebridge.requireSupportedAgenticProvider rejects any provider that is
-- not byte-identical to its own trimmed form, so a padded value fails closed at
-- the agentic boundary even though it survives the not-blank CHECK.
--
-- Providers outside the alias set are deliberately LEFT UNCHANGED: this
-- migration canonicalizes known spellings, it never guesses an identity for an
-- unknown provider. Such rows keep a self-consistent digest and continue to be
-- rejected at the agentic boundary; writes through the application now reject
-- them at modelconfig.CanonicalProvider.
--
-- Digest consequence (critical): a row whose provider text changes gets a new
-- WireConfigDigest, so any stored verification evidence is stale by definition.
-- Every changed row therefore has agentic_capabilities reset to '{}' and all
-- verification evidence cleared, and is marked as requiring re-verification:
--   * VERIFIED / ERROR / UNVERIFIED -> UNVERIFIED
--   * DISABLED stays DISABLED (explicit operator kill switch; it already
--     requires empty caps and null evidence, so it is not "re-enabled" here)
-- lock_version is bumped so pre-migration CAS snapshots cannot be applied on
-- top of the new identity. Nothing is invented: no capability document, no
-- timestamp, no latency, no error code.
--
-- Soft-deleted rows (deleted_at IS NOT NULL) are canonicalized as well, so the
-- column has a single spelling per identity and a restored row can never
-- reintroduce a legacy spelling.

-- Exact pre-migration state of every row this migration rewrites. It exists so
-- the down migration is a real inverse instead of a guess (unlike a lossy
-- canonicalization, where 'OPENAI_COMPATIBLE' and 'OpenAI Compatible' both fold
-- to the same canonical value and could not otherwise be told apart). The down
-- migration restores from it and drops it.
CREATE TABLE model_config_provider_canonicalizations (
    model_config_id UUID PRIMARY KEY,
    legacy_provider TEXT NOT NULL,
    canonical_provider TEXT NOT NULL,
    legacy_status TEXT NOT NULL,
    legacy_agentic_capabilities JSONB NOT NULL,
    legacy_last_verified_at TIMESTAMPTZ,
    legacy_last_latency_ms INTEGER,
    legacy_last_error_code TEXT,
    legacy_lock_version BIGINT NOT NULL,
    legacy_updated_at TIMESTAMPTZ NOT NULL,
    canonical_lock_version BIGINT NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT model_config_provider_canonicalizations_config_fk
        FOREIGN KEY (model_config_id) REFERENCES model_configs (id) ON DELETE CASCADE,
    CONSTRAINT model_config_provider_canonicalizations_changed_check
        CHECK (legacy_provider <> canonical_provider),
    CONSTRAINT model_config_provider_canonicalizations_canonical_check
        CHECK (canonical_provider IN ('openai', 'openai-compatible')),
    CONSTRAINT model_config_provider_canonicalizations_lock_bump_check
        CHECK (canonical_lock_version = legacy_lock_version + 1),
    CONSTRAINT model_config_provider_canonicalizations_caps_object_check
        CHECK (jsonb_typeof(legacy_agentic_capabilities) = 'object')
);

INSERT INTO model_config_provider_canonicalizations (
    model_config_id, legacy_provider, canonical_provider, legacy_status,
    legacy_agentic_capabilities, legacy_last_verified_at, legacy_last_latency_ms,
    legacy_last_error_code, legacy_lock_version, legacy_updated_at,
    canonical_lock_version
)
SELECT
    m.id,
    m.provider,
    alias.canonical,
    m.status,
    m.agentic_capabilities,
    m.last_verified_at,
    m.last_latency_ms,
    m.last_error_code,
    m.lock_version,
    m.updated_at,
    m.lock_version + 1
FROM model_configs AS m
JOIN (
    VALUES
        ('openai', 'openai'),
        ('OPENAI', 'openai'),
        ('OpenAI', 'openai'),
        ('openai-compatible', 'openai-compatible'),
        ('OPENAI-COMPATIBLE', 'openai-compatible'),
        ('openai_compatible', 'openai-compatible'),
        ('OPENAI_COMPATIBLE', 'openai-compatible'),
        ('OpenAI Compatible', 'openai-compatible')
) AS alias(spelling, canonical)
  ON btrim(m.provider) = alias.spelling
-- Rows already stored in canonical form are untouched: no lock bump, no
-- evidence loss, no forced re-verification.
WHERE m.provider IS DISTINCT FROM alias.canonical;

UPDATE model_configs AS m
SET
    provider = b.canonical_provider,
    status = CASE WHEN m.status = 'DISABLED' THEN 'DISABLED' ELSE 'UNVERIFIED' END,
    agentic_capabilities = '{}'::jsonb,
    last_verified_at = NULL,
    last_latency_ms = NULL,
    last_error_code = NULL,
    lock_version = b.canonical_lock_version,
    updated_at = clock_timestamp()
FROM model_config_provider_canonicalizations AS b
WHERE b.model_config_id = m.id;
