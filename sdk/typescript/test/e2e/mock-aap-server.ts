/**
 * Wire-compatible AAP mock for SDK e2e.
 * Replays frozen Golden JSONL as SSE using the same id/event/data framing as the Go encoder.
 * Supports Last-Event-ID, Token Exchange / client_credentials, and injectible TOKEN_EXPIRED.
 */

import { createServer, type IncomingMessage, type ServerResponse } from "node:http";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const goldenDir = join(
  dirname(fileURLToPath(import.meta.url)),
  "../../../../backend/internal/protocolschema/testdata/aap/v1",
);

export interface GoldenEnvelope {
  specVersion: string;
  type: string;
  eventId: string;
  streamId: string;
  sequence: number;
  occurredAt: string;
  workspaceId: string;
  agentId: string;
  conversationId: string;
  runId: string;
  traceId: string;
  data: unknown;
}

export function loadGoldenJSONL(name: string): GoldenEnvelope[] {
  const raw = readFileSync(join(goldenDir, `${name}.jsonl`), "utf8");
  return raw
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => JSON.parse(line) as GoldenEnvelope);
}

export function loadGoldenSnapshot(name: string): unknown {
  return JSON.parse(readFileSync(join(goldenDir, `${name}.snapshot.json`), "utf8"));
}

export function encodeSSE(events: GoldenEnvelope[]): string {
  return events
    .map((event) => {
      const data = JSON.stringify(event);
      return `id: ${event.sequence}\nevent: ${event.type}\ndata: ${data}\n\n`;
    })
    .join("");
}

export interface MockAAPOptions {
  /** Map runId → golden events. */
  runs: Record<string, GoldenEnvelope[]>;
  /** After emitting this many protocol events on a stream, send TOKEN_EXPIRED and close. */
  expireAfterSequence?: number;
  /** Valid Bearer tokens (static). When omitted, any non-empty Bearer is accepted. */
  validTokens?: Set<string>;
  /** client_id:client_secret for Basic auth on /oauth/token */
  clientId?: string;
  clientSecret?: string;
}

export interface StartedMockAAP {
  baseUrl: string;
  port: number;
  close: () => Promise<void>;
  /** Issued tokens from oauth endpoint (for assertions). */
  issuedTokens: string[];
  /** Count of /events requests (side-effect / reconnect counting). */
  eventRequestCount: number;
  /** Tokens seen on Authorization headers for events. */
  eventAuthTokens: string[];
}

export async function startMockAAP(options: MockAAPOptions): Promise<StartedMockAAP> {
  const clientId = options.clientId ?? "demo-client";
  const clientSecret = options.clientSecret ?? "demo-secret";
  const issuedTokens: string[] = [];
  let eventRequestCount = 0;
  const eventAuthTokens: string[] = [];
  let tokenSeq = 0;
  /** TOKEN_EXPIRED injection fires at most once so resume can complete. */
  let expireArmed = options.expireAfterSequence !== undefined;

  const server = createServer((req, res) => {
    void handle(req, res);
  });

  async function handle(req: IncomingMessage, res: ServerResponse): Promise<void> {
    const host = req.headers.host ?? "127.0.0.1";
    const url = new URL(req.url ?? "/", `http://${host}`);

    if (req.method === "POST" && url.pathname === "/api/agent-access/v1/oauth/token") {
      await handleToken(req, res);
      return;
    }

    const eventsMatch = url.pathname.match(
      /^\/api\/agent-access\/v1\/workspaces\/([^/]+)\/agents\/([^/]+)\/runs\/([^/]+)\/events$/,
    );
    if (req.method === "GET" && eventsMatch) {
      handleEvents(req, res, decodeURIComponent(eventsMatch[3]!));
      return;
    }

    json(res, 404, { error: { code: "NOT_FOUND", message: "unknown", retryable: false } });
  }

  async function handleToken(req: IncomingMessage, res: ServerResponse): Promise<void> {
    const auth = req.headers.authorization ?? "";
    const expected =
      "Basic " + Buffer.from(`${clientId}:${clientSecret}`, "utf8").toString("base64");
    if (auth !== expected) {
      json(res, 401, { error: "invalid_client", error_description: "bad credentials" });
      return;
    }
    const body = await readBody(req);
    const form = new URLSearchParams(body);
    const grant = form.get("grant_type") ?? "";
    tokenSeq += 1;
    const accessToken = `issued-token-${tokenSeq}`;
    issuedTokens.push(accessToken);

    if (grant === "client_credentials") {
      json(res, 200, {
        access_token: accessToken,
        token_type: "Bearer",
        expires_in: 600,
        scope: form.get("scope") ?? "event:read",
      });
      return;
    }
    if (grant === "urn:ietf:params:oauth:grant-type:token-exchange") {
      if (!form.get("subject_token")) {
        json(res, 400, { error: "invalid_request", error_description: "subject_token required" });
        return;
      }
      json(res, 200, {
        access_token: accessToken,
        issued_token_type: "urn:ietf:params:oauth:token-type:access_token",
        token_type: "Bearer",
        expires_in: 600,
        scope: form.get("scope") ?? "event:read",
      });
      return;
    }
    json(res, 400, { error: "unsupported_grant_type", error_description: grant });
  }

  function handleEvents(req: IncomingMessage, res: ServerResponse, runId: string): void {
    eventRequestCount += 1;
    // Reject token query.
    if (urlHasAccessToken(req.url ?? "")) {
      json(res, 401, { error: { code: "UNAUTHENTICATED", message: "token query forbidden", retryable: false } });
      return;
    }
    const auth = req.headers.authorization ?? "";
    if (!auth.startsWith("Bearer ") || auth.slice(7).trim() === "") {
      json(res, 401, { error: { code: "UNAUTHENTICATED", message: "missing bearer", retryable: false } });
      return;
    }
    const token = auth.slice(7).trim();
    eventAuthTokens.push(token);
    if (options.validTokens && !options.validTokens.has(token) && !token.startsWith("issued-token-")) {
      json(res, 401, { error: { code: "UNAUTHENTICATED", message: "bad token", retryable: false } });
      return;
    }

    const events = options.runs[runId];
    if (!events) {
      json(res, 404, { error: { code: "RESOURCE_NOT_FOUND", message: "run not found", retryable: false } });
      return;
    }

    const lastHeader = req.headers["last-event-id"];
    const lastEventId =
      typeof lastHeader === "string" ? lastHeader : Array.isArray(lastHeader) ? lastHeader[0] : undefined;
    let after = 0;
    if (lastEventId !== undefined && lastEventId !== "") {
      if (!/^\d+$/.test(lastEventId)) {
        json(res, 422, { error: { code: "REPLAY_CURSOR_INVALID", message: "bad cursor", retryable: false } });
        return;
      }
      after = Number.parseInt(lastEventId, 10);
    }

    const toSend = events.filter((e) => e.sequence > after);
    res.writeHead(200, {
      "Content-Type": "text/event-stream",
      "Cache-Control": "no-store",
      Connection: "keep-alive",
      "X-AAP-Protocol-Version": "1.0",
    });

    for (const event of toSend) {
      res.write(`id: ${event.sequence}\nevent: ${event.type}\ndata: ${JSON.stringify(event)}\n\n`);
      if (
        expireArmed &&
        options.expireAfterSequence !== undefined &&
        event.sequence === options.expireAfterSequence
      ) {
        expireArmed = false;
        res.write(
          `event: stream.error\ndata: ${JSON.stringify({
            specVersion: "1.0",
            type: "stream.error",
            occurredAt: new Date().toISOString(),
            error: { code: "TOKEN_EXPIRED", message: "access token expired", retryable: true },
          })}\n\n`,
        );
        res.end();
        return;
      }
    }
    res.end();
  }

  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => resolve());
  });
  const address = server.address();
  if (!address || typeof address === "string") {
    throw new Error("mock AAP failed to bind");
  }

  return {
    baseUrl: `http://127.0.0.1:${address.port}/api/agent-access/v1`,
    port: address.port,
    issuedTokens,
    get eventRequestCount() {
      return eventRequestCount;
    },
    eventAuthTokens,
    close: () =>
      new Promise((resolve, reject) => {
        server.close((err) => (err ? reject(err) : resolve()));
      }),
  };
}

function urlHasAccessToken(rawUrl: string): boolean {
  try {
    const u = new URL(rawUrl, "http://local");
    return u.searchParams.has("access_token") || u.searchParams.has("token");
  } catch {
    return false;
  }
}

function readBody(req: IncomingMessage): Promise<string> {
  return new Promise((resolve, reject) => {
    const chunks: Buffer[] = [];
    req.on("data", (c) => chunks.push(Buffer.isBuffer(c) ? c : Buffer.from(c)));
    req.on("end", () => resolve(Buffer.concat(chunks).toString("utf8")));
    req.on("error", reject);
  });
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
