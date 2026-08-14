# AAP integration index

[中文](./aap.zh-CN.md) · [Full English integration guide](../aap-integration-guide.md) · [Documentation home](../README.md)

AAP (Agent Access Protocol) is the application-to-ActWeave-Agent-Runtime path, not the Console management API. It uses `/api/agent-access/v1`, and callers use AAP access tokens rather than a Console user session.

## Handoff materials for an integrator

1. [AAP integration guide](../aap-integration-guide.md): authentication, scopes, Conversations, Runs, SSE, errors, CORS, credential rotation, and rollout checklist (includes optional [A2UI §9.2](../aap-integration-guide.md#92-a2ui-optional-additive)).
2. [OpenAPI](../openapi/agent-access-v1.yaml): machine-readable HTTP contract and source of truth for field schemas.
3. [TypeScript SDK](../../sdk/typescript/): `@actweave/agent-client` (helpers `joinTextParts` / `findA2UIPart`, plus `isKnownA2UICatalog` / `resolveBinding` / `iterCharts` for reading a surface when `enableA2UI`).
4. [AAP Chat Demo](../../demos/aap-chat/): local demonstration where a BFF holds the Client Secret.
5. Optional A2UI is documented in the [integration guide §9.2](../aap-integration-guide.md#92-a2ui-optional-additive) (`actions: false` in MVP; surfaces are catalog-validated).

## Shortest call chain

```text
Client credentials / private_key_jwt
  → AAP access token
  → Conversation
  → Run
  → SSE events (Last-Event-ID reconnect)
```

Default deployments accept text `input`. File-upload routes exist but are disabled by default; end-to-end multimodal also needs `runtimeMultimodal`. Optional A2UI is off by default (`context_policy.aap.enableA2UI`); when on, text stays first-class and `a2ui` may appear on `item.completed` only (`streaming: false`, `actions: false`), and every surface conforms to the advertised component catalog. Do not store long-lived Client Secrets in a browser and do not use `/api/v1` as a third-party runtime entry point — its only third-party surface is the public A2UI schema distribution (`GET /api/v1/a2ui/catalogs/standard/v1/catalog.json`), which serves static documents and no workspace data.

For the AAP/A2A boundary, see [concepts](../concepts.md#aap-a2a-and-mcp).
