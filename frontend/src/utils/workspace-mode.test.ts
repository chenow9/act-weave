import { describe, expect, it } from "vitest";

import { isProductionWorkspaceMode, isSandboxWorkspaceMode } from "./workspace-mode";

describe("workspace mode", () => {
  it("treats API PRODUCTION values as production regardless of case", () => {
    expect(isProductionWorkspaceMode("PRODUCTION")).toBe(true);
    expect(isProductionWorkspaceMode("Production")).toBe(true);
    expect(isProductionWorkspaceMode(" production ")).toBe(true);
    expect(isProductionWorkspaceMode("SANDBOX")).toBe(false);
    expect(isProductionWorkspaceMode("")).toBe(false);
    expect(isProductionWorkspaceMode(undefined)).toBe(false);
  });

  it("treats API SANDBOX values as sandbox regardless of case", () => {
    expect(isSandboxWorkspaceMode("SANDBOX")).toBe(true);
    expect(isSandboxWorkspaceMode("Sandbox")).toBe(true);
    expect(isSandboxWorkspaceMode("PRODUCTION")).toBe(false);
  });
});
