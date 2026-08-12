# agent_graph_snapshot.v1 — domain semantic contract (cycle 7)

Source of truth: producers + repository validators. Enforced by
`validateGraphSnapshotSemantics` after raw structural closure
(`parseSnapshotStrict`) and before `ParseSnapshot` returns success.

| Rule | Domain source (reused) | Fail closed when |
| --- | --- | --- |
| Canonical UUID identity | `uuid.Parse` + `id.String()==raw` (same as `isCanonicalModelConfigUUID`) | agentId, rootAgentId, modelConfigId, bindingId, remote id, capabilityId, releaseId, connectionId non-canonical |
| **Model lock identity (3-layer)** | producer `snapshotAgentNode` always sets node `ModelConfigLockVer` + nested `lockVersion` + `modelConfigLockVer` to the same live lock (≥1) | For **each** of `nodes[].modelConfigLockVersion`, `modelSnapshot.lockVersion`, `agentSnapshot.modelConfigLockVer`: missing, null, wrong type (`"1"`), zero, negative. Any pairwise or 3-way divergence. Matching triple is the only legal form. |
| Node model apiBase | `modelapi.ValidateAgenticAPIBase` | empty, non-absolute, userinfo, bad scheme, empty host |
| Node model provider/modelName | `modelconfig.validateNewConfig` nonempty rule | empty / padded |
| Capability kind | `capability.repository` create: `TOOL`\|`WORKFLOW` | other strings |
| Capability riskLevel | same: `LOW`\|`MEDIUM`\|`HIGH`\|`CRITICAL` | other |
| Capability sideEffectLevel | same: `NONE`\|`READ`\|`WRITE`\|`IRREVERSIBLE` | other |
| Internal edge protocol | producer `BindingsToEdges` / freeze always `INTERNAL` | any non-`INTERNAL` (incl. `A2A`) |
| Internal edge mode | `validMode` → `INLINE`\|`TASK` | other |
| Internal edge contextPolicy | `validContextPolicy` → **only** `TASK_ONLY` | `SUMMARY`, `SELECTED_MESSAGES`, other |
| No self-edge | `validateCreate` `ErrSelfLoop` | caller==target |
| Binding uniqueness | integrity + create namespace | duplicate bindingId / caller+callable |
| Topology root depth | producer BFS `seen[root]=0` | root depth ≠ 0 |
| Reachability | producer BFS from root via INTERNAL edges | orphan node |
| Depth == shortest path | producer `seen[tid]=depth+1` | declared depth mismatch |
| No directed cycles | freeze tree is DAG from root | cycle / back-edge |
| Depth ≤ maxDepth | producer stops expanding at maxDepth | node depth > maxDepth |
| Remote allowlist+URL | `egressguard.ValidateRemoteAllowlist` (= a2agateway CreateRemote) | empty allowlist, http/file/userinfo, host not listed, private/special IP |
| Remote authSecretRef | `egressguard.ValidateAuthSecretRef` (format; workspace match when known) | bad format |
| Remote id UUID | CreateRemote `validUUID` + freeze canonical form | non-canonical id |

## Producer notes

- `freezeGraphSnapshot` + `BindingsToEdges` emit only `protocol=INTERNAL`, `contextPolicy=TASK_ONLY`.
- Node model apiBase is live config value; empty is rejected at parse (producer should not emit empty for usable models).
- Remotes freeze copies CreateRemote-validated rows; hostile frozen JSON is still re-validated at parse.
