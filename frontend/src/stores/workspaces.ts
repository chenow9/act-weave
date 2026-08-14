import { defineStore } from "pinia";

import { apiClient, toAPIError } from "../services/api";
import { DEFAULT_PAGE_SIZE, PAGE_SIZE_OPTIONS, type ListPagination } from "../services/paginated-list";
import type {
  Workspace,
  WorkspaceAccessibleSummary,
  WorkspaceListQuery,
  WorkspaceMember,
  WorkspaceRole,
} from "../types/domain";
import { useWorkspaceMembersStore, type MembersLoadStatus } from "./workspaceMembers";

/** Mirrors backend/internal/authz/workspace_policy.go Action set. */
export type WorkspaceAction = "VIEW" | "EDIT" | "TEST" | "PUBLISH" | "EXECUTE" | "MANAGE" | "DELETE";

/** Role → allowed actions, identical to backend workspaceRoleActions. */
export const WORKSPACE_ROLE_ACTIONS: Record<WorkspaceRole, readonly WorkspaceAction[]> = {
  OWNER: ["VIEW", "EDIT", "TEST", "PUBLISH", "EXECUTE", "MANAGE", "DELETE"],
  ADMIN: ["VIEW", "EDIT", "TEST", "PUBLISH", "EXECUTE", "MANAGE"],
  EDITOR: ["VIEW", "EDIT", "TEST", "PUBLISH", "EXECUTE"],
  OPERATOR: ["VIEW", "TEST", "EXECUTE"],
  VIEWER: ["VIEW"],
};

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
  currentUserRole?: WorkspaceRole;
}

interface WorkspaceListResponse {
  items: WorkspaceDTO[];
  pagination: { page: number; pageSize: number; total: number };
  summary: { total: number; active: number; production: number; boundAgents: number };
}

export type { MembersLoadStatus };

interface WorkspaceState {
  /** Switcher / bootstrap page (bounded server page, not a full catalog). */
  items: Workspace[];
  /** Management list page results. */
  pageItems: Workspace[];
  pagination: ListPagination;
  listQuery: Required<Pick<WorkspaceListQuery, "query" | "page" | "pageSize">> &
    Pick<WorkspaceListQuery, "status" | "mode" | "sortBy" | "sortOrder">;
  activeWorkspaceId: string;
  /** Full-set summary for management cards (D9-A). */
  summary: WorkspaceAccessibleSummary;
  loading: boolean;
  pageLoading: boolean;
  pageError: string | null;
  pageHasLoaded: boolean;
}

export const useWorkspaceStore = defineStore("workspaces", {
  state: (): WorkspaceState => ({
    items: [],
    pageItems: [],
    pagination: { page: 1, pageSize: DEFAULT_PAGE_SIZE, total: 0, pageSizeOptions: [...PAGE_SIZE_OPTIONS] },
    listQuery: { query: "", page: 1, pageSize: DEFAULT_PAGE_SIZE, sortBy: undefined, sortOrder: undefined },
    activeWorkspaceId: readActiveWorkspaceID(),
    summary: { total: 0, active: 0, production: 0, boundAgents: 0 },
    loading: false,
    pageLoading: false,
    pageError: null,
    pageHasLoaded: false,
  }),
  getters: {
    activeWorkspace: (state) =>
      state.items.find((item) => item.id === state.activeWorkspaceId) ||
      state.pageItems.find((item) => item.id === state.activeWorkspaceId) ||
      state.items[0] ||
      null,
    /** Compatibility projection onto the members store (member UI only). */
    membersByWorkspace(): Record<string, WorkspaceMember[]> {
      return useWorkspaceMembersStore().membersByWorkspace;
    },
    membersLoadStatusByWorkspace(): Record<string, MembersLoadStatus> {
      return useWorkspaceMembersStore().membersLoadStatusByWorkspace;
    },
  },
  actions: {
    /**
     * Bootstrap / switcher load: first server page only (no limit=500).
     * Active id restored via detail when not on the first page.
     */
    async load() {
      this.loading = true;
      try {
        const page = await this.fetchWorkspacePage({ page: 1, pageSize: 50 });
        this.items = page.items;
        this.summary = page.summary;
        await this.ensureActiveWorkspace();
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
      const requestQuery: WorkspaceListQuery = {
        ...this.listQuery,
        ...query,
        query: query.query ?? this.listQuery.query,
        page: query.page ?? this.listQuery.page,
        pageSize: query.pageSize ?? this.listQuery.pageSize,
        sortBy,
        sortOrder,
      };
      try {
        const page = await this.fetchWorkspacePage(requestQuery);
        this.pageItems = page.items;
        this.pagination = {
          page: page.pagination.page,
          pageSize: page.pagination.pageSize,
          total: page.pagination.total,
          pageSizeOptions: [...PAGE_SIZE_OPTIONS],
        };
        this.summary = page.summary;
        this.listQuery = {
          query: requestQuery.query || "",
          status: requestQuery.status,
          mode: requestQuery.mode,
          page: page.pagination.page,
          pageSize: page.pagination.pageSize,
          sortBy,
          sortOrder,
        };
        this.pageHasLoaded = true;
        // Keep switcher cache in sync for loaded rows.
        for (const item of page.items) {
          this.upsertInList("items", item);
        }
        return this.pageItems;
      } catch (error) {
        this.pageError = toAPIError(error).message;
        return this.pageItems;
      } finally {
        this.pageLoading = false;
      }
    },
    async fetchWorkspacePage(query: WorkspaceListQuery = {}) {
      const params = buildWorkspaceListParams(query);
      const response = await apiClient.get<WorkspaceListResponse>("/workspaces", { params });
      const items = (response.data.items || []).map(workspaceFromDTO);
      const pagination = response.data.pagination || {
        page: query.page || 1,
        pageSize: query.pageSize || DEFAULT_PAGE_SIZE,
        total: items.length,
      };
      const summary = response.data.summary || {
        total: pagination.total,
        active: 0,
        production: 0,
        boundAgents: 0,
      };
      return {
        items,
        pagination: {
          page: pagination.page,
          pageSize: pagination.pageSize,
          total: pagination.total,
        },
        summary: {
          total: summary.total,
          active: summary.active,
          production: summary.production,
          boundAgents: summary.boundAgents,
        },
      };
    },
    async fetchWorkspaceDetail(workspaceId: string) {
      const response = await apiClient.get<WorkspaceDTO>(`/workspaces/${workspaceId}`);
      return workspaceFromDTO(response.data);
    },
    async ensureActiveWorkspace() {
      const activeId = this.activeWorkspaceId;
      if (!activeId) {
        this.selectWorkspace(this.items[0]?.id || "");
        return;
      }
      const known =
        this.items.find((item) => item.id === activeId) || this.pageItems.find((item) => item.id === activeId);
      if (known) {
        writeActiveWorkspaceID(activeId);
        return;
      }
      try {
        const detail = await this.fetchWorkspaceDetail(activeId);
        this.upsertInList("items", detail);
        writeActiveWorkspaceID(activeId);
      } catch (error) {
        const status = toAPIError(error).status;
        if (status === 403 || status === 404) {
          this.selectWorkspace(this.items[0]?.id || "");
          return;
        }
        throw error;
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
      await this.loadWorkspacePage({ page: 1 });
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
      await apiClient.delete(`/workspaces/${workspaceId}`, {
        params: { lockVersion: current.lockVersion },
      });
      this.items = this.items.filter((item) => item.id !== workspaceId);
      this.pageItems = this.pageItems.filter((item) => item.id !== workspaceId);
      if (this.activeWorkspaceId === workspaceId) {
        this.selectWorkspace(this.items[0]?.id || "");
      }
      // Reload current management page; if empty, step back one page.
      const page = this.listQuery.page;
      await this.loadWorkspacePage({ page });
      if (this.pageItems.length === 0 && page > 1) {
        await this.loadWorkspacePage({ page: page - 1 });
      }
    },
    async loadMembers(workspaceId: string, includeDisabled = false) {
      return useWorkspaceMembersStore().loadMembers(workspaceId, includeDisabled);
    },
    async searchMemberCandidates(workspaceId: string, query = "", limit = 20) {
      return useWorkspaceMembersStore().searchMemberCandidates(workspaceId, query, limit);
    },
    async addMember(workspaceId: string, userId: string, role: WorkspaceRole) {
      return useWorkspaceMembersStore().addMember(workspaceId, userId, role);
    },
    async changeMemberRole(workspaceId: string, userId: string, role: WorkspaceRole) {
      return useWorkspaceMembersStore().changeMemberRole(workspaceId, userId, role);
    },
    async removeMember(workspaceId: string, userId: string) {
      return useWorkspaceMembersStore().removeMember(workspaceId, userId);
    },
    roleFor(workspaceId: string, _userId?: string): WorkspaceRole | "" {
      const workspace =
        this.items.find((item) => item.id === workspaceId) || this.pageItems.find((item) => item.id === workspaceId);
      return workspace?.currentUserRole || "";
    },
    /**
     * Authorization helper aligned with backend CanWorkspace.
     * Prefers DTO currentUserRole; members list is not used for permission.
     */
    can(workspaceId: string, userIdOrAction: string, maybeAction?: WorkspaceAction) {
      // Support both can(id, action) and legacy can(id, userId, action).
      const action = (maybeAction || userIdOrAction) as WorkspaceAction;
      const role = this.roleFor(workspaceId);
      if (!role) {
        return false;
      }
      const allowed = WORKSPACE_ROLE_ACTIONS[role];
      return Boolean(allowed?.includes(action));
    },
    selectWorkspace(workspaceId: string) {
      if (workspaceId && this.items.length && !this.items.some((workspace) => workspace.id === workspaceId)) {
        // Allow selecting an id not on the current switcher page when detail recovered it into pageItems.
        if (!this.pageItems.some((workspace) => workspace.id === workspaceId)) {
          return;
        }
      }
      this.activeWorkspaceId = workspaceId;
      writeActiveWorkspaceID(workspaceId);
    },
    requireWorkspace(workspaceId: string) {
      const workspace =
        this.items.find((item) => item.id === workspaceId) || this.pageItems.find((item) => item.id === workspaceId);
      if (!workspace) {
        throw new Error(`Workspace ${workspaceId} is not loaded.`);
      }
      return workspace;
    },
    upsertWorkspace(workspace: Workspace) {
      this.upsertInList("items", workspace);
      if (this.pageItems.some((item) => item.id === workspace.id)) {
        this.upsertInList("pageItems", workspace);
      }
    },
    upsertInList(list: "items" | "pageItems", workspace: Workspace) {
      this[list] = upsertByID(this[list], workspace);
    },
    upsertMember(workspaceId: string, member: WorkspaceMember) {
      useWorkspaceMembersStore().upsertMember(workspaceId, member);
    },
  },
});

function buildWorkspaceListParams(query: WorkspaceListQuery) {
  const params: Record<string, string | number> = {
    page: query.page || 1,
    pageSize: query.pageSize || DEFAULT_PAGE_SIZE,
  };
  if (query.query?.trim()) params.query = query.query.trim();
  if (query.status) params.status = query.status;
  if (query.mode) params.mode = query.mode;
  if (query.sortBy) params.sortBy = query.sortBy;
  if (query.sortOrder) params.sortOrder = query.sortOrder;
  return params;
}

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
    mode: value.mode,
    status: value.status,
    ownerUserId: value.ownerUserId,
    defaultAgentId: value.defaultAgentId || "",
    defaultModelConfigId: value.defaultModelConfigId,
    modelConfigId: value.defaultModelConfigId || "",
    settings: value.settings,
    createdBy: value.createdBy,
    createdByUsername: value.createdByUsername,
    updatedBy: value.updatedBy,
    updatedByUsername: value.updatedByUsername,
    createdAt: value.createdAt,
    updatedAt: value.updatedAt,
    lockVersion: value.lockVersion,
    owner: value.ownerUserId,
    healthScore: 0,
    currentUserRole: value.currentUserRole,
  };
}

function modeToDTO(mode: string) {
  return mode === "SANDBOX" || mode === "Sandbox" ? "SANDBOX" : "PRODUCTION";
}

function upsertByID(items: Workspace[], workspace: Workspace) {
  const index = items.findIndex((item) => item.id === workspace.id);
  if (index < 0) return [workspace, ...items];
  const next = items.slice();
  next[index] = workspace;
  return next;
}
