# Runbook: Agent Context LLM Compaction (ZKL-81)

## Gate (default OFF)

Config path: `runtime.sessionContext.compaction` in `backend/config.yaml`.

| Field | Default | Meaning |
|---|---|---|
| `enabled` | `false` | Master switch |
| `mode` | `disabled` | `disabled` \| `shadow` \| `enforced` |
| `allowAllWorkspaces` | `false` | Independent of parent sessionContext allowlist |
| `workspaceIds` | `[]` | Allowlist when not allowAll |
| `rolloutVersion` | `context-compaction-default` | Frozen into run snapshot |

### Rollout sequence

1. **disabled** — no v2 compact knobs; behavior = ZKL-74 token window / legacy.
2. **shadow** — freeze plan/trigger metrics only; no LLM compact, no READY/step/item body (when fully wired).
3. **enforced + allowlist** — selected workspaces only.
4. **enforced + allowAll** — gradual after metrics green.

### Rollback

1. Set `enabled: false` or `mode: disabled` and/or clear allowlist.
2. **Does not delete** permanent summary objects, steps, items, events, or T4-B protocol plaintext already written.
3. New runs stop compacting; old runs remain replayable as written.

## T4-B permanence

When Agent policy `aap.includeCompactionSummary=true` (default false) is snapshotted at run create:

- Successful compact writes permanent plaintext into `run_items.snapshot` and `item.completed` payload.
- Closing the toggle only affects **new** runs.
- Do not promise UI deletion of historical protocol bodies.

## Safe operations

- Prefer empty allowlist + disabled for production until IC-14 AC green.
- Never log summary body, provider response, secrets, or object URLs.
- Metrics labels must stay low-cardinality (no workspace/session/run IDs in labels).

## Failure modes

| Code | Stage | Operator action |
|---|---|---|
| `CONTEXT_COMPACTION_MODEL_*` | model | Check provider latency/quota; expect token_window fallback |
| `CONTEXT_COMPACTION_OBJECT_PUT_FAILED` | store | Check MinIO/encryption; permanent orphan objects possible |
| `CONTEXT_COMPACTION_EVIDENCE_PERSIST_FAILED` | project | Hard fail before main model; fix DB/protocol UoW |
| `CONTEXT_COMPACTION_TARGET_NOT_MET` | assemble | Expect fallback; review maxSummaryTokens / maxPasses |

## Related docs

- Product: `docs/design/agent-context-llm-compaction-product-design.md` v0.2
- Tech: `docs/design/agent-context-llm-compaction-technical-design.md` v0.1
- Checklist: `docs/design/agent-context-llm-compaction-implementation-checklist.md`
