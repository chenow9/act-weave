/**
 * Provision a sandbox workspace + Agent + AAP client for live DeepSeek AAP tests.
 *
 * Usage (do not commit the key):
 *   DEEPSEEK_API_KEY='sk-...' node scripts/provision-deepseek-aap-lab.mjs
 *
 * Default endpoint is DashScope compatible-mode (`/v1`, Responses + Chat Completions).
 * The agent enables A2UI + outbound attachments and sets disclosure to carry_all
 * so business prompts like 「生成本月对账单」 can publish a file and a chart.
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
  "https://dashscope.aliyuncs.com/compatible-mode/v1";
const MODEL_NAME = process.env.DEEPSEEK_MODEL || "deepseek-v4-flash";
const SLUG = "deepseek-aap-lab";
const DISPLAY = "DeepSeek AAP Lab";
const AGENT_NAME = process.env.DEEPSEEK_AGENT_NAME || "DeepSeek 出站附件助手";
const SYSTEM_PROMPT = [
  "你是门店经营助手，用简体中文回答。用户说的是业务结果，不是技术操作。",
  "「本月」指当前自然月。",
  "遇到对账单、月结、导出明细、给一份表格/清单，或要看趋势/对比时：",
  "- 先用两三句话说明结论（示例数据须标明）",
  "- 产出可下载附件，不要追问文件名或格式",
  "- 再用一张图表展示与附件同一组数字，方便对照",
  "回文件时只调用目录里已有的工具，名字必须完全一致，禁止自造中文或英文工具名。",
  "不要把整表再贴进正文，不要提工具名、协议或 fileId。",
  "不能发图片或外部链接。普通问答用简洁 Markdown。",
].join("\n");

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
      options: { temperature: 0.2 },
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
    model = await req("PATCH", `/workspaces/${wid}/model-configs/${model.id}`, token, {
      apiBase: API_BASE,
      modelName: MODEL_NAME,
      credentialSecretId: secret.id,
      lockVersion: model.lockVersion,
    });
    console.log("updated model endpoint", model.id, model.apiBase, model.status);
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

  if (model.status === "VERIFIED") {
    try {
      model = await req(
        "POST",
        `/workspaces/${wid}/model-configs/${model.id}/__command/set-disclosure`,
        token,
        {
          lockVersion: model.lockVersion,
          toolDisclosurePolicy: { schemaVersion: "tool-disclosure.v1", mode: "carry_all" },
        },
      );
      console.log("disclosure", model.toolDisclosurePolicy);
    } catch (err) {
      console.warn("set-disclosure failed:", String(err.message).slice(0, 300));
    }
  }

  const agents = await req("GET", `/workspaces/${wid}/agents?page=1&pageSize=20`, token);
  let agent = (agents.items || []).find((a) => a.name === AGENT_NAME);
  if (!agent) {
    agent = await req("POST", `/workspaces/${wid}/agents`, token, {
      name: AGENT_NAME,
      roleDescription: "经营助手：对账单、导出明细等业务问题会回传可下载文件。",
      modelConfigId: model.id,
      systemPrompt: SYSTEM_PROMPT,
    });
    console.log("created agent", agent.id);
  } else {
    console.log("reusing agent", agent.id);
  }

  agent = await req("PATCH", `/workspaces/${wid}/agents/${agent.id}`, token, {
    modelConfigId: model.id,
    contextPolicy: {
      schemaVersion: "session-context-policy.v2",
      mode: "token_window",
      maxInputTokens: 64000,
      outputReserveTokens: 4096,
      aap: {
        includeCompactionSummary: false,
        enableA2UI: true,
        enableOutboundAttachments: true,
      },
    },
    lockVersion: agent.lockVersion,
  });
  console.log("enabled outbound attachments on agent, lock", agent.lockVersion, "model", agent.modelConfigId);

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
    console.log("reusing AAP client", client.clientId);
    try {
      const rotated = await req(
        "POST",
        `/workspaces/${wid}/agent-access/clients/${client.id}/credentials`,
        token,
        { type: "client_secret" },
        idem(),
      );
      clientSecret = rotated.secret || "";
    } catch (err) {
      console.warn("rotate failed, reuse demos/aap-chat/.env secret if present:", String(err.message).slice(0, 200));
    }
    clientId = client.clientId;
    if (!clientSecret) {
      const existing = process.env.AAP_CLIENT_SECRET || "";
      if (existing) {
        clientSecret = existing;
        console.log("reusing AAP_CLIENT_SECRET from environment");
      }
    }
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
