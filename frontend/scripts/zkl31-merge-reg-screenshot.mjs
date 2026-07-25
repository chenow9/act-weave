import { chromium } from "@playwright/test";
import path from "node:path";
import fs from "node:fs";

const outDir = path.resolve("../docs/qa/evidence/zkl-31-merge-reg");
fs.mkdirSync(outDir, { recursive: true });
const base = "http://127.0.0.1:5174";
const user = process.env.ACTWEAVE_QA_USER || "admin";
const pass = process.env.ACTWEAVE_QA_PASSWORD || "actweave-admin-dev-change-me";

async function measureOpenAPI(page) {
  return page.evaluate(() => {
    const pick = (sel) => document.querySelector(sel);
    const styles = (el) => {
      if (!el) return null;
      const cs = getComputedStyle(el);
      return {
        className: (el.className?.toString?.() || "").slice(0, 160),
        bg: cs.backgroundColor,
        border: `${cs.borderTopWidth} ${cs.borderTopStyle} ${cs.borderTopColor}`,
        boxShadow: cs.boxShadow,
        padding: cs.padding,
      };
    };
    const btn = (el) => {
      if (!el) return null;
      const cs = getComputedStyle(el);
      return {
        text: (el.textContent || "").trim().replace(/\s+/g, " ").slice(0, 40),
        className: (el.className?.toString?.() || "").slice(0, 100),
        bg: cs.backgroundColor,
        isGreen: cs.backgroundColor === "rgb(15, 159, 110)",
        isDark:
          cs.backgroundColor === "rgb(2, 6, 23)" ||
          cs.backgroundColor === "rgb(15, 23, 42)",
      };
    };

    const listCard = pick(".management-list-card");
    const toolbar = pick(".management-list-toolbar");
    const content = pick(".management-list-content");
    const header = pick(".management-page-header");
    const primary = pick(".management-page-header .primary-button, .primary-button");
    const ghost = pick(".management-page-header .ghost-button, .ghost-button");
    const rowActions = pick(".management-row-actions");
    const openapiPrivateActions = pick(".openapi-row-actions");
    const trigger = pick(
      ".management-row-actions .management-row-actions-trigger, .management-row-actions .management-row-action-button",
    );

    let toolbarInTable = false;
    if (toolbar && content) {
      const t = toolbar.getBoundingClientRect();
      const c = content.getBoundingClientRect();
      toolbarInTable = t.top >= c.top - 2 && t.bottom <= c.bottom + 2;
    }

    const cardCs = listCard ? getComputedStyle(listCard) : null;
    const transparentShell =
      !!cardCs &&
      (cardCs.backgroundColor === "rgba(0, 0, 0, 0)" ||
        cardCs.backgroundColor === "transparent") &&
      cardCs.boxShadow === "none" &&
      parseFloat(cardCs.borderTopWidth) === 0;

    const root = pick(".openapi-import-page");
    const rootBg = root ? getComputedStyle(root).backgroundColor : null;

    return {
      h1: (document.querySelector("h1")?.textContent || "").trim(),
      hasManagementPageHeader: !!header,
      transparentShell,
      grayPageBg: rootBg === "rgb(250, 251, 252)",
      toolbarInTable,
      primary: btn(primary),
      ghost: btn(ghost),
      hasManagementRowActions: !!rowActions,
      hasPrivateOpenapiRowActions: !!openapiPrivateActions,
      rowActionTrigger: trigger
        ? {
            border: getComputedStyle(trigger).borderTopWidth,
            bg: getComputedStyle(trigger).backgroundColor,
            boxShadow: getComputedStyle(trigger).boxShadow,
            className: (trigger.className?.toString?.() || "").slice(0, 120),
          }
        : null,
      listCard: styles(listCard),
      rowCount: document.querySelectorAll(
        ".data-table tbody tr, table tbody tr",
      ).length,
    };
  });
}

async function measureUsers(page) {
  return page.evaluate(() => {
    const pick = (sel) => document.querySelector(sel);
    // Prefer menu trigger in user row actions
    const triggers = [
      ...document.querySelectorAll(
        ".user-row-actions .management-row-action-button, .user-row-actions .management-row-actions-trigger, .management-row-actions-trigger",
      ),
    ];
    const trigger =
      triggers.find((el) =>
        (el.getAttribute("aria-label") || "").includes("更多") ||
        el.classList.contains("management-row-actions-trigger") ||
        (el.textContent || "").includes("⋯") ||
        el.querySelector(".fa-ellipsis"),
      ) || triggers[triggers.length - 1] || null;

    const sample = triggers.slice(0, 6).map((el) => {
      const cs = getComputedStyle(el);
      const borderW = parseFloat(cs.borderTopWidth) || 0;
      const hasBorder =
        borderW > 0 &&
        cs.borderTopStyle !== "none" &&
        cs.borderTopColor !== "rgba(0, 0, 0, 0)";
      const opaqueBg =
        cs.backgroundColor !== "rgba(0, 0, 0, 0)" &&
        cs.backgroundColor !== "transparent";
      const hasShadow = cs.boxShadow !== "none";
      return {
        aria: (el.getAttribute("aria-label") || "").slice(0, 40),
        className: (el.className?.toString?.() || "").slice(0, 100),
        border: `${cs.borderTopWidth} ${cs.borderTopStyle} ${cs.borderTopColor}`,
        bg: cs.backgroundColor,
        boxShadow: cs.boxShadow,
        hasBorder,
        opaqueBg,
        hasShadow,
        framed: hasBorder || (opaqueBg && hasShadow),
      };
    });

    const ellipsis = sample.find(
      (s) =>
        s.className.includes("trigger") ||
        s.aria.includes("更多") ||
        s.className.includes("management-row-action"),
    );

    // All framed?
    const anyFramed = sample.some((s) => s.framed || s.hasBorder);

    return {
      h1: (document.querySelector("h1")?.textContent || "").trim(),
      hasManagementPageHeader: !!pick(".management-page-header"),
      triggerCount: triggers.length,
      sample,
      ellipsis,
      // Pass if no trigger has visible border frame
      ellipsisUnboxed: sample.every((s) => !s.hasBorder && !s.hasShadow),
      anyBorderedTrigger: sample.some((s) => s.hasBorder),
      primaryGreen:
        getComputedStyle(
          pick(".management-page-header .primary-button, .primary-button") ||
            document.body,
        ).backgroundColor === "rgb(15, 159, 110)",
    };
  });
}

const browser = await chromium.launch({ headless: true });
const context = await browser.newContext({
  viewport: { width: 1440, height: 900 },
});
const page = await context.newPage();
const report = {};

try {
  await page.goto(`${base}/login`, { waitUntil: "domcontentloaded", timeout: 30000 });
  await page.waitForTimeout(600);
  await page.locator('input[type="password"]').first().waitFor({ timeout: 10000 });
  const inputs = page.locator("input:visible");
  const n = await inputs.count();
  for (let i = 0; i < n; i++) {
    const input = inputs.nth(i);
    const type = (await input.getAttribute("type")) || "text";
    if (type === "password") await input.fill(pass);
    else if (type === "text" || type === "email" || type === "") await input.fill(user);
  }
  await page.locator('button[type="submit"]').first().click();
  await page.waitForTimeout(1800);
  report.loginUrl = page.url();

  // OpenAPI
  await page.goto(`${base}/openapi-imports`, { waitUntil: "networkidle", timeout: 30000 });
  await page.waitForTimeout(1200);
  await page.screenshot({ path: path.join(outDir, "mr-openapi-desktop.png") });
  report.openapi = await measureOpenAPI(page);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.waitForTimeout(400);
  await page.screenshot({ path: path.join(outDir, "mr-openapi-mobile.png") });
  await page.setViewportSize({ width: 1440, height: 900 });

  // Users
  await page.goto(`${base}/users`, { waitUntil: "networkidle", timeout: 30000 });
  await page.waitForTimeout(1500);
  await page.screenshot({ path: path.join(outDir, "mr-users-desktop.png") });
  // Crop-ish: full page first, then hover last action cell for detail
  report.users = await measureUsers(page);

  // Zoom into actions column by scrolling right if needed and re-screenshot
  const actionCell = page.locator(".user-row-actions, .management-row-actions").first();
  if (await actionCell.count()) {
    await actionCell.scrollIntoViewIfNeeded().catch(() => {});
    await actionCell.hover().catch(() => {});
    await page.waitForTimeout(200);
    const box = await actionCell.boundingBox();
    if (box) {
      await page.screenshot({
        path: path.join(outDir, "mr-users-actions-zoom.png"),
        clip: {
          x: Math.max(0, box.x - 80),
          y: Math.max(0, box.y - 40),
          width: Math.min(400, box.width + 160),
          height: Math.min(200, box.height + 80),
        },
      });
    }
  }

  // Reference management list actions (agents or tools) if available
  await page.goto(`${base}/agents`, { waitUntil: "networkidle", timeout: 30000 });
  await page.waitForTimeout(1000);
  report.agentsActions = await page.evaluate(() => {
    const el = document.querySelector(
      ".management-row-actions-trigger, .management-row-actions .management-row-action-button",
    );
    if (!el) return null;
    const cs = getComputedStyle(el);
    return {
      border: `${cs.borderTopWidth} ${cs.borderTopStyle}`,
      bg: cs.backgroundColor,
      boxShadow: cs.boxShadow,
    };
  });
  await page.screenshot({ path: path.join(outDir, "mr-agents-ref-desktop.png") });

  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(`${base}/users`, { waitUntil: "networkidle", timeout: 30000 });
  await page.waitForTimeout(800);
  await page.screenshot({ path: path.join(outDir, "mr-users-mobile.png") });
} catch (err) {
  report.error = String(err?.stack || err);
  await page.screenshot({ path: path.join(outDir, "error.png"), fullPage: true }).catch(() => {});
} finally {
  fs.writeFileSync(path.join(outDir, "metrics.json"), JSON.stringify(report, null, 2));
  await browser.close();
}
console.log(JSON.stringify(report, null, 2));
