# agent_graph_snapshot.v1 raw schema (producer-derived)

Source of truth: `freezeGraphSnapshot` + `snapshotAgentNode` + `capabilitySnapshotJSON`
(`chatruntimebridge/delegation.go`) and `SnapshotJSON` (`agentdelegation/snapshot.go`).
Validated by `parseSnapshotStrict` **before** `json.Unmarshal` defaults.

## Root object

| path | required | nullable | kind | notes |
| --- | --- | --- | --- | --- |
| schemaVersion | yes | no | string | exact `agent_graph_snapshot.v1` |
| rootAgentId | yes | no | string | non-empty, no pad, equals a node agentId |
| maxDepth | yes | no | integer | > 0 |
| maxTotalDelegations | yes | no | integer | > 0 |
| maxPerBinding | yes | no | integer | > 0 |
| nodes | yes | no | array | explicit `[]` legal |
| edges | yes | no | array | explicit `[]` legal |
| builtAt | yes | no | string | RFC3339 / RFC3339Nano |
| frozenRemotesByCaller | yes | no | object | keys = exactly all node agentIds |
| remotesFrozen | yes | no | bool | must be true for freeze-only |
| extra | no | no | object | **rejected** (producer never emits) |

Unknown root keys rejected. Duplicate keys rejected recursively.

## nodes[]

| path | required | nullable | kind |
| --- | --- | --- | --- |
| agentId | yes | no | string non-empty, unique |
| depth | yes | no | integer ≥ 0 |
| modelConfigId | yes | no | string non-empty |
| modelConfigLockVersion | yes | no | integer ≥ 1; **exact** agreement with nested locks |
| modelSnapshot | yes | no | object — **node model schema** (below) |
| agentSnapshot | yes | no | object — **node agent-binding.v1** (below) |
| capabilitySnapshot | yes | no | object — **capability-snapshot.v1** (below) |
| name | no | no | string |
| promptRevisionId | no | no | string |
| promptRevisionHash | no | no | string |

Unknown node keys rejected.

### nodes[].modelSnapshot (node producer — NOT root run.ModelSnapshot)

Producer: `chatruntimebridge.MarshalNodeModelSnapshot`. Distinct from root
`marshalModelSnapshot`: credentialSecretId is always present (JSON null when
unbound); root may omit the key and rejects explicit null.

| path | required | nullable | kind | notes |
| --- | --- | --- | --- | --- |
| id | yes | no | string | non-empty; **must equal** node.modelConfigId |
| provider | yes | no | string | non-empty, no pad |
| apiBase | yes | no | string | non-empty; **must** pass `modelapi.ValidateAgenticAPIBase` (absolute http/https, host required; empty rejected) |
| modelName | yes | no | string | non-empty |
| options | yes | no | object | always `{}` minimum; never null |
| credentialSecretId | yes | **yes** | string\|null | key always present; `null` when unset; else non-empty |
| lockVersion | yes | no | integer | ≥ 1; **must equal** node.modelConfigLockVersion (unconditional) |
| status | yes | no | string | non-empty |
| agenticCapabilities | yes | no | object | `{}` legal unset |
| runtimeCapabilities | yes | no | object | `{}` legal unset |
| toolDisclosurePolicy | yes | no | object | `{}` legal unset |

Unknown keys rejected (e.g. `forged`).

### nodes[].agentSnapshot (agent-binding.v1)

| path | required | nullable | kind | notes |
| --- | --- | --- | --- | --- |
| schemaVersion | yes | no | string | exact `agent-binding.v1` |
| agentId | yes | no | string | **must equal** node.agentId |
| name | yes | no | string | empty legal |
| roleDescription | yes | no | string | empty legal |
| promptRevisionId | yes | no | string | empty legal |
| promptRevisionHash | yes | no | string | empty legal |
| modelConfigId | yes | no | string | **must equal** node.modelConfigId |
| modelConfigLockVer | yes | no | integer | ≥ 1; **must equal** node.modelConfigLockVersion (unconditional) |

### nodes[].capabilitySnapshot (capability-snapshot.v1)

| path | required | nullable | kind | notes |
| --- | --- | --- | --- | --- |
| schemaVersion | yes | no | string | exact `capability-snapshot.v1` (arbitrary strings fail) |
| releases | yes | no | array | explicit `[]` legal |

#### releases[]

| path | required | nullable | kind |
| --- | --- | --- | --- |
| capabilityId | yes | no | **canonical UUID** |
| releaseId | yes | no | **canonical UUID** |
| kind | yes | no | enum `TOOL` \| `WORKFLOW` |
| callableName | yes | no | string non-empty; unique (case-insensitive) in releases |
| callableDescription | yes | no | string (empty legal) |
| inputSchema | yes | no | object |
| outputSchema | yes | no | object |
| riskLevel | yes | no | enum `LOW` \| `MEDIUM` \| `HIGH` \| `CRITICAL` |
| sideEffectLevel | yes | no | enum `NONE` \| `READ` \| `WRITE` \| `IRREVERSIBLE` |
| requiresConfirmation | yes | no | bool |
| connectionId | no | no | **canonical UUID** when present |

## edges[]

| path | required | nullable | kind | notes |
| --- | --- | --- | --- | --- |
| bindingId | yes | no | string | **canonical UUID**; **unique** across edges |
| callerAgentId | yes | no | string | canonical UUID; must exist in nodes |
| targetAgentId | yes | no | string | canonical UUID; must exist in nodes; ≠ caller |
| callableName | yes | no | string | unique per caller (case-insensitive) |
| mode | yes | no | enum | `INLINE` \| `TASK` only (`validMode`) |
| contextPolicy | yes | no | enum | **`TASK_ONLY` only** (write-path `validContextPolicy`; SUMMARY/SELECTED_MESSAGES rejected) |
| version | yes | no | integer | ≥ 1 |
| protocol | yes | no | enum | **`INTERNAL` only** on edges (A2A lives in remotes map) |
| description | no | no | string |
| externalRef | no | no | string |

Empty object `{}` edges rejected. Unknown enum strings fail closed.
Self-edges and directed cycles fail closed.

## frozenRemotesByCaller

- Map keys: exactly the set of `nodes[].agentId` (no foreign keys, no missing keys).
- Values: non-null arrays (explicit `[]` legal).

### remotes[] members

| path | required | nullable | kind | notes |
| --- | --- | --- | --- | --- |
| id | yes | no | string | **canonical UUID**; unique per caller |
| callerAgentId | yes | no | string | **must equal** map key (canonical UUID) |
| callableName | yes | no | string | unique per caller (case-insensitive) |
| endpointUrl | yes | no | string | HTTPS SSRF policy via `egressguard.ValidateRemoteAllowlist` |
| allowedHosts | yes | no | array of non-empty strings | **non-empty**; must cover endpoint host |
| timeoutMs | yes | no | integer | ≥ 0 |
| version | yes | no | integer | ≥ 1 |
| description | no | no | string |
| agentCardUrl | no | no | string | same SSRF/allowlist policy when present |
| authSecretRef | no | no | string | empty or `secret:<workspaceUUID>:<secretUUID>` |

`[{}]` fails. Empty allowlist fails. Duplicate `(id)` or `(callableName)` for the same caller fails.

## Cross-binds summary

1. `rootAgentId` ∈ nodes
2. `nodes[].modelSnapshot.id` = `nodes[].modelConfigId`
3. `nodes[].agentSnapshot.agentId` = `nodes[].agentId`
4. `nodes[].agentSnapshot.modelConfigId` = `nodes[].modelConfigId`
5. **Lock identity (required):** `nodes[].modelConfigLockVersion` == `modelSnapshot.lockVersion` == `agentSnapshot.modelConfigLockVer` (all ≥ 1). Missing node lock fails closed (no sentinel).
6. Edge caller/target ∈ nodes; unique bindingId; unique caller+callable
7. Remote map keys = node set; remote.callerAgentId = key; unique id/callable per caller

## Domain-semantic layer (cycle 7)

Structural closure alone is insufficient. After raw schema validation,
`validateGraphSnapshotSemantics` enforces the contract in
**`snapshot_semantic.md`** (topology, canonical UUIDs, internal-edge enums,
capability domains, remote SSRF/allowlist via `egressguard` =
CreateRemote policy). Failures prevent `ParseSnapshot` success and map to
`AGENTIC_GRAPH_SNAPSHOT_REQUIRED` on Agentic initial — never
`AGENTIC_DELEGATION_MIGRATION_PENDING` for an invalid graph.
