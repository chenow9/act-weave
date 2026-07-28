# Agent Access Protocol — Developer Guide

- Audience: third-party platforms integrating with ActWeave Agents
- Protocol base path: `/api/agent-access/v1`
- OpenAPI: [`../openapi/agent-access-v1.yaml`](../openapi/agent-access-v1.yaml)
- TypeScript SDK: `sdk/typescript` (`@actweave/agent-client`)

## Quickstart

### 1. Obtain a Client

An ActWeave Workspace admin registers an **Agent Access Client** and grants it access to one or more Agents:

| Field | Notes |
| --- | --- |
| Auth method | Production: prefer `private_key_jwt`; low-friction: `client_secret_basic` |
| First credential | One-time Client Secret **or** JWKS URI / thumbprint |
| Grant scopes | See [Scopes](#scopes) |
| CORS origins | Exact HTTPS origins only (or disable CORS and use a BFF) |

The management UI shows the Client Secret **once**. Store it in your secret manager; it never appears in Protocol Events, Audit detail, or logs.

### 2. Choose an integration topology

| Topology | Who holds credentials | Recommendation |
| --- | --- | --- |
| **BFF (default)** | Your backend only | Production default: BFF holds Client Secret, proxies SSE / cancel to AAP |
| **Short-lived mint + browser** | Browser holds only a short Access Token (memory) | Mint service does Token Exchange; browser must never store secrets in storage/cookies/URL |
| **Server-to-server** | Your backend | `client_credentials` for pure Service Principal work |

**Never** put Client Secrets, JWTs, or Access Tokens in URLs, query strings, cookies, or `localStorage`.

### 3. Issue a short Access Token

```http
POST /api/agent-access/v1/oauth/token
Content-Type: application/x-www-form-urlencoded

grant_type=client_credentials
&agent_id=<agent-uuid>
&scope=agent:read conversation:create conversation:read run:create run:read run:cancel event:read interaction:decide
```

Authenticate with **either**:

- `Authorization: Basic base64(client_id:client_secret)` (`client_secret_basic`), **or**
- `client_assertion_type=urn:ietf:params:oauth:client-assertion-type:jwt-bearer` + `client_assertion=...` (`private_key_jwt`)

Response (illustrative — values are short-lived; no refresh_token):

```json
{
  "access_token": "<short-lived-jwt>",
  "token_type": "Bearer",
  "expires_in": 600,
  "scope": "agent:read run:create event:read ..."
}
```

Default TTL is **10 minutes** (max **15 minutes**). Use the TypeScript SDK `MemoryTokenProvider` to refresh before expiry.

### 4. Create a Conversation and Run (with Idempotency-Key)

```http
POST /api/agent-access/v1/workspaces/{wid}/agents/{aid}/conversations
Authorization: Bearer <access_token>
Idempotency-Key: <canonical-uuid>
Content-Type: application/json

{"title":"Support ticket 42"}
```

```http
POST /api/agent-access/v1/workspaces/{wid}/agents/{aid}/runs
Authorization: Bearer <access_token>
Idempotency-Key: <canonical-uuid>
Content-Type: application/json

{
  "conversationId": "<conversation-uuid>",
  "input": [{"type":"text","text":"Summarize the ticket"}]
}
```

Always send a new canonical UUID `Idempotency-Key` per distinct command. Replays with the same key + body return the original result; different body → **409**.

### 5. Follow Run events (SSE + Last-Event-ID)

```http
GET /api/agent-access/v1/workspaces/{wid}/agents/{aid}/runs/{rid}/events
Authorization: Bearer <access_token>
Accept: text/event-stream
Last-Event-ID: 0
```

- `id:` is the Run-scoped protocol **sequence** (durable cursor).
- Heartbeats are comment lines (`: ping ...`) — **no** `id:`, do not advance the cursor.
- Transport `stream.error` (e.g. `TOKEN_EXPIRED`) also has **no** `id:`.

On disconnect, reconnect with the last successfully applied sequence:

```http
Last-Event-ID: 17
```

### 6. TypeScript SDK (recommended)

```ts
import {
  AgentAccessClient,
  MemoryTokenProvider,
} from "@actweave/agent-client";

const tokens = new MemoryTokenProvider({
  // Server-side refresh that returns { accessToken, expiresAt }
  refresh: async () => mintShortLivedTokenFromYourBackend(),
});

const client = new AgentAccessClient({
  baseUrl: "https://actweave.example/api/agent-access/v1",
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
  workspaceId, agentId, run.run.id,
)) {
  // message: SSE frame / protocol envelope
  // snapshot: RunReducer public view (ignore unknown fields/types)
  if (snapshot.run?.status === "waiting_interaction") {
    // present Interaction UI; call decideInteraction
  }
  if (snapshot.run && ["completed", "failed", "cancelled"].includes(snapshot.run.status)) {
    break;
  }
}
```

SDK guarantees:

- Access Token only in `Authorization` header (never query).
- Auto-reconnect with `Last-Event-ID` on sequence gaps / retryable disconnects.
- Force-refresh on `TOKEN_EXPIRED` / HTTP 401, then resume the same cursor.

---

## Authentication

### Client authentication (Token Endpoint only)

| Method | How | Notes |
| --- | --- | --- |
| `client_secret_basic` | HTTP Basic with Client ID + Secret | Secret shown once at creation/rotation |
| `private_key_jwt` | Signed JWT assertion (EdDSA/PS256) | Preferred in production; JWKS URI must be HTTPS with SSRF guards |

Mutual exclusion: do not mix Basic secret with assertion on one request. `client_secret_post` is rejected.

### Access Token

- Profile: asymmetric **EdDSA** (`at+jwt`), audience fixed to AAP data plane.
- Claims bind **one Workspace + one Agent + Client + Principal (+ optional External Subject)**.
- Management user JWTs are **not** accepted on AAP routes (401).

### Token Exchange (RFC 8693)

For end-user subject binding (business platform already authenticated the user):

```http
POST /api/agent-access/v1/oauth/token
Content-Type: application/x-www-form-urlencoded

grant_type=urn:ietf:params:oauth:grant-type:token-exchange
&agent_id=<agent-uuid>
&subject_token=<user-jwt>
&subject_token_type=urn:ietf:params:oauth:token-type:jwt
&requested_token_type=urn:ietf:params:oauth:token-type:access_token
&scope=...
```

Plus Client auth (`client_secret_basic` or `private_key_jwt`).

Trusted Subject Issuer config (exact Issuer/Audience, algorithm allowlist, inline JWKS or fixed JWKS URI) is set per Client by admins. Subject tokens are verified with SSRF-safe JWKS fetch.

---

## Scopes

Grant scopes are the upper bound; each Access Token requests a subset.

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

Effective permission = **Token scope ∩ Grant ∩ Agent policy ∩ Workspace status ∩ Subject ownership**.

---

## SSE, reconnect, and recovery

### Catch-up → Follow

1. Server reads high watermark after `Last-Event-ID`.
2. Pages historical events (`sequence > cursor`, page size 100 by default).
3. Subscribes for wake-ups; secondary high-watermark read closes race windows.
4. Live follow + 15s heartbeat comments.

### Rules for clients

1. Persist only the last applied **sequence** (SSE `id:`).
2. Ignore unknown event types / fields but still advance the cursor.
3. On `stream.error` with `code=TOKEN_EXPIRED` and `retryable=true`: mint a new short Token; **do not** change the cursor; reconnect with the same `Last-Event-ID`.
4. Invalid cursor → HTTP **422** `REPLAY_CURSOR_INVALID` (before SSE headers when possible).

### CORS and BFF

| Mode | Behavior |
| --- | --- |
| BFF (recommended) | CORS **disabled** on AAP; browser talks only to your origin |
| Exact CORS | Client `AllowedCORSOrigins` = exact HTTPS origins (no `*`, no wildcards) |

Preflight allows only required methods/headers; unauthorized Origin is not reflected.

---

## Approvals (Interactions)

When a Run waits for confirmation:

1. Protocol emits `interaction.requested` + `run.waiting`.
2. Client reads Interaction item (title, risk, allowed decisions, version).
3. Submit decision:

```http
POST /api/agent-access/v1/workspaces/{wid}/agents/{aid}/runs/{rid}/interactions/{iid}:decide
Authorization: Bearer <access_token>
Idempotency-Key: <canonical-uuid>
If-Match: "<interaction-version>"
Content-Type: application/json

{"decision":"approve"}
```

Allowed decisions: `approve` | `decline` | `cancel` (subject to Interaction + policy).

**Pure Service Principal** may only decide LOW/MEDIUM risk when Grant Policy enables `serviceDecision`. HIGH/CRITICAL requires the same External Subject or ActWeave User.

Resume tokens never appear in Protocol Events or public DTOs.

---

## Error codes (stable)

JSON error bodies use stable `code` values (illustrative, not exhaustive):

| Code | Typical HTTP | Meaning |
| --- | --- | --- |
| `TOKEN_EXPIRED` | 401 / SSE signal | Access Token expired; refresh and resume |
| `UNAUTHENTICATED` | 401 | Missing/invalid Authorization |
| `AUTHORIZATION_DENIED` | 403/404* | Scope/grant/ownership failed (*not-visible often as 404) |
| `REPLAY_CURSOR_INVALID` | 422 | Bad `Last-Event-ID` |
| `IDEMPOTENCY_CONFLICT` | 409 | Same key, different body |
| `RATE_LIMITED` | 429 | Multi-dim quota; honor `Retry-After` / `RateLimit-*` |
| `UNSUPPORTED_CONTENT_TYPE` | 400 | Non-text content in v1 |
| `SEQUENCE_CONFLICT` | 5xx path | Internal sequence CAS (ops alert) |

OAuth Token Endpoint uses OAuth-style error objects without leaking Client secrets or IP details.

SSE transport signals:

```text
event: stream.error
data: {"specVersion":"1.0","type":"stream.error","error":{"code":"TOKEN_EXPIRED","message":"...","retryable":true}}
```

No `id:` line — the cursor must not move.

---

## Credential rotation (integrator)

1. Admin creates a new Client Credential (Secret or JWKS update).
2. Deploy the new credential to your secret manager.
3. Switch Token Endpoint callers to the new credential.
4. Revoke the old credential after all instances are upgraded.
5. Security Version increments may force SSE re-auth within ≤60s — clients already recover via new Token + `Last-Event-ID`.

See also: [`../runbooks/aap-signing-key-rotation.md`](../runbooks/aap-signing-key-rotation.md) (platform signing keys).

---

## Checklist before production

- [ ] BFF or mint server owns Client credentials
- [ ] Only short Access Tokens reach browsers (memory only)
- [ ] All mutating calls use `Idempotency-Key`
- [ ] SSE reconnect uses `Last-Event-ID` (never token query)
- [ ] Proxy timeouts ≥ 75s; buffering off for `text/event-stream`
- [ ] Exact CORS or CORS disabled
- [ ] Scopes least-privilege on Grant and Token request
- [ ] Interaction decide path tested for your risk levels
