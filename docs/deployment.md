# Deployment

[中文](./deployment.zh-CN.md) · [Documentation home](./README.md)

This page describes the repository’s startup and configuration boundary. It does not claim production readiness, high availability, or security certification. A production deployer must validate its own network, secrets, backup, monitoring, capacity, and compliance controls.

## Local Compose

The root `docker-compose.yml` runs:

- PostgreSQL 17 (host `15432`)
- Redis 7 (host `16379`)
- MinIO (API `9000`, Console `9001`) plus a bucket-initialization container
- Go backend (host `8082`)
- Nginx-served frontend (host `5174`)

Use [getting started](./getting-started.md) for the command and development defaults. These values come from `docker-compose.yml` and `backend/config.yaml` and are for local development only.

## Values that must change before production

Do not copy the development keys and bootstrap administrator settings in `backend/config.yaml`. At minimum, provide the following through a protected configuration location or Secret Manager/KMS:

| Area | Confirm or replace |
| --- | --- |
| Data and encryption | `ACTWEAVE_POSTGRES_DSN`, `ACTWEAVE_JWT_SECRET`, `ACTWEAVE_SECRET_MASTER_KEY`; use managed PostgreSQL with a backup policy. |
| Object storage | MinIO/S3-compatible endpoint, access key, secret key, bucket lifecycle, and backups. |
| AAP signing | `ACTWEAVE_AAP_TOKEN_ENDPOINT`, active key ID, private-key file, rotation/prepublished public keys, and token TTL. AAP access tokens use EdDSA/Ed25519. |
| Bootstrap identity | `ACTWEAVE_BOOTSTRAP_ADMIN_*`. Bootstrap creates the first platform administrator only when `users` is empty, but development defaults still must not be reused. |
| Domain and transport | Public HTTPS base URL, TLS termination, trusted reverse proxy, CORS, and network-access rules. Configuration validation requires an absolute HTTPS AAP token endpoint outside loopback. |
| Upstream integration | Provider/Connection endpoints, credentials, scopes, environments, outbound identity, and host allowlists. |

YAML has lower precedence than environment variables, and `ACTWEAVE_CONFIG_FILE` can point to a protected configuration file. `backend/config.yaml` and validation in `backend/internal/config` are authoritative for fields.

## Database and object storage

The backend applies embedded pending database migrations before listening and serializes multi-instance migration with a PostgreSQL advisory lock. Manual commands are for controlled maintenance and should not be mixed concurrently with startup migration:

```bash
cd backend
go run ./cmd/migrate version
go run ./cmd/migrate up
```

PostgreSQL is the source of truth for configuration, runs, protocol events, and audit metadata. MinIO holds durable/encrypted objects; Redis is rebuildable event fan-out and cannot replace durable facts. Design backup, restore testing, retention, and access control separately for PostgreSQL and object storage.

## Runtime and reverse proxy

- Console management is `/api/v1`; external Agent Runtime is `/api/agent-access/v1`. Apply separate identity and access policy.
- AAP Run SSE needs streaming proxy behavior. The repository frontend Nginx disables buffering for Run events, but a production edge proxy must still verify read/send timeouts, buffering, and `Last-Event-ID` reconnect behavior.
- AAP file routes are off in default configuration. When enabled, uploaders must reach the presigned URL; file content/download edge proxying needs streaming settings. See the [file-upload runbook](./runbooks/aap-file-upload.md).
- `/metrics` is loopback-only when no bearer token is configured; when configured it requires Authorization. Restrict scraping at the network layer too.

## Feature gates and rollout boundary

| Capability | Default or boundary |
| --- | --- |
| Main AAP runtime plane | `agentAccess.feature` is enabled in repository local config; production can narrow it with workspace/client allowlists. |
| AAP files | `agentAccess.files.enabled: false`; when enabled it is also constrained by workspace/client allowlists, quotas, and `runtimeMultimodal`. |
| LLM context compaction | `runtime.sessionContext.compaction.enabled: false`; enable gradually according to the runbook. |
| Tool force publish | Available only to platform administrators when `tools.allowForcePublish` allows it; not the standard publishing path. |
| A2A no-auth mode | Rejected by default; available for local testing only through an explicit environment switch. |

## Rollout checklist

1. Replace all development passwords, JWT/encryption/AAP signing keys, and validate rotation and revocation.
2. Point PostgreSQL, object storage, Model APIs, and upstream Providers to controlled environments; validate network/host allowlists and least-privilege scopes.
3. Configure HTTPS, Clients/grants, CORS (only if direct browser access is required), and SSE reconnect tests for AAP. A BFF is the safer default CORS starting point.
4. Check Tool Schema, Connection, test, and publish state; understand disable/rollback impact.
5. Check audit visibility, retention, log redaction, and object-storage access; never log tokens, secrets, or presigned URLs.
6. Run backup/restore, health, monitoring, and capacity exercises. The repository provides no production SLO/SLA or HA topology; the deployer must define them.

## Related documentation

- [Architecture](./architecture.md)
- [Security policy](../SECURITY.md)
- [AAP integration guide](./aap-integration-guide.md)
- [AAP file-upload runbook](./runbooks/aap-file-upload.md)
