/**
 * Agent Access Protocol TypeScript API Client.
 * Owns short Token injection, JSON CRUD, SSE follow with auto-reconnect,
 * and TOKEN_EXPIRED / 401 refresh + Last-Event-ID resume.
 * Reuses M9-T4 SSE parser/session; never places tokens in the URL.
 */

import { AgentClientError, errorFromProtocol } from "./errors.js";
import type {
  AAPFile,
  AgentProfile,
  CompleteFileRequest,
  CompleteFileResponse,
  Conversation,
  CreateConversationResponse,
  CreateFileRequest,
  CreateFileResponse,
  CreateRunRequest,
  FileContentResult,
  FileUpload,
  GetFileResponse,
  InteractionDecision,
  InteractionDecisionResponse,
  MintFileDownloadResponse,
  ProtocolErrorValue,
  ReducedRunSnapshot,
  RunResponse,
} from "./models.js";
import {
  isReadyFileStatus,
  isTerminalFileStatus,
  isTerminalRunStatus,
  SDK_PREFER_DOWNLOAD_TOKEN_BYTES,
} from "./models.js";
import { RunReducer } from "./reducer.js";
import { assertNoAccessTokenInURL } from "./sse-parser.js";
import { openAAPSEStream } from "./sse-reader.js";
import { AAPSESession } from "./sse-session.js";
import type { TokenProvider } from "./token-provider.js";
import type { AAPSEMessage, ProtocolEventEnvelope } from "./types.js";

export type FetchLike = (input: string | URL, init?: RequestInit) => Promise<Response>;

export interface AgentAccessClientOptions {
  /**
   * Base URL of the AAP data plane, e.g. `https://host/api/agent-access/v1`.
   * Must not contain access_token / token query parameters.
   */
  baseUrl: string;
  tokenProvider: TokenProvider;
  fetch?: FetchLike;
  /** Max reconnect attempts for a single stream session (default 8). */
  maxReconnectAttempts?: number;
  /** Base backoff between reconnects in ms (default 250). */
  reconnectBackoffMs?: number;
}

export interface StreamRunEventsOptions {
  signal?: AbortSignal | undefined;
  /** Existing session to keep eventId de-duplication across reconnects. */
  session?: AAPSESession | undefined;
  /** Seed last applied sequence before first event (for mid-run attach). */
  initialLastSequence?: number | undefined;
  /**
   * When true (default), TOKEN_EXPIRED / HTTP 401 force-refresh the token and
   * resume from the current Last-Event-ID cursor.
   */
  refreshOnAuthFailure?: boolean | undefined;
  /**
   * When true (default), sequence_gap and retryable disconnects reconnect with
   * Last-Event-ID.
   */
  autoReconnect?: boolean | undefined;
}

export interface FollowRunOptions extends StreamRunEventsOptions {
  /** Optional pre-seeded reducer (e.g. after replaying a snapshot prefix). */
  reducer?: RunReducer;
}

export interface FollowRunEvent {
  message: AAPSEMessage;
  snapshot: ReducedRunSnapshot;
  session: AAPSESession;
}

export interface WaitUntilReadyOptions {
  signal?: AbortSignal | undefined;
  /** Poll interval for GET file status (default 500ms). */
  pollIntervalMs?: number | undefined;
  /** Overall timeout (default 120_000ms). */
  timeoutMs?: number | undefined;
}

export interface GetFileContentOptions {
  signal?: AbortSignal | undefined;
  /**
   * Known size in bytes. When omitted, getFile is called first so the SDK can
   * choose Bearer content vs :download (prefer download when sizeBytes > 4MiB).
   */
  sizeBytes?: number | undefined;
  /**
   * Force path: "content" (Bearer GET .../content) or "download" (mint token).
   * Default auto-selects download when sizeBytes > 4MiB.
   */
  prefer?: "auto" | "content" | "download" | undefined;
}

export class AgentAccessClient {
  private readonly baseUrl: string;
  private readonly tokens: TokenProvider;
  private readonly fetchImpl: FetchLike;
  private readonly maxReconnectAttempts: number;
  private readonly reconnectBackoffMs: number;

  constructor(options: AgentAccessClientOptions) {
    assertNoAccessTokenInURL(options.baseUrl);
    this.baseUrl = stripTrailingSlash(options.baseUrl);
    this.tokens = options.tokenProvider;
    this.fetchImpl = options.fetch ?? globalThis.fetch.bind(globalThis);
    this.maxReconnectAttempts = options.maxReconnectAttempts ?? 8;
    this.reconnectBackoffMs = options.reconnectBackoffMs ?? 250;
  }

  // --- Profile / Conversation / Run CRUD ---------------------------------

  async getAgentProfile(workspaceId: string, agentId: string, signal?: AbortSignal): Promise<AgentProfile> {
    return this.jsonRequest<AgentProfile>(
      "GET",
      `/workspaces/${enc(workspaceId)}/agents/${enc(agentId)}/profile`,
      { signal },
    );
  }

  async createConversation(
    workspaceId: string,
    agentId: string,
    body: { title: string },
    options: { idempotencyKey: string; signal?: AbortSignal },
  ): Promise<CreateConversationResponse> {
    return this.jsonRequest<CreateConversationResponse>(
      "POST",
      `/workspaces/${enc(workspaceId)}/agents/${enc(agentId)}/conversations`,
      {
        body,
        idempotencyKey: options.idempotencyKey,
        signal: options.signal,
        expectedStatuses: [201, 200],
      },
    );
  }

  async getConversation(
    workspaceId: string,
    agentId: string,
    conversationId: string,
    options: { ifNoneMatch?: string; signal?: AbortSignal } = {},
  ): Promise<Conversation | null> {
    const headers: Record<string, string> = {};
    if (options.ifNoneMatch) {
      headers["If-None-Match"] = options.ifNoneMatch;
    }
    const result = await this.rawRequest(
      "GET",
      `/workspaces/${enc(workspaceId)}/agents/${enc(agentId)}/conversations/${enc(conversationId)}`,
      { headers, signal: options.signal, allowStatuses: [200, 304] },
    );
    if (result.status === 304) {
      return null;
    }
    return (await result.json()) as Conversation;
  }

  async createRun(
    workspaceId: string,
    agentId: string,
    body: CreateRunRequest,
    options: { idempotencyKey: string; signal?: AbortSignal },
  ): Promise<RunResponse> {
    return this.jsonRequest<RunResponse>(
      "POST",
      `/workspaces/${enc(workspaceId)}/agents/${enc(agentId)}/runs`,
      {
        body,
        idempotencyKey: options.idempotencyKey,
        signal: options.signal,
        expectedStatuses: [202, 200],
        headers: { Accept: "application/json" },
      },
    );
  }

  async getRun(
    workspaceId: string,
    agentId: string,
    runId: string,
    options: { ifNoneMatch?: string; signal?: AbortSignal } = {},
  ): Promise<RunResponse | null> {
    const headers: Record<string, string> = { Accept: "application/json" };
    if (options.ifNoneMatch) {
      headers["If-None-Match"] = options.ifNoneMatch;
    }
    const result = await this.rawRequest(
      "GET",
      `/workspaces/${enc(workspaceId)}/agents/${enc(agentId)}/runs/${enc(runId)}`,
      { headers, signal: options.signal, allowStatuses: [200, 304] },
    );
    if (result.status === 304) {
      return null;
    }
    return (await result.json()) as RunResponse;
  }

  async cancelRun(
    workspaceId: string,
    agentId: string,
    runId: string,
    options: { idempotencyKey: string; ifMatch: string; signal?: AbortSignal },
  ): Promise<RunResponse> {
    return this.jsonRequest<RunResponse>(
      "POST",
      `/workspaces/${enc(workspaceId)}/agents/${enc(agentId)}/runs/${enc(runId)}:cancel`,
      {
        idempotencyKey: options.idempotencyKey,
        signal: options.signal,
        headers: { "If-Match": options.ifMatch, Accept: "application/json" },
        expectedStatuses: [200],
      },
    );
  }

  async decideInteraction(
    workspaceId: string,
    agentId: string,
    runId: string,
    interactionId: string,
    body: { decision: InteractionDecision },
    options: { idempotencyKey: string; ifMatch: string; signal?: AbortSignal },
  ): Promise<InteractionDecisionResponse> {
    return this.jsonRequest<InteractionDecisionResponse>(
      "POST",
      `/workspaces/${enc(workspaceId)}/agents/${enc(agentId)}/runs/${enc(runId)}/interactions/${enc(interactionId)}:decide`,
      {
        body,
        idempotencyKey: options.idempotencyKey,
        signal: options.signal,
        headers: { "If-Match": options.ifMatch, Accept: "application/json" },
        expectedStatuses: [200],
      },
    );
  }

  // --- Files (presigned PUT → complete → waitUntilReady → createRun) -----

  /**
   * Create a file upload intent. Response may include a write-only `upload`
   * fragment (presigned PUT + required headers). Subsequent GET never echoes it.
   * Requires `file:write`. Gated by agentAccess.files (default off).
   */
  async createFile(
    workspaceId: string,
    agentId: string,
    body: CreateFileRequest,
    options: { idempotencyKey: string; signal?: AbortSignal },
  ): Promise<CreateFileResponse> {
    return this.jsonRequest<CreateFileResponse>(
      "POST",
      `/workspaces/${enc(workspaceId)}/agents/${enc(agentId)}/files`,
      {
        body,
        idempotencyKey: options.idempotencyKey,
        signal: options.signal,
        expectedStatuses: [201, 200],
      },
    );
  }

  /**
   * PUT bytes to the presigned upload URL from createFile.
   * **Must** send the exact headers returned by create (Content-Length / Content-Type
   * are bound into the signature). Does not attach the AAP Bearer token.
   */
  async putFileUpload(
    upload: FileUpload,
    body: ArrayBuffer | Uint8Array | Blob,
    options: { signal?: AbortSignal } = {},
  ): Promise<void> {
    assertNoAccessTokenInURL(upload.url);

    const headers = new Headers();
    for (const [key, value] of Object.entries(upload.headers ?? {})) {
      if (value !== undefined && value !== null && String(value).length > 0) {
        headers.set(key, String(value));
      }
    }
    // Signature requires these; ensure they are present even if map used odd casing.
    if (!hasHeaderIgnoreCase(headers, "Content-Type") || !hasHeaderIgnoreCase(headers, "Content-Length")) {
      throw new AgentClientError(
        "File upload headers must include Content-Type and Content-Length from createFile",
        { code: "INVALID_UPLOAD_HEADERS", retryable: false },
      );
    }

    const init: RequestInit = {
      method: upload.method || "PUT",
      headers,
      body: toRequestBody(body),
    };
    if (options.signal) {
      init.signal = options.signal;
    }

    let response: Response;
    try {
      response = await this.fetchImpl(upload.url, init);
    } catch (cause) {
      if (options.signal?.aborted || (cause instanceof DOMException && cause.name === "AbortError")) {
        throw abortError();
      }
      throw new AgentClientError("File upload PUT failed", {
        code: "NETWORK_ERROR",
        retryable: true,
        cause,
      });
    }

    if (!response.ok) {
      // Presigned storage errors are not AAP error envelopes.
      let details: unknown;
      try {
        details = await response.text();
      } catch {
        details = undefined;
      }
      throw new AgentClientError(`File upload PUT HTTP ${response.status}`, {
        code: "UPLOAD_HTTP_ERROR",
        status: response.status,
        retryable: response.status === 429 || response.status >= 500,
        details,
      });
    }
  }

  /**
   * Confirm staging upload (fast path). Enqueues promote; does not wait for READY.
   * Requires `file:write`.
   */
  async completeFile(
    workspaceId: string,
    agentId: string,
    fileId: string,
    body: CompleteFileRequest | undefined,
    options: { idempotencyKey: string; signal?: AbortSignal },
  ): Promise<CompleteFileResponse> {
    return this.jsonRequest<CompleteFileResponse>(
      "POST",
      `/workspaces/${enc(workspaceId)}/agents/${enc(agentId)}/files/${enc(fileId)}:complete`,
      {
        body: body ?? {},
        idempotencyKey: options.idempotencyKey,
        signal: options.signal,
        expectedStatuses: [200],
      },
    );
  }

  /**
   * Get file status (source of truth; no File SSE in v1). Requires `file:read`.
   * Never returns upload URLs, presign headers, or live download URLs.
   */
  async getFile(
    workspaceId: string,
    agentId: string,
    fileId: string,
    options: { signal?: AbortSignal } = {},
  ): Promise<AAPFile> {
    const response = await this.jsonRequest<GetFileResponse>(
      "GET",
      `/workspaces/${enc(workspaceId)}/agents/${enc(agentId)}/files/${enc(fileId)}`,
      { signal: options.signal, expectedStatuses: [200] },
    );
    return response.file;
  }

  /**
   * Poll GET until status is ready, or throw on failed/expired/timeout.
   * v1 has no File SSE — this is the supported wait helper.
   */
  async waitUntilReady(
    workspaceId: string,
    agentId: string,
    fileId: string,
    options: WaitUntilReadyOptions = {},
  ): Promise<AAPFile> {
    const pollIntervalMs = options.pollIntervalMs ?? 500;
    const timeoutMs = options.timeoutMs ?? 120_000;
    const started = Date.now();

    while (true) {
      if (options.signal?.aborted) {
        throw abortError();
      }
      const file = await this.getFile(
        workspaceId,
        agentId,
        fileId,
        options.signal ? { signal: options.signal } : {},
      );
      if (isReadyFileStatus(file.status)) {
        return file;
      }
      if (isTerminalFileStatus(file.status)) {
        const code =
          file.status === "expired"
            ? "FILE_UPLOAD_EXPIRED"
            : file.error?.code ?? "FILE_PROCESSING_FAILED";
        throw new AgentClientError(
          file.error?.message ?? `File reached terminal status ${file.status}`,
          {
            code,
            status: 422,
            retryable: false,
            details: { file },
          },
        );
      }
      if (Date.now() - started >= timeoutMs) {
        throw new AgentClientError("waitUntilReady timed out before file became ready", {
          code: "FILE_WAIT_TIMEOUT",
          retryable: true,
          details: { fileId, status: file.status, timeoutMs },
        });
      }
      await sleep(pollIntervalMs, options.signal);
    }
  }

  /**
   * Mint an opaque download token (path B). Relative `url` is not a MinIO credential.
   * Requires `file:read` and READY file.
   */
  async mintFileDownload(
    workspaceId: string,
    agentId: string,
    fileId: string,
    options: { signal?: AbortSignal } = {},
  ): Promise<MintFileDownloadResponse> {
    const req: {
      signal?: AbortSignal;
      expectedStatuses: number[];
    } = { expectedStatuses: [200] };
    if (options.signal) {
      req.signal = options.signal;
    }
    return this.jsonRequest<MintFileDownloadResponse>(
      "POST",
      `/workspaces/${enc(workspaceId)}/agents/${enc(agentId)}/files/${enc(fileId)}:download`,
      req,
    );
  }

  /**
   * Download file bytes. Small files use Bearer GET .../content (path A).
   * When sizeBytes > 4MiB (or prefer:"download"), mints :download and GETs the
   * opaque token proxy (path B, no Bearer on the token GET).
   */
  async getFileContent(
    workspaceId: string,
    agentId: string,
    fileId: string,
    options: GetFileContentOptions = {},
  ): Promise<FileContentResult> {
    let sizeBytes = options.sizeBytes;
    if (sizeBytes === undefined && options.prefer !== "content" && options.prefer !== "download") {
      const meta = await this.getFile(
        workspaceId,
        agentId,
        fileId,
        options.signal ? { signal: options.signal } : {},
      );
      sizeBytes = meta.sizeBytes;
    }

    const prefer =
      options.prefer === "content" || options.prefer === "download"
        ? options.prefer
        : sizeBytes !== undefined && sizeBytes > SDK_PREFER_DOWNLOAD_TOKEN_BYTES
          ? "download"
          : "content";

    if (prefer === "download") {
      const minted = await this.mintFileDownload(
        workspaceId,
        agentId,
        fileId,
        options.signal ? { signal: options.signal } : {},
      );
      const absolute = resolveAgainstBase(this.baseUrl, minted.url);
      assertNoAccessTokenInURL(absolute);
      const downloadOpts: {
        method: string;
        headers: Record<string, string>;
        signal?: AbortSignal;
        withAuth: boolean;
        allowStatuses: number[];
      } = {
        method: "GET",
        // Token proxy is the credential — do not send AAP Bearer.
        headers: { Accept: "*/*" },
        withAuth: false,
        allowStatuses: [200],
      };
      if (options.signal) {
        downloadOpts.signal = options.signal;
      }
      const response = await this.fetchBinary(absolute, downloadOpts);
      return {
        body: await response.arrayBuffer(),
        contentType: response.headers.get("content-type") ?? "application/octet-stream",
        via: "download",
      };
    }

    const path = `/workspaces/${enc(workspaceId)}/agents/${enc(agentId)}/files/${enc(fileId)}/content`;
    const contentOpts: {
      method: string;
      headers: Record<string, string>;
      signal?: AbortSignal;
      withAuth: boolean;
      allowStatuses: number[];
      retryOn401: boolean;
    } = {
      method: "GET",
      headers: { Accept: "*/*" },
      withAuth: true,
      allowStatuses: [200],
      retryOn401: true,
    };
    if (options.signal) {
      contentOpts.signal = options.signal;
    }
    const response = await this.fetchBinary(this.baseUrl + path, contentOpts);
    return {
      body: await response.arrayBuffer(),
      contentType: response.headers.get("content-type") ?? "application/octet-stream",
      via: "content",
    };
  }

  // --- SSE Events / Follow -----------------------------------------------

  /**
   * Stream Run events with automatic Last-Event-ID resume and Token refresh.
   * Yields AAPSEMessage values from the M9-T4 session layer.
   */
  async *streamRunEvents(
    workspaceId: string,
    agentId: string,
    runId: string,
    options: StreamRunEventsOptions = {},
  ): AsyncGenerator<AAPSEMessage, void, unknown> {
    const autoReconnect = options.autoReconnect !== false;
    const refreshOnAuthFailure = options.refreshOnAuthFailure !== false;
    const path = `/workspaces/${enc(workspaceId)}/agents/${enc(agentId)}/runs/${enc(runId)}/events`;

    const sessionOptions =
      options.initialLastSequence !== undefined
        ? { initialLastSequence: options.initialLastSequence }
        : {};
    const session = options.session ?? new AAPSESession(sessionOptions);

    let attempts = 0;
    let forceRefresh = false;

    while (true) {
      if (options.signal?.aborted) {
        throw abortError();
      }

      let response: Response;
      try {
        response = await this.openEventStream(path, session, {
          signal: options.signal,
          forceRefresh,
        });
        forceRefresh = false;
        attempts = 0;
      } catch (error) {
        if (error instanceof AgentClientError && error.code === "STREAM_ABORTED") {
          throw error;
        }
        if (
          refreshOnAuthFailure &&
          error instanceof AgentClientError &&
          (error.status === 401 || error.code === "TOKEN_EXPIRED" || error.code === "UNAUTHENTICATED")
        ) {
          forceRefresh = true;
          attempts += 1;
          if (attempts > this.maxReconnectAttempts) {
            throw error;
          }
          await sleep(this.backoff(attempts), options.signal);
          continue;
        }
        if (autoReconnect && isRetryableNetwork(error) && attempts < this.maxReconnectAttempts) {
          attempts += 1;
          await sleep(this.backoff(attempts), options.signal);
          continue;
        }
        throw error;
      }

      const opened = openAAPSEStream(response.body, {
        session,
        ...(options.signal ? { signal: options.signal } : {}),
      });

      let needReconnect = false;
      let reconnectForceRefresh = false;

      try {
        for await (const message of opened.messages) {
          yield message;

          if (message.kind === "sequence_gap") {
            needReconnect = autoReconnect;
            break;
          }

          if (message.kind === "transport_signal") {
            const code = message.signal.error.code;
            if (code === "TOKEN_EXPIRED" || code === "UNAUTHENTICATED") {
              if (refreshOnAuthFailure) {
                needReconnect = true;
                reconnectForceRefresh = true;
                break;
              }
              throw errorFromProtocol(message.signal.error);
            }
            // Non-auth transport errors: surface and stop (caller may retry).
            if (message.signal.error.retryable && autoReconnect) {
              needReconnect = true;
              break;
            }
            return;
          }

          if (message.kind === "protocol_event" && isTerminalProtocolEvent(message.event)) {
            // Terminal run event delivered; stream may close soon. Continue until
            // body ends so any trailing events are not hidden.
          }
        }
      } catch (error) {
        if (options.signal?.aborted || (error instanceof DOMException && error.name === "AbortError")) {
          throw abortError();
        }
        if (autoReconnect && isRetryableNetwork(error) && attempts < this.maxReconnectAttempts) {
          needReconnect = true;
        } else {
          throw error;
        }
      }

      if (!needReconnect) {
        return;
      }

      attempts += 1;
      if (attempts > this.maxReconnectAttempts) {
        throw new AgentClientError("SSE reconnect attempts exhausted", {
          code: "NETWORK_ERROR",
          retryable: false,
        });
      }
      forceRefresh = reconnectForceRefresh;
      session.clearGapLatch();
      await sleep(this.backoff(attempts), options.signal);
    }
  }

  /**
   * Stream events while reducing into a live snapshot. Final Item snapshots are
   * never dropped: item.completed replaces progressive state in order.
   */
  async *followRun(
    workspaceId: string,
    agentId: string,
    runId: string,
    options: FollowRunOptions = {},
  ): AsyncGenerator<FollowRunEvent, ReducedRunSnapshot, unknown> {
    const reducer = options.reducer ?? new RunReducer();
    const sessionOptions =
      options.initialLastSequence !== undefined
        ? { initialLastSequence: options.initialLastSequence }
        : {};
    const session = options.session ?? new AAPSESession(sessionOptions);

    const streamOptions: StreamRunEventsOptions = {
      session,
      autoReconnect: options.autoReconnect,
      refreshOnAuthFailure: options.refreshOnAuthFailure,
    };
    if (options.signal) {
      streamOptions.signal = options.signal;
    }
    if (options.initialLastSequence !== undefined) {
      streamOptions.initialLastSequence = options.initialLastSequence;
    }

    for await (const message of this.streamRunEvents(workspaceId, agentId, runId, streamOptions)) {
      if (message.kind === "protocol_event") {
        // Unknown additive types still advance sequence without mutating payload.
        reducer.apply(message.event);
      }

      const snapshot = reducer.snapshot();
      yield { message, snapshot, session };

      if (
        message.kind === "protocol_event" &&
        snapshot.run &&
        isTerminalRunStatus(String(snapshot.run.status))
      ) {
        // Keep consuming until stream ends; do not hide later items/usage if any.
      }
    }

    return reducer.snapshot();
  }

  /**
   * Reduce a complete offline event list (e.g. golden JSONL) into a snapshot.
   */
  reduceEvents(events: readonly ProtocolEventEnvelope[]): ReducedRunSnapshot {
    const reducer = new RunReducer();
    reducer.applyAll(events);
    return reducer.snapshot();
  }

  // --- Internals ---------------------------------------------------------

  private async openEventStream(
    path: string,
    session: AAPSESession,
    options: { signal?: AbortSignal | undefined; forceRefresh: boolean },
  ): Promise<Response> {
    const url = this.baseUrl + path;
    assertNoAccessTokenInURL(url);

    const token = await this.tokens.getAccessToken({ forceRefresh: options.forceRefresh });
    const headers = session.resumeHeaders({
      Authorization: `Bearer ${token}`,
      Accept: "text/event-stream",
    });

    let response: Response;
    try {
      const init: RequestInit = {
        method: "GET",
        headers,
      };
      if (options.signal) {
        init.signal = options.signal;
      }
      response = await this.fetchImpl(url, init);
    } catch (cause) {
      if (options.signal?.aborted || (cause instanceof DOMException && cause.name === "AbortError")) {
        throw abortError();
      }
      throw new AgentClientError("SSE fetch failed", {
        code: "NETWORK_ERROR",
        retryable: true,
        cause,
      });
    }

    if (response.status === 401) {
      throw new AgentClientError("SSE unauthorized", {
        code: "UNAUTHENTICATED",
        status: 401,
        retryable: true,
      });
    }

    if (!response.ok) {
      throw await this.toHttpError(response);
    }

    const contentType = response.headers.get("content-type") ?? "";
    if (!contentType.toLowerCase().includes("text/event-stream")) {
      throw new AgentClientError(`AAP SSE unexpected Content-Type: ${contentType}`, {
        code: "HTTP_ERROR",
        status: response.status,
        retryable: false,
      });
    }

    return response;
  }

  private async jsonRequest<T>(
    method: string,
    path: string,
    options: {
      body?: unknown;
      headers?: Record<string, string> | undefined;
      idempotencyKey?: string | undefined;
      signal?: AbortSignal | undefined;
      expectedStatuses?: number[] | undefined;
      forceRefresh?: boolean | undefined;
    } = {},
  ): Promise<T> {
    const expected = options.expectedStatuses ?? [200];
    const response = await this.rawRequest(method, path, {
      body: options.body,
      headers: options.headers,
      idempotencyKey: options.idempotencyKey,
      signal: options.signal,
      forceRefresh: options.forceRefresh,
      allowStatuses: expected,
      retryOn401: true,
    });
    if (response.status === 204) {
      return undefined as T;
    }
    return (await response.json()) as T;
  }

  private async rawRequest(
    method: string,
    path: string,
    options: {
      body?: unknown;
      headers?: Record<string, string> | undefined;
      idempotencyKey?: string | undefined;
      signal?: AbortSignal | undefined;
      forceRefresh?: boolean | undefined;
      allowStatuses?: number[] | undefined;
      retryOn401?: boolean | undefined;
    } = {},
  ): Promise<Response> {
    return this.fetchBinary(this.baseUrl + path, {
      method,
      body: options.body !== undefined ? JSON.stringify(options.body) : undefined,
      headers: {
        ...(options.body !== undefined ? { "Content-Type": "application/json" } : {}),
        Accept: "application/json",
        ...options.headers,
      },
      idempotencyKey: options.idempotencyKey,
      signal: options.signal,
      forceRefresh: options.forceRefresh,
      allowStatuses: options.allowStatuses,
      retryOn401: options.retryOn401,
      withAuth: true,
    });
  }

  /**
   * Low-level fetch with optional AAP Bearer, 401 refresh, and status allow-list.
   * Used for JSON CRUD and binary content downloads.
   */
  private async fetchBinary(
    url: string,
    options: {
      method: string;
      body?: BodyInit | undefined;
      headers?: Record<string, string> | undefined;
      idempotencyKey?: string | undefined;
      signal?: AbortSignal | undefined;
      forceRefresh?: boolean | undefined;
      allowStatuses?: number[] | undefined;
      retryOn401?: boolean | undefined;
      withAuth?: boolean | undefined;
    },
  ): Promise<Response> {
    assertNoAccessTokenInURL(url);

    const attempt = async (forceRefresh: boolean): Promise<Response> => {
      const headers = new Headers(options.headers);
      if (options.withAuth !== false) {
        const token = await this.tokens.getAccessToken({ forceRefresh });
        headers.set("Authorization", `Bearer ${token}`);
      }
      if (options.idempotencyKey) {
        headers.set("Idempotency-Key", options.idempotencyKey);
      }

      const init: RequestInit = {
        method: options.method,
        headers,
      };
      if (options.signal) {
        init.signal = options.signal;
      }
      if (options.body !== undefined) {
        init.body = options.body;
      }

      try {
        return await this.fetchImpl(url, init);
      } catch (cause) {
        if (options.signal?.aborted || (cause instanceof DOMException && cause.name === "AbortError")) {
          throw abortError();
        }
        throw new AgentClientError("HTTP request failed", {
          code: "NETWORK_ERROR",
          retryable: true,
          cause,
        });
      }
    };

    const canRetry401 = options.withAuth !== false && options.retryOn401 !== false;
    let response = await attempt(options.forceRefresh === true);
    if (response.status === 401 && canRetry401) {
      response = await attempt(true);
    }

    const allow = options.allowStatuses ?? [200];
    if (!allow.includes(response.status)) {
      throw await this.toHttpError(response);
    }
    return response;
  }

  private async toHttpError(response: Response): Promise<AgentClientError> {
    let body: unknown;
    try {
      body = await response.json();
    } catch {
      body = undefined;
    }
    const protocol = extractProtocolError(body);
    if (protocol) {
      return errorFromProtocol(protocol, response.status);
    }
    return new AgentClientError(`HTTP ${response.status}`, {
      code: response.status === 401 ? "UNAUTHENTICATED" : "HTTP_ERROR",
      status: response.status,
      retryable: response.status === 429 || response.status >= 500,
      details: body,
    });
  }

  private backoff(attempt: number): number {
    const exp = Math.min(attempt, 6);
    return this.reconnectBackoffMs * 2 ** (exp - 1);
  }
}

function isTerminalProtocolEvent(event: ProtocolEventEnvelope): boolean {
  return (
    event.type === "run.completed" || event.type === "run.failed" || event.type === "run.cancelled"
  );
}

function extractProtocolError(body: unknown): ProtocolErrorValue | null {
  if (!body || typeof body !== "object") {
    return null;
  }
  const record = body as Record<string, unknown>;
  const error = record.error;
  if (!error || typeof error !== "object") {
    return null;
  }
  const e = error as Record<string, unknown>;
  if (typeof e.code !== "string" || typeof e.message !== "string" || typeof e.retryable !== "boolean") {
    return null;
  }
  const value: ProtocolErrorValue = {
    code: e.code,
    message: e.message,
    retryable: e.retryable,
  };
  if (e.details !== undefined && e.details !== null) {
    value.details = e.details as NonNullable<ProtocolErrorValue["details"]>;
  }
  return value;
}

function isRetryableNetwork(error: unknown): boolean {
  if (error instanceof AgentClientError) {
    return error.retryable || error.code === "NETWORK_ERROR";
  }
  return true;
}

function abortError(): AgentClientError {
  return new AgentClientError("The AAP request was aborted", {
    code: "STREAM_ABORTED",
    retryable: false,
  });
}

function sleep(ms: number, signal?: AbortSignal): Promise<void> {
  if (ms <= 0) {
    return Promise.resolve();
  }
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(abortError());
      return;
    }
    const timer = setTimeout(() => {
      signal?.removeEventListener("abort", onAbort);
      resolve();
    }, ms);
    const onAbort = (): void => {
      clearTimeout(timer);
      reject(abortError());
    };
    signal?.addEventListener("abort", onAbort, { once: true });
  });
}

function stripTrailingSlash(value: string): string {
  return value.replace(/\/+$/, "");
}

function enc(value: string): string {
  return encodeURIComponent(value);
}

function hasHeaderIgnoreCase(headers: Headers, name: string): boolean {
  const target = name.toLowerCase();
  for (const key of headers.keys()) {
    if (key.toLowerCase() === target) {
      return true;
    }
  }
  return false;
}

function toRequestBody(body: ArrayBuffer | Uint8Array | Blob): BodyInit {
  if (typeof Blob !== "undefined" && body instanceof Blob) {
    return body;
  }
  if (body instanceof ArrayBuffer) {
    return body;
  }
  // Uint8Array (and Node Buffer subclass): pass a plain view for fetch BodyInit.
  if (ArrayBuffer.isView(body)) {
    return body as unknown as BodyInit;
  }
  return body as BodyInit;
}

/**
 * Resolve mint/download relative paths against the AAP base URL origin.
 * Mint responses use absolute-path URLs under /api/agent-access/v1/...
 */
function resolveAgainstBase(baseUrl: string, pathOrUrl: string): string {
  const trimmed = pathOrUrl.trim();
  if (/^https?:\/\//i.test(trimmed)) {
    return trimmed;
  }
  if (trimmed.startsWith("/")) {
    const origin = new URL(baseUrl).origin;
    return origin + trimmed;
  }
  return `${stripTrailingSlash(baseUrl)}/${trimmed.replace(/^\/+/, "")}`;
}
