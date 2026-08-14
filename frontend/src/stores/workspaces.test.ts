import { createPinia, setActivePinia } from "pinia";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { apiClient } from "../services/api";
import type { Workspace, WorkspaceMember, WorkspaceRole } from "../types/domain";
import { useWorkspaceStore, type WorkspaceAction } from "./workspaces";

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

  it("loads a bounded server page for bootstrap and management lists", async () => {
    const pageResponse = {
      items: [
        workspaceDTO(1, { currentUserRole: "OWNER" }),
        workspaceDTO(2, {
          status: "DISABLED",
          mode: "SANDBOX",
          createdByUsername: "alpha.creator",
          currentUserRole: "EDITOR",
        }),
      ],
      pagination: { page: 1, pageSize: 50, total: 2 },
      summary: { total: 2, active: 1, production: 1, boundAgents: 0 },
    };
    vi.mocked(apiClient.get).mockResolvedValue({ data: pageResponse });
    const store = useWorkspaceStore();

    await store.load();
    expect(apiClient.get).toHaveBeenCalledWith("/workspaces", {
      params: expect.objectContaining({ page: 1, pageSize: 50 }),
    });
    expect(store.activeWorkspaceId).toBe("workspace-1");
    expect(store.items).toHaveLength(2);
    expect(store.summary.total).toBe(2);

    vi.mocked(apiClient.get).mockResolvedValueOnce({
      data: {
        items: [workspaceDTO(2, { currentUserRole: "EDITOR" })],
        pagination: { page: 1, pageSize: 10, total: 1 },
        summary: { total: 2, active: 1, production: 1, boundAgents: 0 },
      },
    });
    await store.loadWorkspacePage({
      query: "业务空间 2",
      status: "DISABLED",
      mode: "SANDBOX",
      page: 1,
      pageSize: 10,
    });
    expect(apiClient.get).toHaveBeenLastCalledWith("/workspaces", {
      params: expect.objectContaining({
        page: 1,
        pageSize: 10,
        query: "业务空间 2",
        status: "DISABLED",
        mode: "SANDBOX",
      }),
    });
    expect(store.pageItems.map((workspace) => workspace.id)).toEqual(["workspace-2"]);
    expect(store.pagination).toEqual({ page: 1, pageSize: 10, total: 1, pageSizeOptions: [10, 20, 50] });
    expect(store.summary.total).toBe(2);
  });

  it("submits only the v1 create/update allowlists", async () => {
    vi.mocked(apiClient.post).mockResolvedValueOnce({ data: workspaceDTO(1, { currentUserRole: "OWNER" }) });
    vi.mocked(apiClient.patch).mockResolvedValueOnce({
      data: workspaceDTO(1, { displayName: "更新后的空间", lockVersion: 2, currentUserRole: "OWNER" }),
    });
    vi.mocked(apiClient.get).mockResolvedValue({
      data: {
        items: [workspaceDTO(1, { currentUserRole: "OWNER" })],
        pagination: { page: 1, pageSize: 10, total: 1 },
        summary: { total: 1, active: 1, production: 1, boundAgents: 0 },
      },
    });
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
    const pageResponse = {
      items: [workspaceDTO(1, { currentUserRole: "OWNER" }), workspaceDTO(2, { currentUserRole: "OWNER" })],
      pagination: { page: 1, pageSize: 50, total: 2 },
      summary: { total: 2, active: 2, production: 2, boundAgents: 0 },
    };
    vi.mocked(apiClient.get).mockResolvedValue({ data: pageResponse });
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
    store.items = [workspaceValue({ currentUserRole: "OWNER" })];
    store.pageItems = [...store.items];
    store.listQuery = { query: "", page: 1, pageSize: 10 };
    vi.mocked(apiClient.post)
      .mockResolvedValueOnce({
        data: workspaceDTO(1, { status: "DISABLED", lockVersion: 2, currentUserRole: "OWNER" }),
      })
      .mockResolvedValueOnce({ data: workspaceDTO(1, { status: "ACTIVE", lockVersion: 3, currentUserRole: "OWNER" }) });
    vi.mocked(apiClient.delete).mockResolvedValueOnce({ data: undefined });
    vi.mocked(apiClient.get).mockResolvedValue({
      data: {
        items: [],
        pagination: { page: 1, pageSize: 10, total: 0 },
        summary: { total: 0, active: 0, production: 0, boundAgents: 0 },
      },
    });

    await store.disableWorkspace("workspace-1");
    expect(apiClient.post).toHaveBeenNthCalledWith(1, "/workspaces/workspace-1:disable", { lockVersion: 1 });
    await store.enableWorkspace("workspace-1");
    expect(apiClient.post).toHaveBeenNthCalledWith(2, "/workspaces/workspace-1:enable", { lockVersion: 2 });
    await store.deleteWorkspace("workspace-1");
    expect(apiClient.delete).toHaveBeenCalledWith("/workspaces/workspace-1", { params: { lockVersion: 3 } });
  });

  it("loads and mutates members without using them for authorization", async () => {
    const owner = member("user-owner", "OWNER");
    const editor = member("user-editor", "EDITOR");
    vi.mocked(apiClient.get).mockResolvedValueOnce({ data: { items: [owner, editor] } });
    vi.mocked(apiClient.post).mockResolvedValueOnce({ data: member("user-viewer", "VIEWER") });
    vi.mocked(apiClient.patch).mockResolvedValueOnce({ data: member("user-viewer", "OPERATOR") });
    vi.mocked(apiClient.delete).mockResolvedValueOnce({ data: undefined });
    const store = useWorkspaceStore();
    store.items = [workspaceValue({ currentUserRole: "OWNER" })];

    await store.loadMembers("workspace-1");
    expect(apiClient.get).toHaveBeenCalledWith("/workspaces/workspace-1/members", { params: undefined });
    expect(store.can("workspace-1", "DELETE")).toBe(true);
    expect(store.can("workspace-1", "user-ignored", "DELETE")).toBe(true);

    store.items = [workspaceValue({ currentUserRole: "EDITOR" })];
    expect(store.can("workspace-1", "EDIT")).toBe(true);
    expect(store.can("workspace-1", "MANAGE")).toBe(false);

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

  it("applies the backend role×action matrix from currentUserRole only", async () => {
    const store = useWorkspaceStore();
    const matrix: Array<{ role: WorkspaceRole; action: WorkspaceAction; want: boolean }> = [
      { role: "OWNER", action: "DELETE", want: true },
      { role: "ADMIN", action: "MANAGE", want: true },
      { role: "ADMIN", action: "DELETE", want: false },
      { role: "EDITOR", action: "PUBLISH", want: true },
      { role: "EDITOR", action: "MANAGE", want: false },
      { role: "OPERATOR", action: "TEST", want: true },
      { role: "OPERATOR", action: "EDIT", want: false },
      { role: "VIEWER", action: "VIEW", want: true },
      { role: "VIEWER", action: "EXECUTE", want: false },
    ];
    for (const tc of matrix) {
      store.items = [workspaceValue({ currentUserRole: tc.role })];
      expect(store.can("workspace-1", tc.action), `${tc.role}/${tc.action}`).toBe(tc.want);
    }
    store.items = [workspaceValue({ id: "workspace-2", currentUserRole: undefined })]; // no role
    expect(store.can("workspace-2", "VIEW")).toBe(false);
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

function workspaceDTO(index: number, overrides: Partial<Record<string, unknown>> = {}) {
  return {
    id: `workspace-${index}`,
    slug: `workspace-${index}`,
    displayName: `业务空间 ${index}`,
    mode: "PRODUCTION",
    status: "ACTIVE",
    ownerUserId: "user-owner",
    settings: {},
    createdBy: "user-owner",
    createdByUsername: "workspace.creator",
    updatedBy: "user-owner",
    updatedByUsername: "workspace.editor",
    createdAt: "2026-07-15T03:00:00Z",
    updatedAt: "2026-07-15T04:00:00Z",
    lockVersion: 1,
    currentUserRole: "OWNER",
    ...overrides,
  };
}

function workspaceValue(overrides: Partial<Workspace> = {}): Workspace {
  return {
    id: "workspace-1",
    name: "workspace-1",
    displayName: "业务空间 1",
    mode: "PRODUCTION",
    status: "ACTIVE",
    ownerUserId: "user-owner",
    defaultAgentId: "",
    modelConfigId: "",
    settings: {},
    createdBy: "user-owner",
    updatedBy: "user-owner",
    lockVersion: 1,
    healthScore: 0,
    currentUserRole: "OWNER",
    ...overrides,
  };
}

function member(userId: string, role: WorkspaceRole): WorkspaceMember {
  return { userId, role, joinedAt: "2026-07-15T03:00:00Z" };
}
