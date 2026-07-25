/**
 * BFF SSE proxy: server holds AAP credentials; browser only talks to the BFF.
 * Propagates client abort (cancel) and applies pull-based backpressure — does not
 * buffer the entire upstream stream in memory.
 */

import {
  AgentAccessClient,
  MemoryTokenProvider,
  assertNoAccessTokenInURL,
  type TokenProvider,
} from "@actweave/agent-client";
import { issueClientCredentialsToken } from "../shared/aap-oauth.js";
import type { ExampleEnv } from "../shared/env.js";
import { assertAAPBaseUrl } from "../shared/security.js";

export interface BFFAAPClients {
  tokenProvider: TokenProvider;
  client: AgentAccessClient;
}

export function createBFFAAPClients(
  env: Pick<ExampleEnv, "aapBaseUrl" | "clientId" | "clientSecret" | "agentId" | "scope">,
  fetchImpl: typeof fetch = globalThis.fetch.bind(globalThis),
): BFFAAPClients {
  assertAAPBaseUrl(env.aapBaseUrl);
  const tokenProvider = new MemoryTokenProvider({
    refresh: () =>
      issueClientCredentialsToken({
        aapBaseUrl: env.aapBaseUrl,
        clientId: env.clientId,
        clientSecret: env.clientSecret,
        agentId: env.agentId,
        scope: env.scope,
        fetchImpl,
      }),
  });
  const client = new AgentAccessClient({
    baseUrl: env.aapBaseUrl,
    tokenProvider,
    fetch: fetchImpl,
  });
  return { tokenProvider, client };
}

export interface ProxyRunEventsOptions {
  workspaceId: string;
  agentId: string;
  runId: string;
  /** Downstream abort (browser disconnect or BFF timeout). */
  signal?: AbortSignal;
  /** Optional Last-Event-ID from the browser for resume through the BFF. */
  lastEventId?: string;
  fetchImpl?: typeof fetch;
}

export function buildUpstreamEventsURL(
  aapBaseUrl: string,
  workspaceId: string,
  agentId: string,
  runId: string,
): string {
  const base = aapBaseUrl.replace(/\/+$/, "");
  const url = `${base}/workspaces/${enc(workspaceId)}/agents/${enc(agentId)}/runs/${enc(runId)}/events`;
  assertNoAccessTokenInURL(url);
  return url;
}

/**
 * Open an upstream AAP SSE body as a ReadableStream that:
 * - aborts the upstream when `signal` fires or the stream is cancelled (cancel propagation)
 * - only pulls the next upstream chunk when the consumer is ready (backpressure)
 * - never places the AAP token in any browser-visible URL
 */
export async function openProxiedRunEventStream(
  env: Pick<ExampleEnv, "aapBaseUrl">,
  tokenProvider: TokenProvider,
  options: ProxyRunEventsOptions,
): Promise<{
  body: ReadableStream<Uint8Array>;
  contentType: string;
  abort: () => void;
  upstreamUrl: string;
}> {
  const upstreamUrl = buildUpstreamEventsURL(
    env.aapBaseUrl,
    options.workspaceId,
    options.agentId,
    options.runId,
  );
  const controller = new AbortController();
  const onOuterAbort = (): void => controller.abort();
  if (options.signal) {
    if (options.signal.aborted) {
      controller.abort();
    } else {
      options.signal.addEventListener("abort", onOuterAbort, { once: true });
    }
  }

  const headersFor = async (forceRefresh: boolean): Promise<Headers> => {
    const token = await tokenProvider.getAccessToken({ forceRefresh });
    const headers = new Headers({
      Authorization: `Bearer ${token}`,
      Accept: "text/event-stream",
    });
    if (options.lastEventId && /^\d+$/.test(options.lastEventId)) {
      headers.set("Last-Event-ID", options.lastEventId);
    }
    return headers;
  };

  const fetchImpl = options.fetchImpl ?? globalThis.fetch.bind(globalThis);
  let response = await fetchImpl(upstreamUrl, {
    method: "GET",
    headers: await headersFor(false),
    signal: controller.signal,
  });
  if (response.status === 401) {
    response = await fetchImpl(upstreamUrl, {
      method: "GET",
      headers: await headersFor(true),
      signal: controller.signal,
    });
  }
  if (!response.ok || !response.body) {
    options.signal?.removeEventListener("abort", onOuterAbort);
    throw new Error(`upstream AAP SSE failed with HTTP ${response.status}`);
  }

  const contentType = response.headers.get("content-type") ?? "text/event-stream";
  const reader = response.body.getReader();
  let cleaned = false;

  const body = new ReadableStream<Uint8Array>({
    async pull(ctrl) {
      try {
        const { done, value } = await reader.read();
        if (done) {
          ctrl.close();
          cleanup();
          return;
        }
        if (value) {
          // Enqueue only what was pulled — consumer must call pull again (backpressure).
          ctrl.enqueue(value);
        }
      } catch (error) {
        cleanup();
        ctrl.error(error);
      }
    },
    async cancel(reason) {
      // Browser closed the BFF connection → cancel upstream AAP stream.
      controller.abort();
      try {
        await reader.cancel(reason ?? "downstream-cancel");
      } catch {
        // already cancelled / released
      }
      cleanup();
    },
  });

  function cleanup(): void {
    if (cleaned) {
      return;
    }
    cleaned = true;
    options.signal?.removeEventListener("abort", onOuterAbort);
    try {
      reader.releaseLock();
    } catch {
      // already released
    }
  }

  return {
    body,
    contentType,
    abort: () => {
      controller.abort();
      void reader.cancel("bff-abort").catch(() => undefined);
      cleanup();
    },
    upstreamUrl,
  };
}

function enc(value: string): string {
  return encodeURIComponent(value);
}
