/**
 * Capture key ActWeave console pages for README screenshots.
 * Prefer a seeded demo workspace (see seed-readme-demo-workspace.mjs).
 *
 * Usage:
 *   node scripts/seed-readme-demo-workspace.mjs
 *   node scripts/capture-readme-screenshots.mjs
 */
import { chromium } from "../frontend/node_modules/playwright/index.mjs";
import { mkdirSync, readFileSync, existsSync, readdirSync, unlinkSync } from "node:fs";
import { resolve, dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const ROOT = resolve(__dirname, "..");
const OUT = resolve(ROOT, "docs/images/readme");
const BASE = process.env.ACTWEAVE_UI_URL || "http://127.0.0.1:5173";
const USER = process.env.ACTWEAVE_ADMIN_USER || "admin";
const PASS = process.env.ACTWEAVE_ADMIN_PASS || "actweave-admin-dev-change-me";
const ACTIVE_WS_KEY = "actweave:active-workspace-id";

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
  { name: "01-login", path: "/login", needAuth: false, wait: 700 },
  { name: "02-overview", path: "/overview", needAuth: true, wait: 1400 },
  { name: "03-workspaces", path: "/workspaces", needAuth: true, wait: 1200 },
  { name: "04-agents", path: "/agents", needAuth: true, wait: 1500 },
  { name: "05-tools", path: "/tools", needAuth: true, wait: 1500 },
  { name: "06-workflow", path: "/workflow", needAuth: true, wait: 1500 },
  { name: "07-smart-dag", path: "/smart-dag", needAuth: true, wait: 1400 },
  { name: "08-providers", path: "/providers", needAuth: true, wait: 1200 },
  { name: "09-connections", path: "/connections", needAuth: true, wait: 1200 },
  { name: "10-model-apis", path: "/model-apis", needAuth: true, wait: 1200 },
  { name: "11-agent-access", path: "/agent-access", needAuth: true, wait: 1400 },
  { name: "12-chat", path: "/chat", needAuth: true, wait: 1500 },
  { name: "13-logs", path: "/logs", needAuth: true, wait: 1400 },
];

async function shot(page, name) {
  const file = resolve(OUT, `${name}.png`);
  await page.screenshot({ path: file, fullPage: false });
  console.log("wrote", file);
}

async function login(page, workspaceId) {
  await page.goto(`${BASE}/login`, { waitUntil: "networkidle", timeout: 60_000 });
  if (workspaceId) {
    await page.evaluate(
      ([key, id]) => localStorage.setItem(key, id),
      [ACTIVE_WS_KEY, workspaceId],
    );
  }
  await page.fill('input[autocomplete="username"]', USER);
  await page.fill('input[type="password"], input[autocomplete="current-password"]', PASS);
  await Promise.all([
    page.waitForURL((url) => !url.pathname.includes("/login"), { timeout: 30_000 }).catch(() => null),
    page.click('button[type="submit"]'),
  ]);
  await page.waitForTimeout(900);
  if (workspaceId) {
    await page.evaluate(
      ([key, id]) => localStorage.setItem(key, id),
      [ACTIVE_WS_KEY, workspaceId],
    );
  }
}

async function openNav(page) {
  const trigger = page.locator("button.fluid-trigger").first();
  await trigger.click();
  await page.waitForTimeout(500);
  await page.locator(".fluid-content").waitFor({ state: "visible", timeout: 5000 });
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
console.log("demo workspaceId:", workspaceId || "(none — will use default)");

const browser = await chromium.launch({ headless: true });
const context = await browser.newContext({
  viewport: { width: 1440, height: 900 },
  deviceScaleFactor: 1,
});
const page = await context.newPage();

try {
  // 01 login
  await page.goto(`${BASE}/login`, { waitUntil: "networkidle", timeout: 60_000 });
  await page.waitForTimeout(600);
  await shot(page, "01-login");

  await login(page, workspaceId);

  // 00 navigation menu (open island with full module list)
  await page.goto(`${BASE}/agents`, { waitUntil: "networkidle", timeout: 60_000 });
  await page.waitForTimeout(800);
  await ensureWorkspaceSelected(page, workspaceId);
  await openNav(page);
  await page.waitForTimeout(400);
  await shot(page, "00-navigation-menu");
  await closeNav(page);

  // 00b workspace switcher — filter to demo name so README does not show other tenants
  await page.goto(`${BASE}/tools`, { waitUntil: "networkidle", timeout: 60_000 });
  await page.waitForTimeout(700);
  await ensureWorkspaceSelected(page, workspaceId);
  const sw = page.locator('[data-testid="workspace-switcher"]');
  if (await sw.isVisible().catch(() => false)) {
    await sw.click();
    await page.waitForTimeout(400);
    const search = page.locator('.workspace-switcher-menu input[type="search"]');
    if (await search.isVisible().catch(() => false)) {
      await search.fill("Acme Commerce");
      await page.waitForTimeout(400);
    }
    await shot(page, "00-workspace-switcher");
    await page.keyboard.press("Escape");
    await page.waitForTimeout(200);
  }

  for (const item of pages) {
    if (!item.needAuth) continue;
    await page.goto(`${BASE}${item.path}`, { waitUntil: "networkidle", timeout: 60_000 });
    await page.waitForTimeout(item.wait);
    if (item.path !== "/overview" && item.path !== "/workspaces" && item.path !== "/logs") {
      await ensureWorkspaceSelected(page, workspaceId);
      // re-goto so page reloads under selected workspace
      await page.goto(`${BASE}${item.path}`, { waitUntil: "networkidle", timeout: 60_000 });
      await page.waitForTimeout(item.wait);
    }
    // dismiss dialogs
    const close = page.locator('.el-dialog__headerbtn, button:has-text("取消")').first();
    if (await close.isVisible().catch(() => false)) {
      await close.click().catch(() => null);
      await page.waitForTimeout(200);
    }
    await shot(page, item.name);
  }
} finally {
  await browser.close();
}

console.log("done ->", OUT);

