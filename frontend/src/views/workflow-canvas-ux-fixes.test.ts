import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const currentDir = dirname(fileURLToPath(import.meta.url));
const styles = readFileSync(resolve(currentDir, "../styles/app.css"), "utf8");
const graphCanvas = readFileSync(resolve(currentDir, "../components/workflow/WorkflowGraphCanvas.vue"), "utf8");

describe("workflow canvas UX safeguards", () => {
  it("balances the two-row workflow editor navigation for a desktop workbench", () => {
    const topbarBlock = styles.match(/\.workflow-editor-topbar\s*\{[\s\S]*?\n\}/)?.[0] || "";
    const actionButtonBlock = styles.match(/\.workflow-editor-actions \.primary-button[\s\S]*?\n\}/)?.[0] || "";
    const readinessChipBlock = styles.match(/\.workflow-editor-readiness-chip\s*\{[\s\S]*?\n\}/)?.[0] || "";
    const publishDisabledBlock = styles.match(/\.workflow-editor-publish-button:disabled\s*\{[\s\S]*?\n\}/)?.[0] || "";

    expect(topbarBlock).toContain("grid-template-rows: auto auto");
    expect(topbarBlock).toContain("grid-template-columns: minmax(0, 1fr) auto");
    expect(topbarBlock).toContain("margin: 16px 16px 0");
    expect(topbarBlock).toContain("border-radius: 18px");
    expect(topbarBlock).toContain("padding: 14px 18px 16px");
    expect(styles).toContain(".workflow-editor-header-row");
    expect(styles).toMatch(/\.workflow-editor-header-row\s*\{[\s\S]*?justify-content: flex-start/);
    expect(styles).toContain(".workflow-editor-action-row");
    expect(styles).toContain(".workflow-editor-primary-actions");
    expect(styles).toMatch(/\.workflow-editor-primary-actions\s*\{[\s\S]*?grid-column: 2/);
    expect(styles).toMatch(/\.workflow-editor-primary-actions\s*\{[\s\S]*?grid-row: 1 \/ 3/);
    expect(styles).toMatch(/\.workflow-editor-primary-actions\s*\{[\s\S]*?align-self: center/);
    expect(styles).toContain(".workflow-editor-secondary-actions");
    expect(styles).toMatch(/\.workflow-editor-title-row\s*\{[\s\S]*?display: flex/);
    expect(styles).toMatch(/\.workflow-editor-topbar h3\s*\{[\s\S]*?font-size: 20px/);
    expect(readinessChipBlock).toContain("min-height: 30px");
    expect(readinessChipBlock).toContain("font-size: 14px");
    expect(actionButtonBlock).toContain("min-height: 40px");
    expect(actionButtonBlock).toContain("border-radius: 10px");
    expect(styles).toMatch(/\.workflow-editor-actions \.primary-button[\s\S]*?background: #050505/);
    expect(styles).toMatch(/\.workflow-editor-actions \.primary-button[\s\S]*?box-shadow: none/);
    expect(publishDisabledBlock).toContain("background: #f1f1f1");
    expect(publishDisabledBlock).toContain("color: #b9b9b9");
  });

  it("keeps the node palette scrollable and reachable inside the full-screen editor", () => {
    expect(styles).toMatch(/\.workflow-node-palette[\s\S]*?overflow-y: auto/);
    expect(styles).toMatch(/\.workflow-node-palette[\s\S]*?min-height: 0/);
  });

  it("uses hover-revealed overlay scrollbars and edge fades for independent side panels", () => {
    expect(styles).toContain(".workflow-node-palette::-webkit-scrollbar");
    expect(styles).toContain(".workflow-workbench-side-scrollable::-webkit-scrollbar");
    expect(styles).toMatch(/\.workflow-node-palette::-webkit-scrollbar-thumb[\s\S]*?background: transparent/);
    expect(styles).toMatch(/\.workflow-node-palette:is\(:hover, :focus-within\)::-webkit-scrollbar-thumb[\s\S]*?rgba\(15, 23, 42, 0\.24\)/);
    expect(styles).toContain(".workflow-scroll-fade::before");
    expect(styles).toContain(".workflow-scroll-fade::after");
  });

  it("keeps the stacked responsive canvas usable instead of collapsing to a sliver", () => {
    expect(styles).toMatch(/@media \(max-width: 1180px\)[\s\S]*?\.workflow-workbench[\s\S]*?grid-template-rows: 340px 480px minmax\(360px, auto\)/);
    expect(styles).toMatch(/@media \(max-width: 1180px\)[\s\S]*?\.workflow-graph-canvas[\s\S]*?min-height: 480px/);
    expect(styles).toMatch(/@media \(max-width: 1180px\)[\s\S]*?\.workflow-workbench-side[\s\S]*?min-height: 360px/);
  });

  it("keeps editor action controls and context menu items reachable at desktop sizes", () => {
    const actionBlock = styles.match(/\.workflow-editor-actions \.primary-button[\s\S]*?\n\}/)?.[0] || "";
    const contextItemBlock = styles.match(/\.workflow-context-menu-item\s*\{[\s\S]*?\n\}/)?.[0] || "";

    expect(actionBlock).toContain("min-height: 40px");
    expect(contextItemBlock).toContain("min-height: 44px");
  });

  it("adds a larger hit area and accessible names for graph connection handles", () => {
    expect(styles).toContain(".workflow-flow-handle::before");
    expect(styles).toContain("inset: -12px");
    expect(graphCanvas).toContain(":aria-label=\"`");
    // Single exit handle per side: accessible name uses node label + input/output direction.
    expect(graphCanvas).toContain("port.direction === 'input' ? '输入' : '输出'");
    expect(graphCanvas).toContain("visiblePortsForNode");
  });

  it("does not force fit-view after users manually adjust the viewport", () => {
    expect(graphCanvas).toContain("const hasUserAdjustedViewport = ref(false)");
    expect(graphCanvas).toContain("@move-start=\"handleViewportMoveStart\"");
    expect(graphCanvas).toContain("if (hasUserAdjustedViewport.value) return;");
    expect(graphCanvas).toContain("fitCanvasView(\"auto\")");
  });

  it("shows visible focus for the editor shell, graph nodes, and trial-run dialog", () => {
    expect(styles).toContain(".workflow-editor-shell:focus-visible");
    expect(styles).toContain(".workflow-flow-node:focus-visible");
    expect(styles).toContain(".workflow-trial-run-dialog:focus-visible");
  });
});
