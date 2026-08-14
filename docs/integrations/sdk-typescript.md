# TypeScript SDK integration

[SDK README](../../sdk/typescript/README.md) · [AAP guide](../aap-integration-guide.md) · [Documentation home](../README.md)

`@actweave/agent-client` is a TypeScript client library for the AAP data plane. It does not administer Console resources such as Workspaces, Tools, Agents, or AAP Clients.

## What it includes

- `AgentAccessClient` for AAP Conversations, Runs, cancellation, Interaction decisions, and file helper endpoints.
- Static and in-memory short-lived token providers.
- SSE session handling with reconnect and `Last-Event-ID` continuation.
- A reducer and generated AAP protocol types for projecting Run events.

## Use it safely

- Prefer a BFF: keep a Client Secret/private key on your server and return only short-lived access tokens or proxied results to the browser.
- For browser-direct use, configure exact HTTPS `AllowedCORSOrigins` on the AAP Client; do not use wildcard origins or put long-lived credentials in browser storage.
- Treat the [AAP OpenAPI](../openapi/agent-access-v1.yaml) and the detailed [AAP guide](../aap-integration-guide.md) as the integration contract. The SDK is an optional client implementation.
- File helpers map to an optional server feature. They need `file:read`/`file:write` scopes and server-side file enablement; multimodal Run input also needs `runtimeMultimodal`. Assistant `output_file` parts use the same `getFile` / `getFileContent` helpers plus `findOutputFileParts` (see [integration guide §9.3](../aap-integration-guide.md#93-outbound-attachments-optional-additive)).

## Local verification

```bash
cd sdk/typescript
npm ci
npm run type-check
npm run check:readme-quickstart
npm test
npm run build
```

For a local UI/BFF example, see the [AAP Chat Demo](../../demos/aap-chat/).
