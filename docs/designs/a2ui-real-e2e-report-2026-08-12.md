# ActWeave A2UI Real End-to-End Report

| Field | Value |
|-------|-------|
| **Date** | 2026-08-12 |
| **Branch** | `feat/a2ui-additive-capability` |
| **Worktree** | `/Users/chen/Documents/act-weave-chat` |
| **Browser** | Chrome DevTools MCP (live browser) |
| **Frontend** | Vite `http://127.0.0.1:5174` (direct, not Docker) |
| **Backend** | Go server `http://127.0.0.1:8082` (direct, not Docker) |
| **Data plane** | Existing Docker: Postgres `:15432`, Redis `:16379`, MinIO `:9000` |
| **Agent** | `平台助手` (`019facd9-e088-7ce8-87fe-ebd8d04871d1`) |
| **Workspace** | `AI识别管理平台` (`019facc3-3c6a-707d-9225-a3f2dc1153fe`) |
| **Model** | Verified `sub2api` / GPT-5.4 (`019facc7-6a3a-762a-be50-d0620ee2a2b6`) |
| **Constraints** | **No delete operations** used during testing |

## Environment

- Docker only for infra already running (`actweave-postgres`, `actweave-redis`, `actweave-minio`).
- Frontend/backend started with local process (`npm run dev`, `go run ./cmd/server`).
- Credentials from `backend/config.yaml` bootstrap admin (`admin` / `actweave-admin-dev-change-me`).
- Existing production-like demo data (agents, model configs) reused.

## Scenario steps (Chrome MCP)

1. Open `http://127.0.0.1:5174/login`
2. Login as admin
3. Navigate to Agents (workspace **AI识别管理平台**)
4. Edit **平台助手** → Advanced options → enable **Enable A2UI (additive)** → Save
5. Open Run Console → New session (same agent) → send message requesting fenced A2UI form
6. Wait for run **Completed**
7. Verify DB / protocol (no deletes)

## Results

### PASS — Stack health

| Check | Result |
|-------|--------|
| Backend health `GET /api/v1/health` | 200 `{"status":"ok"}` |
| Frontend `GET /` | 200 |
| Login via Chrome | Success → `/overview` then Agents/Chat |

### PASS — Agent enableA2UI (Console UI → API → DB)

| Check | Result |
|-------|--------|
| Switch visible default off | Yes |
| Toggle + Save agent | Toast: "平台助手 saved..." |
| DB `agents.context_policy.aap.enableA2UI` | **true** (sibling `includeCompactionSummary` preserved true) |
| schemaVersion | `session-context-policy.v2` |

Evidence: screenshot `02-agent-saved-a2ui-on.png`, DB query after PATCH.

### PASS — Live chat run with real model

| Check | Result |
|-------|--------|
| New session created | POST chat/sessions 201 |
| Message accepted | POST messages 202 |
| SSE events stream | GET agent-runs/.../events 200 (~24s) |
| Run status | **Completed** |
| Run snapshot freezes A2UI | `context_policy_snapshot.aap.enableA2UI: true` |

### PASS — Multiparty text + a2ui (after extract fix)

**First live run (before fix):** model streamed full fence body, but extract expected envelope `{surface:{...}}`. Bare surface `{"components":...}` was treated as `invalid_json` → durable text only after strip (`Hello, pick a city:`).  
Run id: `019ff3eb-2862-71d1-97ae-70d204a0e9c8`.

**Fix applied:** `backend/internal/a2ui/split.go` accepts bare surface object when `surface` key missing.

**Second live run (after fix, no deletes):**

| Field | Value |
|-------|-------|
| Run id | `019ff3f0-bfdd-7b38-9c53-f90bf5812478` |
| Stream reconstructed | Contains `<<<A2UI>>>...<<<END_A2UI>>>` |
| Durable `chat_messages.content` | `aap.message-content.v1` multiparty JSON (`is_v1=t`, `has_a2ui=t`) |
| `run_items` completed content | `[{type:text,text:"Hello, pick a city:"},{type:a2ui,version:a2ui-surface.v0,surface:{components:[...]}}]` |
| `item.completed` protocol payload | Same multiparty parts |
| Console UI | Shows **text only** "Hello, pick a city:" (by design: text-first projection; no catalog renderer in Console MVP) |

### PASS — Safety constraints during e2e

- Did **not** use Delete agent / delete session / delete workspace / hard purge.
- Only create session + send message + update agent policy.

## Screenshots

| File | Description |
|------|-------------|
| `01-a2ui-enabled-before-save.png` | Studio with A2UI switch on |
| `02-agent-saved-a2ui-on.png` | After save |
| `03-chat-completed.png` | First completed chat (text after strip) |
| `04-chat-multiparty-text-projection.png` | Second chat after fix (UI text projection) |

## Artifacts

- `env.json` — ids and endpoints
- `run-context-policy-snapshot.json` — frozen snapshot with enableA2UI
- `protocol-key-events.jsonl` — item.started/completed samples
- This report: `REAL_E2E_REPORT.md`

## Verdict

| Layer | Status |
|-------|--------|
| Real stack (local FE/BE + Docker data) | **PASS** |
| Chrome live UI toggle enableA2UI | **PASS** |
| Real model chat + SSE | **PASS** |
| Multiparty durable a2ui attach | **PASS** (after bare-surface extract fix found by live e2e) |
| Console A2UI visual renderer | **N/A** (MVP non-goal; text projection only) |
| Component actions | **N/A** (MVP `actions:false`) |

**Overall: REAL E2E PASS** for the shipped MVP path (text first-class + optional a2ui part on completed). Live testing uncovered and fixed bare-surface fence parsing.

## Follow-ups (optional)

1. Optionally enhance prompt appendix to show both envelope and bare-surface examples (now both work).
2. Console optional JSON preview for a2ui part (still non-goal for catalog render).
3. Keep **no-delete** as a standing e2e safety rule when using shared demo data.
