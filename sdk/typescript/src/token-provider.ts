/**
 * Pluggable short-lived AAP Access Token provider.
 * Tokens must stay in caller memory only — never query strings, localStorage, or cookies.
 */

import { AgentClientError } from "./errors.js";

export interface AccessTokenMaterial {
  /** Bearer access token string (never log this value). */
  accessToken: string;
  /** Seconds until expiry from issuance (OAuth expires_in). */
  expiresIn?: number;
  /** Absolute expiry epoch milliseconds when known. */
  expiresAt?: number;
  tokenType?: string;
  scope?: string;
}

export interface TokenProvider {
  /**
   * Return a usable access token for Authorization: Bearer.
   * When forceRefresh is true, discard any cached token and re-issue.
   */
  getAccessToken(options?: { forceRefresh?: boolean }): Promise<string>;

  /** Drop any in-memory token (e.g. on logout or security version bump). */
  clear?(): void;
}

export interface MemoryTokenProviderOptions {
  /**
   * Refresh callback (Client Credentials re-issue, BFF mint, or Token Exchange).
   * Must not persist the token outside process memory.
   */
  refresh: () => Promise<AccessTokenMaterial>;
  /** Clock for expiry checks (ms). Defaults to Date.now. */
  clock?: () => number;
  /** Refresh this many seconds before expiresAt. Default 30. */
  skewSeconds?: number;
}

/**
 * In-memory short Token Provider. Holds at most one token string in heap memory.
 * Does not write LocalStorage, cookies, or URL parameters.
 */
export class MemoryTokenProvider implements TokenProvider {
  private readonly refresh: () => Promise<AccessTokenMaterial>;
  private readonly clock: () => number;
  private readonly skewMs: number;
  private token: string | null = null;
  private expiresAt: number | null = null;
  private inflight: Promise<string> | null = null;

  constructor(options: MemoryTokenProviderOptions) {
    this.refresh = options.refresh;
    this.clock = options.clock ?? (() => Date.now());
    this.skewMs = (options.skewSeconds ?? 30) * 1000;
  }

  async getAccessToken(options: { forceRefresh?: boolean } = {}): Promise<string> {
    if (!options.forceRefresh && this.token && !this.isExpiringSoon()) {
      return this.token;
    }
    if (this.inflight) {
      return this.inflight;
    }
    this.inflight = this.doRefresh()
      .then((token) => token)
      .finally(() => {
        this.inflight = null;
      });
    return this.inflight;
  }

  clear(): void {
    this.token = null;
    this.expiresAt = null;
  }

  /** Test helper — does not expose the raw token string. */
  hasCachedToken(): boolean {
    return this.token !== null;
  }

  private isExpiringSoon(): boolean {
    if (this.expiresAt === null) {
      return false;
    }
    return this.clock() + this.skewMs >= this.expiresAt;
  }

  private async doRefresh(): Promise<string> {
    let material: AccessTokenMaterial;
    try {
      material = await this.refresh();
    } catch (cause) {
      throw new AgentClientError("token refresh failed", {
        code: "UNAUTHENTICATED",
        retryable: true,
        cause,
      });
    }
    const accessToken = material.accessToken?.trim() ?? "";
    if (!accessToken) {
      throw new AgentClientError("token refresh returned an empty access token", {
        code: "UNAUTHENTICATED",
        retryable: false,
      });
    }
    this.token = accessToken;
    if (typeof material.expiresAt === "number" && Number.isFinite(material.expiresAt)) {
      this.expiresAt = material.expiresAt;
    } else if (typeof material.expiresIn === "number" && material.expiresIn > 0) {
      this.expiresAt = this.clock() + material.expiresIn * 1000;
    } else {
      this.expiresAt = null;
    }
    return accessToken;
  }
}

/**
 * Static token provider for tests or fully external refresh loops.
 * Still memory-only; callers own lifecycle.
 */
export class StaticTokenProvider implements TokenProvider {
  private token: string;

  constructor(token: string) {
    this.token = token;
  }

  async getAccessToken(options: { forceRefresh?: boolean } = {}): Promise<string> {
    if (options.forceRefresh) {
      throw new AgentClientError("StaticTokenProvider cannot force-refresh", {
        code: "UNAUTHENTICATED",
        retryable: false,
      });
    }
    return this.token;
  }

  setToken(token: string): void {
    this.token = token;
  }

  clear(): void {
    this.token = "";
  }
}
