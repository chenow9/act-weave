/**
 * Seed a full AAP → Conversation → Run → Model → Agent delegation → Tool
 * audit Trace into README demo workspaces so screenshots are not empty.
 *
 * Chain (fictional ecommerce demo data only):
 *   AAP Client (Acme Partner App)
 *     └─ Conversation
 *         └─ Run (parent commerce agent)
 *             ├─ Model decision
 *             ├─ Delegate to inventory agent
 *             │   └─ check_inventory Tool
 *             ├─ create_order Tool
 *             └─ Final output
 *
 * Requires: docker postgres container `actweave-postgres`, seeded demo workspaces.
 *
 * Usage:
 *   node scripts/seed-readme-audit-trace.mjs
 *   ACTWEAVE_DEMO_LOCALE=en node scripts/seed-readme-audit-trace.mjs
 *   # both locales:
 *   node scripts/seed-readme-audit-trace.mjs --both
 */
import { createHash, randomBytes } from "node:crypto";
import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const ROOT = resolve(__dirname, "..");
const PG_CONTAINER = process.env.ACTWEAVE_PG_CONTAINER || "actweave-postgres";
const PG_USER = process.env.ACTWEAVE_PG_USER || "actweave";
const PG_DB = process.env.ACTWEAVE_PG_DB || "actweave";
const ADMIN_USER_ID = "019fabf0-a0c3-7207-b979-00a750b987d0";
const SYSTEM_PRINCIPAL_ID = "00000000-0000-0000-0000-000000000001";

const both = process.argv.includes("--both");
const locales = both
  ? ["zh-CN", "en"]
  : [process.env.ACTWEAVE_DEMO_LOCALE === "en" ? "en" : "zh-CN"];

function sha256(text) {
  return createHash("sha256").update(text, "utf8").digest("hex");
}

function uuid() {
  // RFC4122-ish random UUID for demo inserts (not crypto-critical).
  const b = randomBytes(16);
  b[6] = (b[6] & 0x0f) | 0x40;
  b[8] = (b[8] & 0x3f) | 0x80;
  const h = b.toString("hex");
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
}

function sqlStr(value) {
  return `'${String(value).replace(/'/g, "''")}'`;
}

function sqlJson(obj) {
  return sqlStr(JSON.stringify(obj));
}

function psql(sql) {
  return execFileSync(
    "docker",
    ["exec", "-i", PG_CONTAINER, "psql", "-U", PG_USER, "-d", PG_DB, "-v", "ON_ERROR_STOP=1", "-t", "-A"],
    { input: sql, encoding: "utf8", maxBuffer: 10 * 1024 * 1024 },
  ).trim();
}

function loadMeta(locale) {
  const outDir =
    locale === "en" ? "docs/images/readme/en" : "docs/images/readme";
  const metaPath = resolve(ROOT, outDir, "demo-workspace.json");
  if (!existsSync(metaPath)) {
    throw new Error(
      `Missing ${metaPath}. Run seed-readme-demo-workspace.mjs first.`,
    );
  }
  return { outDir, meta: JSON.parse(readFileSync(metaPath, "utf8")) };
}

function copyForLocale(locale) {
  if (locale === "en") {
    return {
      clientName: "Acme Partner App",
      spName: "Acme Partner App",
      inventoryAgent: "Acme Inventory Agent",
      inventoryRole:
        "Specialist inventory agent: check SKU stock for the commerce assistant (demo only).",
      inventoryPrompt:
        "You are the Acme inventory specialist. Use only check_inventory and answer with stock facts.",
      sessionTitle: "Partner order — SKU ACME-TEE-001",
      userMessage:
        "Customer CUST-1001 wants 2 units of ACME-TEE-001. Check inventory, then create the order if stock allows.",
      assistantMessage:
        "Inventory confirmed: ACME-TEE-001 has 48 units available. Order ORD-2026-88421 created for CUST-1001 × 2. Estimated ship date: next business day.",
      invToolName: "check_inventory",
      orderToolName: "create_order",
      delCallable: "inventory_agent",
      invArgs: { sku: "ACME-TEE-001" },
      invResult: {
        ok: true,
        sku: "ACME-TEE-001",
        available: 48,
        warehouse: "US-WEST-1",
      },
      orderArgs: {
        customerId: "CUST-1001",
        sku: "ACME-TEE-001",
        quantity: 2,
      },
      orderResult: {
        ok: true,
        orderId: "ORD-2026-88421",
        status: "CONFIRMED",
        quantity: 2,
      },
    };
  }
  return {
    clientName: "Acme Partner App",
    spName: "Acme Partner App",
    inventoryAgent: "Acme 库存助手",
    inventoryRole:
      "库存专业 Agent：为导购助手查询 SKU 可用库存（仅演示）。",
    inventoryPrompt:
      "你是 Acme 库存专家。只使用库存查询工具，返回库存事实。",
    sessionTitle: "合作伙伴下单 — SKU ACME-TEE-001",
    userMessage:
      "客户 CUST-1001 需要 2 件 ACME-TEE-001。请先查库存，有货则创建订单。",
    assistantMessage:
      "库存确认：ACME-TEE-001 可用 48 件。已为 CUST-1001 创建订单 ORD-2026-88421（数量 2），预计下一工作日发货。",
    invToolName: "check_inventory",
    orderToolName: "create_order",
    delCallable: "inventory_agent",
    invArgs: { sku: "ACME-TEE-001" },
    invResult: {
      ok: true,
      sku: "ACME-TEE-001",
      available: 48,
      warehouse: "US-WEST-1",
    },
    orderArgs: {
      customerId: "CUST-1001",
      sku: "ACME-TEE-001",
      quantity: 2,
    },
    orderResult: {
      ok: true,
      orderId: "ORD-2026-88421",
      status: "CONFIRMED",
      quantity: 2,
    },
  };
}

function seedLocale(locale) {
  const { outDir, meta } = loadMeta(locale);
  const copy = copyForLocale(locale);
  const wid = meta.workspaceId;
  const parentAgentId = meta.agentId;
  const modelId = meta.modelId;
  const modelName = "acme-demo-mini";
  const traceId =
    locale === "en"
      ? "readme-acme-en-order-e2e"
      : "readme-acme-zh-order-e2e";

  const existing = psql(
    `SELECT count(*)::int FROM agent_runs WHERE workspace_id=${sqlStr(wid)} AND trace_id=${sqlStr(traceId)};`,
  );
  if (Number(existing) > 0) {
    console.log(`[${locale}] trace already present:`, traceId);
    writeTraceMeta(outDir, { workspaceId: wid, traceId, locale });
    return { workspaceId: wid, traceId };
  }

  // Inventory agent (reuse by name).
  let invAgentId = psql(
    `SELECT id::text FROM agents WHERE workspace_id=${sqlStr(wid)} AND name=${sqlStr(copy.inventoryAgent)} AND deleted_at IS NULL LIMIT 1;`,
  );
  if (!invAgentId) {
    invAgentId = uuid();
    psql(`
INSERT INTO agents(
  id, workspace_id, name, role_description, model_config_id,
  created_by, updated_by, status, context_policy
) VALUES (
  ${sqlStr(invAgentId)}, ${sqlStr(wid)}, ${sqlStr(copy.inventoryAgent)},
  ${sqlStr(copy.inventoryRole)}, ${sqlStr(modelId)},
  ${sqlStr(ADMIN_USER_ID)}, ${sqlStr(ADMIN_USER_ID)}, 'ACTIVE', '{}'::jsonb
);
`);
    console.log(`[${locale}] created inventory agent`, invAgentId);
  } else {
    console.log(`[${locale}] reusing inventory agent`, invAgentId);
  }

  // Service principal + AAP client "Acme Partner App"
  let spId = psql(
    `SELECT id::text FROM service_principals WHERE workspace_id=${sqlStr(wid)} AND name=${sqlStr(copy.spName)} LIMIT 1;`,
  );
  if (!spId) {
    spId = uuid();
    psql(`
INSERT INTO service_principals(
  id, workspace_id, name, status, security_version,
  created_by, updated_by, lock_version
) VALUES (
  ${sqlStr(spId)}, ${sqlStr(wid)}, ${sqlStr(copy.spName)}, 'ACTIVE', 1,
  ${sqlStr(ADMIN_USER_ID)}, ${sqlStr(ADMIN_USER_ID)}, 1
);
`);
    console.log(`[${locale}] created service principal`, spId);
  }

  psql(`
INSERT INTO principal_refs(workspace_id, principal_type, principal_id, origin)
VALUES (${sqlStr(wid)}, 'SERVICE_PRINCIPAL', ${sqlStr(spId)}, 'DIRECTORY')
ON CONFLICT DO NOTHING;
`);

  let clientRowId = psql(
    `SELECT id::text FROM agent_access_clients WHERE workspace_id=${sqlStr(wid)} AND name=${sqlStr(copy.clientName)} LIMIT 1;`,
  );
  let publicClientId = "";
  if (!clientRowId) {
    clientRowId = uuid();
    publicClientId =
      "awcl_" +
      randomBytes(32)
        .toString("base64url")
        .replace(/[^A-Za-z0-9_-]/g, "x")
        .slice(0, 43);
    psql(`
INSERT INTO agent_access_clients(
  id, workspace_id, service_principal_id, client_id, name, status,
  auth_method, allowed_cors_origins, token_ttl_seconds,
  created_by, updated_by, lock_version
) VALUES (
  ${sqlStr(clientRowId)}, ${sqlStr(wid)}, ${sqlStr(spId)}, ${sqlStr(publicClientId)},
  ${sqlStr(copy.clientName)}, 'ACTIVE', 'client_secret_basic', '[]'::jsonb, 600,
  ${sqlStr(ADMIN_USER_ID)}, ${sqlStr(ADMIN_USER_ID)}, 1
);
`);
    console.log(`[${locale}] created AAP client`, copy.clientName, publicClientId);
  } else {
    publicClientId = psql(
      `SELECT client_id FROM agent_access_clients WHERE id=${sqlStr(clientRowId)};`,
    );
    console.log(`[${locale}] reusing AAP client`, publicClientId);
  }

  const scopes = [
    "agent:read",
    "run:create",
    "run:read",
    "event:read",
    "conversation:create",
    "run:cancel",
    "conversation:read",
    "interaction:decide",
  ];

  function ensureGrant(agentId) {
    let id = psql(
      `SELECT id::text FROM agent_access_grants
       WHERE workspace_id=${sqlStr(wid)} AND client_id=${sqlStr(clientRowId)}
         AND agent_id=${sqlStr(agentId)} AND status='ACTIVE' LIMIT 1;`,
    );
    if (!id) {
      id = uuid();
      psql(`
INSERT INTO agent_access_grants(
  id, workspace_id, client_id, agent_id, scopes, policy, status,
  valid_from, created_by, updated_by, lock_version
) VALUES (
  ${sqlStr(id)}, ${sqlStr(wid)}, ${sqlStr(clientRowId)}, ${sqlStr(agentId)},
  ${sqlJson(scopes)}::jsonb, '{}'::jsonb, 'ACTIVE',
  NOW() - interval '1 hour', ${sqlStr(ADMIN_USER_ID)}, ${sqlStr(ADMIN_USER_ID)}, 1
);
`);
    }
    return id;
  }

  const grantId = ensureGrant(parentAgentId);
  const invGrantId = ensureGrant(invAgentId);

  const parentAgentLock = Number(
    psql(
      `SELECT lock_version::text FROM agents WHERE id=${sqlStr(parentAgentId)};`,
    ) || "1",
  );
  const invAgentLock = Number(
    psql(
      `SELECT lock_version::text FROM agents WHERE id=${sqlStr(invAgentId)};`,
    ) || "1",
  );
  const grantLock = Number(
    psql(
      `SELECT lock_version::text FROM agent_access_grants WHERE id=${sqlStr(grantId)};`,
    ) || "1",
  );
  const invGrantLock = Number(
    psql(
      `SELECT lock_version::text FROM agent_access_grants WHERE id=${sqlStr(invGrantId)};`,
    ) || "1",
  );

  function authEnvelope({
    actorId,
    clientId,
    grantId: gId,
    grantVersion,
    agentPolicyVersion,
    evidence,
  }) {
    return {
      specVersion: "execution.principal.v1",
      workspaceId: wid,
      actor: { type: "SERVICE_PRINCIPAL", id: actorId },
      clientId,
      grantId: gId,
      grantVersion,
      agentPolicyVersion,
      evidence: evidence || { source: "readme-demo-seed", demo: true },
    };
  }

  const parentAuth = authEnvelope({
    actorId: spId,
    clientId: clientRowId,
    grantId,
    grantVersion: grantLock,
    agentPolicyVersion: parentAgentLock,
    evidence: {
      action: "run.create",
      agentId: parentAgentId,
      clientId: clientRowId,
      grantId,
      grantVersion: grantLock,
      agentPolicyVersion: parentAgentLock,
      servicePrincipalId: spId,
      workspaceId: wid,
      source: "readme-demo-seed",
    },
  });
  const childAuth = authEnvelope({
    actorId: spId,
    clientId: clientRowId,
    grantId: invGrantId,
    grantVersion: invGrantLock,
    agentPolicyVersion: invAgentLock,
    evidence: {
      source: "agentdelegation.task",
      agentId: invAgentId,
      parentAgentId,
      demo: true,
    },
  });

  const sessionId = uuid();
  const parentRunId = uuid();
  const childRunId = uuid();
  const delId = uuid();
  const modelObj1 = uuid();
  const modelObj2 = uuid();
  const modelObj3 = uuid();
  const stepModel1 = modelObj1; // MODEL raw_object_id often equals step id in fixtures
  const stepModel2 = modelObj2;
  const stepModel3 = modelObj3;
  const stepDel = uuid();
  const stepInvTool = uuid();
  const stepOrderTool = uuid();
  const msgUser = uuid();
  const msgAsst = uuid();
  const modelSha1 = sha256(`model-turn-parent-plan-${traceId}`);
  const modelSha2 = sha256(`model-turn-child-inventory-${traceId}`);
  const modelSha3 = sha256(`model-turn-parent-final-${traceId}`);
  const userSha = sha256(copy.userMessage);
  const asstSha = sha256(copy.assistantMessage);

  const t0 = "NOW() - interval '12 seconds'";
  const t1 = "NOW() - interval '11 seconds'";
  const t2 = "NOW() - interval '9 seconds'";
  const t3 = "NOW() - interval '8 seconds'";
  const t4 = "NOW() - interval '6 seconds'";
  const t5 = "NOW() - interval '5 seconds'";
  const t6 = "NOW() - interval '3 seconds'";
  const t7 = "NOW() - interval '2 seconds'";
  const t8 = "NOW() - interval '1 second'";
  const t9 = "NOW() - interval '200 milliseconds'";

  const modelSnap = {
    id: modelId,
    modelName,
    name: "Acme Demo LLM",
    provider: "OpenAI Compatible",
  };
  const capSnap = {
    schemaVersion: "capability-snapshot.v1",
    releases: [],
  };
  const agentSnap = {
    schemaVersion: "agent-binding.v1",
    agentId: parentAgentId,
    modelConfigId: modelId,
  };

  // Conversation first (latest_run_id set after parent run exists).
  psql(`
INSERT INTO chat_sessions(
  id, workspace_id, agent_id, title, status, actor_type, actor_id,
  client_id, ownership_mode, ownership_policy_version, lock_version,
  created_at, updated_at
) VALUES (
  ${sqlStr(sessionId)}, ${sqlStr(wid)}, ${sqlStr(parentAgentId)},
  ${sqlStr(copy.sessionTitle)}, 'ACTIVE', 'SERVICE_PRINCIPAL', ${sqlStr(spId)},
  ${sqlStr(clientRowId)}, 'SUBJECT_OWNED', 1, 1,
  ${t0}, ${t0}
);
`);

  // Parent run (AAP client initiator)
  psql(`
INSERT INTO agent_runs(
  id, workspace_id, session_id, agent_id, status, trigger_type,
  triggered_by_type, triggered_by_id, trace_id,
  model_snapshot, capability_snapshot, context_policy_snapshot,
  authorization_snapshot, input_summary, output_summary,
  agent_snapshot, agent_graph_snapshot,
  principal_snapshot_version, client_id, grant_id, grant_version, agent_policy_version,
  started_at, finished_at
) VALUES (
  ${sqlStr(parentRunId)}, ${sqlStr(wid)}, ${sqlStr(sessionId)}, ${sqlStr(parentAgentId)},
  'SUCCEEDED', 'CHAT', 'SERVICE_PRINCIPAL', ${sqlStr(spId)}, ${sqlStr(traceId)},
  ${sqlJson(modelSnap)}::jsonb, ${sqlJson(capSnap)}::jsonb, '{}'::jsonb,
  ${sqlJson(parentAuth)}::jsonb, ${sqlJson({ source: "aap", demo: true })}::jsonb,
  ${sqlJson({ ok: true, orderId: "ORD-2026-88421" })}::jsonb,
  ${sqlJson(agentSnap)}::jsonb, '{}'::jsonb,
  'execution.principal.v1', ${sqlStr(clientRowId)}, ${sqlStr(grantId)}, ${grantLock}, ${parentAgentLock},
  ${t0}, ${t9}
);
`);

  psql(`
UPDATE chat_sessions
SET latest_run_id=${sqlStr(parentRunId)}, updated_at=${t9}, lock_version=lock_version+1
WHERE id=${sqlStr(sessionId)};
`);

  // Delegation row (RUNNING first, then child, then terminalize)
  psql(`
INSERT INTO agent_run_delegations (
  id, workspace_id, parent_run_id, caller_agent_id, target_agent_id,
  mode, protocol, origin, depth, binding_version, tool_call_id, idempotency_key,
  status, input_summary, input_payload, output_summary, output_payload,
  attempt_count, retry_count, started_at
) VALUES (
  ${sqlStr(delId)}, ${sqlStr(wid)}, ${sqlStr(parentRunId)},
  ${sqlStr(parentAgentId)}, ${sqlStr(invAgentId)},
  'TASK', 'INTERNAL', 'INTERNAL', 1, 1,
  'tc_inventory_1', ${sqlStr(`${parentRunId}:tc_inventory_1:1:${invAgentId.slice(0, 8)}`)},
  'RUNNING',
  ${sqlJson({
    callableName: copy.delCallable,
    mode: "TASK",
    protocol: "INTERNAL",
    origin: "INTERNAL",
    depth: 1,
    toolCallId: "tc_inventory_1",
    sku: "ACME-TEE-001",
  })}::jsonb,
  ${sqlJson({ sku: "ACME-TEE-001", quantity: 2 })}::jsonb,
  '{}'::jsonb, '{}'::jsonb,
  1, 0, ${t2}
);
`);

  // Child run for inventory agent (same AAP client; grant bound to inventory agent)
  psql(`
INSERT INTO agent_runs(
  id, workspace_id, agent_id, status, trigger_type,
  triggered_by_type, triggered_by_id, trace_id,
  model_snapshot, capability_snapshot, context_policy_snapshot,
  authorization_snapshot, input_summary, output_summary,
  agent_snapshot, agent_graph_snapshot,
  principal_snapshot_version, client_id, grant_id, grant_version, agent_policy_version,
  parent_run_id, parent_delegation_id,
  started_at, finished_at
) VALUES (
  ${sqlStr(childRunId)}, ${sqlStr(wid)}, ${sqlStr(invAgentId)},
  'SUCCEEDED', 'DELEGATION_TASK', 'SERVICE_PRINCIPAL', ${sqlStr(spId)}, ${sqlStr(traceId)},
  ${sqlJson({ ...modelSnap, modelName })}::jsonb, ${sqlJson(capSnap)}::jsonb, '{}'::jsonb,
  ${sqlJson(childAuth)}::jsonb, ${sqlJson({ sku: "ACME-TEE-001" })}::jsonb,
  ${sqlJson({ available: 48 })}::jsonb,
  ${sqlJson({ schemaVersion: "agent-binding.v1", agentId: invAgentId, modelConfigId: modelId })}::jsonb,
  '{}'::jsonb, 'execution.principal.v1',
  ${sqlStr(clientRowId)}, ${sqlStr(invGrantId)}, ${invGrantLock}, ${invAgentLock},
  ${sqlStr(parentRunId)}, ${sqlStr(delId)},
  ${t2}, ${t5}
);
`);

  psql(`
UPDATE agent_run_delegations SET child_run_id=${sqlStr(childRunId)}
WHERE id=${sqlStr(delId)} AND status='RUNNING';
UPDATE agent_run_delegations SET
  status='SUCCEEDED',
  output_summary=${sqlJson({ ok: true, available: 48, sku: "ACME-TEE-001" })}::jsonb,
  output_payload=${sqlJson(copy.invResult)}::jsonb,
  latency_ms=2100,
  finished_at=${t5},
  input_tokens=128, output_tokens=64, total_tokens=192, tokens_known=true
WHERE id=${sqlStr(delId)} AND status='RUNNING';
`);

  // Stored objects for MODEL evidence
  for (const [objId, sha, key] of [
    [modelObj1, modelSha1, "parent-plan"],
    [modelObj2, modelSha2, "child-inventory"],
    [modelObj3, modelSha3, "parent-final"],
  ]) {
    psql(`
INSERT INTO stored_objects(
  id, workspace_id, bucket, object_key, kind, content_type, size_bytes, sha256,
  encryption_key_id, classification, retention_mode, created_by_type, created_by_id
) VALUES (
  ${sqlStr(objId)}, ${sqlStr(wid)}, 'executions',
  ${sqlStr(`${wid}/model-turn/${key}/${objId}`)},
  'MODEL_TURN', 'application/json', 32, ${sqlStr(sha)},
  'demo-key', 'SENSITIVE', 'PERMANENT', 'SERVICE_PRINCIPAL', ${sqlStr(spId)}
);
`);
  }

  // Parent MODEL (plan / tool calls)
  psql(`
INSERT INTO agent_run_steps (
  id, workspace_id, run_id, sequence_no, step_type, status,
  input_summary, output_summary, agent_id, raw_object_id, raw_sha256, raw_length,
  started_at, finished_at
) VALUES (
  ${sqlStr(stepModel1)}, ${sqlStr(wid)}, ${sqlStr(parentRunId)}, 1, 'MODEL', 'SUCCEEDED',
  ${sqlJson({
    source: "chatruntimebridge",
    tokensKnown: true,
    hasReasoning: false,
    hasToolCalls: true,
    contentLength: 0,
  })}::jsonb,
  ${sqlJson({ contentLength: 96, contentSha256: modelSha1 })}::jsonb,
  ${sqlStr(parentAgentId)}, ${sqlStr(modelObj1)}, ${sqlStr(modelSha1)}, 32,
  ${t1}, ${t2}
);
`);

  // Parent AGENT_DELEGATION step
  psql(`
INSERT INTO agent_run_steps (
  id, workspace_id, run_id, sequence_no, step_type, status,
  input_summary, output_summary, agent_id, delegation_id,
  started_at, finished_at
) VALUES (
  ${sqlStr(stepDel)}, ${sqlStr(wid)}, ${sqlStr(parentRunId)}, 2, 'AGENT_DELEGATION', 'SUCCEEDED',
  ${sqlJson({
    callableName: copy.delCallable,
    mode: "TASK",
    protocol: "INTERNAL",
    origin: "INTERNAL",
    depth: 1,
    toolCallId: "tc_inventory_1",
    source: "agentdelegation",
  })}::jsonb,
  ${sqlJson({
    ok: true,
    mode: "TASK",
    status: "SUCCEEDED",
    message: "",
    errorCode: "",
  })}::jsonb,
  ${sqlStr(parentAgentId)}, ${sqlStr(delId)},
  ${t2}, ${t5}
);
`);

  // Child MODEL (inventory agent)
  psql(`
INSERT INTO agent_run_steps (
  id, workspace_id, run_id, sequence_no, step_type, status,
  input_summary, output_summary, agent_id, delegation_id,
  raw_object_id, raw_sha256, raw_length, started_at, finished_at
) VALUES (
  ${sqlStr(stepModel2)}, ${sqlStr(wid)}, ${sqlStr(childRunId)}, 1, 'MODEL', 'SUCCEEDED',
  ${sqlJson({
    source: "chatruntimebridge.nested",
    tokensKnown: true,
    hasReasoning: false,
    hasToolCalls: true,
    contentLength: 0,
  })}::jsonb,
  ${sqlJson({ contentLength: 48, contentSha256: modelSha2 })}::jsonb,
  ${sqlStr(invAgentId)}, ${sqlStr(delId)},
  ${sqlStr(modelObj2)}, ${sqlStr(modelSha2)}, 32, ${t3}, ${t4}
);
`);

  // Child TOOL check_inventory
  psql(`
INSERT INTO agent_run_steps (
  id, workspace_id, run_id, sequence_no, step_type, status,
  input_summary, output_summary, agent_id, delegation_id,
  started_at, finished_at
) VALUES (
  ${sqlStr(stepInvTool)}, ${sqlStr(wid)}, ${sqlStr(childRunId)}, 2, 'TOOL', 'SUCCEEDED',
  ${sqlJson({
    source: "chatruntimebridge",
    toolName: copy.invToolName,
    callableName: copy.invToolName,
    toolCallId: "tc_check_inventory_1",
    arguments: copy.invArgs,
  })}::jsonb,
  ${sqlJson({
    ok: true,
    cached: false,
    output: copy.invResult,
    source: "chatruntimebridge",
    invocationId: "tc_check_inventory_1",
  })}::jsonb,
  ${sqlStr(invAgentId)}, ${sqlStr(delId)},
  ${t4}, ${t5}
);
`);

  // Parent TOOL create_order
  psql(`
INSERT INTO agent_run_steps (
  id, workspace_id, run_id, sequence_no, step_type, status,
  input_summary, output_summary, agent_id,
  started_at, finished_at
) VALUES (
  ${sqlStr(stepOrderTool)}, ${sqlStr(wid)}, ${sqlStr(parentRunId)}, 3, 'TOOL', 'SUCCEEDED',
  ${sqlJson({
    source: "chatruntimebridge",
    toolName: copy.orderToolName,
    callableName: copy.orderToolName,
    toolCallId: "tc_create_order_1",
    arguments: copy.orderArgs,
  })}::jsonb,
  ${sqlJson({
    ok: true,
    cached: false,
    output: copy.orderResult,
    source: "chatruntimebridge",
    invocationId: "tc_create_order_1",
  })}::jsonb,
  ${sqlStr(parentAgentId)},
  ${t6}, ${t7}
);
`);

  // Parent final MODEL
  psql(`
INSERT INTO agent_run_steps (
  id, workspace_id, run_id, sequence_no, step_type, status,
  input_summary, output_summary, agent_id, raw_object_id, raw_sha256, raw_length,
  started_at, finished_at
) VALUES (
  ${sqlStr(stepModel3)}, ${sqlStr(wid)}, ${sqlStr(parentRunId)}, 4, 'MODEL', 'SUCCEEDED',
  ${sqlJson({
    source: "chatruntimebridge",
    tokensKnown: true,
    hasReasoning: false,
    hasToolCalls: false,
    contentLength: copy.assistantMessage.length,
  })}::jsonb,
  ${sqlJson({
    contentLength: copy.assistantMessage.length,
    contentSha256: modelSha3,
  })}::jsonb,
  ${sqlStr(parentAgentId)}, ${sqlStr(modelObj3)}, ${sqlStr(modelSha3)}, 32,
  ${t7}, ${t8}
);
`);

  // Messages (input + final output on parent run)
  psql(`
INSERT INTO chat_messages(
  id, workspace_id, session_id, role, content, content_sha256, content_length,
  status, run_id, actor_type, actor_id, client_id,
  ownership_mode, ownership_policy_version, created_at
) VALUES
(
  ${sqlStr(msgUser)}, ${sqlStr(wid)}, ${sqlStr(sessionId)}, 'USER',
  ${sqlStr(copy.userMessage)}, ${sqlStr(userSha)}, ${copy.userMessage.length},
  'EXECUTED', ${sqlStr(parentRunId)}, 'SERVICE_PRINCIPAL', ${sqlStr(spId)}, ${sqlStr(clientRowId)},
  'SUBJECT_OWNED', 1, ${t0}
),
(
  ${sqlStr(msgAsst)}, ${sqlStr(wid)}, ${sqlStr(sessionId)}, 'ASSISTANT',
  ${sqlStr(copy.assistantMessage)}, ${sqlStr(asstSha)}, ${copy.assistantMessage.length},
  'EXECUTED', ${sqlStr(parentRunId)}, 'SYSTEM', ${sqlStr(SYSTEM_PRINCIPAL_ID)}, ${sqlStr(clientRowId)},
  'SUBJECT_OWNED', 1, ${t9}
);
`);

  writeTraceMeta(outDir, {
    workspaceId: wid,
    traceId,
    locale,
    clientName: copy.clientName,
    parentRunId,
    childRunId,
    inventoryAgentId: invAgentId,
    parentAgentId,
  });
  console.log(`[${locale}] seeded audit trace`, traceId, "workspace", wid);
  return { workspaceId: wid, traceId };
}

function writeTraceMeta(outDir, data) {
  const dir = resolve(ROOT, outDir);
  mkdirSync(dir, { recursive: true });
  writeFileSync(
    resolve(dir, "demo-audit-trace.json"),
    JSON.stringify(data, null, 2) + "\n",
  );
}

for (const locale of locales) {
  seedLocale(locale);
}
console.log("done");
