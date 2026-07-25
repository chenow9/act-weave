/**
 * Minimal BFF HTTP server (Node).
 *
 * Browser → BFF (business session) → AAP (client_credentials short token).
 * The browser never receives the Client Secret or long-lived credentials.
 */

import { createServer, type IncomingMessage, type ServerResponse } from "node:http";
import { AgentAccessClient, MemoryTokenProvider } from "@actweave/agent-client";
import { issueClientCredentialsToken } from "../shared/aap-oauth.js";
import type { ExampleEnv } from "../shared/env.js";
import { assertAAPBaseUrl } from "../shared/security.js";
import { openProxiedRunEventStream } from "./proxy-sse.js";

export interface BFFServerOptions {
  env: ExampleEnv;
  fetchImpl?: typeof fetch;
  /**
   * Demo business-session authenticator.
   * Real apps validate their own session cookie / JWT here.
   */
  authenticate?: (req: IncomingMessage) => Promise<{ subjectId: string } | null>;
}

export interface StartedBFF {
  port: number;
  host: string;
  baseUrl: string;
  close: () => Promise<void>;
}

export async function startBFFServer(options: BFFServerOptions): Promise<StartedBFF> {
  const env = options.env;
  assertAAPBaseUrl(env.aapBaseUrl);
  const fetchImpl = options.fetchImpl ?? globalThis.fetch.bind(globalThis);
  const authenticate =
    options.authenticate ??
    (async (req) => {
      // Demo: require Authorization: Bearer <business-session>.
      const auth = req.headers.authorization ?? "";
      if (!auth.startsWith("Bearer ") || auth.slice(7).trim() === "") {
        return null;
      }
      return { subjectId: "demo-user" };
    });

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
  const aapClient = new AgentAccessClient({
    baseUrl: env.aapBaseUrl,
    tokenProvider,
    fetch: fetchImpl,
  });

  const server = createServer((req, res) => {
    void handle(req, res);
  });

  async function handle(req: IncomingMessage, res: ServerResponse): Promise<void> {
    try {
      const host = req.headers.host ?? `${env.bffHost}:${env.bffPort}`;
      const url = new URL(req.url ?? "/", `http://${host}`);

      if (req.method === "GET" && url.pathname === "/healthz") {
        json(res, 200, { ok: true, mode: "bff" });
        return;
      }

      const session = await authenticate(req);
      if (!session) {
        json(res, 401, { error: { code: "UNAUTHENTICATED", message: "business session required", retryable: false } });
        return;
      }

      // GET /api/aap/runs/:runId/events
      const eventsMatch = url.pathname.match(/^\/api\/aap\/runs\/([^/]+)\/events$/);
      if (req.method === "GET" && eventsMatch) {
        const runId = decodeURIComponent(eventsMatch[1]!);
        const lastEventIdHeader = req.headers["last-event-id"];
        const lastEventId =
          typeof lastEventIdHeader === "string"
            ? lastEventIdHeader
            : Array.isArray(lastEventIdHeader)
              ? lastEventIdHeader[0]
              : undefined;

        const abort = new AbortController();
        req.on("close", () => abort.abort());

        const proxied = await openProxiedRunEventStream(env, tokenProvider, {
          workspaceId: env.workspaceId,
          agentId: env.agentId,
          runId,
          signal: abort.signal,
          ...(lastEventId ? { lastEventId } : {}),
          fetchImpl,
        });

        res.writeHead(200, {
          "Content-Type": proxied.contentType,
          "Cache-Control": "no-store",
          Connection: "keep-alive",
          // Never set Set-Cookie with AAP tokens.
        });

        const reader = proxied.body.getReader();
        try {
          while (true) {
            const { done, value } = await reader.read();
            if (done) {
              break;
            }
            if (!value) {
              continue;
            }
            if (res.writableEnded || abort.signal.aborted) {
              proxied.abort();
              break;
            }
            // Backpressure: wait for the socket drain if the kernel buffer is full.
            const ok = res.write(Buffer.from(value));
            if (!ok) {
              await onceDrain(res);
            }
          }
        } finally {
          proxied.abort();
          res.end();
        }
        return;
      }

      // POST /api/aap/runs/:runId/cancel
      const cancelMatch = url.pathname.match(/^\/api\/aap\/runs\/([^/]+)\/cancel$/);
      if (req.method === "POST" && cancelMatch) {
        const runId = decodeURIComponent(cancelMatch[1]!);
        const ifMatch = headerString(req, "if-match") ?? '"run:1"';
        const idempotencyKey =
          headerString(req, "idempotency-key") ?? crypto.randomUUID();
        const result = await aapClient.cancelRun(env.workspaceId, env.agentId, runId, {
          idempotencyKey,
          ifMatch,
        });
        json(res, 200, result);
        return;
      }

      json(res, 404, { error: { code: "NOT_FOUND", message: "unknown route", retryable: false } });
    } catch (error) {
      const message = error instanceof Error ? error.message : "internal error";
      json(res, 502, { error: { code: "UPSTREAM_ERROR", message, retryable: true } });
    }
  }

  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(env.bffPort, env.bffHost, () => resolve());
  });

  const address = server.address();
  if (!address || typeof address === "string") {
    throw new Error("BFF failed to bind a TCP port");
  }

  return {
    host: env.bffHost,
    port: address.port,
    baseUrl: `http://${env.bffHost}:${address.port}`,
    close: () =>
      new Promise((resolve, reject) => {
        server.close((err) => (err ? reject(err) : resolve()));
      }),
  };
}

function json(res: ServerResponse, status: number, body: unknown): void {
  const payload = JSON.stringify(body);
  res.writeHead(status, {
    "Content-Type": "application/json",
    "Cache-Control": "no-store",
    "Content-Length": Buffer.byteLength(payload),
  });
  res.end(payload);
}

function headerString(req: IncomingMessage, name: string): string | undefined {
  const value = req.headers[name];
  if (typeof value === "string") {
    return value;
  }
  if (Array.isArray(value)) {
    return value[0];
  }
  return undefined;
}

function onceDrain(res: ServerResponse): Promise<void> {
  return new Promise((resolve) => {
    res.once("drain", () => resolve());
  });
}
