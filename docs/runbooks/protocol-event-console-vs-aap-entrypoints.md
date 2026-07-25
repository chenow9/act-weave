# Runbook: Console vs AAP event entry points

Operator-facing map of **internal Console** vs **external AAP** for protocol SSE.
Design detail: [`docs/design/protocol-event-unification-console-aap.md`](../design/protocol-event-unification-console-aap.md) §5.2.

## One-line contract

- **Realtime SoT** = protocol events (`item.delta`, `run.completed`, …).
- **Do not** treat legacy `RUN_*` as Console’s sole legal live SSE types.
- **Do not** dual-write protocol SoT + legacy `RUN_*` SoT in production.
- **External AAP + SDK** public surface stays stable (no breaking path/auth/frame changes for U4).

## Entry points

| Dimension | Internal Console | External AAP |
|-----------|------------------|--------------|
| Base | `/api/v1` | `/api/agent-access/v1` |
| Auth | User session JWT / cookie + workspace auth | Bearer AAP token (client / grant / subject) |
| Conversation model | `chat_sessions` + `chat_messages` | AAP `conversations` |
| Start execution | `POST .../chat/sessions/:sid/messages` → 202 + `runId` | AAP `createRun` (and related commands) |
| Event subscribe | `GET .../agent-runs/:rid/events` | `GET .../agents/:aid/runs/:rid/events` |
| Human confirmation | Console confirmation + `resumeToken` storage | AAP interaction decision |
| Client | Vue `chat` store + `run-event-stream` | `@actweave/agent-client` |
| This unification | Console consume + stream readiness | **Unchanged** (external freeze) |

## SSE frame semantics (shared)

Both entry points stream the **same protocol** wire shape:

```text
id: <sequence>
event: <protocol-type>   # e.g. item.delta, run.completed
data: <protocol envelope JSON>
```

Console live path maps protocol types in:

- `frontend/src/services/run-event-stream.ts` → `PROTOCOL_STREAM_EVENT_TYPES`

Legacy `RUN_*` frames are **secondary thin-compat only** (one-release residual/tests), not the production whitelist and not a second SoT.

## Operator checks

| Symptom | Likely area | What to check |
|---------|-------------|----------------|
| Console chat needs refresh for assistant text | Console consume | Live frames are protocol (`item.delta` / `run.*`); store uses `run-event-stream`, not a `RUN_*`-only gate |
| First events 404 after send | Stream readiness | Bounded 404 backoff on Console; backend ensure-stream after `SendMessage` |
| External integrator break | AAP / SDK freeze | Base still `/api/agent-access/v1`; SDK `followRun` / `streamRunEvents` defaults unchanged |
| Dual event dialects on one run | SoT violation | No production dual-write of legacy `RUN_*` + protocol as two SoTs |

## Related anchors

| Area | Path |
|------|------|
| Console stream parse / project | `frontend/src/services/run-event-stream.ts` |
| Console orchestration | `frontend/src/stores/chat.ts` |
| Console events route | `backend/internal/transport/http` chat agent-run events |
| AAP OpenAPI | `docs/openapi/agent-access-v1.yaml` |
| SDK client | `sdk/typescript/` (`AgentAccessClient`, `followRun`) |
| Implementation checklist | `docs/design/protocol-event-unification-console-aap-checklist.md` |
