# ACTWEAVE

[中文文档](./README.zh-CN.md)

ActWeave is a console for orchestrating business operations with Agents, Tools, Workflows, and audited executions. Third-party platforms integrate through the **Agent Access Protocol (AAP)** — not the management console session API.

## Documentation

| Document | Audience | Description |
| --- | --- | --- |
| **[AAP Integration Guide (EN)](./docs/aap-integration-guide.md)** | Third-party integrators | Full protocol handoff: auth, scopes, HTTP/SSE, errors, SDK, production checklist |
| **[AAP 对接指南（中文）](./docs/aap-integration-guide.zh-CN.md)** | 第三方对接 | 与英文版同等内容 |
| [OpenAPI — Agent Access v1](./docs/openapi/agent-access-v1.yaml) | Machines / codegen | Authoritative HTTP contract |
| [TypeScript SDK](./sdk/typescript/) | Integrators | `@actweave/agent-client` |

Hand the **AAP Integration Guide** plus the OpenAPI file to external partners. Do not use `/api/v1` management routes for third-party Agent access.

## Product domains

| Domain | Role |
| --- | --- |
| **Workspace** | Tenant / business space boundary |
| **Agent** | Default executor and prompt configuration |
| **ServiceConnection** | External system credentials |
| **Tool** | Callable business capability |
| **Workflow** | Explicit graph orchestration and approvals |
| **Execution / AuditLog** | Run history and audit trail |
| **ChatSession** | Console conversational entry (internal UI) |
| **AAP Conversation / Run** | External protocol conversation and execution |

Business objects are defined by configuration. The repository does **not** ship sample business data.

## Repository layout

```text
.
├── frontend/           # Vue 3 + TypeScript + Vite console
├── backend/            # Go + Gin API server
├── docs/               # AAP integration guide + OpenAPI
├── sdk/typescript/     # @actweave/agent-client
└── docker-compose.yml  # Local dependencies and full stack
```

## Stack

| Layer | Choices |
| --- | --- |
| Frontend | Vue 3.5, TypeScript, Vite 7, Pinia, Vue Router, Element Plus, Vue Flow, Axios, VXE Table |
| Backend | Go 1.25, Gin, JWT, kin-openapi; workflow/tool runtimes (Agent via Eino ADK; Workflow via Eino compose) |
| Data | PostgreSQL (system of record), MinIO (encrypted durable objects), Redis (rebuildable fan-out only) |

- PostgreSQL stores identity, configuration, versions, run records, and audit metadata. There is no full-state JSONB snapshot store.
- MinIO holds encrypted permanent business payloads; metadata, classification, hash, and retention stay in PostgreSQL.
- Redis must not be treated as a durable fact source. Run event truth and `Last-Event-ID` replay come from PostgreSQL.

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

The server reads [`backend/config.yaml`](./backend/config.yaml) by default. Configuration priority: **YAML file &lt; environment variables**. Set `ACTWEAVE_CONFIG_FILE` to load another file. Unknown YAML fields, multi-document YAML, and invalid booleans fail startup.

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
| AAP signing | `ACTWEAVE_AAP_TOKEN_ENDPOINT`, `ACTWEAVE_AAP_SIGNING_ACTIVE_KID`, `ACTWEAVE_AAP_SIGNING_PRIVATE_KEY_FILE`, `ACTWEAVE_AAP_SIGNING_GENERATE_IF_MISSING`, `ACTWEAVE_AAP_SIGNING_MAX_TOKEN_TTL_SECONDS` |
| MinIO | `ACTWEAVE_MINIO_ENDPOINT`, `ACTWEAVE_MINIO_ACCESS_KEY`, `ACTWEAVE_MINIO_SECRET_KEY`, `ACTWEAVE_MINIO_USE_SSL`, `ACTWEAVE_MINIO_REGION` |
| Bootstrap admin | `ACTWEAVE_BOOTSTRAP_ADMIN_USERNAME`, `ACTWEAVE_BOOTSTRAP_ADMIN_PASSWORD`, `ACTWEAVE_BOOTSTRAP_ADMIN_DISPLAY_NAME`, `ACTWEAVE_BOOTSTRAP_ADMIN_LOCALE`, `ACTWEAVE_BOOTSTRAP_ADMIN_TIMEZONE` |

Notes:

- `encryption.masterKey` must be a Base64-encoded 32-byte key.
- Bootstrap username + password (≥ 12 chars) must be provided as a pair. They create the first `PLATFORM_ADMIN` only when `users` is empty; later changes to bootstrap config do **not** update existing users.
- Platform users are managed at runtime via the UI or `/api/v1/admin/users`. Workspace roles live in `workspace_members` and are separate from platform roles.
- At least one `ACTIVE` + `PLATFORM_ADMIN` is always retained.
- AAP Access Tokens use **EdDSA/Ed25519**, not the HS256 user session secret. Local dev may generate keys under `backend/.local/` with mode `0600` when missing; production must set `generateIfMissing=false` and mount a stable PKCS#8 PEM.
- Public JWKS: `GET /api/agent-access/v1/.well-known/jwks.json`

For AAP client authentication, scopes, SSE, and errors, see the **[AAP Integration Guide](./docs/aap-integration-guide.md)**.

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

- The API server applies embedded pending migrations before listening. Dirty or failed migration state prevents startup.
- Concurrent instances serialize migrations with a PostgreSQL advisory lock.
- Manual ops: `go run ./cmd/migrate version`, `go run ./cmd/migrate down 1` (image binary: `/app/actweave-migrate`).
- Requires Go `1.25.x`.

Data volumes (Compose): `postgres-data`, `redis-data`, `minio-data`. `docker compose down` keeps data; `docker compose down -v` **destroys** local volumes. Production restore needs PostgreSQL **and** MinIO **and** the matching encryption keys.

## Console capabilities (high level)

- Overview, Workspaces, Agents, Service Connections, OpenAPI import, model API config, Tools, Workflow editor, intelligent orchestration, conversational console, audit log, and platform user admin (admins only).
- Workflow mainline: `WorkflowGraphDraft` → compilation → `CompiledExecutionPlan` → `WorkflowRevision` → runtime. Legacy `Workflow.dsl` / canvas write paths are removed.
- Tool calls go through an HTTP executor with SSRF guards, secret injection rules, response limits, and idempotency. Phase-1 does not ship Internal/MCP/Connector/Shell executors.
- Intelligent orchestration uses multi-turn generate sessions (`smart-dag.v2`) bound to an Agent with a usable model config — not a model-free rule fake path.

## Known limitations

- Frontend targets desktop layouts (`min-width` around 1180px).
- No unified backend lint/format scripts; rely on `go test` / `go vet`.
- CI workflows may still be minimal in this tree.
- Some advanced Workflow node types are supported in the backend but not fully exposed in the editor UI.

## License / contact

For third-party Agent integration, start with:

1. [docs/aap-integration-guide.md](./docs/aap-integration-guide.md)  
2. [docs/openapi/agent-access-v1.yaml](./docs/openapi/agent-access-v1.yaml)  
3. [sdk/typescript](./sdk/typescript/) (optional client)
