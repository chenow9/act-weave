import { chromium } from "playwright";
import { mkdir, writeFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const fixture = resolve(here, "chrome-fixture.html");
const outDir = here;

async function measure(page, viewport) {
  await page.setViewportSize(viewport);
  await page.goto(`file://${fixture}`);
  const metrics = await page.evaluate(() => {
    const pageEl = document.documentElement;
    const bodies = [...document.querySelectorAll(".body")].map((el) => ({
      id: el.id || el.parentElement?.id || "body",
      scrollWidth: el.scrollWidth,
      clientWidth: el.clientWidth,
      scrollHeight: el.scrollHeight,
      clientHeight: el.clientHeight,
    }));
    const tableWrap = document.querySelector(".table-wrap");
    return {
      documentScrollWidth: pageEl.scrollWidth,
      documentClientWidth: pageEl.clientWidth,
      noPageXOverflow: pageEl.scrollWidth <= pageEl.clientWidth + 1,
      bodies,
      tableInnerScroll:
        tableWrap != null &&
        tableWrap.scrollWidth > tableWrap.clientWidth &&
        tableWrap.scrollWidth > tableWrap.parentElement.clientWidth - 40,
      archiveLabel: document.querySelector(".chat-inline-action")?.textContent?.trim(),
      menuLabels: [...document.querySelectorAll(".menu button")].map((b) => b.textContent?.trim()),
      badges: [...document.querySelectorAll(".badge")].map((b) => b.textContent?.trim()),
      activeTitle: document.querySelector(".workflow-revision-meta-id")?.getAttribute("title"),
    };
  });
  const shot = resolve(outDir, `viewport-${viewport.width}x${viewport.height}.png`);
  await page.screenshot({ path: shot, fullPage: true });
  return { viewport, metrics, shot };
}

const browser = await chromium.launch();
const page = await browser.newPage();
const results = [];
for (const viewport of [
  { width: 1180, height: 900 },
  { width: 1440, height: 900 },
]) {
  results.push(await measure(page, viewport));
}
await browser.close();

const summary = {
  date: "2026-07-26",
  engine: "playwright chromium",
  fixture: "chrome-fixture.html",
  results,
  pass: results.every((r) => r.metrics.noPageXOverflow && r.metrics.archiveLabel === "归档" && r.metrics.menuLabels.includes("查看能力资产")),
};
await writeFile(resolve(outDir, "chrome-viewport-check.json"), JSON.stringify(summary, null, 2));
console.log(JSON.stringify(summary, null, 2));
process.exit(summary.pass ? 0 : 1);
