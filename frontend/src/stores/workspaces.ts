import { defineStore } from "pinia";

import { apiClient, toAPIError } from "../services/api";
import { DEFAULT_PAGE_SIZE, PAGE_SIZE_OPTIONS, type ListPagination } from "../services/paginated-list";
import type {
  Workspace,
  WorkspaceListQuery,
  WorkspaceMember,
  WorkspaceMemberCandidate,
  WorkspaceRole,
} from "../types/domain";

export type WorkspaceAction = "VIEW" | "EDIT" | "MANAGE" | "DELETE";

const ACTIVE_WORKSPACE_STORAGE_KEY = "actweave:active-workspace-id";

interface WorkspaceDTO {
  id: string;
  slug: string;
  displayName: string;
  mode: "PRODUCTION" | "SANDBOX";
  status: "ACTIVE" | "DISABLED";
  ownerUserId: string;
  defaultAgentId?: string;
  defaultModelConfigId?: string;
  settings: Record<string, unknown>;
  createdBy: string;
  createdByUsername?: string;
  updatedBy: string;
  updatedByUsername?: string;
  createdAt: string;
  updatedAt: string;
  lockVersion: number;
}

interface WorkspaceState {
  items: Workspace[];
  pageItems: Workspace[];
  membersByWorkspace: Record<string, WorkspaceMember[]>;
  pagination: ListPagination;
  listQuery: Required<Pick<WorkspaceListQuery, "query" | "page" | "pageSize">> &
    Pick<WorkspaceListQuery, "status" | "mode" | "sortBy" | "sortOrder">;
  activeWorkspaceId: string;
  loading: boolean;
  pageLoading: boolean;
  pageError: string | null;
  pageHasLoaded: boolean;
}

export const useWorkspaceStore = defineStore("workspaces", {
  state: (): WorkspaceState => ({
    items: [],
    pageItems: [],
    membersByWorkspace: {},
    pagination: { page: 1, pageSize: DEFAULT_PAGE_SIZE, total: 0, pageSizeOptions: [...PAGE_SIZE_OPTIONS] },
    listQuery: { query: "", page: 1, pageSize: DEFAULT_PAGE_SIZE, sortBy: undefined, sortOrder: undefined },
    activeWorkspaceId: readActiveWorkspaceID(),
    loading: false,
    pageLoading: false,
    pageError: null,
    pageHasLoaded: false,
  }),
  getters: {
    activeWorkspace: (state) => state.items.find((item) => item.id === state.activeWorkspaceId) || state.items[0] || null,
  },
  actions: {
    async fetchCatalog() {
      const response = await apiClient.get<{ items: WorkspaceDTO[] }>("/workspaces?limit=500");
      return response.data.items.map(workspaceFromDTO);
    },
    async load() {
      this.loading = true;
      try {
        this.items = await this.fetchCatalog();
        if (!this.items.some((workspace) => workspace.id === this.activeWorkspaceId)) {
          this.selectWorkspace(this.items[0]?.id || "");
        } else {
          writeActiveWorkspaceID(this.activeWorkspaceId);
        }
        return this.items;
      } finally {
        this.loading = false;
      }
    },
    async loadWorkspacePage(query: WorkspaceListQuery = {}) {
      this.pageLoading = true;
      this.pageError = null;
      const sortBy = query.sortBy !== undefined ? query.sortBy || undefined : this.listQuery.sortBy;
      const sortOrder = sortBy
        ? query.sortOrder !== undefined
          ? query.sortOrder
          : this.listQuery.sortOrder
        : undefined;
      const requestQuery = {
        ...this.listQuery,
        ...query,
        query: query.query ?? this.listQuery.query,
        page: query.page ?? this.listQuery.page,
        pageSize: query.pageSize ?? this.listQuery.pageSize,
        sortBy,
        sortOrder,
      };
      try {
        const catalog = await this.fetchCatalog();
        const filtered = filterWorkspaces(catalog, requestQuery);
        const sorted = sortWorkspaces(filtered, sortBy, sortOrder);
        const page = Math.max(1, requestQuery.page);
        const pageSize = Math.max(1, requestQuery.pageSize);
        const offset = (page - 1) * pageSize;
        this.pageItems = sorted.slice(offset, offset + pageSize);
        this.pagination = { page, pageSize, total: sorted.length, pageSizeOptions: [...PAGE_SIZE_OPTIONS] };
        this.listQuery = {
          query: requestQuery.query,
          status: requestQuery.status,
          mode: requestQuery.mode,
          page,
          pageSize,
          sortBy,
          sortOrder,
        };
        this.pageHasLoaded = true;
        return this.pageItems;
      } catch (error) {
        this.pageError = toAPIError(error).message;
        return this.pageItems;
      } finally {
        this.pageLoading = false;
      }
    },
    async createWorkspace(workspace: Workspace) {
      const response = await apiClient.post<WorkspaceDTO>("/workspaces", {
        slug: workspace.name,
        displayName: workspace.displayName,
        mode: modeToDTO(workspace.mode),
        settings: workspace.settings || {},
      });
      const created = workspaceFromDTO(response.data);
      this.upsertWorkspace(created);
      this.selectWorkspace(created.id);
      return created;
    },
    async updateWorkspace(workspaceId: string, workspace: Workspace) {
      const response = await apiClient.patch<WorkspaceDTO>(`/workspaces/${workspaceId}`, {
        displayName: workspace.displayName,
        mode: modeToDTO(workspace.mode),
        settings: workspace.settings || {},
        lockVersion: workspace.lockVersion,
      });
      const updated = workspaceFromDTO(response.data);
      this.upsertWorkspace(updated);
      return updated;
    },
    async enableWorkspace(workspaceId: string) {
      return this.setWorkspaceStatus(workspaceId, "enable");
    },
    async disableWorkspace(workspaceId: string) {
      return this.setWorkspaceStatus(workspaceId, "disable");
    },
    async setWorkspaceStatus(workspaceId: string, command: "enable" | "disable") {
      const current = this.requireWorkspace(workspaceId);
      const response = await apiClient.post<WorkspaceDTO>(`/workspaces/${workspaceId}:${command}`, {
        lockVersion: current.lockVersion,
      });
      const updated = workspaceFromDTO(response.data);
      this.upsertWorkspace(updated);
      return updated;
    },
    async deleteWorkspace(workspaceId: string) {
      const current = this.requireWorkspace(workspaceId);
      await apiClient.delete(`/workspaces/${workspaceId}?lockVersion=${current.lockVersion}`);
      this.items = this.items.filter((workspace) => workspace.id !== workspaceId);
      this.pageItems = this.pageItems.filter((workspace) => workspace.id !== workspaceId);
      delete this.membersByWorkspace[workspaceId];
      this.pagination = { ...this.pagination, total: Math.max(0, this.pagination.total - 1) };
      if (this.activeWorkspaceId === workspaceId) {
        this.selectWorkspace(this.items[0]?.id || "");
      }
    },
    async loadMembers(workspaceId: string, includeDisabled = false) {
      const suffix = includeDisabled ? "?includeDisabled=true" : "";
      const response = await apiClient.get<{ items: WorkspaceMember[] }>(`/workspaces/${workspaceId}/members${suffix}`);
      this.membersByWorkspace[workspaceId] = response.data.items;
      return response.data.items;
    },
    async searchMemberCandidates(workspaceId: string, query = "", limit = 20) {
      const params = new URLSearchParams({ query: query.trim(), limit: String(limit) });
      const response = await apiClient.get<{ items: WorkspaceMemberCandidate[] }>(
        `/workspaces/${workspaceId}/member-candidates?${params.toString()}`,
      );
      return response.data.items;
    },
    async loadMemberRoles(userId: string, workspaces?: Workspace[]) {
      const targets = workspaces || this.pageItems;
      await Promise.all(
        targets.map(async (workspace) => {
          if (workspace.ownerUserId === userId || this.membersByWorkspace[workspace.id]) {
            return;
          }
          await this.loadMembers(workspace.id);
        }),
      );
    },
    async addMember(workspaceId: string, userId: string, role: WorkspaceRole) {
      const response = await apiClient.post<WorkspaceMember>(`/workspaces/${workspaceId}/members`, { userId, role });
      this.upsertMember(workspaceId, response.data);
      return response.data;
    },
    async changeMemberRole(workspaceId: string, userId: string, role: WorkspaceRole) {
      const response = await apiClient.patch<WorkspaceMember>(`/workspaces/${workspaceId}/members/${userId}`, { role });
      this.upsertMember(workspaceId, response.data);
      return response.data;
    },
    async removeMember(workspaceId: string, userId: string) {
      await apiClient.delete(`/workspaces/${workspaceId}/members/${userId}`);
      this.membersByWorkspace[workspaceId] = (this.membersByWorkspace[workspaceId] || []).filter(
        (member) => member.userId !== userId,
      );
    },
    roleFor(workspaceId: string, userId: string): WorkspaceRole | "" {
      const workspace = this.items.find((item) => item.id === workspaceId);
      if (workspace?.ownerUserId === userId) {
        return "OWNER";
      }
      return this.membersByWorkspace[workspaceId]?.find((member) => member.userId === userId && !member.disabledAt)?.role || "";
    },
    can(workspaceId: string, userId: string, action: WorkspaceAction) {
      const role = this.roleFor(workspaceId, userId);
      if (action === "VIEW") return Boolean(role);
      if (action === "EDIT") return role === "OWNER" || role === "ADMIN" || role === "EDITOR";
      if (action === "MANAGE") return role === "OWNER" || role === "ADMIN";
      return role === "OWNER";
    },
    selectWorkspace(workspaceId: string) {
      if (workspaceId && this.items.length && !this.items.some((workspace) => workspace.id === workspaceId)) {
        return;
      }
      this.activeWorkspaceId = workspaceId;
      writeActiveWorkspaceID(workspaceId);
    },
    requireWorkspace(workspaceId: string) {
      const workspace = this.items.find((item) => item.id === workspaceId) || this.pageItems.find((item) => item.id === workspaceId);
      if (!workspace) {
        throw new Error(`Workspace ${workspaceId} is not loaded.`);
      }
      return workspace;
    },
    upsertWorkspace(workspace: Workspace) {
      this.items = upsertByID(this.items, workspace);
      if (this.pageItems.some((item) => item.id === workspace.id)) {
        this.pageItems = upsertByID(this.pageItems, workspace);
      }
    },
    upsertMember(workspaceId: string, member: WorkspaceMember) {
      this.membersByWorkspace[workspaceId] = upsertMemberByID(this.membersByWorkspace[workspaceId] || [], member);
    },
  },
});

function readActiveWorkspaceID() {
  if (typeof window === "undefined") return "";
  return window.localStorage.getItem(ACTIVE_WORKSPACE_STORAGE_KEY)?.trim() || "";
}

function writeActiveWorkspaceID(workspaceId: string) {
  if (typeof window === "undefined") return;
  if (workspaceId) window.localStorage.setItem(ACTIVE_WORKSPACE_STORAGE_KEY, workspaceId);
  else window.localStorage.removeItem(ACTIVE_WORKSPACE_STORAGE_KEY);
}

function workspaceFromDTO(value: WorkspaceDTO): Workspace {
  return {
    id: value.id,
    name: value.slug,
    slug: value.slug,
    displayName: value.displayName,
    mode: value.mode === "PRODUCTION" ? "Production" : "Sandbox",
    status: value.status === "ACTIVE" ? "Active" : "Disabled",
    ownerUserId: value.ownerUserId,
    defaultAgentId: value.defaultAgentId || "",
    defaultModelConfigId: value.defaultModelConfigId,
    modelConfigId: value.defaultModelConfigId || "",
    settings: value.settings || {},
    createdBy: value.createdBy,
    createdByUsername: value.createdByUsername,
    updatedBy: value.updatedBy,
    updatedByUsername: value.updatedByUsername,
    createdAt: value.createdAt,
    updatedAt: value.updatedAt,
    lockVersion: value.lockVersion,
    healthScore: 0,
  };
}

function modeToDTO(mode: Workspace["mode"]): WorkspaceDTO["mode"] {
  return mode === "Production" ? "PRODUCTION" : "SANDBOX";
}

function filterWorkspaces(items: Workspace[], query: WorkspaceListQuery) {
  const needle = query.query?.trim().toLocaleLowerCase() || "";
  return items.filter((workspace) => {
    if (query.status && workspace.status !== query.status) return false;
    if (query.mode && workspace.mode !== query.mode) return false;
    if (!needle) return true;
    return [
      workspace.name,
      workspace.displayName,
      workspace.createdByUsername || "",
      workspace.updatedByUsername || "",
      workspace.createdBy || "",
      workspace.updatedBy || "",
    ].some((value) =>
      value.toLocaleLowerCase().includes(needle),
    );
  });
}

function sortWorkspaces(items: Workspace[], sortBy?: string, order?: "asc" | "desc") {
  if (!sortBy || !order) return items;
  const allowed = new Set(["name", "displayName", "status", "mode", "createdBy", "updatedBy", "createdAt", "updatedAt"]);
  if (!allowed.has(sortBy)) return items;
  return [...items].sort((left, right) => {
    const comparison = workspaceSortValue(left, sortBy).localeCompare(
      workspaceSortValue(right, sortBy),
      "zh-Hans",
    );
    return order === "asc" ? comparison : -comparison;
  });
}

function workspaceSortValue(workspace: Workspace, sortBy: string) {
  if (sortBy === "createdBy") return workspace.createdByUsername || workspace.createdBy || "";
  if (sortBy === "updatedBy") return workspace.updatedByUsername || workspace.updatedBy || "";
  return String(workspace[sortBy as keyof Workspace] || "");
}

function upsertByID(items: Workspace[], value: Workspace) {
  return items.some((item) => item.id === value.id)
    ? items.map((item) => (item.id === value.id ? value : item))
    : [value, ...items];
}

function upsertMemberByID(items: WorkspaceMember[], value: WorkspaceMember) {
  return items.some((item) => item.userId === value.userId)
    ? items.map((item) => (item.userId === value.userId ? value : item))
    : [...items, value];
}
