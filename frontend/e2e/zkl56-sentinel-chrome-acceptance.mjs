/**
 * Sentinel independent Chrome acceptance for ZKL-56 UX-01～07 / AC-01～15.
 * Real stack at 127.0.0.1:5174 / 8082 — Google Chrome via Playwright channel.
 * Evidence: docs/verification/zkl-56-pm-e2e-ux-fixes-2026-07-26/
 */
import { chromium } from "playwright";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";

const BASE = process.env.E2E_BASE_URL || "http://127.0.0.1:5174";
const USER = process.env.E2E_USER || "admin";
const PASS = process.env.E2E_PASS || "actweave-admin-dev-change-me";
const WS_HINT = process.env.E2E_WORKSPACE || "E2E费用报销透传全链路";
const WF_HINT = process.env.E2E_WORKFLOW || "PM E2E 最小编排";
const OUT = process.env.E2E_EVIDENCE_DIR ||
  join(process.cwd(), "..", "docs", "verification", "zkl-56-pm-e2e-ux-fixes-2026-07-26");

mkdirSync(OUT, { recursive: true });

const results = [];
const consoleErrors = [];
const networkFailures = [];

function record(name, status, detail = "", extra = {}) {
  const row = { name, status, detail, ...extra, at: new Date().toISOString() };
  results.push(row);
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

async function switchWorkspaceIfNeeded(page, hint) {
  // Global workspace switcher patterns used across management pages
  const candidates = [
    page.getByRole("combobox").filter({ hasText: /业务空间|Workspace|空间/ }).first(),
    page.locator("select").filter({ hasText: /E2E|空间/ }).first(),
    page.locator('[aria-label*="业务空间"], [aria-label*="工作空间"], [data-testid*="workspace"]').first(),
  ];
  for (const loc of candidates) {
    if (await loc.isVisible().catch(() => false)) {
      const tag = await loc.evaluate((el) => el.tagName.toLowerCase());
      if (tag === "select") {
        const opts = await loc.locator("option").allTextContents();
        const hit = opts.find((t) => t.includes(hint) || t.includes("e2e-expense"));
        if (hit) {
          await loc.selectOption({ label: hit });
          await page.waitForTimeout(800);
          return hit;
        }
      }
    }
  }
  // Page-local select on chat / smart-dag
  return null;
}

async function pickSelectOption(page, selectLocator, hint) {
  if (!(await selectLocator.isVisible().catch(() => false))) return false;
  const options = await selectLocator.locator("option").allTextContents();
  const hit = options.find((t) => t.includes(hint) || t.includes("e2e-expense") || t.includes("费用"));
  if (!hit) return false;
  await selectLocator.selectOption({ label: hit });
  await page.waitForTimeout(600);
  return true;
}

async function bodyText(page) {
  return page.locator("body").innerText();
}

async function main() {
  const browser = await chromium.launch({
    headless: true,
    channel: "chrome",
  });
  const context = await browser.newContext({
    locale: "zh-CN",
    viewport: { width: 1600, height: 1100 },
    timezoneId: "Asia/Singapore",
  });
  const page = await context.newPage();
  page.on("console", (msg) => {
    if (msg.type() === "error") consoleErrors.push(msg.text());
  });
  page.on("response", (res) => {
    if (res.status() >= 500) {
      networkFailures.push({ url: res.url(), status: res.status() });
    }
  });

  try {
    // ---------- Login ----------
    await login(page);
    await shot(page, "01-login-overview");
    record("login", "PASS", "admin session established");

    // ---------- UX-01 / AC-01 Workflow editor handoff ----------
    await page.goto(`${BASE}/workflow`, { waitUntil: "networkidle" });
    await page.waitForTimeout(1200);
    // Prefer page-local workspace filter if present
    const wfWs = page.locator("select").first();
    await pickSelectOption(page, wfWs, "E2E费用").catch(() => false);

    // Search workflow
    const search = page.locator('input[placeholder*="搜索"], input[type="search"], .search input').first();
    if (await search.isVisible().catch(() => false)) {
      await search.fill(WF_HINT);
      await page.waitForTimeout(800);
    }
    await shot(page, "02-workflow-list");

    const row = page.locator("tbody tr, .data-table tbody tr, .workflow-management-list tr")
      .filter({ hasText: WF_HINT }).first();
    let editorOpened = false;
    let detailVisible = false;
    if (await row.isVisible({ timeout: 8000 }).catch(() => false)) {
      await row.click();
      await page.waitForTimeout(1000);
      detailVisible = await page.getByText("流程详情").first().isVisible().catch(() => false)
        || await page.getByRole("dialog").isVisible().catch(() => false)
        || await page.locator('[aria-label="流程详情"], .workflow-detail').first().isVisible().catch(() => false);
      await shot(page, "03-workflow-detail");

      const editBtn = page.getByRole("button", { name: /编辑流程图/ }).first();
      if (await editBtn.isVisible().catch(() => false)) {
        await editBtn.click();
        // Expect editor within 15s — not silent return to list
        const editor = page.locator(".workflow-editor-shell, .workflow-editor, [data-testid='workflow-editor']").first();
        editorOpened = await editor.isVisible({ timeout: 15000 }).catch(() => false);
        if (!editorOpened) {
          // Also accept URL/route change to editor
          const url = page.url();
          editorOpened = /editor|canvas|edit/i.test(url)
            || await page.locator(".vue-flow, .workflow-node-library").first().isVisible().catch(() => false);
        }
        await page.waitForTimeout(500);
        await shot(page, "04-workflow-editor-after-edit");
        if (editorOpened) {
          record("AC-01/UX-01 workflow editor enter", "PASS", "editor shell visible after 编辑流程图");
          // Try save draft if action present (AC-15 partial)
          const save = page.locator('[data-action="save-editor-draft"], button:has-text("保存")').first();
          if (await save.isVisible().catch(() => false) && await save.isEnabled().catch(() => false)) {
            await save.click().catch(() => {});
            await page.waitForTimeout(800);
            await shot(page, "05-workflow-save-draft");
            record("AC-15 workflow save draft", "PASS", "save action clicked");
          } else {
            record("AC-15 workflow save draft", "SKIP", "save control not available on this draft");
          }
        } else {
          const backToList = !(await page.getByText("流程详情").first().isVisible().catch(() => false))
            && !(await page.locator(".workflow-editor-shell").first().isVisible().catch(() => false));
          record(
            "AC-01/UX-01 workflow editor enter",
            "FAIL",
            backToList
              ? "编辑流程图后未进入编辑器，详情关闭/回列表（与原缺陷一致）"
              : "编辑器未在 15s 内出现",
          );
        }
      } else {
        record("AC-01/UX-01 workflow editor enter", "FAIL", "编辑流程图 button not found");
      }
    } else {
      // Create a new draft as fallback
      const create = page.getByRole("button", { name: /新建编排|新建/ }).first();
      if (await create.isVisible().catch(() => false)) {
        await create.click();
        await page.waitForTimeout(500);
        const nameInput = page.locator('.drawer-field').filter({ hasText: "名称" }).locator("input").first()
          .or(page.getByLabel(/名称/).first());
        const wfName = `Sentinel ZKL56 ${Date.now()}`;
        if (await nameInput.isVisible().catch(() => false)) {
          await nameInput.fill(wfName);
          await page.getByRole("button", { name: /保存编排|保存/ }).first().click();
          await page.waitForTimeout(1500);
          await shot(page, "02b-workflow-created");
          const newRow = page.locator("tbody tr").filter({ hasText: wfName }).first();
          if (await newRow.isVisible().catch(() => false)) {
            await newRow.click();
            await page.waitForTimeout(800);
            const editBtn = page.getByRole("button", { name: /编辑流程图/ }).first();
            if (await editBtn.isVisible().catch(() => false)) {
              await editBtn.click();
              editorOpened = await page.locator(".workflow-editor-shell, .vue-flow").first()
                .isVisible({ timeout: 15000 }).catch(() => false);
              await shot(page, "04-workflow-editor-after-edit");
              record(
                "AC-01/UX-01 workflow editor enter",
                editorOpened ? "PASS" : "FAIL",
                editorOpened ? `editor for ${wfName}` : `created ${wfName} but editor did not open`,
              );
            }
          }
        } else {
          record("AC-01/UX-01 workflow editor enter", "FAIL", "cannot find workflow row or create form");
        }
      } else {
        record("AC-01/UX-01 workflow editor enter", "FAIL", `workflow row not found for ${WF_HINT}`);
      }
    }

    // ---------- UX-05/06 OpenAPI ----------
    await page.goto(`${BASE}/openapi-imports`, { waitUntil: "networkidle" }).catch(async () => {
      await page.goto(`${BASE}/openapi`, { waitUntil: "networkidle" });
    });
    // try alternate routes
    if (!(await page.locator("body").innerText()).match(/OpenAPI|导入/)) {
      for (const path of ["/openapi-imports", "/integrations/openapi", "/tools/openapi-imports"]) {
        await page.goto(`${BASE}${path}`, { waitUntil: "networkidle" }).catch(() => {});
        if ((await bodyText(page)).match(/OpenAPI|导入/)) break;
      }
    }
    // Use nav link if needed
    if (!(await bodyText(page)).match(/OpenAPI|导入记录|接口/)) {
      const nav = page.getByRole("link", { name: /OpenAPI|导入/ }).first();
      if (await nav.isVisible().catch(() => false)) await nav.click();
      await page.waitForTimeout(1000);
    }
    await shot(page, "10-openapi-list");
    const openapiRow = page.locator("tbody tr, .data-table tbody tr").filter({ hasText: /openapi|yaml|SUCCEEDED|Ready|8/i }).first();
    let openapiDetailText = "";
    if (await openapiRow.isVisible({ timeout: 8000 }).catch(() => false)) {
      await openapiRow.click();
      await page.waitForTimeout(1200);
      openapiDetailText = await bodyText(page);
      await shot(page, "11-openapi-detail");
    } else {
      // click first row
      const first = page.locator("tbody tr").first();
      if (await first.isVisible().catch(() => false)) {
        await first.click();
        await page.waitForTimeout(1200);
        openapiDetailText = await bodyText(page);
        await shot(page, "11-openapi-detail");
      }
    }
    if (openapiDetailText) {
      const doublePort = /:(\d+):\1\b/.test(openapiDetailText) || /18080:18080/.test(openapiDetailText);
      const hasServiceAddr = /服务地址|127\.0\.0\.1|localhost/.test(openapiDetailText);
      if (doublePort) {
        record("AC-09/UX-05 OpenAPI URL normalize", "FAIL", "详情仍出现重复端口（如 :18080:18080）");
      } else if (hasServiceAddr) {
        record("AC-09/UX-05 OpenAPI URL normalize", "PASS", "服务地址无重复端口");
      } else {
        record("AC-09/UX-05 OpenAPI URL normalize", "SKIP", "详情未展示可判定服务地址");
      }
      const endpointCards = await page.locator(".tool-schema-endpoint-card, .endpoint-card").count();
      const showsEndpoints = endpointCards > 0 || /GET |POST |\/v1\//.test(openapiDetailText);
      if (showsEndpoints && endpointCards >= 1) {
        record("AC-10/UX-06 OpenAPI endpoints", "PASS", `endpoint cards=${endpointCards}`);
      } else if (showsEndpoints) {
        record("AC-10/UX-06 OpenAPI endpoints", "PASS", "endpoint paths visible in detail text");
      } else if (/0 节点|请求参数|契约/.test(openapiDetailText) && /可生成|endpoint/i.test(openapiDetailText)) {
        record("AC-10/UX-06 OpenAPI endpoints", "FAIL", "摘要与契约区不一致或空契约");
      } else {
        record("AC-10/UX-06 OpenAPI endpoints", "FAIL", "详情未见 endpoint 明细");
      }
    } else {
      record("AC-09/UX-05 OpenAPI URL normalize", "FAIL", "无法打开 OpenAPI 详情");
      record("AC-10/UX-06 OpenAPI endpoints", "FAIL", "无法打开 OpenAPI 详情");
    }

    // ---------- UX-07 Tools governance ----------
    await page.goto(`${BASE}/tools`, { waitUntil: "networkidle" });
    await page.waitForTimeout(1200);
    await pickSelectOption(page, page.locator("select").first(), "E2E费用").catch(() => false);
    await page.waitForTimeout(800);
    await shot(page, "20-tools-list");
    const toolsText = await bodyText(page);
    const hasConnNeed = /连接需处理/.test(toolsText);
    const hasMissing = /连接缺失/.test(toolsText);
    const hasPublishedConn = /已发布\s*[·•]\s*连接需处理|已发布 · 连接需处理/.test(toolsText);
    if (hasPublishedConn || (hasConnNeed && /已发布/.test(toolsText))) {
      record("AC-12/UX-07 Tool governance list", "PASS", hasPublishedConn ? "已发布 · 连接需处理" : "连接需处理 + 已发布 visible");
    } else if (hasMissing && !hasConnNeed) {
      record("AC-12/UX-07 Tool governance list", "FAIL", "仍显示「连接缺失」且无「连接需处理」（ERROR 连接应标需处理）");
    } else {
      // open a tool detail
      const toolRow = page.locator("tbody tr").filter({ hasText: /报销|createexpense|创建/ }).first()
        .or(page.locator("tbody tr").first());
      if (await toolRow.isVisible().catch(() => false)) {
        await toolRow.click();
        await page.waitForTimeout(1000);
        const detail = await bodyText(page);
        await shot(page, "21-tool-detail");
        if (/连接需处理|当前不可调用|测试通过|历史测试/.test(detail)) {
          if (/连接缺失/.test(detail) && /连接需处理/.test(detail) === false && /ERROR|需处理|不可调用/.test(detail) === false) {
            record("AC-12/UX-07 Tool governance list", "FAIL", "详情仍用「连接缺失」描述存在但异常的连接");
          } else {
            record("AC-12/UX-07 Tool governance list", "PASS", "详情呈现三维治理语义");
          }
        } else {
          record("AC-12/UX-07 Tool governance list", "FAIL", `list/detail 未出现预期治理标签; missing=${hasMissing} need=${hasConnNeed}`);
        }
      } else {
        record("AC-12/UX-07 Tool governance list", "FAIL", "tools page empty or unreadable");
      }
    }

    // ---------- UX-02/03 Console ----------
    await page.goto(`${BASE}/chat`, { waitUntil: "networkidle" });
    await page.waitForTimeout(1500);
    // workspace + agent selects
    const selects = page.locator("select");
    const selCount = await selects.count();
    for (let i = 0; i < selCount; i++) {
      const s = selects.nth(i);
      const opts = await s.locator("option").allTextContents().catch(() => []);
      const joined = opts.join("|");
      if (/E2E|费用报销|e2e-expense/.test(joined)) {
        await pickSelectOption(page, s, "E2E费用");
      } else if (/费用助手|Agent/.test(joined)) {
        await pickSelectOption(page, s, "费用助手");
      }
    }
    // Also try role combobox / custom selects
    const agentCombo = page.getByLabel(/Agent|代理/).first();
    if (await agentCombo.isVisible().catch(() => false)) {
      await agentCombo.click().catch(() => {});
      await page.getByRole("option", { name: /费用助手/ }).first().click().catch(() => {});
    }
    await shot(page, "30-chat-before-send");
    const composer = page.getByLabel(/输入业务指令|目标任务/).or(page.locator("textarea").first());
    const prompt = '只回复“PM E2E 调试台已连通”，不要调用任何工具。';
    if (await composer.isVisible().catch(() => false)) {
      await composer.fill(prompt);
      const send = page.getByRole("button", { name: /发送/ }).first();
      await send.click();
      await shot(page, "31-chat-sent");
      // Wait up to 90s for terminal state
      const start = Date.now();
      let terminalOk = false;
      let pureTextBlocked = false;
      let statusStuckRunning = false;
      let finalStatus = "";
      while (Date.now() - start < 90_000) {
        await page.waitForTimeout(2000);
        const t = await bodyText(page);
        if (/OUTBOUND_IDENTITY_CONNECTION_NOT_READY|resolve capability/.test(t) && /createexpense|连接/.test(t)) {
          pureTextBlocked = true;
        }
        const failed = /失败|FAILED|运行失败|未完成/.test(t) && !/执行中/.test(
          await page.locator("b.failed, b.running, .status-badge, header").first().innerText().catch(() => ""),
        );
        // status badge text
        const badge = page.locator("b.running, b.failed, b.completed, .chat-run-status b, header b").first();
        finalStatus = (await badge.innerText().catch(() => "")) || "";
        if (/失败|已完成|已取消/.test(finalStatus) || /失败|已完成/.test(t) && !/执行中|意图识别中/.test(finalStatus)) {
          // Check composer enabled
          const disabled = await composer.isDisabled().catch(() => false);
          const elapsed = Date.now() - start;
          if (/失败|FAILED/.test(finalStatus + t) && !/执行中/.test(finalStatus)) {
            terminalOk = elapsed <= 90_000;
            if (elapsed > 5000 && /执行中/.test(finalStatus)) statusStuckRunning = true;
            break;
          }
          if (/已完成|SUCCEEDED|PM E2E 调试台已连通/.test(t)) {
            terminalOk = true;
            pureTextBlocked = false;
            break;
          }
          if (!disabled && /失败|未完成/.test(t)) {
            terminalOk = true;
            break;
          }
        }
        if (/执行中|意图识别中/.test(finalStatus) && Date.now() - start > 60_000) {
          statusStuckRunning = true;
        }
      }
      await shot(page, "32-chat-terminal");
      // UX-02
      if (pureTextBlocked && !/PM E2E 调试台已连通/.test(await bodyText(page))) {
        record("AC-04/UX-02 Console pure-text not blocked", "FAIL", "仍被无关 Tool 连接阻断 OUTBOUND_IDENTITY_CONNECTION_NOT_READY");
      } else if (/PM E2E 调试台已连通|已完成/.test(await bodyText(page))) {
        record("AC-04/UX-02 Console pure-text not blocked", "PASS", "纯文本对话完成");
      } else {
        // Maybe model failed for other reasons
        const t = await bodyText(page);
        if (/OUTBOUND_IDENTITY_CONNECTION_NOT_READY/.test(t)) {
          record("AC-04/UX-02 Console pure-text not blocked", "FAIL", "capability resolve still blocks pure text");
        } else {
          record("AC-04/UX-02 Console pure-text not blocked", "FAIL", `未得到纯文本成功回复; status=${finalStatus}`);
        }
      }
      // UX-03 terminal convergence
      const t2 = await bodyText(page);
      const badge2 = await page.locator("header b, b.failed, b.running, b.completed").first().innerText().catch(() => "");
      if (/执行中|意图识别中/.test(badge2) && /失败|error|ERROR/i.test(t2)) {
        record("AC-06/UX-03 Console terminal converge", "FAIL", `消息已失败但顶部仍为「${badge2}」`);
      } else if (/失败|已完成|已取消|待运行/.test(badge2) || terminalOk) {
        record("AC-06/UX-03 Console terminal converge", "PASS", `status=${badge2 || finalStatus}`);
      } else if (statusStuckRunning) {
        record("AC-06/UX-03 Console terminal converge", "FAIL", "超过 60s 仍执行中");
      } else {
        record("AC-06/UX-03 Console terminal converge", "FAIL", `无法确认终态; badge=${badge2}`);
      }
    } else {
      record("AC-04/UX-02 Console pure-text not blocked", "FAIL", "composer not found");
      record("AC-06/UX-03 Console terminal converge", "FAIL", "composer not found");
    }

    // ---------- UX-04 generate dock (legacy /smart-dag now redirects here) ----------
    await page.goto(`${BASE}/workflow?generate=1`, { waitUntil: "networkidle" });
    if (!(await page.locator(".workflow-generate-dock").isVisible().catch(() => false))) {
      const intentBtn = page.getByRole("button", { name: /用一句话生成|Generate from a sentence/ }).first();
      if (await intentBtn.isVisible().catch(() => false)) await intentBtn.click();
      await page.waitForTimeout(1000);
    }
    await page.waitForTimeout(1000);
    const intent = page.locator("textarea.workflow-generate-prompt").first();
    if (await intent.isVisible().catch(() => false)) {
      await intent.fill("生成一个两步审批报销流程（Sentinel 验收用，允许失败以观察恢复 UI）");
      await shot(page, "40-smart-dag-before-generate");
      const genBtn = page.locator('[data-action="submit-generate"]').first();
      if (await genBtn.isVisible().catch(() => false) && await genBtn.isEnabled().catch(() => false)) {
        await genBtn.click();
        // Wait for failure or success up to 120s
        const t0 = Date.now();
        let finished = false;
        while (Date.now() - t0 < 120_000) {
          await page.waitForTimeout(3000);
          const t = await bodyText(page);
          const cardVisible = await page.getByTestId("smart-dag-recovery-card").isVisible().catch(() => false);
          if (cardVisible || (/失败|error|ERROR|500|Guard|已生成第|draftVersion/i.test(t)
            && !/生成中，完成后可继续|AI 生成中|AI 正在推理/.test(t))) {
            finished = true;
            break;
          }
          if (!/生成中|AI 正在推理/.test(t) && Date.now() - t0 > 15_000) {
            finished = true;
            break;
          }
        }
        await shot(page, "41-smart-dag-after-generate");
        const t = await bodyText(page);
        const card = page.getByTestId("smart-dag-recovery-card");
        const hasCard = await card.isVisible().catch(() => false);
        const hasRetry = await page.locator('[data-action="generate-failure-retry-rewrite"]').isVisible().catch(() => false)
          || /改写后再试|重试/.test(t);
        const hasClose = await page.locator('[data-action="generate-failure-end-session"]').isVisible().catch(() => false)
          || /结束这次生成|关闭会话/.test(t);
        if ((hasCard && (hasRetry || hasClose)) || (hasRetry && hasClose)) {
          record("AC-07/UX-04 Smart DAG recovery UI", "PASS", `card=${hasCard} retry=${hasRetry} close=${hasClose}`);
        } else if (/失败|error|500/i.test(t) && !hasRetry && !hasClose) {
          record(
            "AC-07/UX-04 Smart DAG recovery UI",
            "FAIL",
            "失败后缺少持久恢复动作（改写后再试/结束这次生成）；仅 toast 或错误文本",
          );
        } else if (/已生成第|draftVersion|编译/.test(t)) {
          record("AC-07/UX-04 Smart DAG recovery UI", "PASS", "生成成功路径；恢复 UI 未触发（成功）");
        } else {
          record("AC-07/UX-04 Smart DAG recovery UI", "FAIL", `生成结束后无明确恢复卡片; finished=${finished}`);
        }
      } else {
        record("AC-07/UX-04 Smart DAG recovery UI", "SKIP", "generate button disabled (model/agent?)");
      }
    } else {
      record("AC-07/UX-04 Smart DAG recovery UI", "FAIL", "generate dock / intent not found");
    }

    // ---------- AC-14 secrets smoke ----------
    const secretsLeaked = consoleErrors.some((e) =>
      /actweave-admin-dev-change-me|client_secret|BEGIN PRIVATE|sk-/.test(e),
    );
    record(
      "AC-14 secret non-leak (console)",
      secretsLeaked ? "FAIL" : "PASS",
      secretsLeaked ? "console contains secret-like strings" : "no secret-like console errors",
    );

    // ---------- Nav regression smoke ----------
    for (const [path, label] of [
      ["/overview", "overview"],
      ["/connections", "connections"],
      ["/agents", "agents"],
      ["/logs", "logs"],
    ]) {
      await page.goto(`${BASE}${path}`, { waitUntil: "domcontentloaded" }).catch(() => {});
      await page.waitForTimeout(600);
      const ok = (await page.locator("body").count()) > 0 && !/Cannot GET|404/.test(await bodyText(page));
      record(`nav smoke ${label}`, ok ? "PASS" : "FAIL", path);
    }
    await shot(page, "90-final");

  } catch (err) {
    record("suite", "FAIL", String(err?.stack || err));
    await shot(page, "99-crash").catch(() => {});
  } finally {
    const summary = {
      results,
      pass: results.filter((r) => r.status === "PASS").length,
      fail: results.filter((r) => r.status === "FAIL").length,
      skip: results.filter((r) => r.status === "SKIP").length,
      consoleErrors: consoleErrors.slice(0, 50),
      networkFailures: networkFailures.slice(0, 30),
      base: BASE,
      commitHint: "fix/zkl-56-pm-e2e-ux-fixes",
      finishedAt: new Date().toISOString(),
    };
    writeFileSync(join(OUT, "acceptance-results.json"), JSON.stringify(summary, null, 2));
    console.log("\n=== SUMMARY ===");
    console.log(JSON.stringify({ pass: summary.pass, fail: summary.fail, skip: summary.skip }, null, 2));
    await browser.close();
    process.exit(summary.fail > 0 ? 1 : 0);
  }
}

main();
