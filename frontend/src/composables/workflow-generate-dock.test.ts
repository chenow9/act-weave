import { describe, expect, it } from "vitest";

import { createWorkflowGenerateDockState } from "./workflow-generate-dock";

describe("workflow generate dock state", () => {
  it("keeps the prompt when switching tabs and clears it on reset", () => {
    const dock = createWorkflowGenerateDockState();
    dock.syncLeftTabForOpenEditor(true);
    dock.selectGenerateTab();
    dock.prompt.value = "供应商准入，先查资质";

    dock.selectNodesTab(true);
    expect(dock.leftTab.value).toBe("nodes");
    expect(dock.prompt.value).toBe("供应商准入，先查资质");

    dock.resetGenerateDock();
    expect(dock.prompt.value).toBe("");
  });

  it("does not close the generate sheet while generateLock is true", () => {
    const dock = createWorkflowGenerateDockState();
    dock.selectGenerateTab();
    dock.generateLock.value = true;
    dock.closeGenerateSheet(true);
    expect(dock.generateSheetOpen.value).toBe(true);
    expect(dock.leftTab.value).toBe("generate");
  });
});
