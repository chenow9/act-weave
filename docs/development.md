# Development

[中文](./development.zh-CN.md) · [Documentation home](./README.md)

This page gathers existing development entry points. Before running frontend and backend separately, make PostgreSQL, Redis, MinIO, and the local configuration in `backend/config.yaml` available. The shortest full-stack path remains `docker compose up --build`.

## Runtime requirements

| Area | Repository evidence |
| --- | --- |
| Frontend | `frontend/package.json` pins Node `22.22.3` and npm `10.9.8`; use `package-lock.json`. |
| Backend | `backend/go.mod` declares Go `1.25.0`; the Dockerfile builds with Go `1.25.11`. |
| Data services | PostgreSQL, Redis, and MinIO; root Compose can provide them locally. |

## Frontend

```bash
cd frontend
npm ci
npm run dev
```

Useful checks:

```bash
npm run lint
npm run format:check
npm test -- --run
npm run type-check
npm run build
npm run e2e:smoke
```

## Backend

```bash
cd backend
go run ./cmd/server
```

Useful checks:

```bash
go vet ./...
go test ./...
go build ./cmd/server
```

Backend startup attempts to apply embedded migrations. Manual migration commands are for controlled operation only; see [database and object storage](./deployment.md#database-and-object-storage).

## AAP, SDK, and protocol changes

- Public HTTP runtime contract: [`docs/openapi/agent-access-v1.yaml`](./openapi/agent-access-v1.yaml).
- Runtime protocol schemas: `backend/internal/protocolschema/schemas/aap/v1/`; generated outputs include SDK types and OpenAPI components.
- TypeScript SDK: [`sdk/typescript/`](../sdk/typescript/). In that directory run `npm ci`, `npm run type-check`, `npm run check:readme-quickstart`, `npm test`, and `npm run build`.

After changing protocol schema, run:

```bash
make generate
make protocol-compat-check
```

Do not change only generated SDK or OpenAPI output. The compatibility check compares the current schema against a baseline; relevant CI lives in `.github/workflows/`.

## Documentation and screenshots

- Maintain product/document entry points from [documentation home](./README.md); the Chinese README is the primary content source for bilingual messaging.
- Do not pile development commands or every Console screenshot into the project home. Keep the product loop and shortest start there; keep detail in this directory.
- Screenshots use a fictional demo Workspace. Read [regenerate screenshots](./product-tour.md#regenerate-screenshots) first because the script clears existing PNGs.

## Related documentation

- [Contribution guide](../CONTRIBUTING.md)
- [AAP integration guide](./aap-integration-guide.md)
- [Architecture](./architecture.md)
