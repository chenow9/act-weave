import { createPinia, setActivePinia } from "pinia";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { APIError, apiClient, setAuthToken, type AuthTokenResponse } from "../services/api";
import { useAuthStore } from "./auth";

vi.mock("../services/api", async () => {
  const actual = await vi.importActual<typeof import("../services/api")>("../services/api");
  return {
    ...actual,
    apiClient: { post: vi.fn(), get: vi.fn() },
    setAuthSessionHooks: vi.fn(),
    setAuthToken: vi.fn(),
  };
});

describe("v1 auth store", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    localStorage.clear();
  });

  it("uses the v1 login response and keeps access tokens out of localStorage", async () => {
    vi.mocked(apiClient.post).mockResolvedValueOnce({ data: authSession("ey.v1.jwt") });

    const auth = useAuthStore();
    await auth.login("chen.ops", "actweave-demo");

    expect(apiClient.post).toHaveBeenCalledWith("/auth/login", {
      username: "chen.ops",
      password: "actweave-demo",
    });
    expect(auth.token).toBe("ey.v1.jwt");
    expect(auth.user?.platformRole).toBe("PLATFORM_ADMIN");
    expect(auth.user?.role).toBe("Platform Admin");
    expect(auth.isAuthenticated).toBe(true);
    expect(setAuthToken).toHaveBeenCalledWith("ey.v1.jwt");
    expect(localStorage.getItem("actweave.session")).toBeNull();
  });

  it("restores from the HttpOnly refresh cookie once for concurrent callers", async () => {
    localStorage.setItem("actweave.session", JSON.stringify({ token: "legacy-token" }));
    vi.mocked(apiClient.post).mockResolvedValueOnce({ data: authSession("restored.jwt") });
    const auth = useAuthStore();

    await Promise.all([auth.restoreSession(), auth.restoreSession()]);

    expect(apiClient.post).toHaveBeenCalledTimes(1);
    expect(apiClient.post).toHaveBeenCalledWith("/auth/refresh");
    expect(auth.token).toBe("restored.jwt");
    expect(auth.initialized).toBe(true);
    expect(localStorage.getItem("actweave.session")).toBeNull();
  });

  it("loads /users/me as a direct user DTO", async () => {
    const auth = useAuthStore();
    auth.applySession(authSession("current.jwt"));
    vi.mocked(apiClient.get).mockResolvedValueOnce({ data: authSession("unused").user });

    await auth.loadCurrentUser();

    expect(apiClient.get).toHaveBeenCalledWith("/users/me");
    expect(auth.user?.username).toBe("chen.ops");
  });

  it("revokes the refresh session and always clears local authentication", async () => {
    vi.mocked(apiClient.post).mockResolvedValueOnce({ data: authSession("login.jwt") });
    const auth = useAuthStore();
    await auth.login("chen.ops", "actweave-demo");
    vi.mocked(apiClient.post).mockResolvedValueOnce({ data: undefined });

    await auth.logout();

    expect(apiClient.post).toHaveBeenLastCalledWith("/auth/logout");
    expect(auth.token).toBe("");
    expect(auth.user).toBeNull();
    expect(setAuthToken).toHaveBeenLastCalledWith("");
  });

  it("shows the backend requestId when login fails", async () => {
    vi.mocked(apiClient.post).mockRejectedValueOnce(
      new APIError({
        status: 401,
        code: "UNAUTHENTICATED",
        message: "Authentication is required.",
        requestId: "request-login-failed",
      }),
    );
    const auth = useAuthStore();

    await expect(auth.login("chen.ops", "wrong-password")).rejects.toBeInstanceOf(APIError);
    expect(auth.error).toContain("request-login-failed");
    expect(auth.isAuthenticated).toBe(false);
  });
});

function authSession(accessToken: string): AuthTokenResponse {
  return {
    accessToken,
    accessTokenExpires: "2026-07-15T04:15:00Z",
    sessionId: "session-auth-test",
    mustChangePassword: false,
    user: {
      id: "user-chen-ops",
      username: "chen.ops",
      displayName: "Chen Ops",
      status: "ACTIVE",
      platformRole: "PLATFORM_ADMIN",
      locale: "zh-CN",
      timezone: "Asia/Singapore",
      createdAt: "2026-07-15T03:00:00Z",
      updatedAt: "2026-07-15T03:00:00Z",
      lockVersion: 1,
    },
  };
}
