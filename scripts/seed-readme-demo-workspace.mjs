/**
 * Seed a fully fictional workspace for README screenshots.
 * Does not touch existing production-like workspaces (e.g. AI识别管理平台).
 *
 * Usage: node scripts/seed-readme-demo-workspace.mjs
 * Optional English demo: ACTWEAVE_DEMO_LOCALE=en \
 *   ACTWEAVE_DEMO_OUTPUT_DIR=docs/images/readme/en node scripts/seed-readme-demo-workspace.mjs
 * Writes workspace id to <output dir>/.workspace-id
 */
import { writeFileSync, mkdirSync } from "node:fs";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const ROOT = resolve(__dirname, "..");
const API = process.env.ACTWEAVE_API || "http://127.0.0.1:8082/api/v1";
const USER = process.env.ACTWEAVE_ADMIN_USER || "admin";
const PASS = process.env.ACTWEAVE_ADMIN_PASS || "actweave-admin-dev-change-me";

const DEMO_LOCALE = process.env.ACTWEAVE_DEMO_LOCALE === "en" ? "en" : "zh-CN";
const DEMO_OUTPUT_DIR =
  process.env.ACTWEAVE_DEMO_OUTPUT_DIR || "docs/images/readme";
const SLUG =
  DEMO_LOCALE === "en" ? "acme-commerce-demo-en" : "acme-commerce-demo";
const DISPLAY =
  DEMO_LOCALE === "en" ? "Acme Commerce Demo — English" : "Acme Commerce Demo";
const AGENT_NAME =
  DEMO_LOCALE === "en" ? "Acme Commerce Assistant" : "Acme 导购助手";

async function req(method, path, token, body) {
  const res = await fetch(`${API}${path}`, {
    method,
    headers: {
      Accept: "application/json",
      "Content-Type": "application/json",
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
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
    throw new Error(
      `${method} ${path} -> ${res.status} ${JSON.stringify(data).slice(0, 400)}`,
    );
  }
  return data;
}

function draftSpec(path, method, description, inputProps = {}, required = []) {
  return {
    actionSchemaVersion: "http.v1",
    actionConfig: {
      path,
      method,
      parameters: Object.keys(inputProps).map((name) => ({
        in: method === "GET" ? "query" : "body",
        name,
        input: name,
        required: required.includes(name),
      })),
    },
    inputSchema: {
      type: "object",
      required,
      additionalProperties: false,
      properties: Object.fromEntries(
        Object.entries(inputProps).map(([k, v]) => [
          k,
          {
            type: v.type || "string",
            description: v.description || k,
            ...(v.type === "integer" ? { minimum: 1 } : {}),
            "x-actweave-location": method === "GET" ? "query" : "body",
            "x-actweave-parameter-name": k,
          },
        ]),
      ),
    },
    outputSchema: {
      type: "object",
      properties: {
        code: { type: "string" },
        msg: { type: "string" },
        data: {
          type: "object",
          nullable: true,
          additionalProperties: true,
        },
      },
      additionalProperties: true,
    },
    errorMappings: {},
    runtimePolicy: { timeoutMs: 10000, maxResponseBytes: 1048576 },
    riskLevel: "LOW",
    sideEffectLevel: method === "GET" ? "READ" : "WRITE",
    requiresConfirmation: false,
  };
}

async function main() {
  const login = await req("POST", "/auth/login", null, {
    username: USER,
    password: PASS,
  });
  const token = login.accessToken;
  console.log("logged in");

  // Reuse workspace if already seeded
  const listed = await req("GET", "/workspaces?page=1&pageSize=50", token);
  let ws = (listed.items || []).find((w) => w.slug === SLUG);
  if (!ws) {
    ws = await req("POST", "/workspaces", token, {
      slug: SLUG,
      displayName: DISPLAY,
      mode: "SANDBOX",
      settings: {},
    });
    console.log("created workspace", ws.id, ws.displayName);
  } else {
    console.log("reusing workspace", ws.id, ws.displayName);
  }
  const wid = ws.id;

  // Model API (fictional — no real credential; list still shows for screenshots)
  let models = await req("GET", `/workspaces/${wid}/model-configs`, token);
  let model = (models.items || [])[0];
  if (!model) {
    model = await req("POST", `/workspaces/${wid}/model-configs`, token, {
      name: "Acme Demo LLM",
      provider: "OpenAI Compatible",
      apiBase: "https://llm.example-acme.invalid/v1",
      modelName: "acme-demo-mini",
      options: {},
      runtimeCapabilities: {
        schemaVersion: "model-runtime.v1",
        tokenizerProfile: "o200k_base",
        tokenizerVersion: "2026-01",
        contextWindowTokens: 64000,
        outputTokenLimitMode: "max_tokens",
        defaultOutputReserveTokens: 2048,
      },
    });
    console.log("created model", model.id, model.name);
  }

  // Provider
  let providers = await req("GET", `/workspaces/${wid}/providers`, token);
  let provider = (providers.items || []).find(
    (p) => p.name === "Acme Commerce API",
  );
  if (!provider) {
    provider = await req("POST", `/workspaces/${wid}/providers`, token, {
      name: "Acme Commerce API",
      kind: "HTTP_OPENAPI",
      driverKey: "http_openapi",
      transport: "HTTP",
      discoveryMode: "MANUAL",
      endpointConfig: {
        schemaVersion: 2,
        serviceBaseUrl: "https://api.example-acme.invalid",
        egress: { allowedCIDRs: ["0.0.0.0/0"] },
        verification: {
          path: "/health",
          method: "GET",
          expectedStatuses: [200, 204],
        },
      },
      driverConfig: {
        outboundIdentity: {
          schemaVersion: "outbound-identity.v1",
          supportedModes: ["REQUEST_PASSTHROUGH"],
          requestPassthrough: {
            credentialTypes: ["ACCESS_TOKEN"],
            businessInjection: {
              prefix: "Bearer",
              headerName: "Authorization",
            },
          },
          supportedSubjectTypes: ["USER"],
        },
      },
    });
    console.log("created provider", provider.id);
  }

  // Connection
  let connections = await req(
    "GET",
    `/workspaces/${wid}/providers/${provider.id}/connections`,
    token,
  );
  let connection = (connections.items || [])[0];
  if (!connection) {
    connection = await req(
      "POST",
      `/workspaces/${wid}/providers/${provider.id}/connections`,
      token,
      {
        name: "Acme Staging",
        alias: "acme-staging",
        environment: "STAGING",
        outboundIdentity: {
          mode: "REQUEST_PASSTHROUGH",
          schemaVersion: "outbound-connection.v1",
          requestPassthrough: { maxResidenceSeconds: 600 },
        },
        grantedScopes: [],
        policy: {},
      },
    );
    console.log("created connection", connection.id);
  }

  // Tools catalog (fictional ecommerce)
  const toolDefs =
    DEMO_LOCALE === "en"
      ? [
          {
            name: "List Products",
            slug: "list-products",
            description: "List the product catalog by page (demo data)",
            path: "/v1/products",
            method: "GET",
            props: {
              page: { type: "integer", description: "Page number" },
              pageSize: { type: "integer", description: "Items per page" },
              keyword: { type: "string", description: "Search keyword" },
            },
            required: ["page", "pageSize"],
            callable: "list_products",
          },
          {
            name: "Create Order",
            slug: "create-order",
            description: "Create an order for a customer (demo only)",
            path: "/v1/orders",
            method: "POST",
            props: {
              customerId: { type: "string", description: "Customer ID" },
              sku: { type: "string", description: "SKU" },
              quantity: { type: "integer", description: "Quantity" },
            },
            required: ["customerId", "sku", "quantity"],
            callable: "create_order",
          },
          {
            name: "Get Order",
            slug: "get-order",
            description: "Get order details by order ID",
            path: "/v1/orders/{orderId}",
            method: "GET",
            props: { orderId: { type: "string", description: "Order ID" } },
            required: ["orderId"],
            callable: "get_order",
          },
          {
            name: "Search Customers",
            slug: "search-customers",
            description: "Search customers by email or phone number",
            path: "/v1/customers/search",
            method: "GET",
            props: {
              q: { type: "string", description: "Search query" },
              limit: { type: "integer", description: "Result limit" },
            },
            required: ["q"],
            callable: "search_customers",
          },
          {
            name: "Check Inventory",
            slug: "check-inventory",
            description: "Check available inventory for a SKU",
            path: "/v1/inventory/{sku}",
            method: "GET",
            props: { sku: { type: "string", description: "SKU" } },
            required: ["sku"],
            callable: "check_inventory",
          },
          {
            name: "Create Refund",
            slug: "create-refund",
            description: "Submit a refund request (demo only)",
            path: "/v1/refunds",
            method: "POST",
            props: {
              orderId: { type: "string", description: "Order ID" },
              reason: { type: "string", description: "Refund reason" },
            },
            required: ["orderId", "reason"],
            callable: "create_refund",
          },
        ]
      : [
          {
            name: "商品分页列表",
            slug: "list-products",
            description: "分页查询商品目录（演示数据）",
            path: "/v1/products",
            method: "GET",
            props: {
              page: { type: "integer", description: "页码" },
              pageSize: { type: "integer", description: "每页条数" },
              keyword: { type: "string", description: "关键词" },
            },
            required: ["page", "pageSize"],
            callable: "list_products",
          },
          {
            name: "创建订单",
            slug: "create-order",
            description: "为客户创建一笔订单（演示）",
            path: "/v1/orders",
            method: "POST",
            props: {
              customerId: { type: "string", description: "客户 ID" },
              sku: { type: "string", description: "SKU" },
              quantity: { type: "integer", description: "数量" },
            },
            required: ["customerId", "sku", "quantity"],
            callable: "create_order",
          },
          {
            name: "订单详情",
            slug: "get-order",
            description: "按订单号查询详情",
            path: "/v1/orders/{orderId}",
            method: "GET",
            props: { orderId: { type: "string", description: "订单号" } },
            required: ["orderId"],
            callable: "get_order",
          },
          {
            name: "客户搜索",
            slug: "search-customers",
            description: "按邮箱或手机号搜索客户",
            path: "/v1/customers/search",
            method: "GET",
            props: {
              q: { type: "string", description: "搜索词" },
              limit: { type: "integer", description: "返回条数" },
            },
            required: ["q"],
            callable: "search_customers",
          },
          {
            name: "库存查询",
            slug: "check-inventory",
            description: "查询 SKU 可用库存",
            path: "/v1/inventory/{sku}",
            method: "GET",
            props: { sku: { type: "string", description: "SKU" } },
            required: ["sku"],
            callable: "check_inventory",
          },
          {
            name: "退款申请",
            slug: "create-refund",
            description: "提交退款申请（演示）",
            path: "/v1/refunds",
            method: "POST",
            props: {
              orderId: { type: "string", description: "订单号" },
              reason: { type: "string", description: "原因" },
            },
            required: ["orderId", "reason"],
            callable: "create_refund",
          },
        ];

  const toolsList = await req(
    "GET",
    `/workspaces/${wid}/tools?page=1&pageSize=50`,
    token,
  );
  const existingSlugs = new Set((toolsList.items || []).map((t) => t.slug));

  for (const def of toolDefs) {
    if (existingSlugs.has(def.slug)) {
      console.log("tool exists", def.slug);
      continue;
    }
    const created = await req("POST", `/workspaces/${wid}/tools`, token, {
      providerId: provider.id,
      defaultConnectionId: connection.id,
      name: def.name,
      slug: def.slug,
      description: def.description,
      draft: {
        ...draftSpec(
          def.path,
          def.method,
          def.description,
          def.props,
          def.required,
        ),
        defaultConnectionId: connection.id,
      },
    });
    const toolId = created.tool?.id || created.id;
    const versionId = created.draft?.id;
    const lock = created.draft?.lockVersion || 1;
    if (!toolId || !versionId) {
      console.warn("unexpected create tool response", created);
      continue;
    }
    const pub = await req(
      "POST",
      `/workspaces/${wid}/tools/${toolId}/versions/${versionId}/__command/force-publish`,
      token,
      {
        callableName: def.callable,
        callableDescription: def.description,
        lockVersion: lock,
        reason: "seed demo tools for README screenshots (fictional data)",
      },
    );
    console.log("published tool", def.slug, "release", pub.releaseNo);
  }

  // Agent
  let agents = await req(
    "GET",
    `/workspaces/${wid}/agents?page=1&pageSize=20`,
    token,
  );
  let agent = (agents.items || []).find((a) => a.name === AGENT_NAME);
  if (!agent) {
    agent = await req("POST", `/workspaces/${wid}/agents`, token, {
      name: AGENT_NAME,
      roleDescription:
        DEMO_LOCALE === "en"
          ? "Demo Agent for ecommerce operations: look up products and inventory, then assist with orders and refunds (fictional workspace only)."
          : "面向电商运营的演示 Agent：查询商品与库存、协助下单与退款（仅演示空间，无真实业务）。",
      modelConfigId: model.id,
      systemPrompt:
        DEMO_LOCALE === "en"
          ? "You are an Acme Commerce assistant. Use only bound tools for lookups and demo operations, and respond concisely and professionally."
          : "你是 Acme Commerce 的店员助手。只使用已绑定工具完成查询与演示操作，回答简洁专业。",
    });
    console.log("created agent", agent.id);
  }

  // Bind published tools to agent
  const toolsAfter = await req(
    "GET",
    `/workspaces/${wid}/tools?page=1&pageSize=50`,
    token,
  );
  for (const t of toolsAfter.items || []) {
    try {
      await req(
        "PUT",
        `/workspaces/${wid}/agents/${agent.id}/capabilities/${t.id}`,
        token,
        {
          versionPolicy: "FOLLOW_ACTIVE",
          connectionId: connection.id,
          enabled: true,
          configOverrides: {},
          lockVersion: 0,
        },
      );
      console.log("bound", t.name || t.slug);
    } catch (e) {
      // may already be bound with higher lock
      console.warn("bind", t.slug, String(e.message).slice(0, 120));
    }
  }

  // Agent Access client (optional, for screenshots)
  try {
    const clients = await req(
      "GET",
      `/workspaces/${wid}/agent-access/clients?page=1&pageSize=20`,
      token,
    ).catch(() => ({ items: [] }));
    if (!(clients.items || []).length) {
      // try common path
      try {
        const created = await req(
          "POST",
          `/workspaces/${wid}/agent-access/clients`,
          token,
          {
            name: "Acme Partner App",
            description:
              DEMO_LOCALE === "en"
                ? "Fictional third-party integration client for demos"
                : "演示用第三方接入客户端（虚构）",
            grantType: "client_credentials",
          },
        );
        console.log("created aap client", created.id || created.clientId);
      } catch (e) {
        console.warn("aap client skip:", String(e.message).slice(0, 160));
      }
    }
  } catch (e) {
    console.warn("aap list skip", e.message);
  }

  const outDir = resolve(ROOT, DEMO_OUTPUT_DIR);
  mkdirSync(outDir, { recursive: true });
  const meta = {
    workspaceId: wid,
    slug: SLUG,
    displayName: DISPLAY,
    locale: DEMO_LOCALE,
    agentId: agent.id,
    providerId: provider.id,
    connectionId: connection.id,
    modelId: model.id,
  };
  writeFileSync(resolve(outDir, ".workspace-id"), wid + "\n");
  writeFileSync(
    resolve(outDir, "demo-workspace.json"),
    JSON.stringify(meta, null, 2),
  );
  console.log("meta written", meta);
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
