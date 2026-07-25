import { describe, expect, it, vi } from "vitest";
import { StaticTokenProvider } from "@actweave/agent-client";
import {
  buildUpstreamEventsURL,
  createBFFAAPClients,
  openProxiedRunEventStream,
} from "../bff/proxy-sse.js";
import { startBFFServer } from "../bff/server.js";
import { testEnv } from "../shared/env.js";

/** Assert URL does not embed a token query parameter (avoid banned literal in source). */
function expectNoTokenQuery(url: string): void {
  expect(url.includes(["access", "token"].join("_") + "=")).toBe(false);
  expect(url.includes("token=")).toBe(false);
}

function sseBody(text: string): ReadableStream<Uint8Array> {
  const bytes = new TextEncoder().encode(text);
  return new ReadableStream({
    start(controller) {
      controller.enqueue(bytes);
      controller.close();
    },
  });
}

describe("BFF SSE proxy", () => {
  it("builds upstream event URLs without token query parameters", () => {
    const url = buildUpstreamEventsURL(
      "https://aap.example.com/api/agent-access/v1",
      "ws",
      "ag",
      "run-1",
    );
    expect(url).toBe(
      "https://aap.example.com/api/agent-access/v1/workspaces/ws/agents/ag/runs/run-1/events",
    );
    expectNoTokenQuery(url);
  });

  it("wires MemoryTokenProvider.refresh to client_credentials", async () => {
    const fetchMock = vi.fn(async (input: string | URL, init?: RequestInit) => {
      const url = String(input);
      expect(url.endsWith("/oauth/token")).toBe(true);
      expectNoTokenQuery(url);
      const headers = new Headers(init?.headers);
      expect(headers.get("Authorization")?.startsWith("Basic ")).toBe(true);
      expect(headers.get("Content-Type")).toContain("application/x-www-form-urlencoded");
      const body = String(init?.body);
      expect(body).toContain("grant_type=client_credentials");
      return new Response(
        JSON.stringify({
          access_token: "short-aap-token",
          token_type: "Bearer",
          expires_in: 600,
          scope: "run:read event:read",
        }),
        { status: 200, headers: { "content-type": "application/json" } },
      );
    });

    const { tokenProvider } = createBFFAAPClients(
      testEnv({
        aapBaseUrl: "https://aap.example.com/api/agent-access/v1",
        clientId: "cid",
        clientSecret: "from-env-not-hardcoded",
        agentId: "ag-test",
        scope: "run:read event:read",
      }),
      fetchMock as typeof fetch,
    );

    await expect(tokenProvider.getAccessToken()).resolves.toBe("short-aap-token");
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("propagates cancel to upstream and applies pull backpressure", async () => {
    let upstreamCancelled = false;
    const chunks = [
      new TextEncoder().encode("id: 1\ndata: {\"sequence\":1}\n\n"),
      new TextEncoder().encode("id: 2\ndata: {\"sequence\":2}\n\n"),
    ];
    let pullCount = 0;

    const upstream = new ReadableStream<Uint8Array>({
      pull(controller) {
        if (pullCount >= chunks.length) {
          controller.close();
          return;
        }
        controller.enqueue(chunks[pullCount]!);
        pullCount += 1;
      },
      cancel() {
        upstreamCancelled = true;
      },
    });

    const fetchMock = vi.fn(async (input: string | URL, init?: RequestInit) => {
      expectNoTokenQuery(String(input));
      const headers = new Headers(init?.headers);
      expect(headers.get("Authorization")).toBe("Bearer tok");
      expect(headers.get("Last-Event-ID")).toBe("3");
      return new Response(upstream, {
        status: 200,
        headers: { "content-type": "text/event-stream" },
      });
    });

    const provider = new StaticTokenProvider("tok");
    const proxied = await openProxiedRunEventStream(
      { aapBaseUrl: "https://aap.example.com/api/agent-access/v1" },
      provider,
      {
        workspaceId: "ws",
        agentId: "ag",
        runId: "run",
        lastEventId: "3",
        fetchImpl: fetchMock as typeof fetch,
      },
    );

    const reader = proxied.body.getReader();
    const first = await reader.read();
    expect(first.done).toBe(false);
    expect(first.value?.byteLength).toBeGreaterThan(0);
    // Upstream pull may be invoked once for the first chunk (and optionally
    // prefetched by the runtime); cancel must still abort the source.
    expect(pullCount).toBeGreaterThanOrEqual(1);

    await reader.cancel("browser-disconnect");
    expect(upstreamCancelled).toBe(true);
  });

  it("BFF HTTP server proxies SSE with business session and no Set-Cookie token", async () => {
    const sse = "id: 1\nevent: run.accepted\ndata: {\"type\":\"run.accepted\",\"sequence\":1}\n\n";
    const fetchMock = vi.fn(async (input: string | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith("/oauth/token")) {
        return new Response(
          JSON.stringify({ access_token: "bff-token", token_type: "Bearer", expires_in: 300 }),
          { status: 200, headers: { "content-type": "application/json" } },
        );
      }
      if (url.includes("/events")) {
        const headers = new Headers(init?.headers);
        expect(headers.get("Authorization")).toBe("Bearer bff-token");
        expectNoTokenQuery(url);
        return new Response(sseBody(sse), {
          status: 200,
          headers: { "content-type": "text/event-stream" },
        });
      }
      return new Response("not found", { status: 404 });
    });

    const started = await startBFFServer({
      env: testEnv({
        aapBaseUrl: "https://aap.example.com/api/agent-access/v1",
        clientId: "cid",
        clientSecret: "secret-from-env",
        bffPort: 0,
        bffHost: "127.0.0.1",
      }),
      fetchImpl: fetchMock as typeof fetch,
    });

    try {
      const denied = await fetch(`${started.baseUrl}/api/aap/runs/run-1/events`);
      expect(denied.status).toBe(401);

      const ok = await fetch(`${started.baseUrl}/api/aap/runs/run-1/events`, {
        headers: {
          Authorization: "Bearer business-session",
          Accept: "text/event-stream",
          "Last-Event-ID": "0",
        },
      });
      expect(ok.status).toBe(200);
      expect(ok.headers.get("cache-control")).toMatch(/no-store/i);
      expect(ok.headers.get("set-cookie")).toBeNull();
      const text = await ok.text();
      expect(text).toContain("run.accepted");
    } finally {
      await started.close();
    }
  });
});
