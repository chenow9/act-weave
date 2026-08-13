-- Inverse of 000021. Rows using platform_bounded / carry_all fail this down.

ALTER TABLE agent_run_context_assemblies
    DROP CONSTRAINT IF EXISTS agent_run_context_assemblies_agentic_mode_digest_coupling_check;

ALTER TABLE agent_run_context_assemblies
    DROP CONSTRAINT IF EXISTS agent_run_context_assemblies_tool_search_mode_check,
    ADD CONSTRAINT agent_run_context_assemblies_tool_search_mode_check
        CHECK (tool_search_mode IN ('none', 'client_bounded'));

ALTER TABLE agent_run_context_assemblies
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

ALTER TABLE model_configs
    DROP CONSTRAINT IF EXISTS model_configs_tool_disclosure_policy_object_check;

ALTER TABLE model_configs
    DROP COLUMN IF EXISTS tool_disclosure_policy;
