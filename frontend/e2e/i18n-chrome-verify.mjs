/**
 * Real-browser verification of Console i18n (login + shell language switch).
 * Evidence written under SCRATCH when set, otherwise repo-local .run-logs.
 */
import { chromium } from "@playwright/test";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const BASE = process.env.I18N_BASE_URL || "http://127.0.0.1:5174";
const USER = process.env.I18N_USER || "admin";
const PASS = process.env.I18N_PASS || "actweave-admin-dev-change-me";
const SCRATCH =
  process.env.SCRATCH || path.join(path.dirname(fileURLToPath(import.meta.url)), "../../.run-logs");

fs.mkdirSync(SCRATCH, { recursive: true });

const results = [];
function assert(name, cond, detail = "") {
  results.push({ name, ok: Boolean(cond), detail });
  const mark = cond ? "PASS" : "FAIL";
  console.log(`[${mark}] ${name}${detail ? ` — ${detail}` : ""}`);
}

async function shot(page, name) {
  const file = path.join(SCRATCH, name);
  await page.screenshot({ path: file, fullPage: true });
  return file;
}

const browser = await chromium.launch({ headless: true });
const context = await browser.newContext({ locale: "en-US" });
const page = await context.newPage();

try {
  // Clear any prior locale
  await page.goto(`${BASE}/login`, { waitUntil: "networkidle" });
  await page.evaluate(() => localStorage.removeItem("actweave.locale"));
  await page.reload({ waitUntil: "networkidle" });

  // Default product locale is en → brand ActWeave, no 织行
  const loginBrand = await page.locator(".login-logo-area").innerText();
  assert("login brand is ActWeave (en default)", loginBrand.includes("ActWeave") && !loginBrand.includes("织行"), loginBrand);
  assert("login shows Sign in", (await page.locator("h2").innerText()).includes("Sign in"));
  await shot(page, "i18n-01-login-en.png");

  // Switch to Chinese on login
  await page.getByTestId("login-lang-zh-CN").click();
  await page.waitForTimeout(200);
  const loginBrandZh = await page.locator(".login-logo-area").innerText();
  assert("login brand zh contains 织行", loginBrandZh.includes("织行"), loginBrandZh);
  assert("login shows 登录", (await page.locator("h2").innerText()).includes("登录"));
  await shot(page, "i18n-02-login-zh.png");

  // Switch back to English then login (admin may force zh from server locale)
  await page.getByTestId("login-lang-en").click();
  await page.fill('input[autocomplete="username"]', USER);
  await page.fill('input[autocomplete="current-password"]', PASS);
  await page.getByTestId("login-submit").click();
  await page.waitForURL(/\/(overview|change-password)/, { timeout: 20_000 });

  const onOverview = page.url().includes("/overview");
  assert("landed after login", onOverview || page.url().includes("change-password"), page.url());
  if (!onOverview) {
    throw new Error("must-change-password gate; cannot verify shell");
  }

  // Server user locale is zh-CN for bootstrap admin → shell should apply user locale
  await page.waitForSelector(".app-brand", { timeout: 15_000 });
  await shot(page, "i18n-03-shell-after-login.png");

  // Open nav and check Chinese labels from user.locale
  await page.locator(".fluid-trigger").click();
  await page.waitForSelector(".fluid-content .island-module", { timeout: 5_000 });
  const navTextZh = await page.locator(".fluid-content").innerText();
  assert("nav zh has 工具管理", navTextZh.includes("工具管理"), navTextZh.slice(0, 200));
  assert("nav zh has 编排", navTextZh.includes("编排"));
  assert("nav zh does not have 智能编排", !navTextZh.includes("智能编排"));
  assert("nav zh has 运行调试台", navTextZh.includes("运行调试台"));
  await shot(page, "i18n-04-nav-zh.png");
  await page.locator(".nav-scrim").click().catch(() => page.locator(".island-close").click());

  // Switch language to English via profile menu
  await page.getByTestId("user-menu-trigger").click();
  await page.getByTestId("user-menu").waitFor({ state: "visible" });
  await page.getByTestId("lang-en").click();
  await page.waitForTimeout(400);

  const brandEn = await page.locator(".app-brand").innerText();
  assert("shell brand en is ActWeave", brandEn.includes("ActWeave") && !brandEn.includes("织行"), brandEn);
  assert("html lang is en", (await page.locator("html").getAttribute("lang")) === "en");

  await page.locator(".fluid-trigger").click();
  await page.waitForSelector(".fluid-content .island-module", { timeout: 5_000 });
  const navTextEn = await page.locator(".fluid-content").innerText();
  assert("nav en has Tools", navTextEn.includes("Tools"));
  assert("nav en has Workflow", navTextEn.includes("Workflow"));
  assert("nav en does not have Smart Orchestration", !navTextEn.includes("Smart Orchestration"));
  assert("nav en has Run Console", navTextEn.includes("Run Console"));
  assert("nav en has no 织行", !navTextEn.includes("织行"));
  await shot(page, "i18n-05-nav-en.png");

  // Bilingual search: Chinese query while UI is en
  await page.getByTestId("nav-search").fill("工具");
  await page.waitForTimeout(150);
  const filtered = await page.locator(".fluid-content .island-module").allInnerTexts();
  assert("bilingual search 工具 hits Tools", filtered.some((t) => t.includes("Tools")), JSON.stringify(filtered));
  await shot(page, "i18n-06-search-bilingual.png");

  // localStorage persisted
  const stored = await page.evaluate(() => localStorage.getItem("actweave.locale"));
  assert("localStorage actweave.locale is en", stored === "en", String(stored));

  // Switch back to zh and confirm
  await page.locator(".nav-scrim").click().catch(() => {});
  await page.getByTestId("user-menu-trigger").click();
  await page.getByTestId("lang-zh-CN").click();
  await page.waitForTimeout(300);
  const brandZh = await page.locator(".app-brand").innerText();
  assert("shell brand zh has 织行", brandZh.includes("织行"), brandZh);
  assert("html lang is zh-CN", (await page.locator("html").getAttribute("lang")) === "zh-CN");
  await shot(page, "i18n-07-shell-zh.png");
} catch (error) {
  console.error("VERIFY_ERROR", error);
  try {
    await shot(page, "i18n-error.png");
  } catch {
    /* ignore */
  }
  results.push({ name: "script completed without throw", ok: false, detail: String(error) });
} finally {
  await browser.close();
}

const failed = results.filter((r) => !r.ok);
const summary = {
  total: results.length,
  passed: results.filter((r) => r.ok).length,
  failed: failed.length,
  results,
  scratch: SCRATCH,
};
const out = path.join(SCRATCH, "i18n-verify-result.json");
fs.writeFileSync(out, JSON.stringify(summary, null, 2));
console.log("\nSUMMARY", JSON.stringify(summary, null, 2));
console.log("Wrote", out);
process.exit(failed.length ? 1 : 0);
