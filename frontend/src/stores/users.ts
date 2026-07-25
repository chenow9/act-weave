import { defineStore } from "pinia";

import { apiClient, toAPIError, type AuthUserDTO } from "../services/api";
import { DEFAULT_PAGE_SIZE, PAGE_SIZE_OPTIONS, type ListPagination } from "../services/paginated-list";
import type { PlatformRole, User, UserListQuery, UserStatus, UserWorkspaceMembership } from "../types/domain";

export interface CreateUserInput {
  username: string;
  email?: string;
  displayName: string;
  password: string;
  platformRole: PlatformRole;
  locale: string;
  timezone: string;
}

export interface UpdateUserProfileInput {
  displayName?: string;
  email?: string;
  locale?: string;
  timezone?: string;
}

export interface UserActionError {
  status: number;
  code: string;
  message: string;
  requestId: string;
}

interface UserListResponse {
  items: AuthUserDTO[];
  pagination?: { page?: number; pageSize?: number; total?: number };
}

interface UserState {
  items: User[];
  pagination: ListPagination;
  listQuery: Required<Pick<UserListQuery, "query" | "page" | "pageSize">> &
    Pick<UserListQuery, "status" | "platformRole">;
  membershipsByUser: Record<string, UserWorkspaceMembership[]>;
  loading: boolean;
  actionLoading: boolean;
  hasLoaded: boolean;
  error: UserActionError | null;
}

export const useUserStore = defineStore("users", {
  state: (): UserState => ({
    items: [],
    pagination: { page: 1, pageSize: DEFAULT_PAGE_SIZE, total: 0, pageSizeOptions: [...PAGE_SIZE_OPTIONS] },
    listQuery: { query: "", page: 1, pageSize: DEFAULT_PAGE_SIZE },
    membershipsByUser: {},
    loading: false,
    actionLoading: false,
    hasLoaded: false,
    error: null,
  }),
  getters: {
    activeUsers: (state) => state.items.filter((user) => user.status === "ACTIVE"),
  },
  actions: {
    async loadUsers(query: UserListQuery = {}) {
      this.loading = true;
      this.error = null;
      const requestQuery = {
        ...this.listQuery,
        ...query,
        query: query.query ?? this.listQuery.query,
        page: query.page ?? this.listQuery.page,
        pageSize: query.pageSize ?? this.listQuery.pageSize,
      };
      try {
        const response = await apiClient.get<UserListResponse>(`/admin/users?${userQueryString(requestQuery)}`);
        this.items = response.data.items.map(userFromDTO);
        this.pagination = normalizedUserPagination(response.data.pagination, requestQuery, this.items.length);
        this.listQuery = {
          ...requestQuery,
          page: this.pagination.page,
          pageSize: this.pagination.pageSize,
        };
        this.hasLoaded = true;
        return this.items;
      } catch (error) {
        this.error = userActionError(error);
        throw error;
      } finally {
        this.loading = false;
      }
    },
    async searchActiveDirectory(query: string, pageSize = 50) {
      const request: UserListQuery = { query, status: "ACTIVE", page: 1, pageSize };
      const response = await apiClient.get<UserListResponse>(`/admin/users?${userQueryString(request)}`);
      return response.data.items.map(userFromDTO);
    },
    async loadUserWorkspaces(userId: string, includeDisabled = false) {
      const suffix = includeDisabled ? "?includeDisabled=true" : "";
      const response = await apiClient.get<{ items: UserWorkspaceMembership[] }>(
        `/admin/users/${userId}/workspaces${suffix}`,
      );
      this.membershipsByUser[userId] = response.data.items;
      return response.data.items;
    },
    async createUser(input: CreateUserInput) {
      return this.runUserAction(async () => {
        const response = await apiClient.post<AuthUserDTO>("/admin/users", input);
        const created = userFromDTO(response.data);
        this.upsertUser(created);
        this.pagination = { ...this.pagination, total: this.pagination.total + 1 };
        return created;
      });
    },
    async updateProfile(user: User, input: UpdateUserProfileInput) {
      return this.runUserAction(async () => {
        const response = await apiClient.patch<AuthUserDTO>(`/admin/users/${user.id}`, {
          ...input,
          lockVersion: user.lockVersion,
        });
        return this.storeUser(response.data);
      });
    },
    async setStatus(user: User, status: UserStatus) {
      return this.runUserAction(async () => {
        const response = await apiClient.patch<AuthUserDTO>(`/admin/users/${user.id}`, {
          status,
          lockVersion: user.lockVersion,
        });
        return this.storeUser(response.data);
      });
    },
    async changePlatformRole(user: User, platformRole: PlatformRole) {
      return this.runUserAction(async () => {
        const response = await apiClient.post<AuthUserDTO>(`/admin/users/${user.id}:change-platform-role`, {
          platformRole,
          lockVersion: user.lockVersion,
        });
        return this.storeUser(response.data);
      });
    },
    async unlockUser(user: User) {
      return this.runUserAction(async () => {
        const response = await apiClient.post<AuthUserDTO>(`/admin/users/${user.id}:unlock`, {
          lockVersion: user.lockVersion,
        });
        return this.storeUser(response.data);
      });
    },
    async resetPassword(userId: string, temporaryPassword: string) {
      return this.runUserAction(async () => {
        await apiClient.post(`/admin/users/${userId}:reset-password`, { temporaryPassword });
      });
    },
    async runUserAction<T>(operation: () => Promise<T>) {
      this.actionLoading = true;
      this.error = null;
      try {
        return await operation();
      } catch (error) {
        this.error = userActionError(error);
        throw error;
      } finally {
        this.actionLoading = false;
      }
    },
    storeUser(value: AuthUserDTO) {
      const user = userFromDTO(value);
      this.upsertUser(user);
      return user;
    },
    upsertUser(user: User) {
      this.items = this.items.some((item) => item.id === user.id)
        ? this.items.map((item) => (item.id === user.id ? user : item))
        : [user, ...this.items];
    },
  },
});

function userQueryString(query: UserListQuery) {
  const params = new URLSearchParams();
  if (query.query?.trim()) params.set("query", query.query.trim());
  if (query.status) params.set("status", query.status);
  if (query.platformRole) params.set("platformRole", query.platformRole);
  params.set("page", String(query.page || 1));
  params.set("pageSize", String(query.pageSize || DEFAULT_PAGE_SIZE));
  return params.toString();
}

function normalizedUserPagination(
  response: UserListResponse["pagination"],
  request: UserListQuery,
  itemCount: number,
): ListPagination {
  const requestedPage = positiveInteger(request.page, 1);
  const requestedPageSize = positiveInteger(request.pageSize, DEFAULT_PAGE_SIZE);
  return {
    page: positiveInteger(response?.page, requestedPage),
    pageSize: positiveInteger(response?.pageSize, requestedPageSize),
    total: nonNegativeInteger(response?.total, itemCount),
    pageSizeOptions: [...PAGE_SIZE_OPTIONS],
  };
}

function positiveInteger(value: number | undefined, fallback: number) {
  return Number.isInteger(value) && Number(value) > 0 ? Number(value) : fallback;
}

function nonNegativeInteger(value: number | undefined, fallback: number) {
  return Number.isInteger(value) && Number(value) >= 0 ? Number(value) : fallback;
}

function userFromDTO(value: AuthUserDTO): User {
  return {
    ...value,
    role: value.platformRole === "PLATFORM_ADMIN" ? "Platform Admin" : "User",
  };
}

function userActionError(error: unknown): UserActionError {
  const value = toAPIError(error);
  return {
    status: value.status,
    code: value.code,
    message: value.message,
    requestId: value.requestId,
  };
}
