# ActWeave Agent Access Protocol (AAP) — Third-Party Integration Guide

[中文](./aap-integration-guide.zh-CN.md) · [Documentation home](./README.md) · [AAP integration index](./integrations/aap.md)

| | |
| --- | --- |
| **Audience** | External platforms integrating with ActWeave Agents |
| **Protocol base** | `/api/agent-access/v1` |
| **Machine contract** | [`openapi/agent-access-v1.yaml`](./openapi/agent-access-v1.yaml) |
| **TypeScript SDK** | `sdk/typescript` → `@actweave/agent-client` |
| **Language** | English (API identifiers, codes, and examples are authoritative) |
| **中文版** | [`aap-integration-guide.zh-CN.md`](./aap-integration-guide.zh-CN.md) |

This is the detailed **handoff document** for third-party integrators (paired with the Chinese edition). Field-level schemas and enums live in OpenAPI and the Schema Registry; do not invent fields from prose alone.

Product overview and local run: root [`README.md`](../README.md) / [`README.zh-CN.md`](../README.zh-CN.md).

---

## 1. What AAP is

**Agent Access Protocol (AAP)** is the versioned external runtime API for service principals to:

1. Authenticate as an **Agent Access Client**
2. Obtain a **short-lived Access Token** bound to one Workspace + one Agent
3. Create **Conversations** and **Runs**
4. Follow **Run events** over SSE (`Last-Event-ID` resume)
5. Decide **Interactions** (human / policy confirmations)
6. (Optional, **off by default**) Upload **Files**, wait until ready, and reference them from Run input as `input_file` parts
7. (Optional, **off by default**) Receive additive **A2UI** surfaces on assistant messages when the Agent has `enableA2UI` (display-only in MVP)
8. (Optional, **off by default**) Receive assistant **outbound attachments** (`output_file`) when the files HTTP gate (including workspace/client allowlist), `runtimeOutboundAttachments`, and Agent `enableOutboundAttachments` are on, and the frozen `toolCalling` supports tools. v1 publish is text-only (`actweave.publish_attachment`); there is no public ingest HTTP

AAP is **not** the ActWeave management console API (`/api/v1`). Console user session JWTs are **rejected** on AAP routes. Console chat can render files referenced on a session message through a session+message proxy (see [§9.3](#93-outbound-attachments-optional-additive)); that is **not** a file-management UI and **not** an AAP route.

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

A Workspace admin can export the public fields in the table from **Agent Access → Client detail → Export integration config** (`.env` or JSON). The **Client Secret is never exported**—it is shown once at creation or credential rotation and must be handed to the integrator’s secret store separately.

Store the Client Secret / private key in **your** secret manager. Secrets never appear in Protocol Events, audit detail payloads, or application logs.

---

## 3. Core concepts

| Concept | Meaning |
| --- | --- |
| **Agent Access Client** | OAuth-style client registered in a Workspace |
| **Grant** | Permission bound to Client + Agent(s) + scopes (+ optional policies) |
| **Access Token** | Short-lived JWT (`EdDSA` / `at+jwt`). Binds **one** Workspace, **one** Agent, Client, principal, optional External Subject |
| **Conversation** | Durable dialogue container for one Agent |
| **Run** | One execution under a Conversation. Default input is **text**; when AAP files is enabled, user messages may also include `input_file` parts that reference a stable `fileId` |
| **File** | Optional blob with lifecycle status. Inbound upload: `pending_upload` → … → `ready`, referenced as `input_file`. Agent-generated outbound files are written READY with `purpose=AGENT_OUTPUT` and referenced as `output_file`. GET status is the source of truth (no File SSE in v1) |
| **A2UI** | Optional declarative UI surface on assistant messages (`type: "a2ui"` content part). Agent-level `enableA2UI`; default off. Text remains first-class |
| **Outbound attachment** | Optional assistant `output_file` content part (stable `fileId` + display metadata, never a URL). Default off; three gates + `toolCalling` — see [§9.3](#93-outbound-attachments-optional-additive) |
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
| `file:write` | Create upload intent + complete (when files feature is enabled) |
| `file:read` | Read file status / content / mint download (when files feature is enabled) |

Use least privilege: only request scopes your integration actually needs. File scopes are useless until the operator enables `agentAccess.files` for your workspace/client.

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

Default deployments accept **text** message content. When AAP files is enabled and the file is `ready`, you may also attach `{ "type": "input_file", "fileId": "<uuid>" }` parts (see [§9.1](#91-files-optional)). `createRun` **rejects** `output_file` (`UNSUPPORTED_CONTENT_TYPE`); assistant `output_file` parts arrive on `item.completed` only when outbound attachments are enabled (see [§9.3](#93-outbound-attachments-optional-additive)). Unknown content types → `UNSUPPORTED_CONTENT_TYPE`. Multimodal model assembly additionally requires **`RuntimeMultimodal`** (operator flag; default off).

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

### Files (optional; default disabled)

| Method | Path | Scope | Notes |
| --- | --- | --- | --- |
| `POST` | `/workspaces/{wid}/agents/{aid}/files` | `file:write` | Create upload intent + write-only `upload` (presigned PUT). `Idempotency-Key` required |
| `POST` | `/workspaces/{wid}/agents/{aid}/files/{fid}:complete` | `file:write` | Confirm staging; async promote. `Idempotency-Key` required |
| `GET` | `/workspaces/{wid}/agents/{aid}/files/{fid}` | `file:read` | Status SoT (no File SSE in v1) |
| `GET` | `/workspaces/{wid}/agents/{aid}/files/{fid}/content` | `file:read` | Bearer stream (path A); prefer `:download` for large bodies |
| `POST` | `/workspaces/{wid}/agents/{aid}/files/{fid}:download` | `file:read` | Mint opaque download token (path B) |
| `GET` | `/files/downloads/{tokenId}` | none (token) | Stream via opaque token; **no** AAP Bearer |

**v1 does not provide** `GET .../files` (list), `DELETE .../files/{id}`, or a public ingest endpoint. There is no Console file-management UI; Console chat preview uses a session+message proxy (see [§9.3](#93-outbound-attachments-optional-additive)).

Authoritative request/response schemas: [`openapi/agent-access-v1.yaml`](./openapi/agent-access-v1.yaml).

### 9.1 Files (optional)

Feature gate: `agentAccess.files.enabled` defaults to **false**. When disabled, file routes conceal as not visible (**404**). End-to-end multimodal model input additionally requires **`RuntimeMultimodal=true`**; with files enabled but runtime multimodal off, `createRun` with `input_file` fails closed with **422 `FILE_RUNTIME_UNAVAILABLE`** (no Run is created).

Assistant outbound attachments are a **separate** runtime flag (`runtimeOutboundAttachments`) plus Agent policy — see [§9.3](#93-outbound-attachments-optional-additive). GET File responses may show `purpose=AGENT_OUTPUT` for those rows; `createFile` does **not** accept that purpose.

#### Upload flow

1. **`POST .../files`** with `mediaType`, `sizeBytes` (and optional `filename`, `sha256`, `purpose`) + `Idempotency-Key`.
2. Response `201` includes `file` + **write-only** `upload: { method: "PUT", url, headers, expiresAt }`.  
   - `headers` **must** be sent on the PUT exactly as returned. At minimum the signature binds **`Content-Length`** and **`Content-Type`**.  
   - Subsequent **GET never echoes** `upload`, presign headers, or live download URLs.
3. **Client PUT** the raw bytes to `upload.url` (typically object storage). Do **not** attach the AAP Bearer token to this PUT.
4. **`POST .../files/{fileId}:complete`** (optional body `{ "sha256": "..." }`). Fast path: stats staging, validates size/MIME, CAS → `uploaded`, enqueues promote. Does **not** wait for encryption.
5. **Poll `GET .../files/{fileId}`** until `status=ready` (SDK: `waitUntilReady`). Terminal failures: `failed` / `expired`.
6. **`POST .../runs`** with content parts:

```json
{
  "conversationId": "<uuid>",
  "stream": false,
  "input": [{
    "type": "message",
    "role": "user",
    "content": [
      { "type": "text", "text": "Summarize this invoice" },
      { "type": "input_file", "fileId": "<file-uuid>" }
    ]
  }]
}
```

Wire protocol and persisted items carry only stable **`fileId`** — never live download/presign URLs or blob plaintext.

#### Defaults (confirm with operator)

| Limit | Default |
| --- | --- |
| maxBytes | 25 MiB |
| Allowed MIME | `image/png`, `image/jpeg`, `image/webp`, `image/gif`, `application/pdf`, Word (`.doc` / `.docx`), Excel (`.xls` / `.xlsx`), `application/zip` |
| Staging TTL | ~15 minutes |
| Retention | EXPIRING ~30 days (first successful createRun reference may promote retention) |
| Download token TTL | client_content / tool_invoke ≤ 5m; processor_delivery ≤ 10m (hard cap 15m) |

#### Content download

| Path | How | When |
| --- | --- | --- |
| **A** | `GET .../files/{fileId}/content` + Bearer + `file:read` | Small bodies; simple clients |
| **B** | `POST .../files/{fileId}:download` → relative `url` → `GET /files/downloads/{tokenId}` (**no** Bearer) | **Preferred when `sizeBytes > 4 MiB`**; tools/processors always use opaque tokens |

Token ids are opaque DB rows, **not** JWTs and **not** MinIO credentials. Reverse proxies streaming file bodies should disable response buffering and allow read timeouts ≥ 120s for up to maxBytes. Content streams set `X-Content-Type-Options: nosniff`; non-`image/*` responses also set `Content-Disposition: attachment`.

#### Processor webhook contract (partner / DLP / custom stages)

Workspace-configured HTTPS processors receive an async POST after promote (HMAC, not AAP Bearer):

```http
POST https://partner.example/hooks/aap-file
Content-Type: application/json
X-ActWeave-Signature: t=<unix>,v1=<hmac_sha256_hex>
```

Delivery body (illustrative):

```json
{
  "specVersion": "file-processor.v1",
  "eventType": "file.uploaded",
  "deliveryId": "<uuid>",
  "workspaceId": "<uuid>",
  "agentId": "<uuid>",
  "fileId": "<uuid>",
  "mediaType": "application/pdf",
  "sizeBytes": 12345,
  "sha256": "...",
  "download": {
    "url": "https://aap-host/api/agent-access/v1/files/downloads/<tokenId>",
    "expiresAt": "...",
    "purpose": "processor_delivery"
  },
  "callback": {
    "url": "https://aap-host/api/agent-access/v1/internal/file-processor/callbacks/<deliveryId>",
    "expiresAt": "..."
  }
}
```

- **Signature:** HMAC-SHA256 over `t + "." + rawBody` with the workspace processor secret; header form `t=<unix>,v1=<hex>`. Verify within ±5 minutes skew.
- **Download URL** in the delivery is a short-lived opaque token proxy — fetch it without an AAP Bearer.
- **Callback** (partner → ActWeave), also HMAC-signed with the same secret scheme:

```http
POST /api/agent-access/v1/internal/file-processor/callbacks/{deliveryId}
Content-Type: application/json
X-ActWeave-Signature: t=<unix>,v1=<hmac_sha256_hex>
```

```json
{
  "processorId": "partner-dlp",
  "status": "succeeded",
  "artifacts": [
    {
      "kind": "DLP_REPORT",
      "mediaType": "application/json",
      "contentBase64": "..."
    }
  ],
  "attributes": { "dlpRisk": "low" }
}
```

| Rule | Detail |
| --- | --- |
| Callback body cap | ~384 KiB request; **decoded** artifacts total ≤ **256 KiB** |
| Job lifecycle | `PENDING` → `DELIVERED` → `SUCCEEDED` \| `FAILED` \| `TIMED_OUT` |
| Late callback | After `TIMED_OUT` → **409 `FILE_PROCESSOR_CALLBACK_LATE`** (does not break an already-terminal file) |
| Idempotent replay | Same delivery success callback → 200 |
| Webhook URL policy | **https only**; private / link-local / metadata IPs rejected (SSRF) |
| Config surface | Workspace table / operator injection — **no** public list API and **no** Console UI in v1 |

#### Partner tool headers (`x-actweave-file`)

When a tool schema marks a property with `x-actweave-file: true`, ActWeave mints short-lived download tokens at **invoke** time and injects them on the **outbound wire only** (scrubbed before permanent storage / protocol projection):

| Situation | Header |
| --- | --- |
| Exactly one file | `X-ActWeave-File-Download: <absolute-proxy-url>` |
| Multiple files | `X-ActWeave-File-Downloads: application/json` with body `{"<fileId>":"<url>",...}` |

Partners should either:

- declare optional `downloadUrl` on the file object in the tool input schema and read it from the JSON body, **or**
- read `X-ActWeave-File-Download(s)` headers

Partners must **not** be required to hold an AAP token and call `:download` themselves. Model-visible / stored tool arguments contain only `fileId` (and metadata) — never live URLs.

### 9.2 A2UI (optional, additive)

A2UI is an **optional, additive** capability: **text is always first-class**. Enabling it means the Agent **may** attach a declarative UI surface when useful — it does **not** require every reply to include A2UI. Simple Q&A can remain text-only; the same Conversation may mix pure-text turns and text+a2ui turns.

#### Enable (ActWeave admin / Agents Studio)

| Layer | Field | Default |
| --- | --- | --- |
| Agent policy | `context_policy.aap.enableA2UI: boolean` | **`false`** (omit / null → false) |
| Policy schema | Requires `session-context-policy.v2` when any `aap.*` flag is present | Same pattern as `includeCompactionSummary` |
| Run freeze | `context_policy_snapshot.aap.enableA2UI` at createRun | Mid-run Agent edits do **not** change an in-flight Run |

Workspace-scoped context policy **rejects** any `aap` fields. Only Agent-level policy applies.

#### Profile advertisement (`GET .../profile`)

When `enableA2UI` is true, the Agent Profile **advertises** assistant outbound capability:

1. `supportedContent` message parts include `"a2ui"` (stable order: `text` → optional `input_file` → optional `a2ui` → optional `output_file`).
2. Top-level **`a2ui`** object (present **only** when enabled; **omitted** when disabled — not `enabled: false`):

```json
"a2ui": {
  "enabled": true,
  "delivery": "item_completed",
  "streaming": false,
  "actions": false,
  "maxSurfaceBytes": 65536,
  "specHint": "a2ui-surface.v1",
  "catalogIds": ["https://catalog.actweave.dev/standard/v1/catalog.json"]
}
```

| Field | MVP meaning |
| --- | --- |
| `delivery: "item_completed"` | Full A2UI part arrives on **`item.completed`** only |
| `streaming: false` | No A2UI delta / progressive surface in MVP |
| `actions: false` | **No** action channel; controls are **display-only** |
| `maxSurfaceBytes` | Raw `surface` JSON size cap (64 KiB) |
| `specHint` | Envelope / surface version hint for clients |
| `catalogIds` | Component catalogs a surface may declare. Every surface carries one of these as `catalogId`; a client that renders none of them should ignore `a2ui` parts and use the text |

ETag / profile version includes this object: flipping enable or changing advertised metadata changes ETag.

#### Wire shape (assistant outbound)

On `item.completed`, content is multi-part when A2UI was successfully extracted:

```json
{
  "type": "message",
  "role": "assistant",
  "status": "completed",
  "content": [
    { "type": "text", "text": "Please confirm the booking details:" },
    {
      "type": "a2ui",
      "version": "a2ui-surface.v1",
      "catalogId": "https://catalog.actweave.dev/standard/v1/catalog.json",
      "surface": { }
    }
  ]
}
```

| Rule | Detail |
| --- | --- |
| **Text first-class** | Schema always has a `text` part; value may be `""` only when a valid `a2ui` part is present |
| **Optional a2ui** | 0 or 1 `a2ui` part (MVP). Clients that ignore unknown parts still work with text alone |
| **Inbound** | `createRun` **rejects** user/input `a2ui` (`UNSUPPORTED_CONTENT_TYPE` / 4xx). A2UI is assistant outbound only |
| **Degrade** | Invalid / oversized / failed projection → text-only success; A2UI never alone fails the Run |

#### Client rules (authoritative completed vs streaming preview)

1. **Stream text as today.** Only `text_delta` streams (index 0). Concatenated deltas are a **live preview**, not final copy.
2. **Deltas carry prose only.** The A2UI fence (`<<<A2UI>>>` … `<<<END_A2UI>>>`) is how the platform asks a model for a surface, and it is stripped before any delta leaves the server — including a marker split across chunks. You never see a fragment of one, so there is nothing to strip and nothing to parse client-side. A surface streams no text at all, so expect a pause between prose and `item.completed`; keep whatever in-progress affordance you already show until the item completes.
3. **`item.completed` is authoritative.** It **replaces** the whole item snapshot. Use its multiparty `content`: cleaned text [+ optional `a2ui`]. Prefer completed over any in-flight delta buffer.
4. **MVP display-only / client no-op for actions.** Profile advertises `a2ui.actions: false`. Render surfaces with a local catalog if you have one; **do not** submit button clicks, form posts, or other control actions to ActWeave. **Never** reuse `interaction.decide` for A2UI controls (approval Interactions stay separate).
5. **Ignore unknown parts** if you do not implement A2UI; still advance the SSE sequence cursor.

TypeScript SDK helpers (`@actweave/agent-client`):

```ts
import { findA2UIPart, isKnownA2UICatalog, iterCharts, joinTextParts, type ProtocolItem } from "@actweave/agent-client";

function readAssistant(item: ProtocolItem) {
  const text = joinTextParts(item); // text parts only; ignores a2ui / unknown
  const surface = findA2UIPart(item)?.surface; // undefined when text-only
  // A surface from a catalog you do not know: show the text, draw nothing.
  if (!isKnownA2UICatalog(surface)) return { text, charts: [] };
  return { text, charts: iterCharts(surface) }; // series resolved, values numeric
}
```

`RunReducer` already replaces items on `item.completed` so progressive text is overwritten by the authoritative multiparty content.

#### Surface contract (catalog `standard/v1`)

The `surface` is **not** free-form: the server validates every surface against a
component catalog before storing it, and rejects the whole surface if it does not
conform (the message then arrives as text alone). What you receive is therefore
already inside these bounds — the contract exists so your renderer can rely on
that instead of guessing.

**Shape.** A surface is the A2UI `createSurface` payload, so a conforming
renderer can consume it unchanged:

```json
{
  "surfaceId": "019ff3f0-bfdd-7b38-9c53-f90bf5812478:item_1",
  "catalogId": "https://catalog.actweave.dev/standard/v1/catalog.json",
  "components": [
    { "id": "root", "component": "Column", "children": ["t1", "c1"] },
    { "id": "t1", "component": "Text", "text": "2026 Q1 revenue by region", "variant": "heading" },
    { "id": "c1", "component": "Chart", "chartType": "bar", "unit": "万元", "series": { "path": "/revenue" } }
  ],
  "dataModel": {
    "revenue": [{ "name": "revenue", "points": [{ "label": "East", "value": 1280 }] }]
  }
}
```

The graph is **flat**: components are a list, children are referenced by id, and
exactly one component has `id: "root"`. Walk from `root`; a component nobody
references is unreachable and never reaches you.

**Components** (`standard/v1`, 11): `Column` `Row` `Card` `Text` `Divider`
`Chart` `TextField` `CheckBox` `ChoicePicker` `DateTimeInput` `Button`.
Names match exactly — no aliases, no case folding.

**Charts** carry measurements only. There is no colour, size, axis range, legend
or formatted string to inherit; that is your design system's business.

| Member | Contract |
| --- | --- |
| `chartType` | `bar` `hbar` `line` `area` `pie` `donut`. `hbar` exists so long category labels need no rotation |
| `series` | 1–8 series, each with 1–64 `{label, value}` points. Inline or a binding |
| `unit` | Unit of every value (e.g. `万元`, `%`), never baked into the numbers |
| `valueFormat` | `plain` `compact` `percent` `currency` — how a value should read |
| `stacked` | Bar shapes only; the server rejects it elsewhere |
| `title` | Optional. A chart's own title, separate from any sibling `Text` heading |

Cross-field rules the server has already enforced: `pie` / `donut` carry exactly
one series and no negative values; multi-series charts share one label sequence.

**Bindings.** Anywhere a value is allowed, a member may instead be a JSON Pointer
(RFC 6901) into `dataModel`: `{ "path": "/revenue" }`. Resolve every member
through one helper — do not decide literal-vs-pointer by inspecting the value.
A pointer that reaches nothing is not an error; treat it as an absent member.

**Limits** (server-enforced; mirror them and a foreign surface cannot drive
unbounded work in your client):

| Limit | Value |
| --- | --- |
| `surface` JSON bytes | 65536 |
| Components per surface | 64 |
| Tree depth | 16 |
| Series per chart / points per series | 8 / 64 |

**Client obligations.**

1. **Check `catalogId`** against the profile's `a2ui.catalogIds` before rendering. An unknown catalog means unknown components: fall back to the text.
2. **Degrade per component, never per message.** For a component you do not implement, render a placeholder and keep drawing its siblings. The same applies to a dangling child id or a tree deeper than you allow.
3. **Never execute anything from a surface.** Text is text, not markup: interpolate it. `actions: false` means controls are display-only.
4. **Do not infer meaning from ids.** `id` is a graph key, not a semantic hint.

**Schema distribution.** Fetch the catalog and surface schemas (public,
cacheable, `ETag`, no token):

```
GET {base}/api/v1/a2ui/catalogs/standard/v1/catalog.json
GET {base}/api/v1/a2ui/catalogs/standard/v1/surface.schema.json
```

`catalogId` is an *identifier*: per the A2UI spec it need not be fetchable, so
resolve schemas through these endpoints rather than by dereferencing the id.
The surface schema reaches the catalog through a relative `$ref`, so keep the two
documents siblings if you mirror them.

#### Non-goals (MVP)

- A2UI streaming / progressive surface (future)
- Component action channel / `a2ui_action` user parts (future)
- Catalog negotiation: a client cannot yet declare which catalogs it renders
- Catalogs beyond `standard/v1`, and the Basic Catalog components it omits

### 9.3 Outbound attachments (optional, additive)

Outbound attachments are an **optional, additive** capability: **text remains first-class**. When enabled, the Agent **may** attach 0..N generated files to the assistant message as `type: "output_file"` content parts. They render as attachment cards (same shape as inbound user files), not as Markdown links and not as `artifact` Run Items.

v1 model production is **text-only**: the platform tool `actweave.publish_attachment` accepts UTF-8 `text` (plain / CSV / Markdown / JSON) up to **256 KiB**. There is **no** model `base64` field and **no** public ingest HTTP. Bytes land in the existing File plane (`purpose=AGENT_OUTPUT`); clients download with the same path A / path B used for inbound files.

#### Enable (three gates + toolCalling)

All of the following must be true or the runtime **does not inject** the tool (the Run still succeeds as plain text):

| Layer | Field | Default |
| --- | --- | --- |
| Files HTTP gate | `agentAccess.files.enabled` **and** workspace/client allowlist (`allowAllWorkspaces` / `workspaceIds`, `allowAllClients` / `clientIds`) | **off** |
| Runtime | `agentAccess.files.runtimeOutboundAttachments` (env `ACTWEAVE_AAP_FILES_RUNTIME_OUTBOUND_ATTACHMENTS`) | **`false`** |
| Agent policy | `context_policy.aap.enableOutboundAttachments` (policy v2; omit / null → false) | **`false`** |
| Frozen `toolCalling` | `function_calling` or `native_client_search` (v1 empty string follows the existing native-client-search rule) | `none` **does not inject** |

Workspace-scoped context policy **rejects** `aap` fields. Mid-run Agent edits do **not** change an in-flight Run (`context_policy_snapshot.aap.enableOutboundAttachments`).

| files.enabled | allowlist ws+client | runtimeOutbound | policy | toolCalling supports tools | Behavior |
| --- | --- | --- | --- | --- | --- |
| 0 | * | * | * | * | File HTTP 404; no inject; existing `fileId` also cannot be fetched |
| 1 | 0 | * | * | * | HTTP 404; no inject; ingest refuses (`FILE_FEATURE_DISABLED`) |
| 1 | 1 | 0 | * | * | Inbound files available; no inject |
| 1 | 1 | 1 | 0 | * | No inject |
| 1 | 1 | 1 | 1 | 0 (`none`) | **No inject, Run succeeds**; plain text |
| 1 | 1 | 1 | 1 | 1 | Full outbound |

**`toolCalling: none` + a non-empty catalog fails the turn** (`ErrAgentModelToolsUnsupported`). Pure-chat Agents that flip the policy still get text-only replies — the tool is simply absent.

There is **no public ingest HTTP**. `createFile` **does not** accept `purpose=AGENT_OUTPUT`.

#### Profile advertisement (`GET .../profile`)

When the files HTTP gate is open for the workspace **and** `runtimeOutboundAttachments` **and** Agent policy enable:

1. `supportedContent` message parts include `"output_file"` (stable order: `text` → optional `input_file` → optional `a2ui` → optional `output_file`).
2. An `output_file_constraints` object is appended:

```json
{
  "type": "output_file_constraints",
  "mediaTypes": ["text/plain", "text/csv", "text/markdown", "application/json"],
  "maxBytes": 262144
}
```

`createRun` still **rejects** user/input `output_file` (`UNSUPPORTED_CONTENT_TYPE`). Old SDKs ignore unknown parts / unknown constraint objects.

#### Wire shape (assistant outbound)

`output_file` appears only on **`item.completed`** (same as A2UI). `item.delta` never carries files.

```json
{
  "type": "message",
  "role": "assistant",
  "status": "completed",
  "content": [
    { "type": "text", "text": "Statement generated." },
    {
      "type": "output_file",
      "fileId": "019f0000-0000-7000-8000-00000000f001",
      "mediaType": "text/csv",
      "filename": "invoice-2026-08.csv",
      "sizeBytes": 4096
    }
  ]
}
```

| Rule | Detail |
| --- | --- |
| **Text first-class** | Schema always has a `text` part. Attachment-only turns still persist a non-empty envelope (empty text + `output_file`) |
| **0..N files** | Server cap **8** files per turn (`MaxOutboundFilesPerTurn`). Each part is allowlist-rebuilt: `type`, `fileId`, `mediaType`, `filename`, `sizeBytes` only — **never** URL / bytes |
| **This-run only** | Only `fileId`s successfully ingested for **this Run** (`aap_files.source_run_id`, `purpose=AGENT_OUTPUT`) are attached. The model cannot invent ids |
| **Inbound** | `createRun` rejects `output_file` |
| **Degrade** | Terminal preflight failure drops the files and keeps text/A2UI; the Run still succeeds |
| **A2A** | `completeRunA2A` does **not** attach outbound files |

#### Tool contract (`actweave.publish_attachment`)

The model’s only v1 publish channel:

```json
{
  "filename": "invoice-2026-08.csv",
  "mediaType": "text/csv",
  "text": "month,booked,closed\n..."
}
```

| Rule | Detail |
| --- | --- |
| MIME | `text/plain` \| `text/csv` \| `text/markdown` \| `application/json` only |
| Size | UTF-8 `text` ≤ **256 KiB** (`MaxPublishTextBytes`). JSON `maxLength` is code points; the server also checks byte length |
| No `base64` | `additionalProperties: false`; a `base64` field is rejected |
| Result | `{ ok, fileId, filename, mediaType, sizeBytes, sha256 }` — **never** URL / `downloadUrl` / content |
| Quota | READY workspace quota overflow → `FILE_SIZE_EXCEEDED`. More than 8 files this turn → `FILE_OUTBOUND_TURN_LIMIT`. Gate closed → `FILE_FEATURE_DISABLED` |
| Virus / DLP | **No virus scanner and no workspace webhook DLP** on outbound ingest. Do not invent a stricter `VirusScanner` than inbound (inbound virus is a stub that always reports clean) |

Successful ingest writes a READY `aap_files` row with **`purpose=AGENT_OUTPUT`** and `source_run_id` = this Run. Download uses existing File HTTP (`file:read`).

#### Client rules

1. **Stream text as today.** Files do not appear on `item.delta`.
2. **`item.completed` is authoritative.** Read `output_file` parts from the completed item (SDK: `findOutputFileParts` / `isOutputFileContentPart`). `joinTextParts` ignores file parts.
3. **Hydrate by `fileId`.** Call `getFile` / `getFileContent`. Do **not** use `links.content` as an `<img src>`. Reconcile by `fileId` so later snapshots (`run.completed`) do not reset already-ready cards.
4. **Download** with path A or B (same as inbound). Non-image responses include `Content-Disposition: attachment` and `X-Content-Type-Options: nosniff`.
5. Ignore unknown parts if you do not implement outbound files; still advance the SSE cursor.

#### Demos (`demos/aap-chat`)

- **Mock:** stories `export-csv` (**生成本月对账单**, CSV), `site-photos` (**看看这几张现场图**, 2×2 PNG gallery), and `inspection-pack` (**出一份巡检复盘包**, Markdown + photos + file cards + A2UI) paint assistant cards in `.msg-row.is-assistant .msg-attachments` without a backend (`npm run dev:mock`).
- **Live:** `item.completed` → placeholder cards → `getFile` / `getFileContent` hydrate by `fileId` → `patchMessages`. Needs `file:read`.

#### Console (operators; not AAP)

Console chat projects `attachments` on the message DTO and renders cards. Preview/download uses:

```
GET /api/v1/workspaces/{wid}/sessions/{sid}/messages/{mid}/files/{fileId}/content
```

Authorization: Console `ActionView` + the session belongs to the workspace + the caller can read the session + **`fileId` appears on that message’s durable `output_file` (or user `input_file`) parts**. Bytes come from SecureStore (`CreatorSystem`). Console users **must not** call AAP File routes as a service principal. This is **not** a file-management UI and **not** a third-party API.

#### Rollback (operators)

1. Turn **`runtimeOutboundAttachments` off first** (stops new writes / injection).
2. Stop new Agent-policy `enableOutboundAttachments: true`.
3. **Do not** roll back the snapshot parser that understands `enableOutboundAttachments` (old binaries `DisallowUnknownFields` would fail in-flight Runs that already wrote the key).
4. **Do not** run the `000023` down migration while `AGENT_OUTPUT` rows exist (the CHECK rollback refuses).
5. Turn `files.enabled` off last (otherwise historical downloads also 404).

`runtimeMultimodal` is orthogonal.

#### Non-goals (v1)

- No model `base64`, no 25 MiB tool arguments, no `sourceFileId` forwarding
- No outbound virus scanner / webhook DLP
- No public ingest HTTP; `createFile` cannot set `AGENT_OUTPUT`
- Image / PDF generation is not a v1 tool enum (ingest API may accept them for future same-process callers)
- A2A inbound complete does not attach outbound files

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
| `UNSUPPORTED_CONTENT_TYPE` | 400 | no | Use supported content parts only |
| `SLOW_CONSUMER` | SSE disconnect | yes | Reconnect with last `id` |
| `FILE_NOT_FOUND` | 404 | no | Missing / not visible (conceal) |
| `FILE_FEATURE_DISABLED` | 404 | no | Files gate closed (conceal) |
| `FILE_NOT_READY` | 422 | yes if still processing | Wait / poll GET before createRun |
| `FILE_UPLOAD_EXPIRED` | 422 | no | Re-create intent; staging TTL elapsed |
| `FILE_SIZE_EXCEEDED` | 422 | no | Reduce payload / respect maxBytes |
| `FILE_MEDIA_TYPE_DENIED` | 422 | no | Use allowed MIME whitelist |
| `FILE_MEDIA_TYPE_MISMATCH` | 422 | no | Bytes do not match declared mediaType |
| `FILE_INTEGRITY_MISMATCH` | 422 | no | sha256 mismatch |
| `FILE_PROCESSING_FAILED` | 422 | no | Do not reference failed file |
| `FILE_RUNTIME_UNAVAILABLE` | 422 | no | `input_file` requires `RuntimeMultimodal`; no Run created |
| `FILE_PROCESSOR_CALLBACK_LATE` | 409 | no | Job already TIMED_OUT; leave state |
| `FILE_PENDING_LIMIT` | 429 | yes | Back off; concurrent PENDING_UPLOAD cap |
| `FILE_OUTBOUND_TURN_LIMIT` | tool result | no | More than 8 outbound files this turn |
| `MODEL_CONTENT_UNSUPPORTED` | run failed | no | Provider cannot consume media |

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

const { conversation } = await client.createConversation(
  workspaceId,
  agentId,
  { title: "Ticket 42" },
  { idempotencyKey: crypto.randomUUID() },
);

const run = await client.createRun(
  workspaceId,
  agentId,
  {
    conversationId: conversation.id,
    stream: false,
    input: [
      {
        type: "message",
        role: "user",
        content: [{ type: "text", text: "Hello" }],
      },
    ],
  },
  { idempotencyKey: crypto.randomUUID() },
);

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

### 14.1 File upload (when `agentAccess.files` is enabled)

```ts
const bytes = new Uint8Array(/* ... */); // never embed production secrets in samples
const created = await client.createFile(
  workspaceId,
  agentId,
  {
    filename: "invoice.png",
    mediaType: "image/png",
    sizeBytes: bytes.byteLength,
  },
  { idempotencyKey: crypto.randomUUID() },
);

// PUT must use create-returned headers (Content-Length + Content-Type are signed).
await client.putFileUpload(created.upload!, bytes);

await client.completeFile(workspaceId, agentId, created.file.id, undefined, {
  idempotencyKey: crypto.randomUUID(),
});

const ready = await client.waitUntilReady(workspaceId, agentId, created.file.id);

// Multimodal E2E also requires RuntimeMultimodal on the ActWeave side.
await client.createRun(
  workspaceId,
  agentId,
  {
    conversationId: conversation.id,
    stream: false,
    input: [
      {
        type: "message",
        role: "user",
        content: [
          { type: "text", text: "Describe this image" },
          { type: "input_file", fileId: ready.id },
        ],
      },
    ],
  },
  { idempotencyKey: crypto.randomUUID() },
);

// Small: Bearer .../content. Large (>4MiB): prefers :download token proxy.
const content = await client.getFileContent(workspaceId, agentId, ready.id);
```

SDK guarantees:

- Access Token only in `Authorization` (never query)
- Auto-reconnect with `Last-Event-ID` on gaps / retryable disconnects
- Force-refresh on `TOKEN_EXPIRED` / HTTP 401, then resume the same cursor
- File PUT uses **only** create-returned headers (no AAP Bearer on object storage)
- `getFileContent` prefers opaque `:download` when `sizeBytes > 4MiB`

Also exported: `StaticTokenProvider`, `RunReducer`, `AAPSESession`, file types / `SDK_PREFER_DOWNLOAD_TOKEN_BYTES`, A2UI helpers `joinTextParts` / `findA2UIPart`, and outbound helpers `findOutputFileParts` / `isOutputFileContentPart`. See `sdk/typescript/README.md`, [§9.2 A2UI](#92-a2ui-optional-additive), and [§9.3 outbound attachments](#93-outbound-attachments-optional-additive).

### 14.2 Outbound files (when gates + policy + toolCalling are on)

```ts
import { findOutputFileParts, joinTextParts, type ProtocolItem } from "@actweave/agent-client";

function readAssistantFiles(item: ProtocolItem) {
  const text = joinTextParts(item); // ignores output_file / a2ui / unknown
  const files = findOutputFileParts(item); // 0..N; empty when text-only
  // Hydrate with getFile / getFileContent by fileId. Never treat links.content as src.
  return { text, files };
}
```

`CreateRun` input types do **not** include `output_file`. Download uses the same `getFile` / `getFileContent` helpers as inbound files (`file:read`).

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
- [ ] If using files: operator enabled `agentAccess.files` **and** (for model vision/PDF) `RuntimeMultimodal`; Grant includes `file:read` / `file:write`  
- [ ] If using files: PUT always sends create-returned `Content-Length` / `Content-Type`; never store live download URLs in your long-term logs  
- [ ] If you implement a processor: verify `X-ActWeave-Signature`, https-only callback URL policy, and late-callback `FILE_PROCESSOR_CALLBACK_LATE` handling  
- [ ] If using A2UI: Agent has `context_policy.aap.enableA2UI`; client treats `item.completed` as authoritative; Profile `a2ui.actions: false` → display-only / no-op submits
- [ ] If using outbound attachments: operator enabled files HTTP (workspace/client allowlist) **and** `runtimeOutboundAttachments`; Agent `enableOutboundAttachments`; frozen `toolCalling` is `function_calling` or `native_client_search` (`none` does not inject); Grant includes `file:read`; client treats `item.completed` `output_file` as authoritative and hydrates by `fileId`  

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
