/**
 * Chrome verification for minimal LLM SSE paths:
 * 1) Agent create → AI 智能整理
 * 2) Workflow generate dock → send turn
 *
 * Usage (from frontend/):
 *   node e2e/llm-sse-chrome-verify.mjs
 */
import { chromium } from "playwright";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const BASE = process.env.ACTWEAVE_BASE_URL || "http://127.0.0.1:5174";
const USER = process.env.ACTWEAVE_ADMIN_USER || "admin";
const PASS = process.env.ACTWEAVE_ADMIN_PASS || "actweave-admin-dev-change-me";
const OUT_DIR = path.join(path.dirname(fileURLToPath(import.meta.url)), "../../.run-logs");

function ensureOutDir() {
  fs.mkdirSync(OUT_DIR, { recursive: true });
}

async function login(page) {
  await page.goto(`${BASE}/login`, { waitUntil: "networkidle", timeout: 60_000 });
  await page.fill('input[type="text"], input[name="username"], input[autocomplete="username"]', USER);
  await page.fill('input[type="password"], input[autocomplete="current-password"]', PASS);
  await Promise.all([
    page.waitForURL((url) => !url.pathname.includes("/login"), { timeout: 30_000 }).catch(() => null),
    page.click('button[type="submit"], button:has-text("登录"), button:has-text("Login")'),
  ]);
  // Change-password gate: if still on change-password, skip for this verify.
  if (page.url().includes("change-password")) {
    throw new Error("Account requires password change; update credentials before verify.");
  }
}

async function selectFirstOption(page, selectLocator) {
  const select = page.locator(selectLocator).first();
  if ((await select.count()) === 0) return false;
  const options = select.locator("option");
  const count = await options.count();
  for (let i = 0; i < count; i += 1) {
    const value = await options.nth(i).getAttribute("value");
    if (value && value.trim()) {
      await select.selectOption(value);
      return true;
    }
  }
  return false;
}

function trackSse(page, match) {
  const hits = [];
  page.on("request", (req) => {
    if (!match(req.url())) return;
    hits.push({
      phase: "request",
      url: req.url(),
      method: req.method(),
      accept: req.headers()["accept"] || "",
      contentType: req.headers()["content-type"] || "",
    });
  });
  page.on("response", async (res) => {
    if (!match(res.url())) return;
    hits.push({
      phase: "response",
      url: res.url(),
      status: res.status(),
      contentType: res.headers()["content-type"] || "",
    });
  });
  return hits;
}

async function verifyAgentEnhance(page) {
  const hits = trackSse(page, (url) => url.includes("preview-prompt-enhancement"));
  await page.goto(`${BASE}/agents`, { waitUntil: "networkidle", timeout: 60_000 });
  await page.click('button:has-text("新建 Agent"), button:has-text("创建 Agent")').catch(async () => {
    await page.getByRole("button", { name: /新建 Agent|创建 Agent/ }).first().click();
  });
  await page.waitForTimeout(500);

  // Workspace + model selects (native or custom).
  await selectFirstOption(page, "select").catch(() => null);
  // Fill required system prompt.
  const prompt = page.locator("textarea").first();
  await prompt.fill("你是一个电商客服助手，帮助用户查询订单与物流。");

  // Try model select if present among selects.
  const selects = page.locator("select");
  const selectCount = await selects.count();
  for (let i = 0; i < selectCount; i += 1) {
    const el = selects.nth(i);
    const options = el.locator("option");
    const n = await options.count();
    for (let j = 0; j < n; j += 1) {
      const value = await options.nth(j).getAttribute("value");
      if (value && value.trim()) {
        await el.selectOption(value);
        break;
      }
    }
  }

  const weave = page.locator("button.agent-weave-button, button:has-text('AI 智能整理')").first();
  await weave.waitFor({ state: "visible", timeout: 15_000 });
  // Enable if still disabled by choosing more fields via UI labels.
  if (await weave.isDisabled()) {
    // Click any custom dropdowns with "业务空间" / "模型".
    const workspaceTrigger = page.getByText("业务空间", { exact: false }).first();
    if (await workspaceTrigger.count()) {
      await workspaceTrigger.click().catch(() => null);
      await page.keyboard.press("ArrowDown").catch(() => null);
      await page.keyboard.press("Enter").catch(() => null);
    }
    const modelTrigger = page.getByText("模型", { exact: false }).first();
    if (await modelTrigger.count()) {
      await modelTrigger.click().catch(() => null);
      await page.keyboard.press("ArrowDown").catch(() => null);
      await page.keyboard.press("Enter").catch(() => null);
    }
  }

  if (await weave.isDisabled()) {
    // Force-enable path is not available; still attempt click for network if possible.
    await page.screenshot({ path: path.join(OUT_DIR, "llm-sse-agent-disabled.png"), fullPage: true });
  }

  await weave.click({ force: true, timeout: 10_000 }).catch(() => null);
  // Wait for SSE request/response.
  await page.waitForTimeout(3000);
  await page.screenshot({ path: path.join(OUT_DIR, "llm-sse-agent-after-click.png"), fullPage: true });
  return hits;
}

async function verifySmartDag(page) {
  const hits = trackSse(page, (url) => url.includes("/workflow-generate-sessions/") && url.includes("/turns"));
  await page.goto(`${BASE}/workflow?generate=1`, { waitUntil: "networkidle", timeout: 60_000 });
  await page.waitForTimeout(800);

  if ((await page.locator("textarea.workflow-generate-prompt").count()) === 0) {
    const intent = page.getByRole("button", { name: /用一句话生成|Generate from a sentence/ }).first();
    if (await intent.isVisible().catch(() => false)) {
      await intent.click();
      await page.waitForTimeout(500);
    }
  }

  const textarea = page.locator("textarea.workflow-generate-prompt").first();
  if ((await textarea.count()) > 0) {
    await textarea.fill("生成一个查询订单状态的简单工作流");
  }
  const send = page.locator('[data-action="submit-generate"]').first();
  if ((await send.count()) > 0) {
    await send.click({ force: true }).catch(() => null);
    await page.waitForTimeout(4000);
  }
  await page.screenshot({ path: path.join(OUT_DIR, "llm-sse-smartdag-after-send.png"), fullPage: true });
  return hits;
}

function summarize(label, hits) {
  const req = hits.find((h) => h.phase === "request");
  const res = hits.find((h) => h.phase === "response");
  const ok =
    Boolean(req?.accept?.includes("text/event-stream")) &&
    Boolean(res?.contentType?.includes("text/event-stream")) &&
    (res?.status === 200 || res?.status === 201);
  return {
    label,
    ok,
    requestAccept: req?.accept || null,
    responseStatus: res?.status ?? null,
    responseContentType: res?.contentType || null,
    hitCount: hits.length,
    hits,
  };
}

async function main() {
  ensureOutDir();
  const browser = await chromium.launch({ headless: true, channel: process.env.CHROME_CHANNEL || undefined });
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const page = await context.newPage();
  const report = { base: BASE, user: USER, at: new Date().toISOString(), results: [] };

  try {
    await login(page);
    await page.screenshot({ path: path.join(OUT_DIR, "llm-sse-after-login.png"), fullPage: true });

    const agentHits = await verifyAgentEnhance(page);
    report.results.push(summarize("agent-preview-prompt-enhancement", agentHits));

    const dagHits = await verifySmartDag(page);
    report.results.push(summarize("smart-dag-turns", dagHits));
  } catch (error) {
    report.error = error instanceof Error ? error.message : String(error);
    await page.screenshot({ path: path.join(OUT_DIR, "llm-sse-error.png"), fullPage: true }).catch(() => null);
  } finally {
    await browser.close();
  }

  const outFile = path.join(OUT_DIR, "llm-sse-chrome-verify.json");
  fs.writeFileSync(outFile, JSON.stringify(report, null, 2));
  console.log(JSON.stringify(report, null, 2));
  console.log(`\nWrote ${outFile}`);

  const allOk = report.results?.length > 0 && report.results.every((r) => r.ok);
  if (!allOk) process.exitCode = 1;
}

main();
