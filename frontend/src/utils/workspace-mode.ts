/** Workspace.mode is the API enum PRODUCTION | SANDBOX (any case). */
export function isProductionWorkspaceMode(mode?: string | null): boolean {
  return (mode ?? "").trim().toUpperCase() === "PRODUCTION";
}

export function isSandboxWorkspaceMode(mode?: string | null): boolean {
  return (mode ?? "").trim().toUpperCase() === "SANDBOX";
}
