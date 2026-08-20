import { createPinia, setActivePinia } from "pinia";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { APIError, apiClient, refreshAuthSession, setAuthToken, type AuthTokenResponse } from "../services/api";
import { useAuthStore } from "./auth";

const routerReplace = vi.hoisted(() => vi.fn(async () => undefined));
const currentRoute = vi.hoisted(() => ({ value: { name: "agents" as string, fullPath: "/agents" } }));

vi.mock("../services/api", async () => {
  const actual = await vi.importActual<typeof import("../services/api")>("../services/api");
  return {
    ...actual,
    apiClient: { post: vi.fn(), get: vi.fn() },
    refreshAuthSession: vi.fn(),
    setAuthSessionHooks: vi.fn(),
    setAuthToken: vi.fn(),
  };
});

vi.mock("../router", () => ({
  router: {
    currentRoute,
    replace: (...args: unknown[]) => routerReplace(...args),
  },
}));

describe("v1 auth store", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    localStorage.clear();
    currentRoute.value = { name: "agents", fullPath: "/agents" };
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
    vi.mocked(refreshAuthSession).mockResolvedValueOnce(authSession("restored.jwt"));
    const auth = useAuthStore();

    await Promise.all([auth.restoreSession(), auth.restoreSession()]);

    expect(refreshAuthSession).toHaveBeenCalledTimes(1);
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

  it("always calls Cookie-backed logout even when the Access Token is absent", async () => {
    vi.mocked(apiClient.post).mockResolvedValueOnce({ status: 204, data: undefined });
    const auth = useAuthStore();

    await auth.logout();

    expect(apiClient.post).toHaveBeenCalledWith("/auth/logout");
    expect(auth.initialized).toBe(true);
  });

  it("keeps the authenticated session and surfaces an error when logout fails", async () => {
    vi.mocked(apiClient.post).mockResolvedValueOnce({ data: authSession("still-signed-in") });
    const auth = useAuthStore();
    await auth.login("chen.ops", "actweave-demo");
    vi.mocked(apiClient.post).mockRejectedValueOnce(
      new APIError({ status: 503, code: "INTERNAL", message: "temporarily unavailable" }),
    );

    await expect(auth.logout()).rejects.toBeInstanceOf(APIError);

    expect(auth.token).toBe("still-signed-in");
    expect(auth.user?.username).toBe("chen.ops");
    expect(auth.error).not.toBe("");
    expect(setAuthToken).not.toHaveBeenLastCalledWith("");
  });

  it("redirects an initialized expired session to login", async () => {
    const auth = useAuthStore();
    auth.initialized = true;
    auth.applySession(authSession("expired.jwt"));

    await auth.expireSession();

    expect(auth.token).toBe("");
    expect(auth.user).toBeNull();
    expect(setAuthToken).toHaveBeenLastCalledWith("");
    expect(routerReplace).toHaveBeenCalledWith({
      name: "login",
      query: { sessionExpired: "1", redirect: "/agents" },
    });
  });

  it("does not redirect when session restore fails before the router guard", async () => {
    const auth = useAuthStore();
    auth.initialized = false;
    auth.token = "stale";
    auth.user = authSession("stale").user as never;

    await auth.expireSession();

    expect(auth.token).toBe("");
    expect(routerReplace).not.toHaveBeenCalled();
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

  it("changePassword posts to colon command path and clears session on 204", async () => {
    vi.mocked(apiClient.post).mockResolvedValueOnce({ data: authSession("before.change") });
    const auth = useAuthStore();
    await auth.login("chen.ops", "actweave-demo");
    expect(auth.mustChangePassword).toBe(false);

    vi.mocked(apiClient.post).mockResolvedValueOnce({ status: 204, data: undefined });
    await auth.changePassword("current-password-1", "new-password-12");

    expect(apiClient.post).toHaveBeenLastCalledWith("/users/me:change-password", {
      currentPassword: "current-password-1",
      newPassword: "new-password-12",
    });
    expect(auth.token).toBe("");
    expect(auth.user).toBeNull();
    expect(auth.mustChangePassword).toBe(false);
    expect(setAuthToken).toHaveBeenLastCalledWith("");
    // Passwords must not be retained on the store.
    expect(JSON.stringify(auth.$state)).not.toContain("current-password-1");
    expect(JSON.stringify(auth.$state)).not.toContain("new-password-12");
  });

  it("changePassword keeps session on failure and surfaces an error", async () => {
    const session = authSession("still.valid", true);
    vi.mocked(apiClient.post).mockResolvedValueOnce({ data: session });
    const auth = useAuthStore();
    await auth.login("temp.user", "temporary-password");
    expect(auth.mustChangePassword).toBe(true);

    vi.mocked(apiClient.post).mockRejectedValueOnce(
      new APIError({
        status: 401,
        code: "UNAUTHENTICATED",
        message: "Authentication is required.",
        requestId: "request-bad-current",
      }),
    );
    await expect(auth.changePassword("wrong-current", "new-password-12")).rejects.toBeInstanceOf(APIError);
    expect(auth.token).toBe("still.valid");
    expect(auth.mustChangePassword).toBe(true);
    expect(auth.error).toContain("request-bad-current");
  });
});

function authSession(accessToken: string, mustChangePassword = false): AuthTokenResponse {
  return {
    accessToken,
    accessTokenExpires: "2026-07-15T04:15:00Z",
    sessionId: "session-auth-test",
    mustChangePassword,
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
