# Getting started

[中文](./getting-started.zh-CN.md) · [Documentation home](./README.md)

This page covers only local full-stack startup. It uses the repository’s `docker-compose.yml`; it is not a production deployment design.

## Prerequisites

- Docker Desktop (or a compatible Docker Engine)
- Docker Compose v2 (`docker compose`)
- Network access required to build and pull images/dependencies

## Start

```bash
git clone https://github.com/chenow9/act-weave.git
cd act-weave
docker compose up --build
```

The first build pulls images, builds frontend and backend, starts PostgreSQL/Redis/MinIO, then starts the backend and Console. The backend applies embedded database migrations before listening; do not add manual migration steps to this Compose quick path.

## Local addresses and development credentials

| Service | Address |
| --- | --- |
| Console | <http://127.0.0.1:5174> |
| Backend health | <http://127.0.0.1:8082/api/v1/health> |
| PostgreSQL | `127.0.0.1:15432` |
| Redis | `127.0.0.1:16379` |
| MinIO API / Console | <http://127.0.0.1:9000> / <http://127.0.0.1:9001> |

An empty PostgreSQL volume creates this development administrator:

| Username | Temporary password |
| --- | --- |
| `admin` | `actweave-admin-dev-change-me` |

The account and Compose database/MinIO credentials are for local development only. Change the password after first sign-in and never carry these values into production.

## Next steps

1. Read [concepts](./concepts.md) to distinguish Console API, AAP, A2A, and Tools.
2. Configure a Model API, Provider, and Service Connection in a Workspace; import/create and publish Tools.
3. Create an Agent, bind published Tools or Workflows, and trial it in the Console.
4. For application integration, read the [AAP guide](./aap-integration-guide.md) and use the [OpenAPI contract](./openapi/agent-access-v1.yaml).

## Local troubleshooting

- Check `docker compose ps` and the backend health endpoint.
- Backend, frontend, and dependencies use Compose service names; use the ports above from the host.
- Compose volumes are `postgres-data`, `redis-data`, and `minio-data`. `docker compose down -v` permanently deletes local development data.

For configuration, secrets, TLS, production object storage, and edge proxy concerns, see [deployment](./deployment.md).
