# Runbook: AAP File Upload (IC-11 / PR-9)

Release-blocking before gray. Do **not** set `agentAccess.files.enabled=true` in production until staging GC health, MinIO reachability, and this checklist are signed off.

## Feature flags (default closed)

Config path: `agentAccess.files` in `backend/config.yaml`.

| Field | Default | Meaning |
| --- | --- | --- |
| `enabled` | **`false`** | Master switch for File REST routes; when false, file routes conceal as **404** |
| `allowAllWorkspaces` | `false` | When true, all workspaces pass the files gate |
| `workspaceIds` | `[]` | Allowlist when not allow-all |
| `allowAllClients` | `false` | Client allowlist master |
| `clientIds` | `[]` | Client allowlist |
| `maxBytes` | `26214400` (25 MiB) | Hard size cap |
| `maxPendingPerWorkspace` | `20` | Concurrent `PENDING_UPLOAD` cap |
| `maxReadyBytesPerWorkspace` | `5 GiB` | Soft READY total-bytes quota |
| `publicUploadBaseUrl` | empty | Public base used for presign rewrite / docs when MinIO is not client-reachable |
| `runtimeMultimodal` | `false` | Orthogonal: gates model assembly of `input_file`; createRun fails closed with `FILE_RUNTIME_UNAVAILABLE` when false |
| `runtimeOutboundAttachments` | `false` | Orthogonal: gates `IngestGenerated` and `actweave.publish_attachment` injection. Env `ACTWEAVE_AAP_FILES_RUNTIME_OUTBOUND_ATTACHMENTS` |
| `virusScan.enabled` / `required` | `false` | Optional **inbound** pipeline stage. Outbound ingest does **not** run a virus scanner |

Env overrides (see `config.AgentAccessFilesConfig`): prefer `ACTWEAVE_AAP_FILES_ENABLED` and related `ACTWEAVE_AAP_FILES_*` when present. Changes require process restart.

**Orthogonal gates**

1. Global AAP `agentAccess.feature.enabled` — whole Agent Access surface.
2. `files.enabled` + workspace/client allowlist — File routes, inbound upload, and outbound ingest.
3. `runtimeMultimodal` — createRun with `input_file` and model multimodal assembly.
4. `runtimeOutboundAttachments` + Agent policy `enableOutboundAttachments` + frozen `toolCalling ∈ {function_calling, native_client_search}` — assistant `output_file`. `toolCalling: none` does **not** inject the tool (Run still succeeds as text).

## Outbound attachments (gray)

v1 model publish is the text-only platform tool `actweave.publish_attachment` (`text/plain` \| `text/csv` \| `text/markdown` \| `application/json`, UTF-8 `text` ≤ 256 KiB, **no** `base64`). Successful ingest writes READY `aap_files` with **`purpose=AGENT_OUTPUT`** and `source_run_id`. There is **no public ingest HTTP**; `createFile` does not accept `AGENT_OUTPUT`.

**No virus scanner / webhook DLP on outbound.** Inbound `PipelineWorker.runVirusScan` is a stub that always reports clean; do not invent a fail-closed outbound `VirusScanner` that would break existing `virusScan.required=true` deployments.

Demos: Mock story `export-csv` (**生成本月对账单**) + Live hydrate by `fileId`. Console preview/download is

```
GET /api/v1/workspaces/:wid/sessions/:sid/messages/:mid/files/:fileId/content
```

(`ActionView` + session ownership + `fileId` on that message’s durable parts). Not an AAP route.

Integrator contract: [AAP integration guide §9.3](../aap-integration-guide.md#93-outbound-attachments-optional-additive).

## Rollback

1. Turn **`runtimeOutboundAttachments` off first** and restart — stops new outbound writes / tool injection (`IngestGenerated` and `actweave.publish_attachment`). Leave the workspace/client allowlist and `files.enabled` on so historical path A/B downloads still work.
2. Stop new Agent-policy `enableOutboundAttachments: true`.
3. **Do not** roll back the snapshot parser that understands `enableOutboundAttachments`. In-flight Runs may already have written `"enableOutboundAttachments":true`; pre-parser binaries `DisallowUnknownFields` and would fail those snapshots.
4. **Do not** run `000023_aap_file_outbound` down while `AGENT_OUTPUT` rows exist (the CHECK rollback raises). Do **not** delete `aap_files` / permanent objects as the primary rollback.
5. Set `agentAccess.files.enabled: false` **last** (or clear the files allowlist). Either conceal action 404s File create/complete/download, including historical outbound files. Non-file AAP routes stay up.
6. Pipeline / staging GC / retention purge workers idle when there is no work; leave them running.
7. Optional: keep `runtimeMultimodal: false` so any residual `input_file` createRun fails closed.

## MinIO reachability

| Check | How |
| --- | --- |
| Compose buckets | `docker compose` creates `actweave-aap-staging` and `actweave-aap-files` (see `docker-compose.yml` `mc mb`) |
| Server endpoint | `storage.minio.endpoint` reachable from API process |
| Client PUT path | Presigned URL host must be reachable from the **uploader** (browser/partner). If MinIO is private, set `publicUploadBaseUrl` / reverse-proxy same-host path so the signed PUT host is public |
| Health | Stat a known staging key or list buckets with ops credentials; alert on persistent 5xx / connection refused |

### `publicUploadBaseUrl`

- Documents / rewrites the base clients use for staging PUT when the internal MinIO endpoint is not internet-reachable.
- Production matrix: same-host reverse proxy **or** explicit public MinIO hostname; never log full presigned URLs.
- v1 has no server-mediated PUT fallback as the public main path.

## Workers

| Worker | Role | Idle-safe when files disabled? |
| --- | --- | --- |
| `PipelineWorker` | Claims `aap_file_processing_jobs` (promote / mime / virus / webhook) | Yes (empty claim) |
| `StagingGCWorker` | §5.4.3 / KD-21 residual staging delete + markers | Yes |
| Preview/EXPIRING purge | Deletes expired EXPIRING ciphertext including `AAP_FILE` / `AAP_FILE_DERIVED` | Yes |

### Staging GC selection (§5.4.3)

```text
staging_object_key IS NOT NULL
AND staging_deleted_at IS NULL
AND (
  (status = PENDING_UPLOAD AND staging_expires_at < now())
  OR status IN (FAILED, EXPIRED)
  OR stored_object_id IS NOT NULL
  OR promote job attempt >= maxAttempts
)
```

Actions:

1. `Delete(staging)` — **missing object = ok**.
2. Set `staging_deleted_at`, clear `staging_object_key`.
3. If still `PENDING_UPLOAD` (expired window) → `status=EXPIRED` + `FILE_UPLOAD_EXPIRED`.

FAILED leftover staging does **not** count toward READY byte quota; it does contribute to `aap_file_staging_orphan_bytes` until GC clears it.

**Gray gate:** after a forced promote-failure fixture, one GC pass must leave **no** residual staging object for that file.

### EXPIRING permanent purge

- Default retention: 30d EXPIRING on promote (`retention_until`).
- First successful createRun reference can clear retention (KD-16 promote-to-permanent).
- Expired unpromoted `AAP_FILE` / `AAP_FILE_DERIVED` bodies are claimed by the shared EXPIRING purge worker (`PurgeBody`); missing blob = ok; metadata tombstone keeps `body_purged_at`.

## Metrics (low cardinality only)

Process metrics (no `file_id`, token id, or filename labels):

| Series | Meaning |
| --- | --- |
| `aap_file_create_total` | Upload intents created |
| `aap_file_complete_total` | Successful complete CAS |
| `aap_file_promote_duration_ms` | Last promote latency sample |
| `aap_file_processing_*` | Stage/result counters (promote/mime/virus/webhook) |
| `aap_file_download_*` | Download path results |
| `aap_file_pending_upload_gauge` | Global `PENDING_UPLOAD` count |
| `aap_file_staging_orphan_bytes` | Sum `size_bytes` of residual staging rows |
| `aap_file_ingest_generated_total` | Outbound `IngestGenerated` (`ok` / `disabled` / `denied` / `error`) |
| `aap_outbound_publish_total` | Publish tool (`ok` / `denied` / `error` / `disabled` / `unsupported`) |
| `aap_outbound_attach_preflight_fail_total` | Terminal attach degraded to text/A2UI |
| `aap_outbound_turn_files` | Per-turn attached count (0/1/2/4/8 buckets) |

Do **not** alert on “virus required but scanner nil” for outbound — v1 outbound has no scanner.

## Logging allowlist

Only index-safe fields (design §10.2), including: `file_id`, `file_status`, `processor_id`, `media_class`, `delivery_id`, `stage`, `download_purpose`.

**Never** log: presigned URL, query string, Authorization, download token plaintext, object bodies. Field names must not contain `token` / `secret` substrings.

## Ops proxy guidance (edge / reverse proxy)

**Not** shipped frontend package behavior. The compose/frontend `nginx.conf` does **not** include AAP file-stream locations. Operators who front AAP with nginx (or another reverse proxy) must apply stream-friendly settings on the edge for these paths:

| Path pattern | Purpose |
| --- | --- |
| `.../files/{id}/content` | Bearer GET decrypt stream (up to ~25 MiB) |
| `.../files/downloads/{tokenId}` | Opaque download-token proxy stream |

Recommended nginx (or equivalent) settings on those locations:

- `proxy_buffering off`
- `proxy_request_buffering off`
- `gzip off`
- response header `X-Accel-Buffering: no` (or `add_header X-Accel-Buffering "no" always;`)
- `proxy_read_timeout` **≥ 120s** (send timeout similarly if your edge uses separate send/read knobs)

Rationale: decrypt/content streams must not be fully buffered at the proxy; idle buffering can delay first byte or truncate large responses under default gateway timeouts.

Keep **`agentAccess.files.enabled` default `false`** until staging GC health, MinIO reachability, and this runbook’s gray checklist are signed off. Disabling the flag conceals file routes as **404**; edge proxy rules can remain in place without enabling the product surface.

## Compose / local checklist

1. Postgres + MinIO up; migrations applied through `000006_aap_files` (outbound also needs `000023_aap_file_outbound`).
2. `agentAccess.files.enabled: false` in default `config.yaml` (confirm before gray).
3. Staging GC worker starts with the API process; metrics gauges refresh each pass.
4. Optional local enable: allowlist a single workspace + client; leave `runtimeMultimodal` and `runtimeOutboundAttachments` false until those ICs are green.
5. Exercise: create → PUT staging → complete → poll READY; force integrity fail → GC → no staging object.
6. If an edge proxy fronts AAP: confirm ops proxy guidance above for content/download streams (operator config only).
7. Outbound (after inbound gray): set `runtimeOutboundAttachments: true` for the same allowlist; Agent `enableOutboundAttachments`; model `toolCalling` must be `function_calling` or `native_client_search`. Accept Live Demos CSV cards and Console session+message proxy. Confirm `toolCalling: none` Agents still reply with text.

## Gray rollout sequence (design §12)

1. Dark ship: migrations + code, `files.enabled=false`, `runtimeOutboundAttachments=false`.
2. Confirm GC worker health + MinIO reachability table above.
3. Internal allowlist workspace/client.
4. Partner gray + `file:read` / `file:write` scopes.
5. Enable `runtimeMultimodal` separately for model E2E.
6. Enable `runtimeOutboundAttachments` separately for outbound E2E (policy + toolCalling). Do not roll back the snapshot parser after the first `"enableOutboundAttachments":true` snapshot.
7. Rollback drill: `runtimeOutboundAttachments=false` first, then `files.enabled=false`. Do not down `000023` with `AGENT_OUTPUT` data.

## Related

- Integration guides: `docs/aap-integration-guide.md` / `.zh-CN.md` (outbound: §9.3)
