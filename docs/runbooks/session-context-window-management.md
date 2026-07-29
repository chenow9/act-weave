# Session Context Window Management Runbook (ZKL-74)

## Gate configuration

Process config (`runtime.sessionContext`):

| Field | Default | Meaning |
| --- | --- | --- |
| `enabled` | `false` | Master switch (fail-closed) |
| `mode` | `disabled` | `disabled` / `shadow` / `enforced` |
| `allowAllWorkspaces` | `false` | When true, all workspaces are eligible |
| `workspaceIds` | `[]` | Allowlist when not allow-all |
| `rolloutVersion` | `session-context-default` | Recorded in snapshot sources |

Env overrides (if present in config loader): prefer config.yaml for local.

## Modes

1. **disabled** (default): new runs write legacy `run.v1` + `context_policy_snapshot={}`; bridge uses full-history `buildMessages`.
2. **shadow** (IC-10): plan may be computed for metrics only; authoritative manifest must not be written as applied when shadow is incomplete. Current implementation writes v2 snapshots only when gate allows workspace and model capabilities are complete; production default remains disabled.
3. **enforced**: eligible workspaces with complete `runtimeCapabilities` create `run.v2` + `session-context.v1`; bridge runs `AssembleTokenWindow` and persists assembly manifests.

## Prerequisites before enforce allowlist

- Model `runtimeCapabilities` validated (`model-runtime.v1`)
- Tokenizer profile in registry (`o200k_base` / `cl100k_base` / `byte_upper_bound`)
- Estimator actual/estimate ratio not systematically underestimating (monitor after IC-09 usage)
- No elevated CONTEXT_WINDOW_EXCEEDED_UPSTREAM rate

## Rollback

1. Set `runtime.sessionContext.enabled: false` or `mode: disabled` and restart.
2. New runs return to legacy full history.
3. Existing `run.v2` runs continue to honor their immutable snapshots.
4. Do **not** drop tables/columns or delete manifests / permanent messages / summaries.

## Diagnostics (no prompt bodies)

- Assembly table: `agent_run_context_assemblies` by `(workspace_id, run_id)` — IDs, hashes, budgets only
- Run snapshots: `agent_runs.context_policy_snapshot`, `agent_snapshot`, `model_snapshot`
- Error codes: `CONTEXT_*` on failed runs

## Data retention

- Expand-only schema retained on rollback
- Manifests permanent / immutable
- Original `chat_messages` permanent
- Summaries (IC-11+) permanent encrypted objects when rolling is enabled
