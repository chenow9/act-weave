/**
 * AAP multi-turn E2E via @actweave/agent-client against live stack.
 * Business questions only — no tool names in user prompts.
 * Uses REQUEST_PASSTHROUGH business token from mock-corp-expense.
 */
import { readFileSync, writeFileSync, mkdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { AgentAccessClient, StaticTokenProvider } from "../sdk/typescript/dist/index.js";

const __dirname = dirname(fileURLToPath(import.meta.url));
const cfg = JSON.parse(readFileSync("/tmp/aap_e2e_config.json", "utf8"));
const AAP_BASE = process.env.AAP_BASE || "http://127.0.0.1:8082/api/agent-access/v1";
const TOKEN_URL = process.env.AAP_TOKEN_URL || "http://127.0.0.1:8082/api/agent-access/v1/oauth/token";
const MOCK = process.env.MOCK_BIZ || "http://127.0.0.1:18080";
const OUT_DIR = join(__dirname, "../docs/verification/e2e-full-chain-2026-07-25");
mkdirSync(OUT_DIR, { recursive: true });

const SCOPES = [
  "agent:read",
  "conversation:create",
  "conversation:read",
  "run:create",
  "run:read",
  "run:cancel",
  "event:read",
  "interaction:decide",
].join(" ");

async function mintBizToken(username) {
  const res = await fetch(`${MOCK}/oauth/token`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username, password: "demo" }),
  });
  if (!res.ok) throw new Error(`biz token ${username}: ${res.status} ${await res.text()}`);
  const j = await res.json();
  return j.access_token;
}

async function mintAapToken() {
  const basic = Buffer.from(`${cfg.clientId}:${cfg.clientSecret}`).toString("base64");
  const body = new URLSearchParams({
    grant_type: "client_credentials",
    agent_id: cfg.agentId,
    scope: SCOPES,
  });
  const res = await fetch(TOKEN_URL, {
    method: "POST",
    headers: {
      Authorization: `Basic ${basic}`,
      "Content-Type": "application/x-www-form-urlencoded",
      Accept: "application/json",
    },
    body,
  });
  if (!res.ok) throw new Error(`aap token: ${res.status} ${await res.text()}`);
  return res.json();
}

function envelope(bizToken) {
  return {
    schemaVersion: "outbound-credentials.v1",
    bindings: [
      {
        connectionId: cfg.connectionId,
        credentialType: "ACCESS_TOKEN",
        value: bizToken,
        expiresAt: "2099-01-01T00:00:00Z",
      },
    ],
  };
}

function userMessage(text) {
  return {
    type: "message",
    role: "user",
    content: [{ type: "text", text }],
  };
}

function collectFromSnapshot(snapshot, bag) {
  if (!snapshot) return;
  if (snapshot.run?.status) bag.statuses.add(String(snapshot.run.status));
  for (const item of snapshot.items || []) {
    bag.itemTypes.add(String(item.type || "unknown"));
    if (String(item.type).includes("tool") || item.toolName || item.name) {
      bag.toolCalls.push({
        type: item.type,
        name: item.toolName || item.name || item.callableName,
        status: item.status,
        id: item.id,
      });
    }
    if (item.type === "message" || item.role === "assistant") {
      const text = extractText(item);
      if (text) bag.assistantTexts.push(text);
    }
  }
  for (const inter of snapshot.interactions || []) {
    bag.interactions.push({
      id: inter.id,
      kind: inter.kind,
      status: inter.status,
    });
  }
}

function extractText(item) {
  if (typeof item.content === "string") return item.content;
  if (Array.isArray(item.content)) {
    return item.content
      .map((p) => (typeof p === "string" ? p : p?.text || p?.content || ""))
      .filter(Boolean)
      .join("\n");
  }
  if (item.text) return String(item.text);
  return "";
}

async function followUntilTerminal(client, runId, bag, timeoutMs = 180_000) {
  const started = Date.now();
  for await (const { message, snapshot } of client.followRun(cfg.workspaceId, cfg.agentId, runId)) {
    if (message?.kind === "protocol_event") {
      bag.events.push({
        type: message.event?.type,
        eventId: message.event?.eventId,
        sequence: message.event?.sequence,
      });
      const t = String(message.event?.type || "");
      if (t.includes("tool")) bag.eventToolHints.push(t);
      if (t.includes("message") || t.includes("model") || t.includes("assistant")) bag.eventModelHints.push(t);
      if (t.includes("interaction") || t.includes("confirmation")) bag.eventInteractionHints.push(t);
    }
    collectFromSnapshot(snapshot, bag);
    const status = snapshot?.run?.status;
    if (status === "waiting_interaction") {
      const pending = (snapshot.interactions || []).find(
        (i) => String(i.status) === "pending" || String(i.status) === "open",
      );
      if (pending?.id) {
        bag.decisions.push({ interactionId: pending.id, decision: "approve" });
        try {
          await client.decideInteraction(cfg.workspaceId, cfg.agentId, runId, pending.id, "approve", {
            idempotencyKey: crypto.randomUUID(),
          });
        } catch (e) {
          bag.decisionErrors.push(String(e));
        }
      }
    }
    if (status && ["completed", "failed", "cancelled"].includes(String(status))) {
      bag.terminal = status;
      bag.finalSnapshot = snapshot;
      break;
    }
    if (Date.now() - started > timeoutMs) {
      bag.terminal = "timeout";
      bag.finalSnapshot = snapshot;
      break;
    }
  }
}

async function runTurn(client, { conversationId, text, bizToken, turn }) {
  const bag = {
    turn,
    text,
    statuses: new Set(),
    itemTypes: new Set(),
    toolCalls: [],
    assistantTexts: [],
    interactions: [],
    decisions: [],
    decisionErrors: [],
    events: [],
    eventToolHints: [],
    eventModelHints: [],
    eventInteractionHints: [],
    terminal: null,
    finalSnapshot: null,
    runId: null,
    error: null,
  };
  try {
    const run = await client.createRun(
      cfg.workspaceId,
      cfg.agentId,
      {
        conversationId,
        // SDK createRun Accept is application/json; stream=true requires text/event-stream.
        stream: false,
        input: [userMessage(text)],
        outboundCredentials: envelope(bizToken),
      },
      { idempotencyKey: crypto.randomUUID() },
    );
    bag.runId = run.run?.id;
    bag.conversationId = run.run?.conversationId || conversationId;
    await followUntilTerminal(client, bag.runId, bag);
  } catch (e) {
    bag.error = String(e?.stack || e);
  }
  // serialize sets
  return {
    ...bag,
    statuses: [...bag.statuses],
    itemTypes: [...bag.itemTypes],
  };
}

async function main() {
  console.log("mint AAP + biz tokens…");
  const aap = await mintAapToken();
  console.log("aap token ok expires_in=", aap.expires_in);
  const wangTok = await mintBizToken("wang.li");
  const chenTok = await mintBizToken("chen.wei");

  const client = new AgentAccessClient({
    baseUrl: AAP_BASE,
    tokenProvider: new StaticTokenProvider(aap.access_token),
  });

  const conv = await client.createConversation(
    cfg.workspaceId,
    cfg.agentId,
    { title: "费用报销多轮E2E" },
    { idempotencyKey: crypto.randomUUID() },
  );
  const conversationId = conv.conversation?.id || conv.id;
  console.log("conversation", conversationId);

  // Multi-turn real business dialog (employee then manager)
  const turns = [
    {
      who: "wang.li",
      token: wangTok,
      text: "你好，我是销售一部的王丽。帮我看看我现在的身份信息，以及部门预算还剩多少？最近我有没有在途的报销？",
    },
    {
      who: "wang.li",
      token: wangTok,
      text: "我下周要去杭州拜访客户，请帮我提交一笔差旅报销：标题「杭州客户拜访差旅」，类别差旅，金额 3560 元，事由是拜访杭州重点客户并签约跟进。直接提交审批就行。",
    },
    {
      who: "wang.li",
      token: wangTok,
      text: "刚才那笔报销提交成功了吗？把单号和状态告诉我，另外部门预算大概还剩多少？",
    },
    {
      who: "chen.wei",
      token: chenTok,
      text: "我是主管陈伟。把目前待我审批的报销单列出来，重点看金额和事由。如果有王丽或赵敏提交的合理差旅，请直接帮我通过，备注写「费用合理，同意报销」。",
    },
    {
      who: "chen.wei",
      token: chenTok,
      text: "审批做完了吗？再确认一下现在还有没有待我审批的单子，以及销售一部预算还剩多少。",
    },
  ];

  const results = [];
  let cid = conversationId;
  for (let i = 0; i < turns.length; i++) {
    const t = turns[i];
    console.log(`\n=== TURN ${i + 1} (${t.who}) ===`);
    console.log("USER:", t.text);
    const r = await runTurn(client, {
      conversationId: cid,
      text: t.text,
      bizToken: t.token,
      turn: i + 1,
    });
    if (r.conversationId) cid = r.conversationId;
    console.log("terminal:", r.terminal, "runId:", r.runId);
    console.log("tools:", r.toolCalls.map((x) => x.name || x.type).join(", ") || "(none recorded)");
    console.log("assistant snippets:", (r.assistantTexts.join(" | ") || "(none)").slice(0, 400));
    if (r.error) console.error("ERROR:", r.error.slice(0, 500));
    results.push({ who: t.who, text: t.text, ...r });
  }

  const report = {
    at: new Date().toISOString(),
    workspaceId: cfg.workspaceId,
    agentId: cfg.agentId,
    connectionId: cfg.connectionId,
    conversationId: cid,
    turns: results.map((r) => ({
      turn: r.turn,
      who: r.who,
      userText: r.text,
      runId: r.runId,
      terminal: r.terminal,
      statuses: r.statuses,
      itemTypes: r.itemTypes,
      toolCalls: r.toolCalls,
      interactions: r.interactions,
      decisions: r.decisions,
      decisionErrors: r.decisionErrors,
      eventTypesSample: r.events.slice(0, 40).map((e) => e.type),
      eventToolHints: r.eventToolHints,
      eventModelHints: r.eventModelHints,
      eventInteractionHints: r.eventInteractionHints,
      assistantTexts: r.assistantTexts,
      error: r.error,
    })),
  };
  const out = join(OUT_DIR, "aap-multi-turn-result.json");
  writeFileSync(out, JSON.stringify(report, null, 2));
  console.log("\nWrote", out);

  // basic assertions for exit code
  const completed = results.filter((r) => r.terminal === "completed").length;
  const anyTools = results.some((r) => r.toolCalls.length > 0 || r.eventToolHints.length > 0);
  const anyFail = results.some((r) => r.terminal === "failed" || r.error);
  console.log(JSON.stringify({ completed, anyTools, anyFail, turns: results.length }, null, 2));
  if (completed === 0 || anyFail) process.exitCode = 2;
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
