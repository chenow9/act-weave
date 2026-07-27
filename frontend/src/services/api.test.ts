import { AxiosError, type AxiosResponse, type InternalAxiosRequestConfig } from "axios";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  APIError,
  apiClient,
  apiErrorMessage,
  authRefreshClient,
  getAuthToken,
  setAuthSessionHooks,
  setAuthToken,
  toAPIError,
  type AuthTokenResponse,
} from "./api";

const originalAPIAdapter = apiClient.defaults.adapter;
const originalRefreshAdapter = authRefreshClient.defaults.adapter;

describe("v1 API client", () => {
  beforeEach(() => {
    setAuthToken("");
    setAuthSessionHooks({});
  });

  afterEach(() => {
    apiClient.defaults.adapter = originalAPIAdapter;
    authRefreshClient.defaults.adapter = originalRefreshAdapter;
  });

  it("uses the v1 base, sends the refresh cookie, and manages the bearer token", () => {
    expect(apiClient.defaults.baseURL).toBe("/api/v1");
    expect(apiClient.defaults.withCredentials).toBe(true);
    expect(authRefreshClient.defaults.withCredentials).toBe(true);

    setAuthToken("jwt-token");
    expect(apiClient.defaults.headers.common.Authorization).toBe("Bearer jwt-token");
    expect(getAuthToken()).toBe("jwt-token");

    setAuthToken("");
    expect(apiClient.defaults.headers.common.Authorization).toBeUndefined();
    expect(getAuthToken()).toBe("");
  });

  it("coalesces concurrent 401 refreshes and retries each request once", async () => {
    let refreshCalls = 0;
    let protectedCalls = 0;
    const refreshed = authSession("fresh-access-token");
    const onRefreshed = vi.fn();
    setAuthSessionHooks({ onRefreshed });
    setAuthToken("expired-access-token");

    authRefreshClient.defaults.adapter = async (config) => {
      refreshCalls += 1;
      return axiosResponse(config, 200, refreshed);
    };
    apiClient.defaults.adapter = async (config) => {
      protectedCalls += 1;
      if (String(config.headers.Authorization) !== "Bearer fresh-access-token") {
        throw unauthorized(config);
      }
      return axiosResponse(config, 200, { ok: true });
    };

    const [first, second] = await Promise.all([apiClient.get("/workspaces"), apiClient.get("/workspaces")]);

    expect(first.data).toEqual({ ok: true });
    expect(second.data).toEqual({ ok: true });
    expect(refreshCalls).toBe(1);
    expect(protectedCalls).toBe(2);
    expect(getAuthToken()).toBe("fresh-access-token");
    expect(onRefreshed).toHaveBeenCalledWith(refreshed);
  });

  it("coalesces identical concurrent GET requests", async () => {
    let calls = 0;
    let resolvePending!: (response: AxiosResponse<{ ok: boolean }>) => void;
    const pending = new Promise<AxiosResponse<{ ok: boolean }>>((resolve) => {
      resolvePending = resolve;
    });
    apiClient.defaults.adapter = async (config) => {
      calls += 1;
      return pending.then((response) => ({ ...response, config }));
    };

    const first = apiClient.get<{ ok: boolean }>("/workspaces", { params: { limit: 500 } });
    const second = apiClient.get<{ ok: boolean }>("/workspaces", { params: { limit: 500 } });
    resolvePending(axiosResponse({ headers: {} } as InternalAxiosRequestConfig, 200, { ok: true }));

    await expect(Promise.all([first, second])).resolves.toMatchObject([{ data: { ok: true } }, { data: { ok: true } }]);
    expect(calls).toBe(1);
  });

  it("reuses a just-completed GET within the same page-load window", async () => {
    let calls = 0;
    apiClient.defaults.adapter = async (config) => {
      calls += 1;
      return axiosResponse(config, 200, { ok: true });
    };

    await apiClient.get("/workspaces/workspace-1/providers");
    await apiClient.get("/workspaces/workspace-1/providers");

    expect(calls).toBe(1);
  });

  it("clears authentication once when a coalesced refresh fails", async () => {
    let refreshCalls = 0;
    const onExpired = vi.fn();
    setAuthSessionHooks({ onExpired });
    setAuthToken("expired-access-token");
    apiClient.defaults.adapter = async (config) => {
      throw unauthorized(config);
    };
    authRefreshClient.defaults.adapter = async (config) => {
      refreshCalls += 1;
      throw unauthorized(config);
    };

    const results = await Promise.allSettled([apiClient.get("/workspaces"), apiClient.get("/workspaces")]);

    expect(results.every((result) => result.status === "rejected")).toBe(true);
    expect(refreshCalls).toBe(1);
    expect(onExpired).toHaveBeenCalledTimes(1);
    expect(getAuthToken()).toBe("");
  });

  it("normalizes the v1 error DTO and includes requestId in user-facing messages", () => {
    const config = { headers: {} } as InternalAxiosRequestConfig;
    const response = axiosResponse(config, 422, {
      error: {
        code: "VALIDATION_ERROR",
        message: "The request is not valid.",
        requestId: "request-api-test",
        traceId: "trace-api-test",
        details: [{ field: "name", reason: "required" }],
      },
    });
    const parsed = toAPIError(new AxiosError("unprocessable", "ERR_BAD_REQUEST", config, undefined, response));

    expect(parsed).toBeInstanceOf(APIError);
    expect(parsed.status).toBe(422);
    expect(parsed.code).toBe("VALIDATION_ERROR");
    expect(parsed.requestId).toBe("request-api-test");
    expect(parsed.traceId).toBe("trace-api-test");
    expect(parsed.details).toEqual([{ field: "name", reason: "required" }]);
    expect(apiErrorMessage(parsed, "保存失败。")).toBe("保存失败。（请求 ID：request-api-test）");
  });

  it("does not refresh or retry change-password on 401", async () => {
    let refreshCalls = 0;
    let changeCalls = 0;
    setAuthToken("access-token");
    authRefreshClient.defaults.adapter = async (config) => {
      refreshCalls += 1;
      return axiosResponse(config, 200, authSession("should-not-issue"));
    };
    apiClient.defaults.adapter = async (config) => {
      changeCalls += 1;
      throw unauthorized(config);
    };

    await expect(
      apiClient.post("/users/me:change-password", {
        currentPassword: "wrong-current",
        newPassword: "new-password-12",
      }),
    ).rejects.toBeInstanceOf(APIError);

    expect(refreshCalls).toBe(0);
    expect(changeCalls).toBe(1);
    expect(getAuthToken()).toBe("access-token");
  });
});

function unauthorized(config: InternalAxiosRequestConfig) {
  const response = axiosResponse(config, 401, {
    error: {
      code: "UNAUTHENTICATED",
      message: "Authentication is required.",
      requestId: "request-401",
      traceId: "trace-401",
    },
  });
  return new AxiosError("unauthenticated", "ERR_BAD_REQUEST", config, undefined, response);
}

function axiosResponse<T>(config: InternalAxiosRequestConfig, status: number, data: T): AxiosResponse<T> {
  return { data, status, statusText: String(status), headers: {}, config };
}

function authSession(accessToken: string): AuthTokenResponse {
  return {
    accessToken,
    accessTokenExpires: "2026-07-15T04:15:00Z",
    sessionId: "session-api-test",
    mustChangePassword: false,
    user: {
      id: "user-api-test",
      username: "api.test",
      displayName: "API Test",
      status: "ACTIVE",
      platformRole: "USER",
      locale: "zh-CN",
      timezone: "Asia/Singapore",
      createdAt: "2026-07-15T03:00:00Z",
      updatedAt: "2026-07-15T03:00:00Z",
      lockVersion: 1,
    },
  };
}
