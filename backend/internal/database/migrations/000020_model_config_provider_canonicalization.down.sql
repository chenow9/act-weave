-- Rollback of the provider canonicalization. Restores the exact pre-migration
-- state (provider spelling, status, agentic_capabilities, verification evidence,
-- lock_version, updated_at) for every row the up migration rewrote, using the
-- state it recorded in model_config_provider_canonicalizations. Nothing is
-- guessed: canonicalization is lossy on its own, which is precisely why the up
-- migration recorded the pre-state.
--
-- Compare-and-swap guard: a row is restored only if it still carries the exact
-- provider and lock_version the up migration wrote. Any row edited, re-verified,
-- or otherwise bumped after the migration is left alone — a rollback must never
-- clobber newer work with a stale legacy snapshot. Skipped rows keep their
-- canonical provider, which the pre-R11-2 application accepts as well (it stored
-- providers verbatim and the agentic allowlist already contained both canonical
-- values), so leaving them canonical is safe rather than a broken state.
--
-- Restoring the legacy (lower) lock_version is intentional: it is the exact
-- inverse of the bump, and after rollback the pre-migration CAS snapshots are
-- the current ones again. The recording table is dropped last, so re-applying
-- the up migration records the pre-state again from scratch.

UPDATE model_configs AS m
SET
    provider = b.legacy_provider,
    status = b.legacy_status,
    agentic_capabilities = b.legacy_agentic_capabilities,
    last_verified_at = b.legacy_last_verified_at,
    last_latency_ms = b.legacy_last_latency_ms,
    last_error_code = b.legacy_last_error_code,
    lock_version = b.legacy_lock_version,
    updated_at = b.legacy_updated_at
FROM model_config_provider_canonicalizations AS b
WHERE b.model_config_id = m.id
  AND m.provider = b.canonical_provider
  AND m.lock_version = b.canonical_lock_version;

DROP TABLE model_config_provider_canonicalizations;
