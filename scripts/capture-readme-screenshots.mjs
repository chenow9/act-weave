/**
 * Capture key ActWeave console pages for README screenshots.
 * Usage: node scripts/capture-readme-screenshots.mjs
 * Requires: frontend at BASE_URL, backend login API healthy.
 */
import { chromium } from "../frontend/node_modules/playwright/index.mjs";
import { mkdirSync } from "node:fs";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const ROOT = resolve(__dirname, "..");
const OUT = resolve(ROOT, "docs/images/readme");
const BASE = process.env.ACTWEAVE_UI_URL || "http://127.0.0.1:5173";
const USER = process.env.ACTWEAVE_ADMIN_USER || "admin";
const PASS = process.env.ACTWEAVE_ADMIN_PASS || "actweave-admin-dev-change-me";

mkdirSync(OUT, { recursive: true });

const pages = [
  { name: "01-login", path: "/login", needAuth: false, wait: 800 },
  { name: "02-overview", path: "/overview", needAuth: true, wait: 1200 },
  { name: "03-agents", path: "/agents", needAuth: true, wait: 1500 },
  { name: "04-tools", path: "/tools", needAuth: true, wait: 1500 },
  { name: "05-workflow", path: "/workflow", needAuth: true, wait: 1800 },
  { name: "06-smart-dag", path: "/smart-dag", needAuth: true, wait: 1500 },
  { name: "07-chat", path: "/chat", needAuth: true, wait: 1500 },
  { name: "08-agent-access", path: "/agent-access", needAuth: true, wait: 1500 },
  { name: "09-providers", path: "/providers", needAuth: true, wait: 1200 },
  { name: "10-connections", path: "/connections", needAuth: true, wait: 1200 },
  { name: "11-logs", path: "/logs", needAuth: true, wait: 1500 },
];

async function login(page) {
  await page.goto(`${BASE}/login`, { waitUntil: "networkidle", timeout: 60_000 });
  await page.fill('input[autocomplete="username"]', USER);
  await page.fill('input[type="password"], input[autocomplete="current-password"]', PASS);
  await Promise.all([
    page.waitForURL((url) => !url.pathname.includes("/login"), { timeout: 30_000 }).catch(() => null),
    page.click('button[type="submit"]'),
  ]);
  // password-change gate
  if (page.url().includes("change-password")) {
    console.warn("Account requires password change; screenshots may stop at that gate.");
  }
  await page.waitForTimeout(800);
}

async function shot(page, name) {
  const file = resolve(OUT, `${name}.png`);
  await page.screenshot({ path: file, fullPage: false });
  console.log("wrote", file);
}

const browser = await chromium.launch({ headless: true });
const context = await browser.newContext({
  viewport: { width: 1440, height: 900 },
  deviceScaleFactor: 1,
});
const page = await context.newPage();

try {
  // Login page first (unauthenticated)
  await page.goto(`${BASE}/login`, { waitUntil: "networkidle", timeout: 60_000 });
  await page.waitForTimeout(600);
  await shot(page, "01-login");

  await login(page);

  for (const item of pages) {
    if (!item.needAuth) continue;
    await page.goto(`${BASE}${item.path}`, { waitUntil: "networkidle", timeout: 60_000 });
    await page.waitForTimeout(item.wait);
    // dismiss obvious dialogs if any
    const close = page.locator('[aria-label="Close"], .el-dialog__headerbtn, button:has-text("取消")').first();
    if (await close.isVisible().catch(() => false)) {
      await close.click().catch(() => null);
      await page.waitForTimeout(300);
    }
    await shot(page, item.name);
  }

  // Optional AAP chat demo
  const demo = process.env.AAP_CHAT_URL || "http://127.0.0.1:5188";
  try {
    const res = await fetch(demo, { signal: AbortSignal.timeout(3000) });
    if (res.ok) {
      await page.goto(demo, { waitUntil: "networkidle", timeout: 30_000 });
      await page.waitForTimeout(1000);
      await shot(page, "12-aap-chat-demo");
    }
  } catch {
    console.warn("AAP chat demo not available; skipped 12-aap-chat-demo");
  }
} finally {
  await browser.close();
}

console.log("done ->", OUT);
