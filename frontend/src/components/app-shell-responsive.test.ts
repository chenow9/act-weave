import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { describe, expect, it } from "vitest";

const currentDir = dirname(fileURLToPath(import.meta.url));
const appCss = readFileSync(resolve(currentDir, "../styles/app.css"), "utf8");

describe("app shell responsive layout", () => {
  it("supports a full-bleed workspace without changing standard page padding", () => {
    expect(appCss).toContain(".content-area.content-area--workspace");
    expect(appCss).toContain("overflow: hidden");
    expect(appCss).toContain("padding: 0");
  });

  it("scales the fluid navigation and page canvas at tablet and mobile widths", () => {
    expect(appCss).toContain("@media (max-width: 980px)");
    expect(appCss).toContain(".fluid-island.open");
    expect(appCss).toContain("height: min(620px, calc(100vh - 24px))");
    expect(appCss).toContain("@media (max-width: 700px)");
    expect(appCss).toContain("width: calc(100vw - 16px)");
    expect(appCss).toContain(".island-grid");
    expect(appCss).toContain("grid-template-columns: repeat(2, 1fr)");
    expect(appCss).toContain(".app-shell .content-area");
    expect(appCss).toContain("padding: 20px 11px 48px");
  });

  it("collapses the fluid navigation immediately after selecting a menu item", () => {
    const collapsedIsland = appCss.match(/\.fluid-island\s*\{([\s\S]*?)\}/)?.[1] || "";
    const openIsland = appCss.match(/\.fluid-island\.open\s*\{([\s\S]*?)\}/)?.[1] || "";

    expect(collapsedIsland).toContain("height: 42px");
    expect(collapsedIsland).toContain("max-height: 42px");
    expect(collapsedIsland).not.toContain("height 0.34s");
    expect(appCss).toContain(".fluid-island:not(.open) .fluid-trigger");
    expect(appCss).toContain("height: 100%");
    expect(openIsland).toContain("height 0.34s");
    expect(openIsland).toContain("max-height: min(520px, calc(100vh - 28px))");
  });

  it("keeps the topbar decoration inside its 64px layout box", () => {
    const topbar = appCss.match(/\.app-shell > \.app-topbar\s*\{([\s\S]*?)\}/)?.[1] || "";

    expect(topbar).toContain("height: 64px");
    expect(topbar).toContain("box-shadow: inset 0 -1px");
    expect(appCss).not.toContain(".app-shell > .app-topbar::after");
  });
});
