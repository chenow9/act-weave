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
- File upload helpers (`createFile` → `putFileUpload` with signed Content-Length/Type → `completeFile` → `waitUntilReady` → `createRun` with `input_file`)
- `getFileContent` (Bearer for small files; prefers opaque `:download` when `sizeBytes > 4MiB`)
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

## A2UI content (additive)

When an Agent has `enableA2UI`, assistant messages may carry a first-class
`type: "a2ui"` content part **in addition to** `type: "text"`.

| Concern | Contract |
| --- | --- |
| Streaming | Only `text_delta` streams. Concatenated deltas are a live preview only. |
| Fences | Until `item.completed`, delta text may include raw A2UI fence fragments (e.g. `<<<A2UI>>>` …). Do **not** treat delta concatenation as final copy. |
| Completed | `item.completed` **replaces** the whole item. Its `content` is authoritative (cleaned text [+ optional `a2ui`]). |
| Helpers | `joinTextParts(item)` / `findA2UIPart(item)` read text and the first a2ui part from a content array or item. |
| Actions | MVP Profile advertises `a2ui.actions: false`. UI controls are display-only; client should no-op submits. |

```ts
import { findA2UIPart, joinTextParts, type ProtocolItem } from "@actweave/agent-client";

function renderAssistant(item: ProtocolItem) {
  const text = joinTextParts(item); // ignores a2ui / unknown parts
  const a2ui = findA2UIPart(item);  // undefined when text-only
  // Prefer completed snapshot over any in-flight delta buffer.
  return { text, surface: a2ui?.surface, version: a2ui?.version };
}
```

`RunReducer` already replaces items on `item.started` / `item.completed` /
`item.failed`; progressive `text_delta` only mutates the live text part until
the completed snapshot overwrites it.

### Reading a surface

A surface is a flat component graph plus an optional `dataModel`. Any member is
either a literal or a JSON Pointer into that data model, so read members through
`resolveBinding` rather than by inspecting their shape. The catalog vocabulary
(`A2UI_CATALOG_ID`, `A2UI_COMPONENT_NAMES`, `A2UI_CHART_TYPES`, `A2UI_LIMITS`, …)
is generated from the same catalog the server validates against.

```ts
import { findA2UIPart, isKnownA2UICatalog, iterCharts, joinTextParts, type ProtocolItem } from "@actweave/agent-client";

function readAssistant(item: ProtocolItem) {
  const text = joinTextParts(item);
  const surface = findA2UIPart(item)?.surface;
  // A surface from a newer catalog may use components that do not exist here.
  // Falling back to the text is honest; drawing half a UI is not.
  if (!isKnownA2UICatalog(surface)) return { text, charts: [] };
  // Charts arrive as measurements: values are numbers, `unit` and `valueFormat`
  // say how they should read, and nothing visual is prescribed.
  return { text, charts: iterCharts(surface) };
}
```

| Concern | Contract |
| --- | --- |
| Catalog | Every delivered surface declares `catalogId`. Check it with `isKnownA2UICatalog` before rendering; the Agent Profile advertises the set as `a2ui.catalogIds`. |
| Schemas | Fetch from `GET {base}/api/v1/a2ui/catalogs/standard/v1/catalog.json` (public, ETag). `catalogId` is an identifier, not necessarily a URL you can fetch. |
| Bindings | `{ "path": "/pointer" }` anywhere a value is allowed. `resolveBinding(surface, value)` returns `undefined` when the pointer reaches nothing. |
| Unknown components | Degrade per component: render a placeholder for the one you do not know, keep drawing its siblings. Never fail the message. |
| Limits | `A2UI_LIMITS` mirrors the server's structural caps (components, depth, series, points). Respect them and a foreign surface cannot drive unbounded work. |
| Official renderers | The surface is the A2UI `createSurface` payload, so it can be fed to a conforming renderer unchanged. |

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
