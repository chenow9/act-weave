/**
 * Browser / SPA side of the short-lived delegated token topology.
 *
 * Security rules (enforced by construction):
 * - Never import client secrets.
 * - Never write AAP tokens to durable browser storage, cookies, or URL query.
 * - Refresh only via the business-platform mint endpoint (session-authenticated).
 * - Use MemoryTokenProvider + AgentAccessClient against the AAP base URL.
 */

import {
  AgentAccessClient,
  MemoryTokenProvider,
  assertNoAccessTokenInURL,
  type AccessTokenMaterial,
  type AgentAccessClientOptions,
  type TokenProvider,
} from "@actweave/agent-client";
import { assertAAPBaseUrl, assertShortTokenResponse } from "../shared/security.js";

export interface BrowserDirectClientOptions {
  /** AAP data plane base URL (public, no secrets). */
  aapBaseUrl: string;
  /** Business mint endpoint, e.g. https://app.example.com/api/aap/mint-token */
  mintUrl: string;
  /**
   * Return the business session Authorization header value (e.g. "Bearer <session>").
   * Must not return an AAP access token.
   */
  getBusinessAuthorization: () => string | Promise<string>;
  fetchImpl?: typeof fetch;
  clientOptions?: Omit<AgentAccessClientOptions, "baseUrl" | "tokenProvider" | "fetch">;
}

export interface BrowserDirectClient {
  tokenProvider: TokenProvider;
  aap: AgentAccessClient;
  /** Drop the in-memory AAP token (e.g. on business logout). */
  clearAAPToken: () => void;
}

/**
 * Create a browser AgentAccessClient whose TokenProvider.refresh calls the
 * business mint service (Token Exchange on the server).
 */
export function createBrowserDirectClient(options: BrowserDirectClientOptions): BrowserDirectClient {
  assertAAPBaseUrl(options.aapBaseUrl);
  assertNoAccessTokenInURL(options.mintUrl);

  const fetchImpl = options.fetchImpl ?? globalThis.fetch.bind(globalThis);

  const refresh = async (): Promise<AccessTokenMaterial> => {
    const authorization = await options.getBusinessAuthorization();
    const response = await fetchImpl(options.mintUrl, {
      method: "POST",
      headers: {
        Authorization: authorization,
        Accept: "application/json",
        // Explicitly avoid credentials mode that would attach cookies cross-site
        // unless the product intentionally uses same-site business cookies.
      },
      // Note: intentionally no body containing client secrets.
    });
    if (!response.ok) {
      throw new Error(`mint-token failed with HTTP ${response.status}`);
    }
    // Cache-Control: no-store is expected from the mint service.
    const cacheControl = response.headers.get("cache-control") ?? "";
    if (cacheControl && !/no-store/i.test(cacheControl)) {
      // Soft signal in demos; real apps should fail closed if persistence is allowed.
      console.warn("mint-token response should send Cache-Control: no-store");
    }
    const json: unknown = await response.json();
    const short = assertShortTokenResponse(json);
    return {
      accessToken: short.accessToken,
      expiresIn: short.expiresIn,
    };
  };

  const tokenProvider = new MemoryTokenProvider({ refresh });
  const aap = new AgentAccessClient({
    baseUrl: options.aapBaseUrl,
    tokenProvider,
    fetch: fetchImpl,
    ...(options.clientOptions ?? {}),
  });

  return {
    tokenProvider,
    aap,
    clearAAPToken: () => tokenProvider.clear(),
  };
}

/**
 * Example follow-run helper for docs / demos.
 * Tokens remain in MemoryTokenProvider only for the lifetime of this page session.
 */
export async function followRunInBrowser(
  client: BrowserDirectClient,
  workspaceId: string,
  agentId: string,
  runId: string,
  options: { signal?: AbortSignal } = {},
): Promise<{ lastSequence: number; terminalStatus: string | null }> {
  let lastSequence = 0;
  let terminalStatus: string | null = null;
  const streamOpts = options.signal ? { signal: options.signal } : {};
  for await (const step of client.aap.followRun(workspaceId, agentId, runId, streamOpts)) {
    lastSequence = step.snapshot.lastSequence;
    if (step.snapshot.run?.status) {
      const status = String(step.snapshot.run.status);
      if (status === "completed" || status === "failed" || status === "cancelled") {
        terminalStatus = status;
      }
    }
  }
  return { lastSequence, terminalStatus };
}
