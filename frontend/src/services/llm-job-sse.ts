/**
 * Minimal Console LLM job SSE client.
 * POST with Accept: text/event-stream, wait for completed|failed (+ heartbeats).
 */

import {
  APIError,
  authRefreshClient,
  getAuthToken,
  setAuthToken,
  toAPIError,
  type AuthTokenResponse,
} from "./api";

const apiBaseURL = import.meta.env.VITE_API_BASE_URL || "/api/v1";

export type LlmJobSseEventName = "started" | "completed" | "failed" | string;

export interface LlmJobSseFrame {
  id?: string;
  event: LlmJobSseEventName;
  data: Record<string, unknown>;
}

export interface PostLlmJobSseOptions {
  signal?: AbortSignal;
  /** Absolute or relative path under apiBaseURL when path starts with /. */
  path: string;
  body: unknown;
}

function joinURL(path: string): string {
  if (/^https?:\/\//i.test(path)) return path;
  const base = apiBaseURL.replace(/\/$/, "");
  const suffix = path.startsWith("/") ? path : `/${path}`;
  return `${base}${suffix}`;
}

function parseSseBlock(block: string): LlmJobSseFrame | undefined {
  const lines = block.split(/\r?\n/);
  let id = "";
  let event = "message";
  const dataLines: string[] = [];
  for (const line of lines) {
    if (!line || line.startsWith(":")) continue;
    if (line.startsWith("id:")) {
      id = line.slice(3).trim();
      continue;
    }
    if (line.startsWith("event:")) {
      event = line.slice(6).trim() || "message";
      continue;
    }
    if (line.startsWith("data:")) {
      dataLines.push(line.slice(5).trimStart());
    }
  }
  if (dataLines.length === 0) return undefined;
  let parsed: unknown;
  try {
    parsed = JSON.parse(dataLines.join("\n"));
  } catch {
    return undefined;
  }
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return undefined;
  return { id: id || undefined, event, data: parsed as Record<string, unknown> };
}

async function readErrorBody(response: Response): Promise<APIError> {
  let data: unknown;
  try {
    data = await response.json();
  } catch {
    data = undefined;
  }
  const body = data as { error?: { code?: string; message?: string; requestId?: string; traceId?: string; details?: unknown[] } } | undefined;
  return new APIError({
    status: response.status,
    code: body?.error?.code || `HTTP_${response.status}`,
    message: body?.error?.message || response.statusText || "The request could not be completed.",
    requestId: body?.error?.requestId,
    traceId: body?.error?.traceId,
    details: (body?.error?.details as APIError["details"]) || [],
    responseData: data,
  });
}

/**
 * POST a long LLM job over SSE. Resolves with completed event data, or rejects with APIError.
 */
export async function postLlmJobSse<T extends Record<string, unknown> = Record<string, unknown>>(
  options: PostLlmJobSseOptions,
): Promise<T> {
  const url = joinURL(options.path);
  const headers: Record<string, string> = {
    Accept: "text/event-stream",
    "Content-Type": "application/json",
  };
  const token = getAuthToken();
  if (token) headers.Authorization = `Bearer ${token}`;

  const doFetch = () =>
    fetch(url, {
      method: "POST",
      headers,
      body: JSON.stringify(options.body ?? {}),
      credentials: "include",
      signal: options.signal,
    });

  let response = await doFetch();
  if (response.status === 401) {
    try {
      const session = await authRefreshClient.post<AuthTokenResponse>("/auth/refresh");
      setAuthToken(session.data.accessToken);
      headers.Authorization = `Bearer ${session.data.accessToken}`;
      response = await doFetch();
    } catch (error) {
      throw toAPIError(error);
    }
  }

  if (!response.ok) {
    throw await readErrorBody(response);
  }

  const contentType = response.headers.get("content-type") || "";
  if (!contentType.includes("text/event-stream")) {
    // Backward-compatible JSON response (tests / old servers).
    return (await response.json()) as T;
  }
  if (!response.body) {
    throw new APIError({
      status: response.status,
      code: "NETWORK_ERROR",
      message: "SSE response body is missing.",
    });
  }

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";

  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });
    buffer = buffer.replace(/\r\n/g, "\n");

    let splitAt = buffer.indexOf("\n\n");
    while (splitAt >= 0) {
      const block = buffer.slice(0, splitAt);
      buffer = buffer.slice(splitAt + 2);
      const frame = parseSseBlock(block);
      if (frame) {
        if (frame.event === "completed") {
          return frame.data as T;
        }
        if (frame.event === "failed") {
          const err = frame.data.error as
            | { code?: string; message?: string; requestId?: string; traceId?: string; details?: unknown[] }
            | undefined;
          throw new APIError({
            status: 0,
            code: err?.code || "LLM_JOB_FAILED",
            message: err?.message || "The generation job failed.",
            requestId: err?.requestId,
            traceId: err?.traceId,
            details: (err?.details as APIError["details"]) || [],
            responseData: frame.data,
          });
        }
      }
      splitAt = buffer.indexOf("\n\n");
    }
  }

  throw new APIError({
    status: 0,
    code: "NETWORK_ERROR",
    message: "The SSE stream ended before a terminal event.",
  });
}
