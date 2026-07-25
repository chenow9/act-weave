# Eino Runtime Rollout (Agent + Workflow)

**Scope:**  
- **Agent** conversation engine (`runtime.agent`) — ADK / `chatruntimebridge`  
- **Workflow** DAG engine (`runtime.workflow.engine`) — compose / `EinoCoreRunner` after **P0**

Related: [`docs/design/eino-agent-runtime-base-checklist.md`](../design/eino-agent-runtime-base-checklist.md) · No-reinvent: [`eino-no-reinvent-checklist.md`](../design/eino-no-reinvent-checklist.md)

---

## One-line production defaults (after config Load)

| Surface | Default engine | Rollback |
|---------|----------------|----------|
| **Agent** | ADK (`agentrun.Factory` → bridge) | Previous binary / drain traffic |
| **Workflow** | **`eino`** (compose `CoreGraphRunner`) | Explicit `engine: wrapper` or `ACTWEAVE_RUNTIME_WORKFLOW_ENGINE=wrapper` |

`eino` and `eino_core` share the same compose runner. Zero-value `RuntimeConfig` **without** Load does **not** stage these opens (tests stay fail-closed / Normalized workflow → wrapper).

---

## Agent runtime

| Item | Production |
|------|------------|
| Enqueue | Always **eino** via `agentrun.Factory` → `chatruntimebridge.Bridge` |
| Continue | **einoChatResume only**; `chatLoop`-only snapshots → invalid / error |
| In-process alternate engine | **None** |
| `runtime.agent.enabled=false` | Does **not** change Enqueue routing; still eino |
| Emergency rollback | **Previous binary** and/or **drain / stop traffic** |

### Agent staged defaults

After config **Load** (`applyRuntimeDefaults`):

| `runtime.agent` input | Effective process default |
|-----------------------|---------------------------|
| Omitted / empty | `enabled=true`, `allowAllWorkspaces=true` |
| Explicit `enabled: false` | Flag retained for diagnostics only; Enqueue still eino |
| Allowlist only | Diagnostics via `AllowsWorkspace`; does **not** switch engines |

### Agent env

| Env | Notes |
|-----|--------|
| `ACTWEAVE_RUNTIME_AGENT_ENABLED` | Diagnostics / product gates only — not an engine valve |
| `ACTWEAVE_RUNTIME_AGENT_ALLOW_ALL_WORKSPACES` | Diagnostics allowlist |
| `ACTWEAVE_RUNTIME_AGENT_WORKSPACE_IDS` | Comma-separated allowlist |

### Emergency agent rollback

There is **no** in-process flag flip to another agent engine.

1. Deploy previous known-good binary, or  
2. Drain / stop agent traffic at the edge.

In-flight confirmation rows may hold compose / ADK checkpoints with TTL = confirmation expiry. Prefer wait-for-TTL or fail the run rather than mixing binaries mid-interrupt.

---

## Workflow runtime (P0)

| Item | After P0 |
|------|----------|
| Omitted `runtime.workflow` after Load | `engine=eino` + `allowAllWorkspaces=true` |
| Explicit `engine: wrapper` | Preserved (PlanRunner rollback valve) |
| `eino` / `eino_core` | Same `EinoCoreRunner` |
| Compose checkpoint store | Required on application bootstrap when engine ≠ wrapper |
| PlanRunner | **FROZEN** — rollback/tests only; new nodes → `einoruntime` graph |

### Workflow staged defaults

| `runtime.workflow` input | Effective process default |
|--------------------------|---------------------------|
| Omitted / empty | `engine=eino`, `allowAllWorkspaces=true` → **compose for all workspaces** |
| Explicit `engine: wrapper` | **PlanRunner** for all workspaces (rollback) |
| `engine: eino` + `allowAllWorkspaces: true` | Compose for all |
| `engine: eino` + allowlist | Compose only for listed workspaces; others → wrapper |
| `engine: eino` + empty allowlist + `allowAll=false` | Fail-closed → **wrapper for everyone** |

### Workflow emergency rollback

```yaml
runtime:
  workflow:
    engine: wrapper
```

or:

```bash
export ACTWEAVE_RUNTIME_WORKFLOW_ENGINE=wrapper
# restart process
```

In-flight Approval rows may hold compose checkpoints (`eino_checkpoints`) with TTL = confirmation expiry. After switching to wrapper, prefer wait-for-TTL / fail the execution / keep serving on compose until interrupt window ends.

---

## Agent audit debug (reasoning)

```yaml
agentAudit:
  debug: true   # process restart required; env: ACTWEAVE_AGENT_AUDIT_DEBUG
```

When on, the bridge persists MODEL step `output_summary.reasoning` from provider `reasoning_content` for the platform-admin audit timeline.

---

## Smoke checks

1. Start process with default config → agent chat completes (text stream + optional tools).  
2. Confirm path: tool requiring confirmation → approve → resume without second Invoke.  
3. Reject / expire chatLoop-only historical snapshots → invalid continue (no resume).  
4. Workflow trial with `engine: eino` succeeds; flip to `wrapper` only if rolling back compose.

---

## Out of scope

| Item | Note |
|------|------|
| Dual agent engines in one process | Removed |
| Flag-based agent engine switch | Removed |
| Expanding PlanRunner | Frozen — use `einoruntime` graph nodes |
