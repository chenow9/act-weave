import { createPinia, setActivePinia } from "pinia";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { apiClient } from "../services/api";
import type { Workspace, WorkspaceMember } from "../types/domain";
import { useWorkspaceStore } from "./workspaces";

vi.mock("../services/api", async () => {
  const actual = await vi.importActual<typeof import("../services/api")>("../services/api");
  return {
    ...actual,
    apiClient: { delete: vi.fn(), get: vi.fn(), patch: vi.fn(), post: vi.fn() },
  };
});

describe("v1 workspace and member store", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.resetAllMocks();
    localStorage.clear();
  });

  it("loads the accessible v1 catalog and locally pages supported filters", async () => {
    const catalog = [
      workspaceDTO(1),
      workspaceDTO(2, { status: "DISABLED", mode: "SANDBOX", createdByUsername: "alpha.creator" }),
    ];
    vi.mocked(apiClient.get).mockResolvedValue({ data: { items: catalog } });
    const store = useWorkspaceStore();

    await store.load();
    await store.loadWorkspacePage({ query: "业务空间 2", status: "Disabled", mode: "Sandbox", page: 1, pageSize: 10 });

    expect(apiClient.get).toHaveBeenCalledWith("/workspaces?limit=500");
    expect(store.activeWorkspaceId).toBe("workspace-1");
    expect(store.pageItems.map((workspace) => workspace.id)).toEqual(["workspace-2"]);
    expect(store.pagination).toEqual({ page: 1, pageSize: 10, total: 1, pageSizeOptions: [10, 20, 50] });
    expect(store.items[0]).toMatchObject({
      createdBy: "user-owner",
      createdByUsername: "workspace.creator",
      updatedBy: "user-owner",
      updatedByUsername: "workspace.editor",
    });

    await store.loadWorkspacePage({
      query: "workspace.editor",
      status: undefined,
      mode: undefined,
      page: 1,
      pageSize: 10,
    });
    expect(store.pageItems).toHaveLength(2);

    await store.loadWorkspacePage({
      query: "",
      status: undefined,
      mode: undefined,
      page: 1,
      pageSize: 10,
      sortBy: "createdBy",
      sortOrder: "asc",
    });
    expect(store.pageItems.map((workspace) => workspace.createdByUsername)).toEqual([
      "alpha.creator",
      "workspace.creator",
    ]);
  });

  it("submits only the v1 create/update allowlists", async () => {
    vi.mocked(apiClient.post).mockResolvedValueOnce({ data: workspaceDTO(1) });
    vi.mocked(apiClient.patch).mockResolvedValueOnce({ data: workspaceDTO(1, { displayName: "更新后的空间", lockVersion: 2 }) });
    const store = useWorkspaceStore();
    const draft = workspaceValue({
      id: "",
      name: "orders",
      displayName: "订单空间",
      owner: "must-not-submit",
      healthScore: 99,
      toolCount: 4,
      workflowCount: 5,
      agentCount: 2,
      settings: { color: "blue" },
    });

    const created = await store.createWorkspace(draft);
    expect(apiClient.post).toHaveBeenCalledWith("/workspaces", {
      slug: "orders",
      displayName: "订单空间",
      mode: "PRODUCTION",
      settings: { color: "blue" },
    });

    await store.updateWorkspace(created.id, { ...created, displayName: "更新后的空间" });
    expect(apiClient.patch).toHaveBeenCalledWith("/workspaces/workspace-1", {
      displayName: "更新后的空间",
      mode: "PRODUCTION",
      settings: {},
      lockVersion: 1,
    });
  });

  it("persists and restores the active Workspace selection", async () => {
    const catalog = [workspaceDTO(1), workspaceDTO(2)];
    vi.mocked(apiClient.get).mockResolvedValue({ data: { items: catalog } });
    const firstStore = useWorkspaceStore();
    await firstStore.load();
    firstStore.selectWorkspace("workspace-2");
    expect(localStorage.getItem("actweave:active-workspace-id")).toBe("workspace-2");

    setActivePinia(createPinia());
    const restoredStore = useWorkspaceStore();
    await restoredStore.load();
    expect(restoredStore.activeWorkspaceId).toBe("workspace-2");
  });

  it("uses colon lifecycle commands and lockVersion delete preconditions", async () => {
    const store = useWorkspaceStore();
    store.items = [workspaceValue()];
    store.pageItems = [...store.items];
    vi.mocked(apiClient.post)
      .mockResolvedValueOnce({ data: workspaceDTO(1, { status: "DISABLED", lockVersion: 2 }) })
      .mockResolvedValueOnce({ data: workspaceDTO(1, { status: "ACTIVE", lockVersion: 3 }) });
    vi.mocked(apiClient.delete).mockResolvedValueOnce({ data: undefined });

    await store.disableWorkspace("workspace-1");
    expect(apiClient.post).toHaveBeenNthCalledWith(1, "/workspaces/workspace-1:disable", { lockVersion: 1 });
    await store.enableWorkspace("workspace-1");
    expect(apiClient.post).toHaveBeenNthCalledWith(2, "/workspaces/workspace-1:enable", { lockVersion: 2 });
    await store.deleteWorkspace("workspace-1");
    expect(apiClient.delete).toHaveBeenCalledWith("/workspaces/workspace-1?lockVersion=3");
  });

  it("loads and mutates members while applying the backend RBAC matrix", async () => {
    const owner = member("user-owner", "OWNER");
    const editor = member("user-editor", "EDITOR");
    vi.mocked(apiClient.get).mockResolvedValueOnce({ data: { items: [owner, editor] } });
    vi.mocked(apiClient.post).mockResolvedValueOnce({ data: member("user-viewer", "VIEWER") });
    vi.mocked(apiClient.patch).mockResolvedValueOnce({ data: member("user-viewer", "OPERATOR") });
    vi.mocked(apiClient.delete).mockResolvedValueOnce({ data: undefined });
    const store = useWorkspaceStore();
    store.items = [workspaceValue()];

    await store.loadMembers("workspace-1");
    expect(apiClient.get).toHaveBeenCalledWith("/workspaces/workspace-1/members");
    expect(store.can("workspace-1", "user-owner", "DELETE")).toBe(true);
    expect(store.can("workspace-1", "user-editor", "EDIT")).toBe(true);
    expect(store.can("workspace-1", "user-editor", "MANAGE")).toBe(false);

    await store.addMember("workspace-1", "user-viewer", "VIEWER");
    expect(apiClient.post).toHaveBeenCalledWith("/workspaces/workspace-1/members", {
      userId: "user-viewer",
      role: "VIEWER",
    });
    await store.changeMemberRole("workspace-1", "user-viewer", "OPERATOR");
    expect(apiClient.patch).toHaveBeenCalledWith("/workspaces/workspace-1/members/user-viewer", { role: "OPERATOR" });
    await store.removeMember("workspace-1", "user-viewer");
    expect(apiClient.delete).toHaveBeenCalledWith("/workspaces/workspace-1/members/user-viewer");
    expect(store.membersByWorkspace["workspace-1"].some((value) => value.userId === "user-viewer")).toBe(false);
  });

  it("searches the Workspace-scoped active user directory for member candidates", async () => {
    const candidates = [
      {
        userId: "user-new",
        username: "candidate.user",
        displayName: "Candidate User",
        platformRole: "USER" as const,
      },
    ];
    vi.mocked(apiClient.get).mockResolvedValueOnce({ data: { items: candidates } });
    const store = useWorkspaceStore();

    await expect(store.searchMemberCandidates("workspace-1", " candidate user ", 20)).resolves.toEqual(candidates);
    expect(apiClient.get).toHaveBeenCalledWith(
      "/workspaces/workspace-1/member-candidates?query=candidate+user&limit=20",
    );
  });
});

function workspaceDTO(index: number, overrides: Record<string, unknown> = {}) {
  return {
    id: `workspace-${index}`,
    slug: `workspace-${index}`,
    displayName: `业务空间 ${index}`,
    mode: "PRODUCTION",
    status: "ACTIVE",
    ownerUserId: "user-owner",
    defaultAgentId: `agent-${index}`,
    settings: {},
    createdBy: "user-owner",
    createdByUsername: "workspace.creator",
    updatedBy: "user-owner",
    updatedByUsername: "workspace.editor",
    createdAt: "2026-07-15T03:00:00Z",
    updatedAt: "2026-07-15T03:00:00Z",
    lockVersion: 1,
    ...overrides,
  };
}

function workspaceValue(overrides: Partial<Workspace> = {}): Workspace {
  return {
    id: "workspace-1",
    name: "workspace-1",
    slug: "workspace-1",
    displayName: "业务空间 1",
    mode: "Production",
    status: "Active",
    ownerUserId: "user-owner",
    defaultAgentId: "agent-1",
    modelConfigId: "",
    settings: {},
    createdBy: "user-owner",
    createdByUsername: "workspace.creator",
    updatedBy: "user-owner",
    updatedByUsername: "workspace.editor",
    lockVersion: 1,
    healthScore: 0,
    ...overrides,
  };
}

function member(userId: string, role: WorkspaceMember["role"]): WorkspaceMember {
  return { userId, role, joinedAt: "2026-07-15T03:00:00Z" };
}
