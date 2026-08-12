-- Rollback Task 3 expand-only columns. Drop constraints first, then columns.
-- Does not rewrite historical assembly rows.
--
-- NON-RESTORABILITY: up-migration may have discarded pre-Agentic evidence:
--   - VERIFIED → UNVERIFIED with cleared last_verified_*/caps
--   - UNVERIFIED/DISABLED legacy evidence cleared
--   - ERROR incomplete/unknown-code → UNVERIFIED with cleared evidence
-- Keep-ERROR rows (complete allowlisted evidence) retain their evidence fields.
-- Down intentionally does NOT guess or restore discarded evidence/statuses.
-- Schema additions (agentic_capabilities + assembly columns/constraints) are
-- removed safely below.

ALTER TABLE agent_run_context_assemblies
    DROP CONSTRAINT IF EXISTS agent_run_context_assemblies_agentic_mode_digest_coupling_check,
    DROP CONSTRAINT IF EXISTS agent_run_context_assemblies_agentic_counts_non_negative_check,
    DROP CONSTRAINT IF EXISTS agent_run_context_assemblies_tool_catalog_digest_check,
    DROP CONSTRAINT IF EXISTS agent_run_context_assemblies_tool_search_mode_check;

ALTER TABLE agent_run_context_assemblies
    DROP COLUMN IF EXISTS dynamic_tool_load_reserve_tokens,
    DROP COLUMN IF EXISTS deferred_metadata_tokens,
    DROP COLUMN IF EXISTS immediate_tools_tokens,
    DROP COLUMN IF EXISTS max_loaded_tool_count,
    DROP COLUMN IF EXISTS deferred_tool_count,
    DROP COLUMN IF EXISTS immediate_tool_count,
    DROP COLUMN IF EXISTS tool_catalog_digest,
    DROP COLUMN IF EXISTS tool_search_mode;

ALTER TABLE model_configs
    DROP CONSTRAINT IF EXISTS model_configs_agentic_capabilities_object_check;

ALTER TABLE model_configs
    DROP COLUMN IF EXISTS agentic_capabilities;
