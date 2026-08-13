-- Expand-only tool disclosure policy on model_configs, plus assembly mode
-- allowlist for later platform_bounded / carry_all writers (production still
-- only writes none / client_bounded). No backfill of guessed policy.

ALTER TABLE model_configs
    ADD COLUMN tool_disclosure_policy JSONB NOT NULL DEFAULT '{}'::JSONB;

ALTER TABLE model_configs
    ADD CONSTRAINT model_configs_tool_disclosure_policy_object_check
        CHECK (jsonb_typeof(tool_disclosure_policy) = 'object');

ALTER TABLE agent_run_context_assemblies
    DROP CONSTRAINT agent_run_context_assemblies_tool_search_mode_check,
    ADD CONSTRAINT agent_run_context_assemblies_tool_search_mode_check
        CHECK (tool_search_mode IN ('none', 'client_bounded', 'platform_bounded', 'carry_all'));

-- Rewrite mode/digest coupling (design §8.5). none + client_bounded keep today's
-- shape; platform_bounded / carry_all are accepted so later PRs can write them.
ALTER TABLE agent_run_context_assemblies
    DROP CONSTRAINT agent_run_context_assemblies_agentic_mode_digest_coupling_check,
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
                AND tool_catalog_digest ~ '^[0-9a-f]{64}$'
                AND estimator_version = 'contextwindow-estimator.agentic-openai-responses.v1'
                AND max_loaded_tool_count = LEAST(deferred_tool_count, 40)
                AND tools_overhead_tokens::numeric =
                    (immediate_tools_tokens::numeric
                     + deferred_metadata_tokens::numeric
                     + dynamic_tool_load_reserve_tokens::numeric)
            )
            OR
            (
                tool_search_mode = 'platform_bounded'
                AND tool_catalog_digest ~ '^[0-9a-f]{64}$'
                AND estimator_version = 'contextwindow-estimator.agentic-openai-responses.v2'
                AND deferred_metadata_tokens = 0
                AND max_loaded_tool_count = LEAST(deferred_tool_count, 5)
                AND tools_overhead_tokens::numeric =
                    (immediate_tools_tokens::numeric
                     + deferred_metadata_tokens::numeric
                     + dynamic_tool_load_reserve_tokens::numeric)
            )
            OR
            (
                tool_search_mode = 'carry_all'
                AND tool_catalog_digest ~ '^[0-9a-f]{64}$'
                AND estimator_version = 'contextwindow-estimator.agentic-openai-responses.v2'
                AND deferred_tool_count = 0
                AND deferred_metadata_tokens = 0
                AND max_loaded_tool_count = 0
                AND dynamic_tool_load_reserve_tokens = 0
                AND tools_overhead_tokens::numeric = immediate_tools_tokens::numeric
            )
        );
