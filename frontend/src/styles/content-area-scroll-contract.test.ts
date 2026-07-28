import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

/**
 * Contract: management pages must scroll inside .content-area, not the document.
 * Document scrollbars change the usable width of a centered max-width layout and
 * shift the whole page when list filters empty/fill content (Tools 工具管理).
 */
const cssPath = resolve(dirname(fileURLToPath(import.meta.url)), "app.css");
const css = readFileSync(cssPath, "utf8");

function ruleBlock(selector: string) {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const match = css.match(new RegExp(`${escaped}\\s*\\{([^}]+)\\}`, "m"));
  expect(match, `expected CSS rule for ${selector}`).toBeTruthy();
  return match![1];
}

describe("content-area scrollport contract", () => {
  it("pins body overflow and shell height so document does not scroll", () => {
    expect(css).toMatch(/html\s*,\s*body\s*,\s*#app\s*\{[^}]*height:\s*100%/s);
    // Prototype body block must lock document scroll (content-area is the scrollport).
    expect(css).toMatch(
      /body\s*\{[^}]*Keep document from gaining a second scrollbar[^}]*overflow:\s*hidden/s,
    );
    // Final app-shell containment must not re-open overflow:visible.
    const shellMatches = [...css.matchAll(/\.app-shell\s*\{([^}]+)\}/g)].map((m) => m[1]);
    expect(shellMatches.length).toBeGreaterThan(0);
    const lastShell = shellMatches.at(-1)!;
    expect(lastShell).toMatch(/overflow:\s*hidden/);
    expect(lastShell).not.toMatch(/overflow:\s*visible/);
  });

  it("makes main-shell + content-area a fixed-height internal scrollport", () => {
    const mainShell = ruleBlock(".app-shell > .main-shell");
    expect(mainShell).toMatch(/height:\s*calc\(100%\s*-\s*64px\)/);
    expect(mainShell).toMatch(/min-height:\s*0/);

    const contentArea = ruleBlock(".app-shell .content-area");
    expect(contentArea).toMatch(/height:\s*100%/);
    expect(contentArea).toMatch(/max-height:\s*100%/);
    expect(contentArea).toMatch(/overflow-y:\s*scroll/);
    expect(contentArea).toMatch(/scrollbar-gutter:\s*stable/);
    // min-height alone (without max/height pin) is the regression that caused
    // document scrollbar + centered layout horizontal jump.
    expect(contentArea).not.toMatch(/min-height:\s*calc\(100vh\s*-\s*64px\)/);
  });

  it("keeps management grids centered with stable rail comments", () => {
    expect(css).toMatch(
      /\.app-shell \.content-area > \.page-grid\.management-page-grid\s*\{[^}]*max-width:\s*1344px/s,
    );
    expect(css).toMatch(/margin:\s*0 auto/);
    expect(css).toMatch(/scrollbar-gutter:\s*stable/);
  });
});
