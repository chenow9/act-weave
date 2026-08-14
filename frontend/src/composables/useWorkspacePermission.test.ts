import { createPinia, setActivePinia } from "pinia";
import { describe, expect, it } from "vitest";

import { useWorkspaceStore, type WorkspaceAction } from "../stores/workspaces";
import type { Workspace, WorkspaceRole } from "../types/domain";
import { actionsForRole, roleCan } from "./useWorkspacePermission";

describe("useWorkspacePermission matrix", () => {
  it("mirrors backend workspace_policy role×action matrix", () => {
    const matrix: Array<{ role: WorkspaceRole; allowed: WorkspaceAction[] }> = [
      { role: "OWNER", allowed: ["VIEW", "EDIT", "TEST", "PUBLISH", "EXECUTE", "MANAGE", "DELETE"] },
      { role: "ADMIN", allowed: ["VIEW", "EDIT", "TEST", "PUBLISH", "EXECUTE", "MANAGE"] },
      { role: "EDITOR", allowed: ["VIEW", "EDIT", "TEST", "PUBLISH", "EXECUTE"] },
      { role: "OPERATOR", allowed: ["VIEW", "TEST", "EXECUTE"] },
      { role: "VIEWER", allowed: ["VIEW"] },
    ];
    const all: WorkspaceAction[] = ["VIEW", "EDIT", "TEST", "PUBLISH", "EXECUTE", "MANAGE", "DELETE"];
    for (const row of matrix) {
      expect([...actionsForRole(row.role)].sort()).toEqual([...row.allowed].sort());
      for (const action of all) {
        expect(roleCan(row.role, action)).toBe(row.allowed.includes(action));
      }
    }
    expect(actionsForRole("")).toEqual([]);
    expect(roleCan(undefined, "VIEW")).toBe(false);
  });

  it("store can() reads only currentUserRole", () => {
    setActivePinia(createPinia());
    const store = useWorkspaceStore();
    store.items = [
      {
        id: "ws-1",
        name: "ws",
        displayName: "WS",
        mode: "PRODUCTION",
        status: "ACTIVE",
        defaultAgentId: "",
        modelConfigId: "",
        healthScore: 0,
        currentUserRole: "VIEWER",
      } as Workspace,
    ];
    expect(store.can("ws-1", "VIEW")).toBe(true);
    expect(store.can("ws-1", "EDIT")).toBe(false);
    expect(store.can("ws-1", "DELETE")).toBe(false);
    store.items[0].currentUserRole = "EDITOR";
    expect(store.can("ws-1", "EDIT")).toBe(true);
    expect(store.can("ws-1", "MANAGE")).toBe(false);
  });
});
