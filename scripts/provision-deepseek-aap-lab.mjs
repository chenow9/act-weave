/**
 * Provision a sandbox workspace + Agent + AAP client for live DeepSeek AAP tests.
 *
 * Usage (do not commit the key):
 *   DEEPSEEK_API_KEY='sk-...' node scripts/provision-deepseek-aap-lab.mjs
 *
 * Writes demos/aap-chat/.env (gitignored).
 */
import { writeFileSync } from "node:fs";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { randomUUID } from "node:crypto";

const __dirname = dirname(fileURLToPath(import.meta.url));
const ROOT = resolve(__dirname, "..");
const API = process.env.ACTWEAVE_API || "http://127.0.0.1:8082/api/v1";
const USER = process.env.ACTWEAVE_ADMIN_USER || "admin";
const PASS = process.env.ACTWEAVE_ADMIN_PASS || "actweave-admin-dev-change-me";
const API_KEY = (process.env.DEEPSEEK_API_KEY || "").trim();
const API_BASE =
  process.env.DEEPSEEK_API_BASE ||
  "https://llm-n67ta77ocgtnlpls.cn-beijing.maas.aliyuncs.com/compatible-mode/v1";
const MODEL_NAME = process.env.DEEPSEEK_MODEL || "deepseek-v4-flash";
const SLUG = "deepseek-aap-lab";
const DISPLAY = "DeepSeek AAP Lab";
const AGENT_NAME = "DeepSeek 富文本助手";

if (!API_KEY) {
  console.error("DEEPSEEK_API_KEY is required");
  process.exit(1);
}

async function req(method, path, token, body, extraHeaders = {}) {
  const res = await fetch(`${API}${path}`, {
    method,
    headers: {
      Accept: "application/json",
      "Content-Type": "application/json",
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      ...extraHeaders,
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await res.text();
  let data = {};
  try {
    data = text ? JSON.parse(text) : {};
  } catch {
    data = { raw: text };
  }
  if (!res.ok) {
    throw new Error(`${method} ${path} -> ${res.status} ${JSON.stringify(data).slice(0, 800)}`);
  }
  return data;
}

function idem() {
  return { "Idempotency-Key": randomUUID() };
}

async function main() {
  const login = await req("POST", "/auth/login", null, { username: USER, password: PASS });
  const token = login.accessToken;
  console.log("logged in");

  const listed = await req("GET", "/workspaces?page=1&pageSize=50", token);
  let ws = (listed.items || []).find((w) => w.slug === SLUG);
  if (!ws) {
    ws = await req("POST", "/workspaces", token, {
      slug: SLUG,
      displayName: DISPLAY,
      mode: "SANDBOX",
      settings: {},
    });
    console.log("created workspace", ws.id);
  } else {
    console.log("reusing workspace", ws.id);
  }
  const wid = ws.id;

  const secret = await req("POST", `/workspaces/${wid}/secrets`, token, {
    name: `deepseek-v4-flash-${Date.now()}`,
    kind: "API_KEY",
    plaintext: API_KEY,
  });
  console.log("created secret", secret.id);

  let models = await req("GET", `/workspaces/${wid}/model-configs`, token);
  let model = (models.items || []).find((m) => m.modelName === MODEL_NAME);
  if (!model) {
    model = await req("POST", `/workspaces/${wid}/model-configs`, token, {
      name: "Aliyun DeepSeek v4 Flash",
      provider: "openai-compatible",
      apiBase: API_BASE,
      modelName: MODEL_NAME,
      credentialSecretId: secret.id,
      options: { temperature: 0.3 },
      runtimeCapabilities: {
        schemaVersion: "model-runtime.v1",
        tokenizerProfile: "o200k_base",
        tokenizerVersion: "2026-01",
        contextWindowTokens: 128000,
        outputTokenLimitMode: "max_tokens",
        defaultOutputReserveTokens: 4096,
      },
    });
    console.log("created model", model.id, model.status);
  } else {
    console.log("reusing model", model.id, model.status);
  }

  try {
    model = await req(
      "POST",
      `/workspaces/${wid}/model-configs/${model.id}/__command/verify`,
      token,
      {},
    );
    console.log("verify model", model.status, model.lastErrorCode || "ok");
  } catch (err) {
    console.warn("verify failed (agent can still run if runtime accepts it):", String(err.message).slice(0, 300));
  }

  const agents = await req("GET", `/workspaces/${wid}/agents?page=1&pageSize=20`, token);
  let agent = (agents.items || []).find((a) => a.name === AGENT_NAME);
  if (!agent) {
    agent = await req("POST", `/workspaces/${wid}/agents`, token, {
      name: AGENT_NAME,
      roleDescription:
        "Live AAP 演示助手：Markdown 富文本、图片/文件理解、以及 A2UI 统计图。",
      modelConfigId: model.id,
      systemPrompt: [
        "你是 ActWeave 的业务助手。默认用简体中文回答。",
        "普通问答用 Markdown：标题、列表、表格、加粗、行内代码、必要时用 $...$ 公式。",
        "用户上传了图片或文件时，先描述你看到的内容，再回答问题。",
        "当用户要统计图、趋势、分布、看板、KPI 时：先写一段 Markdown 说明（可含表格），再在正文之后附加一个 A2UI 围栏。",
        "A2UI 围栏必须是完整可解析的 JSON 对象，不要包一层 surface，不要写 surfaceId/catalogId。",
        "混用场景（月报 / 图文并茂）必须同时给出 Markdown 表格和 A2UI Chart。",
        "不要编造无法核对的精确财务数字；演示可用合理的示例数据并标明是示例。",
      ].join("\n"),
    });
    console.log("created agent", agent.id);
  } else {
    console.log("reusing agent", agent.id);
  }

  agent = await req("PATCH", `/workspaces/${wid}/agents/${agent.id}`, token, {
    contextPolicy: {
      schemaVersion: "session-context-policy.v2",
      mode: "token_window",
      maxInputTokens: 64000,
      outputReserveTokens: 4096,
      aap: { includeCompactionSummary: false, enableA2UI: true },
    },
    lockVersion: agent.lockVersion,
  });
  console.log("enabled A2UI on agent, lock", agent.lockVersion);

  const clients = await req(
    "GET",
    `/workspaces/${wid}/agent-access/clients?page=1&pageSize=20`,
    token,
  ).catch(() => ({ items: [] }));
  let client = (clients.items || []).find((c) => c.name === "DeepSeek AAP Chat Demo");
  let clientSecret = "";
  let clientId = client?.clientId || "";
  if (!client) {
    const created = await req(
      "POST",
      `/workspaces/${wid}/agent-access/clients`,
      token,
      {
        name: "DeepSeek AAP Chat Demo",
        authMethod: "client_secret_basic",
        allowedCorsOrigins: ["http://127.0.0.1:5188", "http://localhost:5188"],
        tokenTtlSeconds: 900,
      },
      idem(),
    );
    client = created.client;
    clientSecret = created.secret || "";
    clientId = client.clientId;
    console.log("created AAP client", clientId);
  } else {
    console.log("reusing AAP client", client.clientId, "— rotate credential");
    const rotated = await req(
      "POST",
      `/workspaces/${wid}/agent-access/clients/${client.id}/credentials`,
      token,
      { type: "client_secret" },
      idem(),
    );
    clientSecret = rotated.secret || "";
    clientId = client.clientId;
  }
  if (!clientSecret) {
    throw new Error("AAP client secret was not returned; cannot write demo .env");
  }

  const grants = await req(
    "GET",
    `/workspaces/${wid}/agent-access/clients/${client.id}/grants`,
    token,
  ).catch(() => ({ items: [] }));
  const existingGrant = (grants.items || []).find((g) => g.agentId === agent.id && g.status === "ACTIVE");
  if (!existingGrant) {
    const grant = await req(
      "POST",
      `/workspaces/${wid}/agent-access/clients/${client.id}/grants`,
      token,
      {
        agentId: agent.id,
        scopes: [
          "agent:read",
          "conversation:create",
          "conversation:read",
          "run:create",
          "run:read",
          "run:cancel",
          "event:read",
          "interaction:decide",
          "file:write",
          "file:read",
        ],
        policy: {},
      },
      idem(),
    );
    console.log("created grant", grant.grant?.id || grant.id);
  } else {
    console.log("reusing grant", existingGrant.id);
  }

  const envPath = resolve(ROOT, "demos/aap-chat/.env");
  const envBody = [
    "AAP_BASE_URL=http://127.0.0.1:8082/api/agent-access/v1",
    `AAP_CLIENT_ID=${clientId}`,
    `AAP_CLIENT_SECRET=${clientSecret}`,
    `AAP_WORKSPACE_ID=${wid}`,
    `AAP_AGENT_ID=${agent.id}`,
    "AAP_SCOPES=agent:read conversation:create conversation:read run:create run:read run:cancel event:read interaction:decide file:write file:read",
    "BFF_PORT=8790",
    "",
  ].join("\n");
  writeFileSync(envPath, envBody, { mode: 0o600 });
  console.log("wrote", envPath);
  console.log(
    JSON.stringify(
      { workspaceId: wid, agentId: agent.id, clientId, modelId: model.id, modelStatus: model.status },
      null,
      2,
    ),
  );
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
