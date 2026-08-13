# demos/aap-chat Real E2E Report

| Field | Value |
|-------|-------|
| **Date** | 2026-08-12 |
| **Demo** | `demos/aap-chat` Live AAP |
| **UI** | `http://127.0.0.1:5188` |
| **BFF** | `http://127.0.0.1:8791` |
| **AAP** | `http://127.0.0.1:8082/api/agent-access/v1` |
| **Agent** | 平台助手 `019facd9-e088-7ce8-87fe-ebd8d04871d1` |
| **Workspace** | AI识别管理平台 |
| **Client** | `A2UI Demo E2E` (new, non-delete) |
| **Constraints** | No delete APIs used |

## Setup performed

1. Local backend/frontend already running (8082 / 5174).
2. Created AAP Client + Grant for 平台助手 (no deletes).
3. Configured `demos/aap-chat/.env` (gitignored secrets).
4. Fixed demo BFF `PROTOCOL_VERSION` → `2026-08-11` (matches server after A2UI schema bump).
5. Set `OUTBOUND_CONNECTION_ID` + demo token so REQUEST_PASSTHROUGH createRun is allowed.
6. Started demo with Node 22: `npm run dev` (BFF+UI).

## Chrome flow

1. Open demo Live AAP UI
2. Confirm BFF health / Live AAP / outbound bound
3. Send A2UI fence instruction
4. Wait for Run completed
5. Assert A2UI display-only card with surface JSON

## Results

| Check | Result |
|-------|--------|
| Demo loads Live AAP | PASS |
| BFF token + createConversation | PASS (after protocol version fix) |
| createRun with outboundCredentials | PASS |
| Model run completed | PASS (one earlier stream error from model API; retry succeeded) |
| **A2UI card rendered** | **PASS** — label `A2UI` + `display-only` + surface JSON with `components` |
| Text first-class shown | PASS — `Hello, pick a city:` |
| No delete operations | PASS |

## Screenshot

- `01-a2ui-card-success.png` — demo bubble with A2UI JSON card

## Code fixes needed for demo e2e

| Fix | Commit |
|-----|--------|
| Accept bare surface in fence body | `18155b5` (backend) |
| Demo protocol version 2026-08-11 | this change to `demos/aap-chat/server/index.mjs` |

## Verdict

**demos/aap-chat Live AAP E2E: PASS** for A2UI multiparty preview path.
