# Agent Access Protocol — API Reference

- Machine-readable contract: [`../openapi/agent-access-v1.yaml`](../openapi/agent-access-v1.yaml)
- Protocol Event schemas: `backend/internal/protocolschema/schemas/aap/v1/`
- Base URL: `/api/agent-access/v1`
- Auth: AAP Access Token (`Authorization: Bearer`), except OAuth Token and JWKS

This document is the human-oriented index. Field-level schemas and enums live in OpenAPI and the Schema Registry (do not hand-edit generated catalogs).

## Headers (common)

| Header | Where | Purpose |
| --- | --- | --- |
| `Authorization: Bearer <access_token>` | Data plane | Short-lived AAP Token only |
| `Idempotency-Key: <uuid>` | POST Conversation / Run / Decide | Canonical UUID; command receipt |
| `If-Match: "<version>"` | Interaction decide | Strong ETag / Interaction version |
| `If-None-Match` | GET Conversation | Conditional read |
| `Last-Event-ID: <sequence>` | GET Run events | Resume cursor (integer ≥ 0) |
| `Accept: text/event-stream` | GET Run events | SSE |
| `ActWeave-Protocol-Version` | Optional | Snapshot date (server echoes actual) |

**Forbidden:** Access Token or Client Secret in query string, fragment, or cookies.

## Resources

### Discovery / Token

| Method | Path | Auth | Notes |
| --- | --- | --- | --- |
| `GET` | `/.well-known/jwks.json` | none | Public verification keys only (`kty/crv/x/kid/alg/use`) |
| `POST` | `/oauth/token` | Client auth | `client_credentials` or Token Exchange; form body ≤ 32 KiB |

#### Token grant types

| `grant_type` | Purpose |
| --- | --- |
| `client_credentials` | Service Principal Token for one `agent_id` + `scope` |
| `urn:ietf:params:oauth:grant-type:token-exchange` | Bind External Subject via `subject_token` JWT |

#### Client authentication at Token Endpoint

| Mode | Mechanism |
| --- | --- |
| `client_secret_basic` | HTTP Basic `client_id:client_secret` |
| `private_key_jwt` | `client_assertion` JWT (EdDSA or PS256 only) |

Response never includes `refresh_token`. Cache-Control: no-store.

### Profile

| Method | Path | Scope |
| --- | --- | --- |
| `GET` | `/workspaces/{wid}/agents/{aid}/profile` | `agent:read` |

### Conversations

| Method | Path | Scope | Idempotent |
| --- | --- | --- | --- |
| `POST` | `/workspaces/{wid}/agents/{aid}/conversations` | `conversation:create` | `Idempotency-Key` |
| `GET` | `/workspaces/{wid}/agents/{aid}/conversations/{cid}` | `conversation:read` | ETag |

### Runs

| Method | Path | Scope | Notes |
| --- | --- | --- | --- |
| `POST` | `/workspaces/{wid}/agents/{aid}/runs` | `run:create` | Text input only in v1 |
| `GET` | `/workspaces/{wid}/agents/{aid}/runs/{rid}` | `run:read` | Status / summary |
| `POST` | `/workspaces/{wid}/agents/{aid}/runs/{rid}:cancel` | `run:cancel` | Idempotent cancel |
| `GET` | `/workspaces/{wid}/agents/{aid}/runs/{rid}/events` | `event:read` | SSE follow |

### Interactions

| Method | Path | Scope | Notes |
| --- | --- | --- | --- |
| `POST` | `.../runs/{rid}/interactions/{iid}:decide` | `interaction:decide` | Body: `{"decision":"approve\|decline\|cancel"}` + `If-Match` + `Idempotency-Key` |

## SSE frame formats

### Protocol Event (persisted)

```text
id: 12
event: item.delta
data: {"specVersion":"1.0","type":"item.delta","eventId":"...","streamId":"run:...","sequence":12,...}
```

### Heartbeat (not persisted)

```text
: ping 2026-07-21T12:00:00Z
```

### Transport signal (not persisted, no id)

```text
event: stream.error
data: {"specVersion":"1.0","type":"stream.error","error":{"code":"TOKEN_EXPIRED","retryable":true,...}}
```

## Protocol event types (v1)

Persisted event type catalog is generated from the Schema Registry (`AAP_V1_EVENT_TYPES` / OpenAPI components). Clients must ignore unknown types while advancing `sequence`.

Core lifecycle families:

- `run.accepted` / `run.started` / `run.waiting` / `run.resumed` / `run.completed` / `run.failed` / `run.cancelled`
- `item.started` / `item.delta` / `item.completed`
- `interaction.requested` / `interaction.resolved`
- `usage.updated` (when present)

## Error envelope

Data plane errors (non-OAuth):

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

### Stable 错误码 (selected)

| Code | HTTP | Retry? | Client action |
| --- | --- | --- | --- |
| `TOKEN_EXPIRED` | 401 / SSE | yes | Refresh Token; same `Last-Event-ID` |
| `UNAUTHENTICATED` | 401 | no* | Fix credentials (*or refresh if near expiry) |
| `AUTHORIZATION_DENIED` | 403/404 | no | Check Grant/scopes/ownership |
| `REPLAY_CURSOR_INVALID` | 422 | no | Reset from known good sequence or 0 |
| `IDEMPOTENCY_CONFLICT` | 409 | no | New key or identical body |
| `RATE_LIMITED` | 429 | yes | Honor `Retry-After` |
| `UNSUPPORTED_CONTENT_TYPE` | 400 | no | Use text-only input |
| `SLOW_CONSUMER` | SSE disconnect | yes | Reconnect with last `id` |

OAuth errors use RFC 6749-style `error` / `error_description` without echoing secrets.

## Rate limits and quotas

Multi-dimensional (Workspace / Client / Agent / Subject / IP depending on operation). Responses may include:

- `Retry-After`
- `RateLimit-Limit` / `RateLimit-Remaining` / `RateLimit-Reset` (when configured)

Token Endpoint: Client × IP × grant dimensions.
SSE connections: per Client / Subject / Run (defaults 16 / 8 / 4 per process instance).

## Versioning

| Axis | Meaning |
| --- | --- |
| URL `/v1` | Resource/Command major version |
| `ActWeave-Protocol-Version: YYYY-MM-DD` | Freeze snapshot |
| Event `specVersion=1.0` | Envelope major within v1 |

Additive changes (optional fields, new event types) are allowed; breaking changes require a new major or new event name.

## Related docs

- Developer Guide (Quickstart): [`agent-access-developer-guide.md`](./agent-access-developer-guide.md)
- Operator Runbook: [`../runbooks/agent-access-operator-runbook.md`](../runbooks/agent-access-operator-runbook.md)
- Migration Guide: [`agent-access-migration-guide.md`](./agent-access-migration-guide.md)
