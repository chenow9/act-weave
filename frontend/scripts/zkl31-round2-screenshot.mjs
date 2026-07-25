import { chromium } from "@playwright/test";
import path from "node:path";
import fs from "node:fs";

const outDir = path.resolve("../docs/qa/evidence/zkl-31-round2");
fs.mkdirSync(outDir, { recursive: true });
const base = "http://127.0.0.1:5174";
const user = process.env.ACTWEAVE_QA_USER || "admin";
const pass = process.env.ACTWEAVE_QA_PASSWORD || "actweave-admin-dev-change-me";

const pages = [
  { name: "workspaces", path: "/workspaces" },
  { name: "workflow", path: "/workflow" },
  { name: "openapi-imports", path: "/openapi-imports" },
  { name: "model-apis", path: "/model-apis" },
  { name: "users", path: "/users" },
];

async function measurePage(page) {
  return page.evaluate(() => {
    const pick = (sel) => document.querySelector(sel);
    const styles = (el) => {
      if (!el) return null;
      const cs = getComputedStyle(el);
      return {
        tag: el.tagName,
        className: (el.className?.toString?.() || "").slice(0, 200),
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
      const rgb = cs.backgroundColor;
      const isDark =
        rgb === "rgb(2, 6, 23)" ||
        rgb === "rgb(15, 23, 42)" ||
        rgb === "#020617" ||
        rgb === "#0f172a";
      const isGreen =
        rgb === "rgb(15, 159, 110)" ||
        rgb.includes("15, 159, 110") ||
        rgb === "rgb(11, 137, 95)";
      return {
        text: (el.textContent || "").trim().replace(/\s+/g, " ").slice(0, 60),
        className: (el.className?.toString?.() || "").slice(0, 120),
        bg: rgb,
        color: cs.color,
        borderColor: cs.borderTopColor,
        isDark,
        isGreen,
      };
    };

    const pageRoot = pick(
      ".model-config-page, .openapi-import-page, .workflow-page, .user-access-grid, .management-page-grid, .workspace-grid",
    );
    const header = pick(".management-page-header");
    const headerIcon = pick(".management-page-header-icon, .management-page-header .icon, [class*='page-header'] .fa-solid");
    const listCard = pick(".management-list-card");
    const toolbar = pick(".management-list-toolbar");
    const content = pick(".management-list-content");
    const pagination = pick(
      ".management-list-pagination, .management-list-footer, .management-list-pager",
    );
    const primary = pick(
      ".management-page-header .primary-button, .page-header .primary-button, header .primary-button, .primary-button",
    );
    const emptyPrimary = pick(
      ".management-list-content .primary-button, .registry-empty-state .primary-button, .openapi-empty-state .primary-button, .empty-state .primary-button, .workflow-empty-state .primary-button",
    );
    const emptyAnyBtn = pick(
      ".management-list-content .empty-state button, .registry-empty-state button, .openapi-empty-state button, .workflow-empty-state button, .management-list-content button.primary-button",
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

    const rootBg = pageRoot ? getComputedStyle(pageRoot).backgroundColor : null;
    const grayPageBg =
      rootBg === "rgb(250, 251, 252)" || rootBg === "rgb(248, 250, 252)";

    const iconEl =
      pick(".management-page-header [class*='icon']") ||
      pick(".management-page-header span i") ||
      pick(".management-page-header i");
    let iconWrap = iconEl;
    while (
      iconWrap &&
      iconWrap.parentElement &&
      !iconWrap.parentElement.className?.toString?.().includes("management-page-header")
    ) {
      if (
        getComputedStyle(iconWrap).backgroundColor === "rgb(234, 248, 242)" ||
        getComputedStyle(iconWrap).backgroundColor.includes("234, 248, 242") ||
        getComputedStyle(iconWrap).backgroundColor.includes("236, 253, 245") ||
        getComputedStyle(iconWrap).backgroundColor.includes("15, 159, 110")
      ) {
        break;
      }
      iconWrap = iconWrap.parentElement;
    }
    const iconWrapBg = iconWrap ? getComputedStyle(iconWrap).backgroundColor : null;

    return {
      h1: (
        document.querySelector(
          "h1, .management-page-header h1, .management-page-header h2, .page-header h2",
        )?.textContent || ""
      )
        .trim()
        .slice(0, 80),
      hasManagementPageHeader: !!header,
      pageRootBg: rootBg,
      grayPageBg,
      listCard: styles(listCard),
      toolbar: styles(toolbar),
      content: styles(content),
      pagination: styles(pagination),
      primaryButton: btnStyles(primary),
      emptyPrimaryButton: btnStyles(emptyPrimary || emptyAnyBtn),
      headerIconWrapBg: iconWrapBg,
      shellCards,
      doubleChrome,
      toolbarInTableCard,
      bodyBg: getComputedStyle(document.body).backgroundColor,
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

  await page.locator('input[type="password"]').first().waitFor({ timeout: 10000 });
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
      path: path.join(outDir, `r2-${p.name}-desktop.png`),
    });
    checklist[p.name] = await measurePage(page);
    await page.setViewportSize({ width: 390, height: 844 });
    await page.waitForTimeout(500);
    await page.screenshot({
      path: path.join(outDir, `r2-${p.name}-mobile.png`),
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
