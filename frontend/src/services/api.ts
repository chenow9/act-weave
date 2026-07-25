import axios, { AxiosError, type AxiosResponse, type InternalAxiosRequestConfig } from "axios";

const apiBaseURL = import.meta.env.VITE_API_BASE_URL || "/api/v1";

export interface APIErrorDetail {
  field?: string;
  reason?: string;
  [key: string]: unknown;
}

export interface APIErrorBody {
  error: {
    code: string;
    message: string;
    requestId: string;
    traceId?: string;
    details?: APIErrorDetail[];
  };
}

export interface AuthUserDTO {
  id: string;
  username: string;
  email?: string;
  displayName: string;
  avatarUrl?: string;
  status: "ACTIVE" | "LOCKED" | "DISABLED";
  platformRole: "USER" | "PLATFORM_ADMIN";
  locale: string;
  timezone: string;
  lastLoginAt?: string;
  createdAt: string;
  updatedAt: string;
  lockVersion: number;
}

export interface AuthTokenResponse {
  accessToken: string;
  accessTokenExpires: string;
  sessionId: string;
  mustChangePassword: boolean;
  user: AuthUserDTO;
}

export class APIError extends Error {
  readonly isAxiosError = true;
  readonly status: number;
  readonly code: string;
  readonly requestId: string;
  readonly traceId: string;
  readonly details: APIErrorDetail[];
  readonly response?: { status: number; data: unknown };

  constructor(input: {
    status: number;
    code: string;
    message: string;
    requestId?: string;
    traceId?: string;
    details?: APIErrorDetail[];
    responseData?: unknown;
  }) {
    super(input.message);
    this.name = "APIError";
    this.status = input.status;
    this.code = input.code;
    this.requestId = input.requestId || "";
    this.traceId = input.traceId || "";
    this.details = input.details || [];
    this.response = input.status ? { status: input.status, data: input.responseData } : undefined;
  }
}

export const apiClient = axios.create({
  baseURL: apiBaseURL,
  timeout: 12_000,
  withCredentials: true,
});

const rawAPIGet = apiClient.get.bind(apiClient);
const sharedGetWindowMs = 1_000;
interface SharedGetEntry {
  request: ReturnType<typeof rawAPIGet>;
  response?: AxiosResponse;
  expiresAt?: number;
}
const sharedGetEntries = new Map<string, SharedGetEntry>();
let sharedGetGeneration = 0;

apiClient.get = ((url: string, config?: Parameters<typeof rawAPIGet>[1]) => {
  if (config?.signal || config?.onDownloadProgress) {
    return rawAPIGet(url, config);
  }

  const key = sharedGetKey(url, config);
  const existing = sharedGetEntries.get(key);
  if (existing?.response && (existing.expiresAt || 0) > Date.now()) {
    return Promise.resolve(existing.response);
  }
  if (existing && !existing.response) return existing.request;
  if (existing) sharedGetEntries.delete(key);

  const generation = sharedGetGeneration;
  const request = rawAPIGet(url, config).then((response) => {
    if (generation === sharedGetGeneration) {
      sharedGetEntries.set(key, {
        request: Promise.resolve(response),
        response,
        expiresAt: Date.now() + sharedGetWindowMs,
      });
    }
    return response;
  }).catch((error) => {
    sharedGetEntries.delete(key);
    throw error;
  });
  sharedGetEntries.set(key, { request });
  return request;
}) as typeof apiClient.get;

// Kept separate so a failed refresh cannot recursively enter the 401 interceptor.
export const authRefreshClient = axios.create({
  baseURL: apiBaseURL,
  timeout: 12_000,
  withCredentials: true,
});

interface RetriableRequestConfig extends InternalAxiosRequestConfig {
  _actweaveAuthRetried?: boolean;
}

interface AuthSessionHooks {
  onRefreshed?: (session: AuthTokenResponse) => void;
  onExpired?: () => void;
}

let refreshInFlight: Promise<AuthTokenResponse> | null = null;
let authSessionHooks: AuthSessionHooks = {};

export function setAuthSessionHooks(hooks: AuthSessionHooks) {
  authSessionHooks = hooks;
}

export function setAuthToken(token: string) {
  clearSharedGetEntries();
  if (token) {
    apiClient.defaults.headers.common.Authorization = `Bearer ${token}`;
    return;
  }

  delete apiClient.defaults.headers.common.Authorization;
}

export function getAuthToken() {
  const authorization = apiClient.defaults.headers.common.Authorization;
  if (typeof authorization !== "string") {
    return "";
  }
  const match = authorization.match(/^Bearer\s+(.+)$/);
  return match?.[1]?.trim() || "";
}

export function toAPIError(error: unknown): APIError {
  if (error instanceof APIError) {
    return error;
  }
  if (!axios.isAxiosError(error)) {
    return new APIError({
      status: 0,
      code: "NETWORK_ERROR",
      message: error instanceof Error ? error.message : "The request could not be completed.",
    });
  }
  const response = error.response;
  const body = response?.data as Partial<APIErrorBody> | undefined;
  const payload = body?.error;
  const requestIdHeader = response?.headers?.["x-request-id"];
  return new APIError({
    status: response?.status || 0,
    code: payload?.code || (response ? `HTTP_${response.status}` : "NETWORK_ERROR"),
    message: payload?.message || error.message || "The request could not be completed.",
    requestId: payload?.requestId || (typeof requestIdHeader === "string" ? requestIdHeader : ""),
    traceId: payload?.traceId,
    details: payload?.details,
    responseData: response?.data,
  });
}

export function apiErrorMessage(error: unknown, fallback: string) {
  const value = toAPIError(error);
  return value.requestId ? `${fallback}（请求 ID：${value.requestId}）` : fallback;
}

apiClient.interceptors.response.use(
  (response) => {
    if (response.config.method?.toLocaleLowerCase() !== "get") {
      clearSharedGetEntries();
    }
    return response;
  },
  async (error: AxiosError<APIErrorBody>) => {
    const config = error.config as RetriableRequestConfig | undefined;
    if (error.response?.status !== 401 || !config || config._actweaveAuthRetried || isAuthLifecycleRequest(config.url)) {
      throw toAPIError(error);
    }

    config._actweaveAuthRetried = true;
    try {
      const session = await refreshAccessToken();
      config.headers.Authorization = `Bearer ${session.accessToken}`;
      return await apiClient.request(config);
    } catch {
      throw toAPIError(error);
    }
  },
);

async function refreshAccessToken() {
  if (!refreshInFlight) {
    refreshInFlight = authRefreshClient
      .post<AuthTokenResponse>("/auth/refresh")
      .then((response: AxiosResponse<AuthTokenResponse>) => {
        setAuthToken(response.data.accessToken);
        authSessionHooks.onRefreshed?.(response.data);
        return response.data;
      })
      .catch((error: unknown) => {
        setAuthToken("");
        authSessionHooks.onExpired?.();
        throw error;
      })
      .finally(() => {
        refreshInFlight = null;
      });
  }
  return refreshInFlight;
}

function isAuthLifecycleRequest(url?: string) {
  return Boolean(url && ["/auth/login", "/auth/refresh", "/auth/logout"].some((path) => url.endsWith(path)));
}

function sharedGetKey(url: string, config?: Parameters<typeof rawAPIGet>[1]) {
  let params = "";
  try {
    params = JSON.stringify(config?.params || {}, Object.keys(config?.params || {}).sort());
  } catch {
    params = String(config?.params || "");
  }
  return `${url}|${params}|${config?.responseType || "json"}`;
}

function clearSharedGetEntries() {
  sharedGetGeneration += 1;
  sharedGetEntries.clear();
}
