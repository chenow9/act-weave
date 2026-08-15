/**
 * Sentinel retest after Forge DEF-01/DEF-02 fix @ 89d75ce.
 * Real Chrome + live stack; Smart DAG failure path uses network fulfill
 * (retryable SMART_DAG_TURN_FAILURE) so recovery card is exercised even if model GW is down.
 */
import { chromium } from "playwright";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";

const BASE = process.env.E2E_BASE_URL || "http://127.0.0.1:5174";
const USER = process.env.E2E_USER || "admin";
const PASS = process.env.E2E_PASS || "actweave-admin-dev-change-me";
const OUT =
  process.env.E2E_EVIDENCE_DIR ||
  join(process.cwd(), "..", "docs", "verification", "zkl-56-pm-e2e-ux-fixes-2026-07-26-retest");

mkdirSync(OUT, { recursive: true });
const results = [];

function record(name, status, detail = "") {
  results.push({ name, status, detail, at: new Date().toISOString() });
  const line = `${status.padEnd(4)} ${name}${detail ? " — " + detail : ""}`;
  if (status === "PASS") console.log(line);
  else if (status === "SKIP") console.warn(line);
  else console.error(line);
}

async function shot(page, name) {
  const file = join(OUT, `${name}.png`);
  await page.screenshot({ path: file, fullPage: true });
  return file;
}

async function login(page) {
  await page.goto(`${BASE}/login`, { waitUntil: "networkidle" });
  await page.locator('input[autocomplete="username"]').fill(USER);
  await page.locator('input[autocomplete="current-password"]').fill(PASS);
  await page.locator('button.login-primary-button[type="submit"], button[type="submit"]').first().click();
  await page.waitForURL((u) => !u.pathname.includes("/login"), { timeout: 30_000 });
}

async function pickSelectOption(selectLocator, hint) {
  if (!(await selectLocator.isVisible().catch(() => false))) return false;
  const options = await selectLocator.locator("option").allTextContents();
  const hit = options.find((t) => t.includes(hint) || t.includes("e2e-expense") || t.includes("费用"));
  if (!hit) return false;
  await selectLocator.selectOption({ label: hit });
  await selectLocator.page().waitForTimeout(500);
  return true;
}

async function bodyText(page) {
  return page.locator("body").innerText();
}

async function main() {
  const browser = await chromium.launch({ headless: true, channel: "chrome" });
  const context = await browser.newContext({
    locale: "zh-CN",
    viewport: { width: 1600, height: 1100 },
    timezoneId: "Asia/Singapore",
  });
  const page = await context.newPage();

  try {
    await login(page);
    record("login", "PASS");
    await shot(page, "r01-login");

    // ========== DEF-01 / AC-09 OpenAPI URL ==========
    await page.goto(`${BASE}/openapi-imports`, { waitUntil: "networkidle" }).catch(async () => {
      await page.goto(`${BASE}/openapi`, { waitUntil: "networkidle" });
    });
    if (!(await bodyText(page)).match(/OpenAPI|导入/)) {
      const nav = page.getByRole("link", { name: /OpenAPI|导入/ }).first();
      if (await nav.isVisible().catch(() => false)) await nav.click();
      await page.waitForTimeout(1000);
    }
    await page.waitForTimeout(800);
    const row = page.locator("tbody tr").filter({ hasText: /openapi|yaml|Ready|SUCCEEDED|8/i }).first()
      .or(page.locator("tbody tr").first());
    if (await row.isVisible({ timeout: 10000 }).catch(() => false)) {
      await row.click();
      await page.waitForTimeout(1200);
      const text = await bodyText(page);
      await shot(page, "r10-openapi-detail");
      const doublePort = /:(\d+):\1\b/.test(text) || /18080:18080/.test(text);
      const addrMatch = text.match(/https?:\/\/[^\s]+/g) || [];
      const serviceish = addrMatch.filter((a) => /127\.0\.0\.1|localhost|18080/.test(a));
      if (doublePort) {
        record("DEF-01/AC-09 OpenAPI URL", "FAIL", `still double port; sample=${serviceish.slice(0, 3).join(",")}`);
      } else if (serviceish.some((a) => /18080/.test(a) && !/:\d+:\d+/.test(a)) || /127\.0\.0\.1:18080(?!:)/.test(text)) {
        record("DEF-01/AC-09 OpenAPI URL", "PASS", `addresses=${serviceish.slice(0, 3).join(" | ") || "normalized no double port"}`);
      } else if (!doublePort && /服务地址|未配置/.test(text)) {
        // Strong text scrape of the summary strong
        const strongs = await page.locator(".config-summary-item strong, .import-detail strong").allTextContents();
        const joined = strongs.join(" | ");
        if (/:\d+:\d+/.test(joined) || /18080:18080/.test(joined)) {
          record("DEF-01/AC-09 OpenAPI URL", "FAIL", `summary strongs=${joined}`);
        } else {
          record("DEF-01/AC-09 OpenAPI URL", "PASS", `no double port in strongs=${joined.slice(0, 200)}`);
        }
      } else {
        record("DEF-01/AC-09 OpenAPI URL", "FAIL", `could not assert address; text snippet lacks host`);
      }
      // close dialog if open
      const close = page.getByRole("button", { name: /关闭|Close/ }).first();
      if (await close.isVisible().catch(() => false)) await close.click().catch(() => {});
    } else {
      record("DEF-01/AC-09 OpenAPI URL", "FAIL", "no openapi row");
    }

    // ========== DEF-02 / AC-07 Smart DAG recovery card ==========
    // Intercept session create (pass-through) and turns (force retryable failure)
    let sessionCreated = false;
    await page.route((url) => url.pathname.includes("/workflow-generate-sessions"), async (route) => {
      const req = route.request();
      const url = req.url();
      const method = req.method();
      // turns endpoint
      if (url.includes("/turns") && method === "POST") {
        await route.fulfill({
          status: 502,
          contentType: "application/json",
          headers: { "x-request-id": "req-sentinel-def02" },
          body: JSON.stringify({
            error: {
              code: "SMART_DAG_MODEL_UNAVAILABLE",
              message: "Sentinel retest: injected MODEL stage failure (retryable)",
              retryable: true,
              requestId: "req-sentinel-def02",
              traceId: "tr-sentinel-def02",
              details: [
                {
                  kind: "SMART_DAG_TURN_FAILURE",
                  stage: "MODEL",
                  retryable: true,
                  sessionId: "sess-sentinel-def02",
                  sessionLockVersion: 1,
                  turnId: "turn-sentinel-1",
                },
              ],
            },
          }),
        });
        return;
      }
      // :close
      if (url.includes(":close") || /\/workflow-generate-sessions\/[^/]+:close/.test(url)) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ id: "sess-sentinel-def02", status: "CLOSED" }),
        });
        return;
      }
      // create session
      if (method === "POST" && !url.includes("/turns")) {
        sessionCreated = true;
        await route.fulfill({
          status: 201,
          contentType: "application/json",
          body: JSON.stringify({
            id: "sess-sentinel-def02",
            sessionId: "sess-sentinel-def02",
            status: "OPEN",
            lockVersion: 1,
            workspaceId: "ws",
            agentId: "ag",
          }),
        });
        return;
      }
      await route.continue();
    });

    await page.goto(`${BASE}/workflow?generate=1`, { waitUntil: "networkidle" });
    if (!(await page.locator(".workflow-generate-dock").isVisible().catch(() => false))) {
      const intentBtn = page.getByRole("button", { name: /用一句话生成|Generate from a sentence/ }).first();
      if (await intentBtn.isVisible().catch(() => false)) await intentBtn.click();
      await page.waitForTimeout(800);
    }
    await page.waitForTimeout(800);

    const intent = page.locator("textarea.workflow-generate-prompt").first();
    await intent.fill("Sentinel DEF-02 恢复卡片验收意图");
    await shot(page, "r20-smart-dag-before");
    const genBtn = page.locator('[data-action="submit-generate"]').first();
    if (await genBtn.isVisible().catch(() => false) && await genBtn.isEnabled().catch(() => false)) {
      await genBtn.click();
      // Wait for recovery card (still data-testid=smart-dag-recovery-card, now in the generate dock)
      const card = page.getByTestId("smart-dag-recovery-card");
      const cardVisible = await card.isVisible({ timeout: 20000 }).catch(() => false);
      await shot(page, "r21-smart-dag-recovery-card");
      if (cardVisible) {
        const cardText = await card.innerText();
        const hasRetry = await page.locator('[data-action="generate-failure-retry-rewrite"]').isVisible().catch(() => false);
        const hasClose = await page.locator('[data-action="generate-failure-end-session"]').isVisible().catch(() => false);
        const hasStage = /MODEL|阶段|UNKNOWN/.test(cardText);
        const hasCode = /SMART_DAG|错误码|MODEL/.test(cardText);
        if (hasRetry && hasClose) {
          record(
            "DEF-02/AC-07 recovery card",
            "PASS",
            `retry+close visible; stage/code hints=${hasStage}/${hasCode}; sessionCreated=${sessionCreated}`,
          );
        } else {
          record("DEF-02/AC-07 recovery card", "FAIL", `card ok but actions retry=${hasRetry} close=${hasClose}`);
        }

        // CLOSED path: inline dock confirm, then a fresh generate attempt
        if (hasClose) {
          await page.locator('[data-action="generate-failure-end-session"]').click();
          await page.waitForTimeout(400);
          const confirm = page.locator('[data-action="confirm-end-generate"]').first();
          if (await confirm.isVisible().catch(() => false)) {
            await confirm.click().catch(() => {});
          }
          await page.waitForTimeout(1000);
          await shot(page, "r22-smart-dag-after-close");
          const retryAfter = await page.locator('[data-action="generate-failure-retry-rewrite"]').isVisible().catch(() => false);
          const cardAfter = await page.getByTestId("smart-dag-recovery-card").isVisible().catch(() => false);
          const dockStill = await page.locator(".workflow-generate-dock").isVisible().catch(() => false);
          const taDisabled = await intent.isDisabled().catch(() => false);
          if (dockStill && !retryAfter && (!cardAfter || !taDisabled)) {
            record(
              "DEF-02/AC-08 closed recovery",
              "PASS",
              `cardGone=${!cardAfter} retryGone=${!retryAfter} textareaDisabled=${taDisabled}`,
            );
          } else {
            const t = await bodyText(page);
            if (/结束这次生成|对话会清空|再开一轮|草稿还在/.test(t) || (dockStill && !cardAfter)) {
              record("DEF-02/AC-08 closed recovery", "PASS", "session ended / generate dock ready for a new attempt");
            } else {
              record("DEF-02/AC-08 closed recovery", "FAIL", `unexpected post-close UI`);
            }
          }
        }
      } else {
        // Fallback: maybe error path used different code without lastFailure
        const t = await bodyText(page);
        record(
          "DEF-02/AC-07 recovery card",
          "FAIL",
          `recovery card not visible; body has injected? ${/Sentinel retest|SMART_DAG|本轮生成未完成|这一版没通过/.test(t)}`,
        );
      }
    } else {
      record("DEF-02/AC-07 recovery card", "FAIL", "generate button not available");
    }
    await page.unroute("**/workflow-generate-sessions**").catch(() => {});

    // ========== UX-01 regression ==========
    await page.goto(`${BASE}/workflow`, { waitUntil: "networkidle" });
    await page.waitForTimeout(1000);
    const search = page.locator('input[placeholder*="搜索"], input[type="search"]').first();
    if (await search.isVisible().catch(() => false)) {
      await search.fill("PM E2E");
      await page.waitForTimeout(600);
    }
    const wfRow = page.locator("tbody tr").filter({ hasText: /PM E2E|最小编排/ }).first();
    if (await wfRow.isVisible({ timeout: 8000 }).catch(() => false)) {
      await wfRow.click();
      await page.waitForTimeout(800);
      const edit = page.getByRole("button", { name: /编辑流程图/ }).first();
      if (await edit.isVisible().catch(() => false)) {
        await edit.click();
        const editor = page.locator(".workflow-editor-shell, .vue-flow").first();
        const ok = await editor.isVisible({ timeout: 15000 }).catch(() => false);
        await shot(page, "r30-workflow-editor");
        record("UX-01 regression editor", ok ? "PASS" : "FAIL", ok ? "editor mounted" : "editor not visible");
      } else {
        record("UX-01 regression editor", "FAIL", "edit button missing");
      }
    } else {
      record("UX-01 regression editor", "FAIL", "workflow row missing");
    }

    // ========== UX-07 regression ==========
    await page.goto(`${BASE}/tools`, { waitUntil: "networkidle" });
    await page.waitForTimeout(1200);
    await pickSelectOption(page.locator("select").first(), "E2E费用").catch(() => false);
    await page.waitForTimeout(600);
    const toolsText = await bodyText(page);
    await shot(page, "r40-tools-list");
    if (/已发布\s*[·•]\s*连接需处理|已发布 · 连接需处理/.test(toolsText) || /连接需处理/.test(toolsText)) {
      record("UX-07 regression governance", "PASS", "连接需处理 visible");
    } else if (/连接缺失/.test(toolsText) && !/连接需处理/.test(toolsText)) {
      record("UX-07 regression governance", "FAIL", "only 连接缺失 for ERROR connection");
    } else {
      record("UX-07 regression governance", "FAIL", "expected governance label not found");
    }

    // ========== UX-03 regression (terminal on failure) ==========
    await page.goto(`${BASE}/chat`, { waitUntil: "networkidle" });
    await page.waitForTimeout(1200);
    const chatSelects = page.locator("select");
    const cn = await chatSelects.count();
    for (let i = 0; i < cn; i++) {
      const s = chatSelects.nth(i);
      const opts = await s.locator("option").allTextContents().catch(() => []);
      const joined = opts.join("|");
      if (/E2E|费用/.test(joined)) await pickSelectOption(s, "E2E费用");
      else if (/费用助手/.test(joined)) await pickSelectOption(s, "费用助手");
    }
    // New session to avoid old history noise if possible
    const newSessBtn = page.getByRole("button", { name: /新建会话/ }).first();
    if (await newSessBtn.isVisible().catch(() => false)) {
      await newSessBtn.click().catch(() => {});
      await page.waitForTimeout(500);
    }
    const composer = page.getByLabel(/输入业务指令|目标任务/).or(page.locator("textarea").first());
    if (await composer.isVisible().catch(() => false)) {
      await composer.fill('只回复“Sentinel retest ping”，不要调用任何工具。');
      await page.getByRole("button", { name: /发送/ }).first().click();
      const start = Date.now();
      let terminal = false;
      let stuck = false;
      while (Date.now() - start < 60000) {
        await page.waitForTimeout(2000);
        const badge = await page.locator("header b, b.failed, b.running, b.completed").first().innerText().catch(() => "");
        if (/失败|已完成|已取消/.test(badge)) {
          terminal = true;
          break;
        }
        if (/执行中/.test(badge) && Date.now() - start > 45000) stuck = true;
      }
      await shot(page, "r50-chat-terminal");
      const badge2 = await page.locator("header b, b.failed, b.running, b.completed").first().innerText().catch(() => "");
      const t = await bodyText(page);
      if (terminal && !/执行中/.test(badge2)) {
        record("UX-03 regression terminal", "PASS", `status=${badge2}`);
      } else if (stuck) {
        record("UX-03 regression terminal", "FAIL", "stuck 执行中 >45s");
      } else if (/失败|未完成/.test(t) && !/执行中/.test(badge2)) {
        record("UX-03 regression terminal", "PASS", `converged badge=${badge2}`);
      } else {
        record("UX-03 regression terminal", "FAIL", `badge=${badge2}`);
      }
    } else {
      record("UX-03 regression terminal", "FAIL", "composer missing");
    }
  } catch (err) {
    record("suite", "FAIL", String(err?.stack || err));
    await shot(page, "r99-crash").catch(() => {});
  } finally {
    const summary = {
      results,
      pass: results.filter((r) => r.status === "PASS").length,
      fail: results.filter((r) => r.status === "FAIL").length,
      skip: results.filter((r) => r.status === "SKIP").length,
      commit: "89d75ce",
      finishedAt: new Date().toISOString(),
    };
    writeFileSync(join(OUT, "retest-results.json"), JSON.stringify(summary, null, 2));
    console.log("\n=== RETEST SUMMARY ===");
    console.log(JSON.stringify({ pass: summary.pass, fail: summary.fail, skip: summary.skip }, null, 2));
    await browser.close();
    process.exit(summary.fail > 0 ? 1 : 0);
  }
}

main();
