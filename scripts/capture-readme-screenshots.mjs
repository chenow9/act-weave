/**
 * Capture key ActWeave console pages for README screenshots.
 * Prefer a seeded demo workspace (see seed-readme-demo-workspace.mjs).
 *
 * Usage:
 *   node scripts/seed-readme-demo-workspace.mjs
 *   node scripts/seed-readme-audit-trace.mjs          # full AAP→delegation Trace
 *   node scripts/capture-readme-screenshots.mjs
 *
 * Optional English capture (separate assets, excluding views with visible
 * untranslated strings):
 *   ACTWEAVE_SCREENSHOT_LOCALE=en \
 *   ACTWEAVE_SCREENSHOT_OUTPUT_DIR=docs/images/readme/en \
 *   node scripts/capture-readme-screenshots.mjs
 *
 * For both locales of the audit Trace screenshot after reseeding:
 *   node scripts/seed-readme-audit-trace.mjs --both
 *   ACTWEAVE_UI_URL=http://127.0.0.1:5174 node scripts/capture-readme-screenshots.mjs
 *   ACTWEAVE_SCREENSHOT_LOCALE=en ACTWEAVE_UI_URL=http://127.0.0.1:5174 \
 *     ACTWEAVE_SCREENSHOT_OUTPUT_DIR=docs/images/readme/en \
 *     node scripts/capture-readme-screenshots.mjs
 */
import { chromium } from "../frontend/node_modules/playwright/index.mjs";
import {
  mkdirSync,
  readFileSync,
  existsSync,
  readdirSync,
  unlinkSync,
} from "node:fs";
import { resolve, dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const ROOT = resolve(__dirname, "..");
const SCREENSHOT_LOCALE =
  process.env.ACTWEAVE_SCREENSHOT_LOCALE === "en" ? "en" : "zh-CN";
const OUT = resolve(
  ROOT,
  process.env.ACTWEAVE_SCREENSHOT_OUTPUT_DIR ||
    (SCREENSHOT_LOCALE === "en"
      ? "docs/images/readme/en"
      : "docs/images/readme"),
);
const BASE = process.env.ACTWEAVE_UI_URL || "http://127.0.0.1:5174";
const USER = process.env.ACTWEAVE_ADMIN_USER || "admin";
const PASS = process.env.ACTWEAVE_ADMIN_PASS || "actweave-admin-dev-change-me";
const ACTIVE_WS_KEY = "actweave:active-workspace-id";
const LOCALE_STORAGE_KEY = "actweave.locale";
const DEMO_WORKSPACE_FILTER =
  process.env.ACTWEAVE_DEMO_WORKSPACE_FILTER ||
  (SCREENSHOT_LOCALE === "en"
    ? "Acme Commerce Demo — English"
    : "Acme Commerce");

function loadDemoTraceId() {
  const p = resolve(OUT, "demo-audit-trace.json");
  if (existsSync(p)) {
    try {
      const meta = JSON.parse(readFileSync(p, "utf8"));
      if (meta?.traceId) return String(meta.traceId);
    } catch {
      // ignore
    }
  }
  return process.env.ACTWEAVE_DEMO_TRACE_ID || "";
}

mkdirSync(OUT, { recursive: true });

function loadWorkspaceId() {
  const p = resolve(OUT, ".workspace-id");
  if (existsSync(p)) return readFileSync(p, "utf8").trim();
  return process.env.ACTWEAVE_DEMO_WORKSPACE_ID || "";
}

/** Remove previous PNG captures (keep meta json / workspace id). */
function clearOldPngs() {
  for (const name of readdirSync(OUT)) {
    if (name.endsWith(".png")) unlinkSync(join(OUT, name));
  }
}

const pages = [
  ...(SCREENSHOT_LOCALE === "en"
    ? []
    : [{ name: "01-login", path: "/login", needAuth: false, wait: 700 }]),
  { name: "02-overview", path: "/overview", needAuth: true, wait: 1400 },
  { name: "03-workspaces", path: "/workspaces", needAuth: true, wait: 1200 },
  { name: "04-agents", path: "/agents", needAuth: true, wait: 1500 },
  { name: "05-tools", path: "/tools", needAuth: true, wait: 1500 },
  { name: "06-workflow", path: "/workflow", needAuth: true, wait: 1500 },
  ...(SCREENSHOT_LOCALE === "en"
    ? []
    : [
        {
          name: "07-smart-dag",
          path: "/smart-dag",
          needAuth: true,
          wait: 1400,
        },
      ]),
  { name: "08-providers", path: "/providers", needAuth: true, wait: 1200 },
  { name: "09-connections", path: "/connections", needAuth: true, wait: 1200 },
  { name: "10-model-apis", path: "/model-apis", needAuth: true, wait: 1200 },
  {
    name: "11-agent-access",
    path: "/agent-access",
    needAuth: true,
    wait: 1400,
  },
  { name: "12-chat", path: "/chat", needAuth: true, wait: 1500 },
  { name: "13-logs", path: "/logs", needAuth: true, wait: 1400 },
];

async function shot(page, name, { fullPage = false } = {}) {
  const file = resolve(OUT, `${name}.png`);
  await page.screenshot({ path: file, fullPage });
  console.log("wrote", file);
}

async function forceLocale(page) {
  const langBtn = page.locator(
    SCREENSHOT_LOCALE === "en"
      ? '[data-testid="login-lang-en"]'
      : '[data-testid="login-lang-zh-CN"]',
  );
  if (await langBtn.isVisible().catch(() => false)) await langBtn.click();
  await page.evaluate(
    ([key, loc]) => localStorage.setItem(key, loc),
    [LOCALE_STORAGE_KEY, SCREENSHOT_LOCALE],
  );
}

/** After login, server user.locale may override storage — switch via shell menu. */
async function forceLocaleAfterLogin(page) {
  await page.evaluate(
    ([key, loc]) => localStorage.setItem(key, loc),
    [LOCALE_STORAGE_KEY, SCREENSHOT_LOCALE],
  );
  const triggers = [
    '[data-testid="user-menu-trigger"]',
    '[data-testid="profile-menu-trigger"]',
    "button:has-text('AA')",
    ".topbar-avatar",
    ".profile-trigger",
  ];
  for (const sel of triggers) {
    const el = page.locator(sel).first();
    if (await el.isVisible().catch(() => false)) {
      await el.click().catch(() => null);
      await page.waitForTimeout(200);
      break;
    }
  }
  const lang = page.locator(
    SCREENSHOT_LOCALE === "en"
      ? '[data-testid="lang-en"]'
      : '[data-testid="lang-zh-CN"]',
  );
  if (await lang.isVisible().catch(() => false)) {
    await lang.click();
    await page.waitForTimeout(400);
    return;
  }
  await page.keyboard.press("Escape").catch(() => null);
}

async function login(page, workspaceId) {
  await page.goto(`${BASE}/login`, {
    waitUntil: "networkidle",
    timeout: 60_000,
  });
  await forceLocale(page);
  if (workspaceId) {
    await page.evaluate(
      ([key, id]) => localStorage.setItem(key, id),
      [ACTIVE_WS_KEY, workspaceId],
    );
  }
  await page.fill('input[autocomplete="username"]', USER);
  await page.fill(
    'input[type="password"], input[autocomplete="current-password"]',
    PASS,
  );
  await Promise.all([
    page
      .waitForURL((url) => !url.pathname.includes("/login"), {
        timeout: 30_000,
      })
      .catch(() => null),
    page.click('button[type="submit"]'),
  ]);
  await page.waitForTimeout(900);
  if (workspaceId) {
    await page.evaluate(
      ([key, id]) => localStorage.setItem(key, id),
      [ACTIVE_WS_KEY, workspaceId],
    );
  }
  await forceLocaleAfterLogin(page);
}

async function openNav(page) {
  const trigger = page.locator("button.fluid-trigger").first();
  await trigger.click();
  await page.waitForTimeout(500);
  await page
    .locator(".fluid-content")
    .waitFor({ state: "visible", timeout: 5000 });
}

async function closeNav(page) {
  const close = page.locator("button.island-close").first();
  if (await close.isVisible().catch(() => false)) {
    await close.click();
    await page.waitForTimeout(300);
  } else {
    await page.keyboard.press("Escape");
    await page.waitForTimeout(200);
  }
}

async function ensureWorkspaceSelected(page, workspaceId) {
  if (!workspaceId) return;
  // On non-overview pages, open switcher and pick demo workspace if visible
  const switcher = page.locator('[data-testid="workspace-switcher"]');
  if (!(await switcher.isVisible().catch(() => false))) return;
  await switcher.click();
  await page.waitForTimeout(400);
  const opt = page.locator(`[data-workspace-id="${workspaceId}"]`);
  if (await opt.isVisible().catch(() => false)) {
    await opt.click();
    await page.waitForTimeout(600);
  } else {
    await page.keyboard.press("Escape");
  }
}

clearOldPngs();
const workspaceId = loadWorkspaceId();
const demoTraceId = loadDemoTraceId();
console.log("demo workspaceId:", workspaceId || "(none — will use default)");
console.log("demo traceId:", demoTraceId || "(open first list row)");

const browser = await chromium.launch({ headless: true });
const context = await browser.newContext({
  // Taller viewport so expanded Trace detail fits README screenshots.
  viewport: { width: 1440, height: 1700 },
  deviceScaleFactor: 1,
  locale: SCREENSHOT_LOCALE === "en" ? "en-US" : "zh-CN",
});
const page = await context.newPage();

try {
  // 01 login: the English UI still shows a Chinese locale option, so do not
  // write it into the English documentation screenshot set.
  if (SCREENSHOT_LOCALE !== "en") {
    await page.goto(`${BASE}/login`, {
      waitUntil: "networkidle",
      timeout: 60_000,
    });
    await page.waitForTimeout(600);
    await shot(page, "01-login");
  }

  await login(page, workspaceId);

  // 00 navigation menu (open island with full module list)
  await page.goto(`${BASE}/agents`, {
    waitUntil: "networkidle",
    timeout: 60_000,
  });
  await page.waitForTimeout(800);
  await ensureWorkspaceSelected(page, workspaceId);
  await openNav(page);
  await page.waitForTimeout(400);
  await shot(page, "00-navigation-menu");
  await closeNav(page);

  // 00b workspace switcher — filter to demo name so README does not show other tenants
  await page.goto(`${BASE}/tools`, {
    waitUntil: "networkidle",
    timeout: 60_000,
  });
  await page.waitForTimeout(700);
  await ensureWorkspaceSelected(page, workspaceId);
  const sw = page.locator('[data-testid="workspace-switcher"]');
  if (await sw.isVisible().catch(() => false)) {
    await sw.click();
    await page.waitForTimeout(400);
    const search = page.locator(
      '.workspace-switcher-menu input[type="search"]',
    );
    if (await search.isVisible().catch(() => false)) {
      await search.fill(DEMO_WORKSPACE_FILTER);
      await page.waitForTimeout(400);
    }
    await shot(page, "00-workspace-switcher");
    await page.keyboard.press("Escape");
    await page.waitForTimeout(200);
  }

  for (const item of pages) {
    if (!item.needAuth) continue;
    await page.goto(`${BASE}${item.path}`, {
      waitUntil: "networkidle",
      timeout: 60_000,
    });
    await page.waitForTimeout(item.wait);
    if (
      item.path !== "/overview" &&
      item.path !== "/workspaces" &&
      item.path !== "/logs"
    ) {
      await ensureWorkspaceSelected(page, workspaceId);
      // re-goto so page reloads under selected workspace
      await page.goto(`${BASE}${item.path}`, {
        waitUntil: "networkidle",
        timeout: 60_000,
      });
      await page.waitForTimeout(item.wait);
    }
    if (item.path === "/workspaces" && SCREENSHOT_LOCALE === "en") {
      const search = page.locator('input[type="search"]');
      if (await search.isVisible().catch(() => false)) {
        await search.fill(DEMO_WORKSPACE_FILTER);
        await page.waitForTimeout(400);
      }
    }

    // Agent audit: capture expanded Trace detail (not the empty list state).
    if (item.path === "/logs") {
      await ensureWorkspaceSelected(page, workspaceId);
      await page.goto(`${BASE}/logs`, {
        waitUntil: "networkidle",
        timeout: 60_000,
      });
      await page.waitForTimeout(1200);
      await openAuditTraceDetail(page, demoTraceId);
      await page.waitForTimeout(900);
    }

    // dismiss dialogs
    const close = page
      .locator('.el-dialog__headerbtn, button:has-text("取消")')
      .first();
    if (await close.isVisible().catch(() => false)) {
      await close.click().catch(() => null);
      await page.waitForTimeout(200);
    }
    // Trace detail benefits from fullPage so nested Agent/Tool steps are visible.
    await shot(page, item.name, { fullPage: item.path === "/logs" });
  }
} finally {
  await browser.close();
}

/**
 * Open the seeded (or first) audit Trace detail so README shows the
 * Client → Run → Model → Agent delegation → Tool timeline.
 */
async function openAuditTraceDetail(page, preferredTraceId) {
  // Prefer clicking the seeded trace row when present.
  if (preferredTraceId) {
    const row = page
      .locator("code.aw-table-mono, .aw-table-mono")
      .filter({ hasText: preferredTraceId })
      .first();
    if (await row.isVisible().catch(() => false)) {
      await row.click();
      await page
        .locator(".timeline, .timeline-item, .detail-header")
        .first()
        .waitFor({ state: "visible", timeout: 15_000 })
        .catch(() => null);
      await expandDelegations(page);
      return;
    }
  }

  // Fallback: first data row / detail action in the audit list.
  const detailLink = page.locator(".audit-detail-link, tr.aw-table-row, tbody tr").first();
  if (await detailLink.isVisible().catch(() => false)) {
    await detailLink.click();
    await page
      .locator(".timeline, .timeline-item, .detail-header")
      .first()
      .waitFor({ state: "visible", timeout: 15_000 })
      .catch(() => null);
    await expandDelegations(page);
  }
}

async function expandDelegations(page) {
  const toggles = page.locator(
    'button.delegation-toggle[aria-expanded="false"]',
  );
  const count = await toggles.count().catch(() => 0);
  for (let i = 0; i < count; i++) {
    const btn = toggles.nth(i);
    if (await btn.isVisible().catch(() => false)) {
      await btn.click().catch(() => null);
      await page.waitForTimeout(150);
    }
  }
  // Ensure nested tool/model cards are in view for the screenshot.
  const nested = page.locator(".timeline-item.nested, .timeline-card.nested").first();
  if (await nested.isVisible().catch(() => false)) {
    await nested.scrollIntoViewIfNeeded().catch(() => null);
  }
  // Scroll timeline header into view so Client/status meta is visible.
  const header = page.locator(".detail-header").first();
  if (await header.isVisible().catch(() => false)) {
    await header.scrollIntoViewIfNeeded().catch(() => null);
  }
}

console.log("done ->", OUT);
