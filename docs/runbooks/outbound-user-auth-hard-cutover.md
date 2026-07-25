# Outbound User Auth Hard Cutover Runbook

| Field | Value |
| --- | --- |
| Issue | ZKL-51 |
| Migration | `000060_outbound_identity_hard_cutover` |
| Product | v0.3 |
| Technical | v0.2 |
| Checklist | v1.0 |

## Purpose

Execute the irreversible T4 hard cutover of legacy shared-account HTTP outbound
identity to dual-mode `BROKER_OBO` / `REQUEST_PASSTHROUGH`, with physical Secret
deletion. This runbook is **authorization-gated**: it does not itself run
production mutation.

## Non-negotiable boundaries

1. After `000060` commits, **roll-forward only**. Do not restore deleted Secrets
   from snapshots into the live primary.
2. No third mode, `NONE`, SYSTEM user-scoped exception, or legacy feature flag.
3. Token / Assertion / Broker body must never enter PostgreSQL, MinIO, Redis,
   logs, audit payloads, Trace, Chat, or model context.
4. Preflight SQL and aggregation counts only — no Secret IDs / names / ciphertext.

## Order of operations

```text
1. Staging full rehearsal (59 → 60, block fixtures, dual-mode smoke)
2. Production maintenance window announced
3. Drain all live AgentRuns / WorkflowExecutions to zero
4. Terminate old boots / instances (no residual Vault affinity owners)
5. Read-only preflight (docs/design/outbound-user-auth-000060-preflight-runbook.sql)
6. Prepare exact binary + migration artifact (checksum recorded)
7. Stop write traffic for HTTP Tool configuration + execution
8. Apply 000060 in a single transaction
9. Prove: target credential_secret_id refs = 0; candidate secrets/versions = 0
10. Roll-forward validation: dual-mode management API, JWKS, Broker/Vault paths
11. Open traffic; watch metrics/alerts
```

## Preflight (read-only)

Run `docs/design/outbound-user-auth-000060-preflight-runbook.sql` against the
target database. Expect:

| Check | Expectation |
| --- | --- |
| HTTP target Connections | Count known; will become DISABLED + MIGRATION_REQUIRED |
| Candidate Secrets | Only those referenced by target Connections |
| Model-config share | **Must be zero** or abort |
| Non-target Connection share | **Must be zero** or abort |
| Dirty migration flag | Clean |

## Apply

```bash
# Staging / authorized production only
cd backend
go build -o bin/migrate ./cmd/migrate
go build -o bin/server ./cmd/server
# Record checksums
shasum -a 256 bin/migrate bin/server
# Apply (single transaction inside migration)
./bin/migrate up   # or platform-equivalent
```

Immediately after up:

```sql
-- Aggregation only (no Secret identifiers)
SELECT migration_state, status, count(*)
FROM service_connections
WHERE deleted_at IS NULL
GROUP BY 1, 2;

-- Candidate secret residual must be zero for hard-cut targets
-- (exact queries in preflight runbook)
```

## Roll-forward validation

1. Provider create rejects `service-auth.v1` / shared modes.
2. Connection create accepts only `BROKER_OBO` / `REQUEST_PASSTHROUGH`.
3. DTO exposes `machineCredentialConfigured` only (no Secret IDs).
4. `GET /api/outbound-identity/v1/.well-known/jwks.json` returns public OKP keys only.
5. Trial with passthrough envelope attaches; production rejects `outboundCredentials`.
6. Debug attach: message body has attachment id only.
7. Owner-loss passthrough resume returns `OUTBOUND_CREDENTIAL_EXPIRED`.

## Alerts & ops responses

| Signal | Action |
| --- | --- |
| Broker 5xx / latency | Fail closed; do not enable legacy fallback |
| Vault capacity | Reject new attach; do not LRU-evict other Subjects |
| Owner loss | Fail closed for passthrough roots; pure Broker may re-exchange |
| Suspected Token leak | Rotate keys, scrub logs, stop traffic, security incident path |
| Migration blocked by shared Secret | Fix consumers first; re-run preflight |

## Rollback boundary

| Phase | Allowed |
| --- | --- |
| Before `000060` commit | Abort maintenance; restore traffic on old binary |
| After commit | **No** production down to restore Secrets; only roll-forward fixes |
| Disaster snapshot restore | Isolate environment; re-apply `000060`; re-prove deletion before open |

## Related docs

- `docs/design/outbound-user-auth-product-design.md` v0.3
- `docs/design/outbound-user-auth-technical-design.md` v0.2
- `docs/design/outbound-user-auth-implementation-checklist.md` v1.0
- `docs/design/outbound-user-auth-000060-preflight-runbook.sql`
