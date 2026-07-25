# @actweave/agent-client

TypeScript client primitives for the ActWeave **Agent Access Protocol (AAP)** data plane.

## Install

```bash
npm install @actweave/agent-client
```

Requires Node.js 20+.

## Features

- Short-lived access token providers (client credentials / token exchange / BFF mint)
- Run command client (`createRun`, interaction decisions, cancel)
- SSE session with automatic reconnect and `Last-Event-ID` resume
- Protocol event reducer for conversation/run projections
- Generated protocol enums and envelope types from the Schema Registry

## Quick start

```ts
import {
  AgentAccessClient,
  StaticTokenProvider,
} from "@actweave/agent-client";

const client = new AgentAccessClient({
  baseUrl: "https://api.example.test/api/agent-access/v1",
  tokenProvider: new StaticTokenProvider(process.env.AAP_ACCESS_TOKEN!),
});

// followRun is an AsyncGenerator: three positional path args + optional options.
for await (const { message, snapshot } of client.followRun(
  "...workspaceId...",
  "...agentId...",
  "...runId...",
)) {
  // apply via reducer (snapshot already reduced) or your UI store
  if (message.kind === "protocol_event") {
    console.log(message.event.type, message.event.eventId);
  }
  if (snapshot.run && ["completed", "failed", "cancelled"].includes(String(snapshot.run.status))) {
    break;
  }
}
```

Other constructors:

| Export | Role |
| --- | --- |
| `AgentAccessClient` | Data-plane client (`new AgentAccessClient(options)`) |
| `StaticTokenProvider` | Fixed short-lived access token |
| `MemoryTokenProvider` | In-memory refreshable token material |
| `RunReducer` | Pure protocol-event → run/item projection |
| `AAPSESession` | SSE reconnect / `Last-Event-ID` state |

There are **no** factory helpers named `createAgentAccessClient` or `createStaticTokenProvider`.

## BFF vs browser-direct

| Mode | Recommendation |
| --- | --- |
| BFF | Prefer: keep tokens server-side; disable AAP CORS |
| Browser-direct | Register exact HTTPS `AllowedCORSOrigins` on the Agent Access Client |

Never put long-lived secrets or refresh tokens in `localStorage` / query strings.

## Protocol version

Send and expect header `ActWeave-Protocol-Version` (see OpenAPI `docs/openapi/agent-access-v1.yaml`).

## Development

```bash
npm run type-check
npm run check:readme-quickstart   # extracts Quick start fence and tsc --noEmit
npm test
npm run test:e2e
npm run build
npm pack --dry-run
```

## License

Apache-2.0 — see [LICENSE](./LICENSE).
