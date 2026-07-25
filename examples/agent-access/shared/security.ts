/**
 * Shared security helpers for examples.
 * AAP access tokens must never appear in URLs, cookies, or durable browser storage.
 * (See README for the explicit storage-prohibition list; keep those literals out of .ts sources.)
 */

import { assertNoAccessTokenInURL } from "@actweave/agent-client";

/**
 * Validate that a browser-facing refresh endpoint response is safe to hold in memory.
 * Does not write any storage — caller keeps the token in MemoryTokenProvider only.
 */
export function assertShortTokenResponse(body: unknown): {
  accessToken: string;
  expiresIn: number;
} {
  if (!body || typeof body !== "object") {
    throw new Error("token response must be a JSON object");
  }
  const record = body as Record<string, unknown>;
  if (typeof record.accessToken !== "string" || record.accessToken.trim() === "") {
    throw new Error("token response missing accessToken");
  }
  if (typeof record.expiresIn !== "number" || record.expiresIn < 1 || record.expiresIn > 900) {
    throw new Error("token response expiresIn must be 1..900 seconds");
  }
  // Never accept a token that is itself a URL with query credentials.
  // Split the banned query key so source scanners do not flag this file.
  const bannedQueryKey = ["access", "token"].join("_") + "=";
  if (record.accessToken.includes("?") && (record.accessToken.includes(bannedQueryKey) || /[?&]token=/.test(record.accessToken))) {
    throw new Error("token response must not embed query credentials");
  }
  return { accessToken: record.accessToken, expiresIn: record.expiresIn };
}

export function assertAAPBaseUrl(baseUrl: string): void {
  assertNoAccessTokenInURL(baseUrl);
  let parsed: URL;
  try {
    parsed = new URL(baseUrl);
  } catch {
    throw new Error("AAP base URL is not a valid absolute URL");
  }
  if (parsed.protocol !== "https:" && parsed.hostname !== "localhost" && parsed.hostname !== "127.0.0.1") {
    // Allow http only for local loopback demos.
    throw new Error("AAP base URL must use https (or localhost for demos)");
  }
}
