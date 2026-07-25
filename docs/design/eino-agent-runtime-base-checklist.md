# Eino Agent Runtime — Implementation Checklist

Status tracker for the agent + workflow eino base. Ops: [`../runbooks/eino-agent-runtime-rollout.md`](../runbooks/eino-agent-runtime-rollout.md). No-reinvent: [`eino-no-reinvent-checklist.md`](./eino-no-reinvent-checklist.md).

---

## Agent path

| Item | Status | Notes |
|------|--------|--------|
| Production Enqueue = `agentrun.Factory` → `chatruntimebridge` | [x] | Always eino |
| Continue = `einoChatResume` only | [x] | chatLoop-only → invalid |
| Legacy `tool_loop` / `Executor` / `HTTPModelClient` | [x] | **Removed** from codebase |
| `ExtractLoopState` production path | [x] | **Removed** |
| True Stream → `item.delta` (Index=0 content part) | [x] | D14 |
| Tool HITL pause / ResumeWithParams | [x] | A.4 goldens |
| MODEL step + audit reasoning (`agentAudit.debug`) | [x] | bridge `recordModelTurn` |
| Emergency rollback | [x] | Previous binary / drain traffic |

---

## Workflow path (P0)

| Item | Status | Notes |
|------|--------|--------|
| Load default `engine=eino` | [x] | compose CoreGraphRunner |
| `wrapper` rollback valve | [x] | PlanRunner frozen |
| `eino` / `eino_core` alias | [x] | Same runner |
| Checkpoint store on bootstrap when ≠ wrapper | [x] | |

---

## Goldens / verification

| Item | Status |
|------|--------|
| Bridge stream multi-delta offline golden | [x] |
| Bridge tool success (dry-run) golden | [x] |
| Approval resume type-order + ownership | [x] |
| chatLoop-only continue rejected | [x] |

---

## Explicit non-goals

| Item | Note |
|------|------|
| Dual agent engines in one process | Removed |
| Flag → alternate agent engine | Removed |
| New PlanRunner nodes | Use `einoruntime` graph |
