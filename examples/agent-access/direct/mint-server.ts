/**
 * Business-platform mint service for the short-lived delegated token topology.
 *
 * Flow:
 *   Browser (business session) → POST /api/aap/mint-token → this service
 *     → RFC 8693 Token Exchange (client_secret stays here)
 *     → returns short AAP access token in JSON body (Cache-Control: no-store)
 *
 * The browser holds the token only in MemoryTokenProvider (heap), never in
 * durable browser storage, cookies, or URL query parameters.
 */

import { createServer, type IncomingMessage, type ServerResponse } from "node:http";
import { issueTokenExchange } from "../shared/aap-oauth.js";
import type { ExampleEnv } from "../shared/env.js";
import { assertAAPBaseUrl } from "../shared/security.js";

export interface MintServerOptions {
  env: ExampleEnv;
  fetchImpl?: typeof fetch;
  /**
   * Build a subject JWT for the authenticated business user.
   * Real apps sign with the configured Trusted Subject Issuer private key.
   */
  mintSubjectToken?: (session: { subjectId: string }) => Promise<string>;
  authenticate?: (req: IncomingMessage) => Promise<{ subjectId: string } | null>;
}

export interface StartedMint {
  port: number;
  host: string;
  baseUrl: string;
  close: () => Promise<void>;
}

export async function startMintServer(options: MintServerOptions): Promise<StartedMint> {
  const env = options.env;
  assertAAPBaseUrl(env.aapBaseUrl);
  const fetchImpl = options.fetchImpl ?? globalThis.fetch.bind(globalThis);

  const authenticate =
    options.authenticate ??
    (async (req) => {
      const auth = req.headers.authorization ?? "";
      if (!auth.startsWith("Bearer ") || auth.slice(7).trim() === "") {
        return null;
      }
      return { subjectId: "demo-user" };
    });

  const mintSubjectToken =
    options.mintSubjectToken ??
    (async (session) => {
      // Placeholder subject assertion for demos/tests. Production must sign a real JWT
      // with the Trusted Subject Issuer key registered on the Agent Access Client.
      return `demo-subject-token:${session.subjectId}`;
    });

  const server = createServer((req, res) => {
    void handle(req, res);
  });

  async function handle(req: IncomingMessage, res: ServerResponse): Promise<void> {
    try {
      const host = req.headers.host ?? `${env.mintHost}:${env.mintPort}`;
      const url = new URL(req.url ?? "/", `http://${host}`);

      if (req.method === "GET" && url.pathname === "/healthz") {
        json(res, 200, { ok: true, mode: "direct-mint" });
        return;
      }

      if (req.method === "POST" && url.pathname === "/api/aap/mint-token") {
        const session = await authenticate(req);
        if (!session) {
          json(res, 401, {
            error: { code: "UNAUTHENTICATED", message: "business session required", retryable: false },
          });
          return;
        }

        const subjectToken = await mintSubjectToken(session);
        const material = await issueTokenExchange({
          aapBaseUrl: env.aapBaseUrl,
          clientId: env.clientId,
          clientSecret: env.clientSecret,
          agentId: env.agentId,
          scope: env.scope,
          subjectToken,
          fetchImpl,
        });

        // Short token for browser memory only. Never Set-Cookie.
        json(res, 200, {
          accessToken: material.accessToken,
          expiresIn: material.expiresIn ?? 600,
          tokenType: material.tokenType ?? "Bearer",
          ...(material.scope ? { scope: material.scope } : {}),
        });
        return;
      }

      json(res, 404, { error: { code: "NOT_FOUND", message: "unknown route", retryable: false } });
    } catch (error) {
      const message = error instanceof Error ? error.message : "internal error";
      // Do not include subject_token / client_secret in error payloads.
      json(res, 502, { error: { code: "MINT_FAILED", message, retryable: true } });
    }
  }

  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(env.mintPort, env.mintHost, () => resolve());
  });

  const address = server.address();
  if (!address || typeof address === "string") {
    throw new Error("mint server failed to bind a TCP port");
  }

  return {
    host: env.mintHost,
    port: address.port,
    baseUrl: `http://${env.mintHost}:${address.port}`,
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
