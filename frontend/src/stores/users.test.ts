import { createPinia, setActivePinia } from "pinia";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { APIError, apiClient, type AuthUserDTO } from "../services/api";
import { useUserStore } from "./users";

vi.mock("../services/api", async () => {
  const actual = await vi.importActual<typeof import("../services/api")>("../services/api");
  return { ...actual, apiClient: { get: vi.fn(), patch: vi.fn(), post: vi.fn() } };
});

describe("platform user management store", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.resetAllMocks();
  });

  it("loads server-filtered paginated users and active directory results", async () => {
    vi.mocked(apiClient.get)
      .mockResolvedValueOnce({ data: { items: [userFixture()], pagination: { page: 2, pageSize: 20, total: 23 } } })
      .mockResolvedValueOnce({ data: { items: [userFixture({ id: "user-2", username: "active.two" })], pagination: { page: 1, pageSize: 50, total: 1 } } });
    const store = useUserStore();
    await store.loadUsers({ query: "admin name", status: "ACTIVE", platformRole: "PLATFORM_ADMIN", page: 2, pageSize: 20 });
    expect(apiClient.get).toHaveBeenNthCalledWith(
      1,
      "/admin/users?query=admin+name&status=ACTIVE&platformRole=PLATFORM_ADMIN&page=2&pageSize=20",
    );
    expect(store.pagination).toMatchObject({ page: 2, pageSize: 20, total: 23 });
    const directory = await store.searchActiveDirectory("active");
    expect(apiClient.get).toHaveBeenNthCalledWith(2, "/admin/users?query=active&status=ACTIVE&page=1&pageSize=50");
    expect(directory[0].username).toBe("active.two");
  });

  it("normalizes missing legacy pagination metadata instead of exposing NaN", async () => {
    vi.mocked(apiClient.get).mockResolvedValueOnce({ data: { items: [userFixture()] } });
    const store = useUserStore();

    await store.loadUsers({ page: 2, pageSize: 20 });

    expect(store.pagination).toEqual({ page: 2, pageSize: 20, total: 1, pageSizeOptions: [10, 20, 50] });
    expect(Number.isNaN(store.pagination.pageSize)).toBe(false);
  });

  it("covers create, profile, status, role, unlock, password and workspace commands", async () => {
    const created = userFixture();
    vi.mocked(apiClient.post)
      .mockResolvedValueOnce({ data: created })
      .mockResolvedValueOnce({ data: userFixture({ lockVersion: 4, platformRole: "PLATFORM_ADMIN" }) })
      .mockResolvedValueOnce({ data: userFixture({ lockVersion: 6, status: "ACTIVE" }) })
      .mockResolvedValueOnce({ data: undefined })
      .mockResolvedValueOnce({ data: undefined });
    vi.mocked(apiClient.patch)
      .mockResolvedValueOnce({ data: userFixture({ lockVersion: 2, displayName: "Updated" }) })
      .mockResolvedValueOnce({ data: userFixture({ lockVersion: 5, status: "LOCKED" }) });
    vi.mocked(apiClient.get).mockResolvedValueOnce({
      data: { items: [{ workspaceId: "workspace-1", workspaceSlug: "core", workspaceDisplayName: "Core", workspaceStatus: "ACTIVE", role: "EDITOR", joinedAt: "2026-07-15T03:00:00Z" }] },
    });
    const store = useUserStore();
    const user = await store.createUser({
      username: "managed.user", displayName: "Managed", password: "temporary-password-1",
      platformRole: "USER", locale: "zh-CN", timezone: "Asia/Singapore",
    });
    const profiled = await store.updateProfile(user, { displayName: "Updated" });
    await store.changePlatformRole({ ...profiled, lockVersion: 3 }, "PLATFORM_ADMIN");
    const locked = await store.setStatus({ ...profiled, lockVersion: 4 }, "LOCKED");
    await store.unlockUser(locked);
    await store.resetPassword(user.id, "replacement-password-2");
    await store.loadUserWorkspaces(user.id);

    expect(apiClient.post).toHaveBeenCalledWith("/admin/users/user-1:change-platform-role", { platformRole: "PLATFORM_ADMIN", lockVersion: 3 });
    expect(apiClient.patch).toHaveBeenCalledWith("/admin/users/user-1", { status: "LOCKED", lockVersion: 4 });
    expect(apiClient.post).toHaveBeenCalledWith("/admin/users/user-1:reset-password", { temporaryPassword: "replacement-password-2" });
    expect(store.membershipsByUser[user.id][0].role).toBe("EDITOR");
  });

  it("preserves request ids for conflict feedback", async () => {
    vi.mocked(apiClient.post).mockRejectedValueOnce(new APIError({
      status: 409, code: "CONFLICT", message: "conflict", requestId: "request-conflict-1",
    }));
    const store = useUserStore();
    await expect(store.changePlatformRole(userFixture(), "USER")).rejects.toBeInstanceOf(APIError);
    expect(store.error).toEqual({ status: 409, code: "CONFLICT", message: "conflict", requestId: "request-conflict-1" });
  });

  it("preserves forbidden responses from platform-admin endpoints", async () => {
    vi.mocked(apiClient.get).mockRejectedValueOnce(new APIError({
      status: 403, code: "FORBIDDEN", message: "forbidden", requestId: "request-forbidden-1",
    }));
    const store = useUserStore();

    await expect(store.loadUsers()).rejects.toBeInstanceOf(APIError);
    expect(store.error).toEqual({
      status: 403, code: "FORBIDDEN", message: "forbidden", requestId: "request-forbidden-1",
    });
  });
});

function userFixture(overrides: Partial<AuthUserDTO> = {}): AuthUserDTO {
  return {
    id: "user-1",
    username: "platform.admin",
    displayName: "Platform Admin",
    status: "ACTIVE",
    platformRole: "USER",
    locale: "zh-CN",
    timezone: "Asia/Singapore",
    createdAt: "2026-07-15T03:00:00Z",
    updatedAt: "2026-07-15T03:00:00Z",
    lockVersion: 1,
    ...overrides,
  };
}
