# ACTWEAVE

[中文文档](./README.zh-CN.md)

**ActWeave** is an **Agent orchestration and execution console** for enterprise business systems.

It turns external business APIs into governed **Tools**, configures decision-making **Agents**, optionally wires multi-step processes with **Workflow / Smart DAG**, and keeps every invocation **auditable**. Third-party platforms integrate through the **Agent Access Protocol (AAP)** — not the management console session API.

---

## What problem does it solve?

| Pain | ActWeave approach |
| --- | --- |
| Models call HTTP ad hoc, without contracts or ACL | Register APIs as versioned **Tools** (schema, connection, publish) |
| Credentials scattered in frontends | **Service Connection** + outbound identity (incl. REQUEST_PASSTHROUGH) |
| Opaque multi-step flows | **Workflow** graphs with trial run, publish, and audit |
| Partners cannot integrate safely | **AAP**: OAuth clients, scopes, Conversation / Run, SSE |
| Incidents hard to explain | **Agent audit / logs** with full run traces |

In one line: **ActWeave = Tool governance + Agent config + orchestration + audited execution + external AAP.**

---

## Product surface

```text
┌─────────────┐     ┌──────────────┐     ┌────────────────┐
│  Console UI │────▶│ Agent runtime│────▶│ Business APIs  │
│  configure  │     │ Tool/Workflow│     │ (outbound auth)│
└─────────────┘     └──────┬───────┘     └────────────────┘
                           │
                    ┌──────▼───────┐
                    │  AAP plane   │  ← partner apps / BFF
                    │ Conversation │
                    │ Run + SSE    │
                    └──────────────┘
```

| Capability | Role |
| --- | --- |
| **Workspace** | Tenant / project boundary |
| **Provider / Connection** | Upstream systems and credentials |
| **Tool** | OpenAPI import or hand-authored callable |
| **Agent** | Model, prompt, bound Tools / Workflows |
| **Workflow** | Visual graph, trial, publish |
| **Smart DAG** | NL → draft business graph |
| **Console chat** | Internal Agent trial (not production AAP) |
| **Agent Access** | External AAP clients and grants |
| **Audit logs** | Run and admin audit trail |

Business objects come from configuration. The repository does **not** ship sample business data.

---

## Screenshots

Captured against a **fictional demo workspace** `Acme Commerce Demo` (mock ecommerce tools: products, orders, inventory, refunds). **No real tenant data.**

Regenerate:

```bash
node scripts/seed-readme-demo-workspace.mjs   # create/refresh Acme Commerce Demo
node scripts/capture-readme-screenshots.mjs   # write docs/images/readme/
```

### Navigation menu

Top “navigation island” groups modules: **Space → Build → Integrate → Run → Govern**, plus primary shortcuts.

![Navigation menu](./docs/images/readme/00-navigation-menu.png)

### Workspace switcher

Switch the active workspace from the top bar (demo space selected).

![Workspace switcher](./docs/images/readme/00-workspace-switcher.png)

### Login

![Login](./docs/images/readme/01-login.png)

### Workspace overview

Platform-level health (sessions, tool success rate, risks). Overview is cross-workspace.

![Overview](./docs/images/readme/02-overview.png)

### Workspaces

Tenant / project boundaries, mode (production / sandbox), status.

![Workspaces](./docs/images/readme/03-workspaces.png)

### Agents

Demo agent: `Acme 导购助手` with model binding and prompt.

![Agents](./docs/images/readme/04-agents.png)

### Tools

Mock ecommerce tools (list products, create order, inventory, refunds) with HTTP contracts and publish state.

![Tools](./docs/images/readme/05-tools.png)

### Workflow

Design, validate, trial-run, and publish flows.

![Workflow](./docs/images/readme/06-workflow.png)

### Smart DAG

NL → draft graph generation.

![Smart DAG](./docs/images/readme/07-smart-dag.png)

### Providers

Upstream business systems (demo: `Acme Commerce API`).

![Providers](./docs/images/readme/08-providers.png)

### Connections

Service connections and outbound identity (e.g. REQUEST_PASSTHROUGH).

![Connections](./docs/images/readme/09-connections.png)

### Model API configs

LLM endpoints bound to Agents.

![Model APIs](./docs/images/readme/10-model-apis.png)

### Agent Access

Third-party AAP clients and grants.

![Agent Access](./docs/images/readme/11-agent-access.png)

### Console run / chat

Internal Agent trial (not production AAP).

![Chat](./docs/images/readme/12-chat.png)

### Audit logs

Run and admin audit trail.

![Logs](./docs/images/readme/13-logs.png)

---

## Documentation

| Document | Audience | Description |
| --- | --- | --- |
| **[AAP Integration Guide (EN)](./docs/aap-integration-guide.md)** | Third-party integrators | Auth, scopes, HTTP/SSE, errors, SDK, production checklist |
| **[AAP 对接指南（中文）](./docs/aap-integration-guide.zh-CN.md)** | 第三方对接 | Same content in Chinese |
| [OpenAPI — Agent Access v1](./docs/openapi/agent-access-v1.yaml) | Machines / codegen | Authoritative HTTP contract |
| [TypeScript SDK](./sdk/typescript/) | Integrators | `@actweave/agent-client` |
| [AAP Chat Demo](./demos/aap-chat/) | Local demo | Browser chat + BFF for secrets |

Hand the **AAP Integration Guide** plus the OpenAPI file to external partners. Do not use `/api/v1` management routes for third-party Agent access.

---

## Repository layout

```text
.
├── frontend/           # Vue 3 + TypeScript + Vite console
├── backend/            # Go + Gin API server
├── docs/               # AAP guide, OpenAPI, README screenshots
├── demos/aap-chat/     # AAP integration demo (BFF + chat UI)
├── sdk/typescript/     # @actweave/agent-client
├── scripts/            # Ops / screenshot helpers
└── docker-compose.yml  # Local dependencies and full stack
```

## Stack

| Layer | Choices |
| --- | --- |
| Frontend | Vue 3.5, TypeScript, Vite 7, Pinia, Vue Router, Element Plus, Vue Flow, Axios, VXE Table |
| Backend | Go 1.25, Gin, JWT, kin-openapi; runtimes (Agent via Eino ADK; Workflow via Eino compose) |
| Data | PostgreSQL (system of record), MinIO (encrypted durable objects), Redis (rebuildable fan-out only) |

- PostgreSQL stores identity, configuration, versions, run records, and audit metadata.
- MinIO holds encrypted permanent business payloads; metadata and retention stay in PostgreSQL.
- Redis must not be treated as a durable fact source. Run event truth and `Last-Event-ID` replay come from PostgreSQL.

---

## Quick start

### Option A — Docker Compose

```bash
docker compose up --build
```

On an empty volume, Compose creates a **development** admin:

| | |
| --- | --- |
| Username | `admin` |
| Temporary password | `actweave-admin-dev-change-me` |

You must change the password after first login. Never use these credentials in production.

Default local ports:

| Service | URL / port |
| --- | --- |
| Frontend | http://127.0.0.1:5174 |
| Backend | http://127.0.0.1:8082 |
| PostgreSQL | 127.0.0.1:15432 |
| Redis | 127.0.0.1:16379 |
| MinIO API | 127.0.0.1:9000 |
| MinIO Console | 127.0.0.1:9001 |

Health check: `GET http://127.0.0.1:8082/api/v1/health`

### Option B — Frontend and backend separately

**Frontend** (Node `22.22.3` / npm `10.9.8`, lockfile only):

```bash
cd frontend
npm ci
npm run dev
```

**Backend:**

```bash
cd backend
go run ./cmd/server
```

The server reads [`backend/config.yaml`](./backend/config.yaml) by default. Configuration priority: **YAML file &lt; environment variables**. Set `ACTWEAVE_CONFIG_FILE` to load another file.

Production: copy config out of the tree, inject secrets via Secret Manager / KMS, and never reuse repository development values.

Common overrides:

```bash
cd backend
ACTWEAVE_CONFIG_FILE=/etc/actweave/config.yaml \
ACTWEAVE_POSTGRES_DSN='postgres://user:password@database:5432/actweave?sslmode=require' \
ACTWEAVE_JWT_SECRET='replace-with-a-random-secret-of-at-least-32-bytes' \
ACTWEAVE_AAP_SIGNING_PRIVATE_KEY_FILE='/run/secrets/aap-signing-private.pem' \
ACTWEAVE_AAP_SIGNING_GENERATE_IF_MISSING=false \
ACTWEAVE_AAP_TOKEN_ENDPOINT='https://actweave.example.com/api/agent-access/v1/oauth/token' \
go run ./cmd/server
```

Useful environment variables:

| Area | Variables |
| --- | --- |
| Service | `ACTWEAVE_API_ADDR`, `ACTWEAVE_LOG_LEVEL`, `ACTWEAVE_LOG_FORMAT` (`text` \| `json`) |
| Data / crypto | `ACTWEAVE_POSTGRES_DSN`, `ACTWEAVE_JWT_SECRET`, `ACTWEAVE_SECRET_MASTER_KEY` |
| AAP signing | `ACTWEAVE_AAP_TOKEN_ENDPOINT`, `ACTWEAVE_AAP_SIGNING_*` |
| MinIO | `ACTWEAVE_MINIO_*` |
| Bootstrap admin | `ACTWEAVE_BOOTSTRAP_ADMIN_*` |

Notes:

- `encryption.masterKey` must be a Base64-encoded 32-byte key.
- Bootstrap creates the first `PLATFORM_ADMIN` only when `users` is empty.
- AAP Access Tokens use **EdDSA/Ed25519**, not the HS256 user session secret.
- Public JWKS: `GET /api/agent-access/v1/.well-known/jwks.json`

For AAP client authentication, scopes, SSE, and errors, see the **[AAP Integration Guide](./docs/aap-integration-guide.md)**.

---

## Common commands

### Frontend

```bash
cd frontend
npm ci
npm run lint
npm run format:check
npm run dev
npm run build
npm test -- --run
npm run type-check
```

Use **npm** + `package-lock.json` only (`npm ci`). Do not use pnpm/yarn.

### Backend

```bash
cd backend
go run ./cmd/migrate up
go build ./cmd/server
go test ./...
```

Migrations:

- The API server applies embedded pending migrations before listening.
- Concurrent instances serialize migrations with a PostgreSQL advisory lock.
- Manual ops: `go run ./cmd/migrate version`, `go run ./cmd/migrate down 1`.
- Requires Go `1.25.x`.

Data volumes (Compose): `postgres-data`, `redis-data`, `minio-data`. `docker compose down -v` **destroys** local volumes.

---

## Implementation notes

- Workflow mainline: `WorkflowGraphDraft` → compilation → `CompiledExecutionPlan` → `WorkflowRevision` → runtime.
- Tools run through an HTTP executor with SSRF guards, secret injection, response limits, and idempotency.
- Smart DAG uses multi-turn generate sessions (`smart-dag.v2`) bound to an Agent with a usable model config.
- AAP is separated from console `/api/v1` management routes; partners use `/api/agent-access/v1` only.

## Known limitations

- Frontend targets desktop layouts (`min-width` around 1180px).
- No unified backend lint/format scripts; rely on `go test` / `go vet`.
- Some advanced Workflow node types are supported in the backend but not fully exposed in the editor UI.

## Third-party integration entry points

1. [docs/aap-integration-guide.md](./docs/aap-integration-guide.md)  
2. [docs/openapi/agent-access-v1.yaml](./docs/openapi/agent-access-v1.yaml)  
3. [sdk/typescript](./sdk/typescript/) (optional client)  
4. [demos/aap-chat](./demos/aap-chat/) (local AAP chat demo)
