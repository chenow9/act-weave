# ActWeave Agent Access Protocol (AAP) — Third-Party Integration Guide

| | |
| --- | --- |
| **Audience** | External platforms integrating with ActWeave Agents |
| **Protocol base** | `/api/agent-access/v1` |
| **Machine contract** | [`openapi/agent-access-v1.yaml`](./openapi/agent-access-v1.yaml) |
| **TypeScript SDK** | `sdk/typescript` → `@actweave/agent-client` |
| **Language** | English (API identifiers, codes, and examples are authoritative) |
| **中文版** | [`aap-integration-guide.zh-CN.md`](./aap-integration-guide.zh-CN.md) |

This is the **handoff document** for third-party integrators (paired with the Chinese edition). Field-level schemas and enums live in OpenAPI and the Schema Registry; do not invent fields from prose alone.

Product overview and local run: root [`README.md`](../README.md) / [`README.zh-CN.md`](../README.zh-CN.md).

---

## 1. What AAP is

**Agent Access Protocol (AAP)** is the **external, stable API** for service principals to:

1. Authenticate as an **Agent Access Client**
2. Obtain a **short-lived Access Token** bound to one Workspace + one Agent
3. Create **Conversations** and **Runs**
4. Follow **Run events** over SSE (`Last-Event-ID` resume)
5. Decide **Interactions** (human / policy confirmations)

AAP is **not** the ActWeave management console API (`/api/v1`). Console user session JWTs are **rejected** on AAP routes.

| Surface | Base path | Auth | Who uses it |
| --- | --- | --- | --- |
| **AAP (this doc)** | `/api/agent-access/v1` | AAP Access Token | Your backend / BFF / mint service |
| Console / management | `/api/v1` | Platform user session | ActWeave UI & internal ops |

---

## 2. What the ActWeave admin must provide

Before you code, obtain from the Workspace admin:

| Item | Notes |
| --- | --- |
| **Base URL** | e.g. `https://actweave.example.com/api/agent-access/v1` |
| **Workspace ID** | UUID |
| **Agent ID** | UUID of the Agent you may call |
| **Client ID** | Agent Access Client identifier |
| **Client credential** | One-time **Client Secret** (shown once) **or** private key for `private_key_jwt` + registered JWKS |
| **Granted scopes** | Upper bound; your Token request must be a subset |
| **CORS policy** | Prefer **no browser-direct CORS** (BFF). If browser-direct is required: exact HTTPS origins only |

Store the Client Secret / private key in **your** secret manager. Secrets never appear in Protocol Events, audit detail payloads, or application logs.

---

## 3. Core concepts

| Concept | Meaning |
| --- | --- |
| **Agent Access Client** | OAuth-style client registered in a Workspace |
| **Grant** | Permission bound to Client + Agent(s) + scopes (+ optional policies) |
| **Access Token** | Short-lived JWT (`EdDSA` / `at+jwt`). Binds **one** Workspace, **one** Agent, Client, principal, optional External Subject |
| **Conversation** | Durable dialogue container for one Agent |
| **Run** | One execution under a Conversation (v1 input is **text only**) |
| **Protocol Event** | Durable fact on the Run stream (`sequence` is the cursor) |
| **Interaction** | Run paused for approve / decline / cancel |
| **External Subject** | End-user identity from your IdP via Token Exchange (optional) |

Effective permission for every call:

```text
Token scope ∩ Grant ∩ Agent policy ∩ Workspace status ∩ Subject ownership
```

---

## 4. Recommended integration topologies

| Topology | Who holds long-lived credentials | Recommendation |
| --- | --- | --- |
| **BFF (default)** | Your backend only | **Production default.** Browser talks only to your origin; BFF holds Client Secret, mints Tokens, proxies SSE / cancel if needed |
| **Server-to-server** | Your backend | Pure automation with `client_credentials` |
| **Short-lived mint + browser** | Mint service holds secrets; browser holds only a short Access Token **in memory** | Token Exchange for end users; never put secrets in storage / cookies / URL |

**Never** put Client Secrets, private keys, or Access Tokens in:

- URLs / query strings / fragments  
- Cookies  
- `localStorage` / `sessionStorage`  

---

## 5. Versioning

| Axis | Meaning |
| --- | --- |
| URL `/v1` | Resource / command major version |
| Header `ActWeave-Protocol-Version: YYYY-MM-DD` | Optional freeze snapshot (server echoes the actual version) |
| Event `specVersion=1.0` | Envelope major within v1 |

**Compatibility rule for clients:** ignore unknown event types and unknown fields, but **always advance** the sequence cursor when an `id:` is present.

Additive changes (optional fields, new event types) may ship in v1. Breaking changes require a new major path or a new event name.

---

## 6. Authentication

### 6.1 Token Endpoint

```http
POST /api/agent-access/v1/oauth/token
Content-Type: application/x-www-form-urlencoded
```

- Body size limit: **32 KiB**
- Successful responses: `Cache-Control: no-store`
- **No** `refresh_token` is ever issued
- Default Access Token TTL: **~10 minutes** (range **5–15 minutes**, also capped by Client config, server signing window, and Grant expiry)

#### Grant types

| `grant_type` | Purpose |
| --- | --- |
| `client_credentials` | Service Principal Token for one `agent_id` + `scope` |
| `urn:ietf:params:oauth:grant-type:token-exchange` | Bind an External Subject via `subject_token` JWT (RFC 8693) |

#### Client authentication (exactly one mode per request)

| Mode | How |
| --- | --- |
| `client_secret_basic` | HTTP Basic `client_id:client_secret` |
| `private_key_jwt` | `client_assertion_type=urn:ietf:params:oauth:client-assertion-type:jwt-bearer` + `client_assertion` (EdDSA or PS256 only) |

Do **not** mix Basic secret and assertion on one request. `client_secret_post` is **rejected**.

#### Example: client_credentials

```http
POST /api/agent-access/v1/oauth/token
Authorization: Basic base64(<client_id>:<client_secret>)
Content-Type: application/x-www-form-urlencoded

grant_type=client_credentials
&agent_id=<agent-uuid>
&scope=agent:read conversation:create conversation:read run:create run:read run:cancel event:read interaction:decide
```

Illustrative response:

```json
{
  "access_token": "<short-lived-jwt>",
  "token_type": "Bearer",
  "expires_in": 600,
  "scope": "agent:read conversation:create conversation:read run:create run:read run:cancel event:read interaction:decide"
}
```

#### Example: Token Exchange (end-user subject)

```http
POST /api/agent-access/v1/oauth/token
Authorization: Basic base64(<client_id>:<client_secret>)
Content-Type: application/x-www-form-urlencoded

grant_type=urn:ietf:params:oauth:grant-type:token-exchange
&agent_id=<agent-uuid>
&subject_token=<user-jwt>
&subject_token_type=urn:ietf:params:oauth:token-type:jwt
&requested_token_type=urn:ietf:params:oauth:token-type:access_token
&scope=...
```

Trusted Subject Issuer settings (exact Issuer / Audience, algorithm allowlist, JWKS) are configured **per Client** by the ActWeave admin. Your IdP must match that config.

### 6.2 Using the Access Token on the data plane

```http
Authorization: Bearer <access_token>
```

| Rule | Detail |
| --- | --- |
| Token profile | Asymmetric **EdDSA**, `typ=at+jwt`, fixed AAP audience |
| Public JWKS | `GET /api/agent-access/v1/.well-known/jwks.json` (public OKP fields only) |
| Management user JWT | **Not** accepted on AAP (401) |
| Token in query / cookie | **Forbidden** |
| Live revocation | Ordinary requests compare Token `ver` to current security version; revoke / disable invalidates old Tokens immediately. Active SSE re-checks within a bounded window (≤ 60s) |

On `TOKEN_EXPIRED` or revocation disconnect: mint a **new** Token and reconnect with the **same** `Last-Event-ID`.

---

## 7. Scopes

Grant scopes are the **upper bound**. Each Token request asks for a **subset**.

| Scope | Capability |
| --- | --- |
| `agent:read` | Read Agent profile |
| `conversation:create` | Create conversations |
| `conversation:read` | Read conversations |
| `run:create` | Create runs |
| `run:read` | Read run status |
| `run:cancel` | Cancel runs |
| `event:read` | SSE / event follow |
| `interaction:decide` | Approve / decline / cancel interactions |
| `artifact:read` | Artifact access (when authorized) |

Use least privilege: only request scopes your integration actually needs.

---

## 8. End-to-end quickstart

Replace placeholders: `{base}`, `{wid}`, `{aid}`, credentials.

### Step 1 — Issue Access Token

See [§6](#6-authentication).

### Step 2 — Create a Conversation

```http
POST {base}/workspaces/{wid}/agents/{aid}/conversations
Authorization: Bearer <access_token>
Idempotency-Key: <canonical-uuid>
Content-Type: application/json

{"title":"Support ticket 42"}
```

### Step 3 — Create a Run

```http
POST {base}/workspaces/{wid}/agents/{aid}/runs
Authorization: Bearer <access_token>
Idempotency-Key: <canonical-uuid>
Content-Type: application/json

{
  "conversationId": "<conversation-uuid>",
  "input": [{"type":"text","text":"Summarize the ticket"}]
}
```

v1 accepts **text input only**. Other content types → `UNSUPPORTED_CONTENT_TYPE`.

### Step 4 — Follow Run events (SSE)

```http
GET {base}/workspaces/{wid}/agents/{aid}/runs/{rid}/events
Authorization: Bearer <access_token>
Accept: text/event-stream
Last-Event-ID: 0
```

On disconnect, reconnect with the last applied sequence:

```http
Last-Event-ID: 17
```

### Step 5 — Decide an Interaction (when waiting)

```http
POST {base}/workspaces/{wid}/agents/{aid}/runs/{rid}/interactions/{iid}:decide
Authorization: Bearer <access_token>
Idempotency-Key: <canonical-uuid>
If-Match: "<interaction-version>"
Content-Type: application/json

{"decision":"approve"}
```

Allowed decisions: `approve` | `decline` | `cancel` (subject to Interaction + policy).

---

## 9. HTTP API index

Base URL: `/api/agent-access/v1`  
Auth: `Authorization: Bearer <access_token>` except Token Endpoint and JWKS.

### Common headers

| Header | Where | Purpose |
| --- | --- | --- |
| `Authorization: Bearer <access_token>` | Data plane | Short-lived AAP Token only |
| `Idempotency-Key: <uuid>` | POST Conversation / Run / Cancel / Decide | Canonical UUID; safe retries |
| `If-Match: "<version>"` | Decide / Cancel (as required) | Strong version / ETag |
| `If-None-Match` | GET Conversation / Run | Conditional read |
| `Last-Event-ID: <sequence>` | GET Run events | Resume cursor (integer ≥ 0) |
| `Accept: text/event-stream` | GET Run events | SSE |
| `ActWeave-Protocol-Version` | Optional | Snapshot date |

**Idempotency:** always send a **new** UUID for a distinct command. Same key + same body → original result. Same key + different body → **409** `IDEMPOTENCY_CONFLICT`.

### Discovery / Token

| Method | Path | Auth | Notes |
| --- | --- | --- | --- |
| `GET` | `/.well-known/jwks.json` | none | Public verification keys |
| `POST` | `/oauth/token` | Client auth | `client_credentials` or Token Exchange |

### Profile

| Method | Path | Scope |
| --- | --- | --- |
| `GET` | `/workspaces/{wid}/agents/{aid}/profile` | `agent:read` |

### Conversations

| Method | Path | Scope | Notes |
| --- | --- | --- | --- |
| `POST` | `/workspaces/{wid}/agents/{aid}/conversations` | `conversation:create` | `Idempotency-Key` required |
| `GET` | `/workspaces/{wid}/agents/{aid}/conversations/{cid}` | `conversation:read` | ETag / `If-None-Match` |

### Runs

| Method | Path | Scope | Notes |
| --- | --- | --- | --- |
| `POST` | `/workspaces/{wid}/agents/{aid}/runs` | `run:create` | Text input only; may return 202 |
| `GET` | `/workspaces/{wid}/agents/{aid}/runs/{rid}` | `run:read` | Status / item snapshots |
| `POST` | `/workspaces/{wid}/agents/{aid}/runs/{rid}:cancel` | `run:cancel` | Idempotent cancel + `If-Match` |
| `GET` | `/workspaces/{wid}/agents/{aid}/runs/{rid}/events` | `event:read` | SSE follow |

### Interactions

| Method | Path | Scope | Notes |
| --- | --- | --- | --- |
| `POST` | `.../runs/{rid}/interactions/{iid}:decide` | `interaction:decide` | Body + `If-Match` + `Idempotency-Key` |

Authoritative request/response schemas: [`openapi/agent-access-v1.yaml`](./openapi/agent-access-v1.yaml).

---

## 10. SSE event stream

### 10.1 Frame kinds

#### Protocol Event (persisted — advances cursor)

```text
id: 12
event: item.delta
data: {"specVersion":"1.0","type":"item.delta","eventId":"...","streamId":"run:...","sequence":12,...}
```

- SSE `id:` == Run-scoped durable **sequence**
- Persist only the last **successfully applied** sequence

#### Heartbeat (not persisted — do not move cursor)

```text
: ping 2026-07-21T12:00:00Z
```

Sent about every **15 seconds**. Comment line only; **no** `id:`.

#### Transport signal (not persisted — do not move cursor)

```text
event: stream.error
data: {"specVersion":"1.0","type":"stream.error","error":{"code":"TOKEN_EXPIRED","retryable":true,...}}
```

No `id:` line.

### 10.2 Catch-up → live follow

1. Server reads high watermark after `Last-Event-ID`
2. Pages historical events with `sequence > cursor`
3. Switches to live follow + heartbeats

### 10.3 Client rules

1. Ignore unknown event types / fields; still advance sequence when `id:` is present  
2. On `TOKEN_EXPIRED` (`retryable=true`): new Token, **same** cursor, reconnect  
3. Invalid cursor → HTTP **422** `REPLAY_CURSOR_INVALID` (prefer before SSE headers)  
4. Terminal Run statuses typically include: `completed`, `failed`, `cancelled`

### 10.4 Protocol event families (v1)

Clients must treat the Schema Registry / OpenAPI catalog as source of truth. Core families:

- `run.accepted` / `run.started` / `run.waiting` / `run.resumed` / `run.completed` / `run.failed` / `run.cancelled`
- `item.started` / `item.delta` / `item.completed`
- `interaction.requested` / `interaction.resolved`
- `usage.updated` (when present)

### 10.5 Reverse proxy requirements (SSE)

Your edge / BFF proxy for `text/event-stream` must:

| Requirement | Detail |
| --- | --- |
| No response buffering | e.g. Nginx `X-Accel-Buffering: no` |
| Preserve headers | `Cache-Control: no-cache, no-transform` |
| No gzip on the stream | Dynamic compression off for SSE |
| Idle / read timeout | **≥ 60s** (75s recommended) |

---

## 11. Interactions (approvals)

When a Run needs confirmation:

1. Stream emits `interaction.requested` and `run.waiting`
2. Client presents Interaction UI (title, risk, allowed decisions, version)
3. Client POSTs `:decide` with `If-Match` and `Idempotency-Key`
4. Stream continues (`interaction.resolved`, `run.resumed`, …)

| Decision | Meaning |
| --- | --- |
| `approve` | Continue |
| `decline` | Reject the pending step |
| `cancel` | Cancel per policy / Interaction rules |

**Risk / subject policy (summary):**

- Pure Service Principal may only decide **LOW / MEDIUM** risk when Grant policy enables service decision  
- **HIGH / CRITICAL** typically requires the **same External Subject** (Token Exchange) or ActWeave user path  
- Resume tokens never appear in Protocol Events or public DTOs  

---

## 12. Errors

### 12.1 Data-plane envelope

```json
{
  "error": {
    "code": "REPLAY_CURSOR_INVALID",
    "message": "Human-readable summary without secrets.",
    "retryable": false,
    "requestId": "...",
    "traceId": "..."
  }
}
```

### 12.2 Stable codes (selected)

| Code | HTTP / channel | Retry? | Client action |
| --- | --- | --- | --- |
| `TOKEN_EXPIRED` | 401 / SSE | yes | Refresh Token; same `Last-Event-ID` |
| `UNAUTHENTICATED` | 401 | no* | Fix credentials (*or refresh if near expiry) |
| `AUTHORIZATION_DENIED` | 403 / 404 | no | Check Grant / scopes / ownership (not-visible often 404) |
| `AUTHORIZATION_REVOKED` | SSE disconnect | yes | New Token + same cursor |
| `REPLAY_CURSOR_INVALID` | 422 | no | Reset from known-good sequence or `0` |
| `IDEMPOTENCY_CONFLICT` | 409 | no | New key or identical body |
| `RATE_LIMITED` | 429 | yes | Honor `Retry-After` / `RateLimit-*` |
| `UNSUPPORTED_CONTENT_TYPE` | 400 | no | Use text-only input |
| `SLOW_CONSUMER` | SSE disconnect | yes | Reconnect with last `id` |

OAuth Token Endpoint errors use RFC 6749-style `error` / `error_description` and **must not** echo secrets.

---

## 13. Rate limits and quotas

Limits are multi-dimensional (Workspace / Client / Agent / Subject / IP depending on the operation). Responses may include:

- `Retry-After`
- `RateLimit-Limit` / `RateLimit-Remaining` / `RateLimit-Reset` (when configured)

Indicative defaults (per process instance; confirm with your operator for production):

- Token Endpoint: Client × IP × grant dimensions  
- SSE connections: per Client / Subject / Run (order of ~16 / 8 / 4)

---

## 14. TypeScript SDK (`@actweave/agent-client`)

Package path in this repository: `sdk/typescript`.

```ts
import {
  AgentAccessClient,
  MemoryTokenProvider,
} from "@actweave/agent-client";

const tokens = new MemoryTokenProvider({
  // Your BFF/mint returns { accessToken, expiresAt }
  refresh: async () => mintShortLivedTokenFromYourBackend(),
});

const client = new AgentAccessClient({
  baseUrl: "https://actweave.example.com/api/agent-access/v1",
  tokenProvider: tokens,
});

const { conversation } = await client.createConversation(workspaceId, agentId, {
  title: "Ticket 42",
});

const run = await client.createRun(workspaceId, agentId, {
  conversationId: conversation.id,
  input: [{ type: "text", text: "Hello" }],
});

for await (const { message, snapshot } of client.followRun(
  workspaceId,
  agentId,
  run.run.id,
)) {
  if (snapshot.run?.status === "waiting_interaction") {
    // Present Interaction UI; call decideInteraction
  }
  if (
    snapshot.run &&
    ["completed", "failed", "cancelled"].includes(String(snapshot.run.status))
  ) {
    break;
  }
}
```

SDK guarantees:

- Access Token only in `Authorization` (never query)
- Auto-reconnect with `Last-Event-ID` on gaps / retryable disconnects
- Force-refresh on `TOKEN_EXPIRED` / HTTP 401, then resume the same cursor

Also exported: `StaticTokenProvider`, `RunReducer`, `AAPSESession`. See `sdk/typescript/README.md`.

---

## 15. CORS

| Mode | Behavior |
| --- | --- |
| **BFF (recommended)** | AAP CORS disabled; browser never calls AAP origin directly |
| **Exact CORS** | Client `AllowedCORSOrigins` = exact HTTPS origins only (no `*`, no wildcards) |

Unauthorized `Origin` is not reflected. Prefer moving secrets server-side and disabling browser-direct access.

---

## 16. Credential rotation (integrator side)

1. Admin creates a **new** Client credential (Secret or JWKS update)  
2. Deploy the new credential to your secret manager  
3. Switch all Token Endpoint callers  
4. Revoke the old credential after rollout  
5. Security version bumps may force SSE re-auth within ≤ 60s — recover with new Token + same `Last-Event-ID`  

---

## 17. Production checklist

- [ ] BFF or mint server owns Client credentials  
- [ ] Only short Access Tokens reach browsers (memory only)  
- [ ] All mutating commands send `Idempotency-Key`  
- [ ] SSE resume uses `Last-Event-ID` only (never put Token in query)  
- [ ] Proxy timeouts ≥ 75s; buffering off for `text/event-stream`  
- [ ] Exact CORS **or** CORS disabled  
- [ ] Least-privilege scopes on Grant and Token request  
- [ ] Interaction decide path tested for your risk levels  
- [ ] Error handling maps stable codes (`TOKEN_EXPIRED`, `REPLAY_CURSOR_INVALID`, …)  
- [ ] Token Exchange subject issuer config verified (if you bind end users)  
- [ ] OpenAPI / SDK versions match the ActWeave deployment  

---

## 18. Related artifacts in this repository

| Artifact | Path | Role |
| --- | --- | --- |
| **This guide (EN)** | `docs/aap-integration-guide.md` | Human integration contract |
| **对接指南（中文）** | `docs/aap-integration-guide.zh-CN.md` | Same content in Chinese |
| OpenAPI | `docs/openapi/agent-access-v1.yaml` | Machine-readable HTTP contract |
| TypeScript SDK | `sdk/typescript/` | Client library |
| Product README (EN) | `README.md` | Product overview & local run |
| Product README (ZH) | `README.zh-CN.md` | 产品说明与本地运行 |

Internal ActWeave console event paths (`/api/v1/...`) are **out of scope** for third-party integrators. Use AAP only.
