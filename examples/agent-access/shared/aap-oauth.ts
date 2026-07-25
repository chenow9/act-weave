/**
 * Server-side AAP OAuth helpers (Client Credentials + RFC 8693 Token Exchange).
 * Credentials stay on the business platform / BFF — never in the browser.
 */

import { assertNoAccessTokenInURL, type AccessTokenMaterial } from "@actweave/agent-client";

export const TOKEN_EXCHANGE_GRANT = "urn:ietf:params:oauth:grant-type:token-exchange";
export const SUBJECT_TOKEN_TYPE_JWT = "urn:ietf:params:oauth:token-type:jwt";
export const REQUESTED_TOKEN_TYPE_ACCESS = "urn:ietf:params:oauth:token-type:access_token";

export interface OAuthTokenResponse {
  access_token: string;
  token_type: string;
  expires_in: number;
  scope?: string;
  issued_token_type?: string;
}

export interface ClientCredentialsOptions {
  aapBaseUrl: string;
  clientId: string;
  clientSecret: string;
  agentId: string;
  scope: string;
  fetchImpl?: typeof fetch;
}

export interface TokenExchangeOptions {
  aapBaseUrl: string;
  clientId: string;
  clientSecret: string;
  agentId: string;
  scope: string;
  /** Business-platform JWT asserting the end-user subject (server-minted). */
  subjectToken: string;
  fetchImpl?: typeof fetch;
}

/** Issue a short AAP access token via client_credentials (BFF / pure SP). */
export async function issueClientCredentialsToken(
  options: ClientCredentialsOptions,
): Promise<AccessTokenMaterial> {
  const body = new URLSearchParams({
    grant_type: "client_credentials",
    agent_id: options.agentId,
    scope: options.scope,
  });
  return postToken(options.aapBaseUrl, options.clientId, options.clientSecret, body, options.fetchImpl);
}

/**
 * Exchange a trusted subject JWT for a short delegated AAP access token
 * bound to Workspace + Agent + External Subject.
 */
export async function issueTokenExchange(
  options: TokenExchangeOptions,
): Promise<AccessTokenMaterial> {
  const body = new URLSearchParams({
    grant_type: TOKEN_EXCHANGE_GRANT,
    agent_id: options.agentId,
    scope: options.scope,
    subject_token: options.subjectToken,
    subject_token_type: SUBJECT_TOKEN_TYPE_JWT,
    requested_token_type: REQUESTED_TOKEN_TYPE_ACCESS,
  });
  return postToken(options.aapBaseUrl, options.clientId, options.clientSecret, body, options.fetchImpl);
}

async function postToken(
  aapBaseUrl: string,
  clientId: string,
  clientSecret: string,
  body: URLSearchParams,
  fetchImpl: typeof fetch = globalThis.fetch.bind(globalThis),
): Promise<AccessTokenMaterial> {
  const url = joinUrl(aapBaseUrl, "/oauth/token");
  assertNoAccessTokenInURL(url);

  const response = await fetchImpl(url, {
    method: "POST",
    headers: {
      Authorization: basicAuth(clientId, clientSecret),
      "Content-Type": "application/x-www-form-urlencoded",
      Accept: "application/json",
    },
    body,
  });

  if (!response.ok) {
    // Never echo credential material from error bodies in logs of real apps.
    throw new Error(`AAP OAuth token request failed with HTTP ${response.status}`);
  }

  const json = (await response.json()) as OAuthTokenResponse;
  if (!json.access_token || typeof json.expires_in !== "number") {
    throw new Error("AAP OAuth token response is missing access_token or expires_in");
  }

  return {
    accessToken: json.access_token,
    expiresIn: json.expires_in,
    tokenType: json.token_type,
    ...(json.scope ? { scope: json.scope } : {}),
  };
}

export function basicAuth(clientId: string, clientSecret: string): string {
  const raw = `${clientId}:${clientSecret}`;
  return `Basic ${Buffer.from(raw, "utf8").toString("base64")}`;
}

export function joinUrl(base: string, path: string): string {
  return `${base.replace(/\/+$/, "")}${path.startsWith("/") ? path : `/${path}`}`;
}
