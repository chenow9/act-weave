import { describe, expect, it, vi } from "vitest";
import { createBrowserDirectClient, followRunInBrowser } from "../direct/browser-client.js";
import { startMintServer } from "../direct/mint-server.js";
import { TOKEN_EXCHANGE_GRANT } from "../shared/aap-oauth.js";
import { testEnv } from "../shared/env.js";
import { assertShortTokenResponse } from "../shared/security.js";

function expectNoTokenQuery(url: string): void {
  expect(url.includes(["access", "token"].join("_") + "=")).toBe(false);
}

function sseFromSequences(sequences: number[]): string {
  return sequences
    .map((sequence) => {
      const event = {
        specVersion: "1.0",
        type: sequence === sequences[sequences.length - 1] ? "run.completed" : "run.started",
        eventId: `e${sequence}`,
        streamId: "run:r1",
        sequence,
        occurredAt: "2026-07-20T01:00:00Z",
        workspaceId: "ws",
        agentId: "ag",
        conversationId: "c1",
        runId: "r1",
        traceId: "t1",
        data: {
          run: {
            id: "r1",
            conversationId: "c1",
            agentId: "ag",
            status: sequence === sequences[sequences.length - 1] ? "completed" : "running",
            trigger: "api",
            startedAt: "2026-07-20T01:00:00Z",
            ...(sequence === sequences[sequences.length - 1]
              ? { completedAt: "2026-07-20T01:00:01Z" }
              : {}),
          },
        },
      };
      return `id: ${sequence}\nevent: ${event.type}\ndata: ${JSON.stringify(event)}\n\n`;
    })
    .join("");
}

describe("direct short-token topology", () => {
  it("rejects unsafe short-token responses", () => {
    expect(() => assertShortTokenResponse({ accessToken: "x", expiresIn: 0 })).toThrow(/expiresIn/);
    expect(() =>
      assertShortTokenResponse({
        accessToken: "https://evil/?" + ["access", "token"].join("_") + "=abc",
        expiresIn: 60,
      }),
    ).toThrow(/query/);
  });

  it("mint server exchanges subject token server-side and returns short token without Set-Cookie", async () => {
    const fetchMock = vi.fn(async (input: string | URL, init?: RequestInit) => {
      const url = String(input);
      expect(url.endsWith("/oauth/token")).toBe(true);
      expectNoTokenQuery(url);
      const headers = new Headers(init?.headers);
      expect(headers.get("Authorization")?.startsWith("Basic ")).toBe(true);
      const body = String(init?.body);
      expect(body).toContain(`grant_type=${encodeURIComponent(TOKEN_EXCHANGE_GRANT)}`);
      expect(body).toContain("subject_token=");
      // client secret is only in Basic auth header, not form body for this example
      return new Response(
        JSON.stringify({
          access_token: "delegated-short-token",
          token_type: "Bearer",
          expires_in: 600,
          issued_token_type: "urn:ietf:params:oauth:token-type:access_token",
          scope: "run:read event:read",
        }),
        { status: 200, headers: { "content-type": "application/json" } },
      );
    });

    const started = await startMintServer({
      env: testEnv({
        aapBaseUrl: "https://aap.example.com/api/agent-access/v1",
        clientId: "cid",
        clientSecret: "secret-from-env",
        mintPort: 0,
        mintHost: "127.0.0.1",
      }),
      fetchImpl: fetchMock as typeof fetch,
      mintSubjectToken: async (session) => `signed-jwt-for-${session.subjectId}`,
    });

    try {
      const denied = await fetch(`${started.baseUrl}/api/aap/mint-token`, { method: "POST" });
      expect(denied.status).toBe(401);

      const ok = await fetch(`${started.baseUrl}/api/aap/mint-token`, {
        method: "POST",
        headers: { Authorization: "Bearer business-session", Accept: "application/json" },
      });
      expect(ok.status).toBe(200);
      expect(ok.headers.get("cache-control")).toMatch(/no-store/i);
      expect(ok.headers.get("set-cookie")).toBeNull();
      const json = (await ok.json()) as { accessToken: string; expiresIn: number };
      expect(json.accessToken).toBe("delegated-short-token");
      expect(json.expiresIn).toBe(600);
    } finally {
      await started.close();
    }
  });

  it("browser client uses MemoryTokenProvider.refresh via mint URL and follows AAP SSE", async () => {
    let mintCalls = 0;
    const fetchMock = vi.fn(async (input: string | URL, init?: RequestInit) => {
      const url = String(input);
      expectNoTokenQuery(url);

      if (url.endsWith("/api/aap/mint-token")) {
        mintCalls += 1;
        const headers = new Headers(init?.headers);
        expect(headers.get("Authorization")).toBe("Bearer business-session");
        return new Response(
          JSON.stringify({ accessToken: `short-${mintCalls}`, expiresIn: 600 }),
          {
            status: 200,
            headers: { "content-type": "application/json", "cache-control": "no-store" },
          },
        );
      }

      if (url.includes("/events")) {
        const headers = new Headers(init?.headers);
        expect(headers.get("Authorization")).toBe("Bearer short-1");
        const body = new TextEncoder().encode(sseFromSequences([1, 2]));
        return new Response(
          new ReadableStream({
            start(controller) {
              controller.enqueue(body);
              controller.close();
            },
          }),
          { status: 200, headers: { "content-type": "text/event-stream" } },
        );
      }

      return new Response("no", { status: 404 });
    });

    const browser = createBrowserDirectClient({
      aapBaseUrl: "https://aap.example.com/api/agent-access/v1",
      mintUrl: "https://app.example.com/api/aap/mint-token",
      getBusinessAuthorization: () => "Bearer business-session",
      fetchImpl: fetchMock as typeof fetch,
    });

    // Memory only — clear drops the short token without touching durable storage APIs.
    const result = await followRunInBrowser(browser, "ws", "ag", "r1");
    expect(result.lastSequence).toBe(2);
    expect(result.terminalStatus).toBe("completed");
    expect(mintCalls).toBe(1);

    browser.clearAAPToken();
    await expect(browser.tokenProvider.getAccessToken()).resolves.toBe("short-2");
    expect(mintCalls).toBe(2);
  });
});
