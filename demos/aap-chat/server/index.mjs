/**
 * Minimal AAP BFF for the chat demo.
 * Holds Client Secret + optional REQUEST_PASSTHROUGH business token server-side;
 * browser only talks to /bff/* and never sees long-lived secrets.
 */
import http from "node:http";
import { randomUUID } from "node:crypto";
import { readFileSync, existsSync } from "node:fs";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));
loadDotEnv(resolve(__dirname, "../.env"));

const PORT = Number(process.env.BFF_PORT || 8790);
const AAP_BASE = stripSlash(process.env.AAP_BASE_URL || "http://127.0.0.1:8082/api/agent-access/v1");
const CLIENT_ID = (process.env.AAP_CLIENT_ID || "").trim();
const CLIENT_SECRET = (process.env.AAP_CLIENT_SECRET || "").trim();
const WORKSPACE_ID = (process.env.AAP_WORKSPACE_ID || "").trim();
const AGENT_ID = (process.env.AAP_AGENT_ID || "").trim();
const SCOPES = (
  process.env.AAP_SCOPES ||
  "agent:read conversation:create conversation:read run:create run:read run:cancel event:read interaction:decide"
).trim();
/** Service Connection UUID used by Agent capabilities (REQUEST_PASSTHROUGH). */
const OUTBOUND_CONNECTION_ID = (process.env.OUTBOUND_CONNECTION_ID || "").trim();
/** Optional static business token from .env (demo only; prefer UI attach). */
const OUTBOUND_ACCESS_TOKEN_ENV = (process.env.OUTBOUND_ACCESS_TOKEN || "").trim();
const OUTBOUND_TOKEN_TTL_SECONDS = Math.max(
  60,
  Number(process.env.OUTBOUND_TOKEN_TTL_SECONDS || 600) || 600,
);
const PROTOCOL_VERSION = "2026-07-20";

/** @type {{ token: string, expiresAt: number } | null} */
let cachedToken = null;

/**
 * In-memory business ACCESS_TOKEN for REQUEST_PASSTHROUGH.
 * Never returned to the browser after attach; cleared on expiry.
 * @type {{ value: string, expiresAt: string } | null}
 */
let outboundBinding = null;

if (OUTBOUND_ACCESS_TOKEN_ENV && OUTBOUND_CONNECTION_ID) {
  outboundBinding = {
    value: OUTBOUND_ACCESS_TOKEN_ENV,
    expiresAt: new Date(Date.now() + OUTBOUND_TOKEN_TTL_SECONDS * 1000).toISOString(),
  };
}

const server = http.createServer(async (req, res) => {
  try {
    const url = new URL(req.url || "/", `http://${req.headers.host || "localhost"}`);
    if (req.method === "OPTIONS") {
      cors(res);
      res.writeHead(204);
      res.end();
      return;
    }

    if (req.method === "GET" && url.pathname === "/bff/health") {
      return json(res, 200, {
        ok: true,
        aapConfigured: isAapConfigured(),
        workspaceId: WORKSPACE_ID || null,
        agentId: AGENT_ID || null,
        outbound: publicOutboundStatus(),
      });
    }

    if (req.method === "GET" && url.pathname === "/bff/config") {
      return json(res, 200, {
        workspaceId: WORKSPACE_ID,
        agentId: AGENT_ID,
        aapBaseUrl: AAP_BASE,
        aapConfigured: isAapConfigured(),
        outbound: publicOutboundStatus(),
      });
    }

    // Attach / clear REQUEST_PASSTHROUGH business token (write-only).
    if (req.method === "POST" && url.pathname === "/bff/outbound-credentials") {
      if (!OUTBOUND_CONNECTION_ID) {
        return json(res, 400, {
          error: {
            code: "OUTBOUND_CONNECTION_NOT_CONFIGURED",
            message:
              "Set OUTBOUND_CONNECTION_ID in demos/aap-chat/.env to the Service Connection UUID (e.g. AI识别管理平台).",
          },
        });
      }
      const body = await readJSON(req);
      if (body?.clear === true) {
        clearOutboundBinding();
        return json(res, 200, { outbound: publicOutboundStatus() });
      }
      const value = String(body?.value || "").trim();
      if (!value) {
        return json(res, 400, {
          error: { code: "EMPTY_TOKEN", message: "value (business ACCESS_TOKEN) is required" },
        });
      }
      let expiresAt = String(body?.expiresAt || "").trim();
      if (expiresAt) {
        const parsed = Date.parse(expiresAt);
        if (Number.isNaN(parsed)) {
          return json(res, 400, {
            error: { code: "INVALID_EXPIRES_AT", message: "expiresAt must be ISO-8601" },
          });
        }
        expiresAt = new Date(parsed).toISOString();
      } else {
        expiresAt = new Date(Date.now() + OUTBOUND_TOKEN_TTL_SECONDS * 1000).toISOString();
      }
      outboundBinding = { value, expiresAt };
      // Never echo the token.
      return json(res, 200, {
        outbound: publicOutboundStatus(),
        message: "业务出站 Token 已绑定到 BFF 内存（仅用于后续 createRun）",
      });
    }

    if (req.method === "POST" && url.pathname === "/bff/chat") {
      if (!isAapConfigured()) {
        return json(res, 503, {
          error: {
            code: "BFF_NOT_CONFIGURED",
            message: "Set AAP_CLIENT_ID / AAP_CLIENT_SECRET / AAP_WORKSPACE_ID / AAP_AGENT_ID in demos/aap-chat/.env",
          },
        });
      }
      const body = await readJSON(req);
      const text = String(body?.text || "").trim();
      if (!text) {
        return json(res, 400, { error: { code: "EMPTY_TEXT", message: "text is required" } });
      }
      // Soft gate: when OUTBOUND_REQUIRE_FOR_CHAT=true, refuse chat without a live
      // business token. Default is optional so pure-LLM turns still work; tools that
      // need REQUEST_PASSTHROUGH will fail later unless a token is bound.
      const requireOutbound =
        Boolean(OUTBOUND_CONNECTION_ID) &&
        /^(1|true|yes)$/i.test(String(process.env.OUTBOUND_REQUIRE_FOR_CHAT || "").trim());
      if (requireOutbound && !getLiveOutboundBinding()) {
        return json(res, 422, {
          error: {
            code: "OUTBOUND_CREDENTIAL_REQUIRED",
            message:
              "Agent 工具走 REQUEST_PASSTHROUGH：请先在页面绑定业务 ACCESS_TOKEN，或在 .env 设置 OUTBOUND_ACCESS_TOKEN。",
            connectionId: OUTBOUND_CONNECTION_ID,
          },
        });
      }
      const conversationId = body?.conversationId ? String(body.conversationId) : undefined;
      const token = await getAccessToken();
      const run = await createRun(token, text, conversationId);
      return json(res, 200, {
        conversationId: run.conversationId,
        runId: run.runId,
        accessToken: token,
        expiresIn: tokenExpiresInSeconds(),
        aapBaseUrl: AAP_BASE,
        workspaceId: WORKSPACE_ID,
        agentId: AGENT_ID,
        outboundAttached: Boolean(getLiveOutboundBinding()),
      });
    }

    if (req.method === "POST" && url.pathname === "/bff/token") {
      if (!CLIENT_ID || !CLIENT_SECRET || !AGENT_ID) {
        return json(res, 503, {
          error: { code: "BFF_NOT_CONFIGURED", message: "AAP credentials missing" },
        });
      }
      // force=true so SSE 401 / force-refresh always gets a fresh short-lived token.
      const token = await getAccessToken(true);
      return json(res, 200, {
        accessToken: token,
        expiresIn: tokenExpiresInSeconds(),
        aapBaseUrl: AAP_BASE,
        workspaceId: WORKSPACE_ID,
        agentId: AGENT_ID,
      });
    }

    json(res, 404, { error: { code: "NOT_FOUND", message: "unknown route" } });
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    console.error("[bff]", message);
    // Surface stable AAP outbound codes instead of a generic 502 when possible.
    if (/OUTBOUND_CREDENTIAL_REQUIRED/.test(message)) {
      return json(res, 422, {
        error: {
          code: "OUTBOUND_CREDENTIAL_REQUIRED",
          message:
            "Agent 需要 REQUEST_PASSTHROUGH 业务 Token：请在页面顶部绑定 ACCESS_TOKEN（Connection 已配置）。",
          connectionId: OUTBOUND_CONNECTION_ID || null,
        },
      });
    }
    if (/OUTBOUND_CREDENTIAL_INVALID|OUTBOUND_CREDENTIAL_/.test(message)) {
      return json(res, 400, {
        error: {
          code: "OUTBOUND_CREDENTIAL_INVALID",
          message:
            "业务出站凭证无效：检查 Token 是否过期、Connection ID 是否与 Agent 能力绑定一致。",
          detail: message,
        },
      });
    }
    json(res, 502, {
      error: {
        code: "BFF_UPSTREAM",
        message,
      },
    });
  }
});

server.listen(PORT, "127.0.0.1", () => {
  console.log(`[aap-chat-bff] http://127.0.0.1:${PORT}`);
  console.log(`[aap-chat-bff] AAP=${AAP_BASE}`);
  console.log(
    `[aap-chat-bff] credentials=${CLIENT_ID ? "set" : "missing"} workspace=${WORKSPACE_ID || "—"} agent=${AGENT_ID || "—"}`,
  );
  console.log(
    `[aap-chat-bff] outbound connection=${OUTBOUND_CONNECTION_ID || "—"} token=${getLiveOutboundBinding() ? "bound" : "missing"}`,
  );
});

function isAapConfigured() {
  return Boolean(CLIENT_ID && CLIENT_SECRET && WORKSPACE_ID && AGENT_ID);
}

/** Public outbound status — never includes token value. */
function publicOutboundStatus() {
  const live = getLiveOutboundBinding();
  const configured = Boolean(OUTBOUND_CONNECTION_ID);
  const requireForChat = /^(1|true|yes)$/i.test(
    String(process.env.OUTBOUND_REQUIRE_FOR_CHAT || "").trim(),
  );
  return {
    /** Show the bind panel when a Connection is configured. */
    required: configured,
    /** When true, /bff/chat refuses messages until a token is bound. */
    requireForChat: configured && requireForChat,
    connectionId: OUTBOUND_CONNECTION_ID || null,
    bound: Boolean(live),
    expiresAt: live?.expiresAt || null,
  };
}

function getLiveOutboundBinding() {
  if (!outboundBinding?.value) return null;
  const exp = Date.parse(outboundBinding.expiresAt);
  if (!Number.isNaN(exp) && exp <= Date.now() + 5_000) {
    clearOutboundBinding();
    return null;
  }
  return outboundBinding;
}

function clearOutboundBinding() {
  if (outboundBinding) {
    outboundBinding.value = "";
  }
  outboundBinding = null;
}

function buildOutboundCredentialsEnvelope() {
  const live = getLiveOutboundBinding();
  if (!OUTBOUND_CONNECTION_ID || !live) return undefined;
  return {
    schemaVersion: "outbound-credentials.v1",
    bindings: [
      {
        connectionId: OUTBOUND_CONNECTION_ID,
        credentialType: "ACCESS_TOKEN",
        value: live.value,
        expiresAt: live.expiresAt,
      },
    ],
  };
}

async function getAccessToken(force = false) {
  const now = Date.now();
  if (!force && cachedToken && cachedToken.expiresAt > now + 30_000) {
    return cachedToken.token;
  }
  const basic = Buffer.from(`${CLIENT_ID}:${CLIENT_SECRET}`, "utf8").toString("base64");
  const body = new URLSearchParams({
    grant_type: "client_credentials",
    agent_id: AGENT_ID,
    scope: SCOPES,
  });
  const res = await fetch(`${AAP_BASE}/oauth/token`, {
    method: "POST",
    headers: {
      Authorization: `Basic ${basic}`,
      "Content-Type": "application/x-www-form-urlencoded",
      Accept: "application/json",
    },
    body,
  });
  const payload = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new Error(
      `token failed HTTP ${res.status}: ${payload?.error_description || payload?.error || JSON.stringify(payload)}`,
    );
  }
  const token = String(payload.access_token || "");
  const expiresIn = Number(payload.expires_in || 600);
  if (!token) throw new Error("token response missing access_token");
  cachedToken = { token, expiresAt: now + expiresIn * 1000 };
  return token;
}

/** Remaining lifetime of the cached AAP access token, for client MemoryTokenProvider skew. */
function tokenExpiresInSeconds() {
  if (!cachedToken) return undefined;
  const secs = Math.floor((cachedToken.expiresAt - Date.now()) / 1000);
  return secs > 0 ? secs : 1;
}

async function createRun(accessToken, text, conversationId) {
  let convId = conversationId;
  if (!convId) {
    const convRes = await fetch(
      `${AAP_BASE}/workspaces/${WORKSPACE_ID}/agents/${AGENT_ID}/conversations`,
      {
        method: "POST",
        headers: aapJsonHeaders(accessToken),
        body: JSON.stringify({ title: "AAP Chat Demo" }),
      },
    );
    const convPayload = await convRes.json().catch(() => ({}));
    if (!convRes.ok) {
      throw new Error(
        `createConversation HTTP ${convRes.status}: ${JSON.stringify(convPayload?.error || convPayload)}`,
      );
    }
    convId = convPayload.conversation?.id || convPayload.id;
    if (!convId) throw new Error("createConversation missing id");
  }

  // stream:false + Accept: application/json — BFF only needs the accepted Run id.
  // Browser follows events via SSE (followRun).
  const runBody = {
    conversationId: convId,
    stream: false,
    input: [
      {
        type: "message",
        role: "user",
        content: [{ type: "text", text }],
      },
    ],
  };
  const outbound = buildOutboundCredentialsEnvelope();
  if (outbound) {
    runBody.outboundCredentials = outbound;
  }

  const runRes = await fetch(`${AAP_BASE}/workspaces/${WORKSPACE_ID}/agents/${AGENT_ID}/runs`, {
    method: "POST",
    headers: aapJsonHeaders(accessToken),
    body: JSON.stringify(runBody),
  });
  // Best-effort: do not retain envelope on the request object beyond fetch.
  if (runBody.outboundCredentials) {
    for (const b of runBody.outboundCredentials.bindings) b.value = "";
    delete runBody.outboundCredentials;
  }

  const runPayload = await runRes.json().catch(() => ({}));
  if (!runRes.ok) {
    throw new Error(`createRun HTTP ${runRes.status}: ${JSON.stringify(runPayload?.error || runPayload)}`);
  }
  const runId = runPayload.run?.id || runPayload.id;
  if (!runId) throw new Error("createRun missing run id");
  return { conversationId: convId || runPayload.run?.conversationId || "", runId };
}

function aapJsonHeaders(accessToken) {
  return {
    Authorization: `Bearer ${accessToken}`,
    "Content-Type": "application/json",
    Accept: "application/json",
    "ActWeave-Protocol-Version": PROTOCOL_VERSION,
    "Idempotency-Key": randomUUID(),
  };
}

function cors(res) {
  res.setHeader("Access-Control-Allow-Origin", "*");
  res.setHeader("Access-Control-Allow-Methods", "GET,POST,OPTIONS");
  res.setHeader("Access-Control-Allow-Headers", "Content-Type, Authorization");
}

function json(res, status, body) {
  cors(res);
  res.writeHead(status, { "Content-Type": "application/json; charset=utf-8", "Cache-Control": "no-store" });
  res.end(JSON.stringify(body));
}

async function readJSON(req) {
  const chunks = [];
  for await (const chunk of req) chunks.push(chunk);
  const raw = Buffer.concat(chunks).toString("utf8");
  if (!raw.trim()) return {};
  return JSON.parse(raw);
}

function stripSlash(value) {
  return value.replace(/\/+$/, "");
}

function loadDotEnv(path) {
  if (!existsSync(path)) return;
  const text = readFileSync(path, "utf8");
  for (const line of text.split("\n")) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#")) continue;
    const eq = trimmed.indexOf("=");
    if (eq < 0) continue;
    const key = trimmed.slice(0, eq).trim();
    let val = trimmed.slice(eq + 1).trim();
    if (
      (val.startsWith('"') && val.endsWith('"')) ||
      (val.startsWith("'") && val.endsWith("'"))
    ) {
      val = val.slice(1, -1);
    }
    if (!(key in process.env)) process.env[key] = val;
  }
}
