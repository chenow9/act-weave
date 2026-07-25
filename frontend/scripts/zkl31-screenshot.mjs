import { chromium } from "@playwright/test";
import path from "node:path";
import fs from "node:fs";

const outDir = path.resolve("../docs/qa/evidence/zkl-31");
fs.mkdirSync(outDir, { recursive: true });
const base = "http://127.0.0.1:5174";
const user = process.env.ACTWEAVE_QA_USER || "admin";
const pass = process.env.ACTWEAVE_QA_PASSWORD || "actweave-admin-dev-change-me";

const pages = [
  { name: "workspaces", path: "/workspaces" },
  { name: "agents", path: "/agents" },
  { name: "connections", path: "/connections" },
  { name: "providers", path: "/providers" },
  { name: "model-apis", path: "/model-apis" },
  { name: "openapi-imports", path: "/openapi-imports" },
  { name: "tools", path: "/tools" },
];

async function measurePage(page) {
  return page.evaluate(() => {
    const pick = (sel) => document.querySelector(sel);
    const styles = (el) => {
      if (!el) return null;
      const cs = getComputedStyle(el);
      return {
        tag: el.tagName,
        className: (el.className?.toString?.() || "").slice(0, 180),
        bg: cs.backgroundColor,
        borderTop: `${cs.borderTopWidth} ${cs.borderTopStyle} ${cs.borderTopColor}`,
        borderRadius: cs.borderRadius,
        boxShadow: cs.boxShadow,
        padding: cs.padding,
        overflow: cs.overflow,
      };
    };
    const btnStyles = (el) => {
      if (!el) return null;
      const cs = getComputedStyle(el);
      return {
        text: (el.textContent || "").trim().replace(/\s+/g, " ").slice(0, 50),
        className: (el.className?.toString?.() || "").slice(0, 100),
        bg: cs.backgroundColor,
        color: cs.color,
        borderColor: cs.borderTopColor,
        height: cs.height,
      };
    };

    const listCard = pick(".management-list-card");
    const toolbar = pick(".management-list-toolbar");
    const content = pick(".management-list-content");
    const pagination = pick(
      ".management-list-pagination, .management-list-footer, .management-list-pager",
    );
    const primary = pick(
      ".connection-page-header .primary-button, .providers-page-header .primary-button, .model-config-header .primary-button, .openapi-header-actions .primary-button, .page-header .primary-button, header .primary-button, .primary-button",
    );
    const ghost = pick(
      ".connection-page-header .ghost-button, .providers-page-header .ghost-button, .openapi-header-actions .ghost-button, .page-header .ghost-button, header .ghost-button, .ghost-button",
    );

    const shellCards = [
      ...document.querySelectorAll(
        ".management-list-card, .panel, [class*='table-card']",
      ),
    ].map((el) => {
      const cs = getComputedStyle(el);
      const bg = cs.backgroundColor;
      const opaque = bg !== "rgba(0, 0, 0, 0)" && bg !== "transparent";
      return {
        className: (el.className?.toString?.() || "").slice(0, 120),
        bg,
        opaque,
        borderTopW: cs.borderTopWidth,
        shadow: cs.boxShadow !== "none",
        hasToolbar: !!el.querySelector(".management-list-toolbar"),
        hasTable: !!el.querySelector("table, .data-table"),
      };
    });

    const doubleChrome = shellCards.some(
      (c) =>
        c.hasToolbar &&
        c.hasTable &&
        c.opaque &&
        (c.shadow || parseFloat(c.borderTopW) > 0),
    );

    let toolbarInTableCard = false;
    if (toolbar && content) {
      const tRect = toolbar.getBoundingClientRect();
      const cRect = content.getBoundingClientRect();
      toolbarInTableCard =
        tRect.top >= cRect.top - 2 && tRect.bottom <= cRect.bottom + 2;
    }

    return {
      h1: (
        document.querySelector("h1, .page-header h2, header h1")?.textContent ||
        ""
      )
        .trim()
        .slice(0, 80),
      listCard: styles(listCard),
      toolbar: styles(toolbar),
      content: styles(content),
      pagination: styles(pagination),
      primaryButton: btnStyles(primary),
      ghostButton: btnStyles(ghost),
      shellCards,
      doubleChrome,
      toolbarInTableCard,
      bodyBg: getComputedStyle(document.body).backgroundColor,
      mainBg: (() => {
        const main = pick(
          ".app-shell-main, .app-main, main, .content-area, .app-shell",
        );
        return main ? getComputedStyle(main).backgroundColor : null;
      })(),
    };
  });
}

const browser = await chromium.launch({ headless: true });
const context = await browser.newContext({
  viewport: { width: 1440, height: 900 },
});
const page = await context.newPage();
const checklist = {};

try {
  await page.goto(`${base}/login`, {
    waitUntil: "domcontentloaded",
    timeout: 30000,
  });
  await page.waitForTimeout(800);

  const password = page.locator('input[type="password"]').first();
  await password.waitFor({ timeout: 10000 });
  const allInputs = page.locator("input:visible");
  const n = await allInputs.count();
  for (let i = 0; i < n; i++) {
    const input = allInputs.nth(i);
    const type = (await input.getAttribute("type")) || "text";
    if (type === "password") {
      await input.fill(pass);
    } else if (type === "text" || type === "email" || type === "") {
      await input.fill(user);
    }
  }
  await page.locator('button[type="submit"]').first().click();
  await page.waitForTimeout(2000);
  await page.screenshot({ path: path.join(outDir, "00-after-login.png") });
  checklist.loginUrl = page.url();

  for (const p of pages) {
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto(`${base}${p.path}`, {
      waitUntil: "networkidle",
      timeout: 30000,
    });
    await page.waitForTimeout(1500);
    await page.screenshot({
      path: path.join(outDir, `after-${p.name}-desktop.png`),
    });
    checklist[p.name] = await measurePage(page);
    await page.setViewportSize({ width: 390, height: 844 });
    await page.waitForTimeout(500);
    await page.screenshot({
      path: path.join(outDir, `after-${p.name}-mobile.png`),
    });
  }
} catch (err) {
  console.error("ERROR", err);
  checklist.error = String(err?.stack || err);
  await page
    .screenshot({ path: path.join(outDir, "error.png"), fullPage: true })
    .catch(() => {});
} finally {
  fs.writeFileSync(
    path.join(outDir, "metrics.json"),
    JSON.stringify(checklist, null, 2),
  );
  await browser.close();
}
console.log(JSON.stringify(checklist, null, 2));
