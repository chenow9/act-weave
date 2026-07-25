import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it, vi } from "vitest";

import {
  AgentAccessClient,
  assertNoAccessTokenInURL,
  MemoryTokenProvider,
  type ProtocolEventEnvelope,
  StaticTokenProvider,
} from "../src/index.js";

const goldenDir = join(
  dirname(fileURLToPath(import.meta.url)),
  "../../../backend/internal/protocolschema/testdata/aap/v1",
);

function loadJSONL(name: string): ProtocolEventEnvelope[] {
  const raw = readFileSync(join(goldenDir, `${name}.jsonl`), "utf8");
  return raw
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => JSON.parse(line) as ProtocolEventEnvelope);
}

function toSSE(events: ProtocolEventEnvelope[]): string {
  return events
    .map((event) => {
      const data = JSON.stringify(event);
      return `id: ${event.sequence}\nevent: ${event.type}\ndata: ${data}\n\n`;
    })
    .join("");
}

function streamBody(text: string): ReadableStream<Uint8Array> {
  const bytes = new TextEncoder().encode(text);
  return new ReadableStream({
    start(controller) {
      controller.enqueue(bytes);
      controller.close();
    },
  });
}

describe("AgentAccessClient", () => {
  it("calls Profile / Conversation / Run / Cancel / Decision APIs with Bearer token", async () => {
    const calls: Array<{ url: string; method: string; headers: Headers; body?: string }> = [];
    const fetchMock: typeof fetch = vi.fn(async (input, init) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      const headers = new Headers(init?.headers);
      calls.push({
        url,
        method,
        headers,
        body: typeof init?.body === "string" ? init.body : undefined,
      });
      assertNoAccessTokenInURL(url);

      if (url.endsWith("/profile")) {
        return new Response(
          JSON.stringify({
            object: "agent_profile",
            id: "agent-1",
            name: "Demo",
            description: "d",
            version: "1",
            supportedContent: [],
            capabilities: [],
            interactionRequirements: {},
          }),
          { status: 200, headers: { "content-type": "application/json" } },
        );
      }
      if (method === "POST" && url.endsWith("/conversations")) {
        return new Response(
          JSON.stringify({
            conversation: {
              object: "conversation",
              id: "c1",
              agentId: "agent-1",
              title: "t",
              status: "active",
              version: 1,
              createdAt: "2026-07-20T00:00:00Z",
              updatedAt: "2026-07-20T00:00:00Z",
              runs: [],
            },
            idempotent: false,
          }),
          { status: 201, headers: { "content-type": "application/json" } },
        );
      }
      if (method === "POST" && url.endsWith("/runs")) {
        return new Response(
          JSON.stringify({
            run: {
              object: "run",
              id: "r1",
              conversationId: "c1",
              agentId: "agent-1",
              status: "accepted",
              version: 1,
              startedAt: "2026-07-20T00:00:00Z",
              items: [],
              links: { events: "/events" },
            },
          }),
          { status: 202, headers: { "content-type": "application/json" } },
        );
      }
      if (url.includes(":cancel")) {
        return new Response(
          JSON.stringify({
            run: {
              object: "run",
              id: "r1",
              conversationId: "c1",
              agentId: "agent-1",
              status: "cancelled",
              version: 2,
              startedAt: "2026-07-20T00:00:00Z",
              items: [],
              links: { events: "/events" },
            },
          }),
          { status: 200, headers: { "content-type": "application/json" } },
        );
      }
      if (url.includes(":decide")) {
        return new Response(
          JSON.stringify({
            interaction: { id: "i1", kind: "approval", status: "approved", version: 2 },
            idempotent: false,
            links: { events: "/events" },
          }),
          { status: 200, headers: { "content-type": "application/json" } },
        );
      }
      if (method === "GET" && url.includes("/conversations/")) {
        return new Response(
          JSON.stringify({
            object: "conversation",
            id: "c1",
            agentId: "agent-1",
            title: "t",
            status: "active",
            version: 1,
            createdAt: "2026-07-20T00:00:00Z",
            updatedAt: "2026-07-20T00:00:00Z",
            runs: [],
          }),
          { status: 200, headers: { "content-type": "application/json" } },
        );
      }
      if (method === "GET" && url.includes("/runs/r1") && !url.includes("/events")) {
        return new Response(
          JSON.stringify({
            run: {
              object: "run",
              id: "r1",
              conversationId: "c1",
              agentId: "agent-1",
              status: "completed",
              version: 3,
              startedAt: "2026-07-20T00:00:00Z",
              items: [{ id: "m1", type: "message", status: "completed" }],
              links: { events: "/events" },
            },
          }),
          { status: 200, headers: { "content-type": "application/json" } },
        );
      }
      return new Response(JSON.stringify({ error: { code: "NOT_FOUND", message: "x", retryable: false } }), {
        status: 404,
        headers: { "content-type": "application/json" },
      });
    });

    const client = new AgentAccessClient({
      baseUrl: "https://example.test/api/agent-access/v1",
      tokenProvider: new StaticTokenProvider("secret-token"),
      fetch: fetchMock,
    });

    const profile = await client.getAgentProfile("ws", "agent-1");
    expect(profile.name).toBe("Demo");

    const created = await client.createConversation(
      "ws",
      "agent-1",
      { title: "t" },
      { idempotencyKey: "11111111-1111-4111-8111-111111111111" },
    );
    expect(created.conversation.id).toBe("c1");

    await client.getConversation("ws", "agent-1", "c1");
    const run = await client.createRun(
      "ws",
      "agent-1",
      {
        conversationId: "c1",
        stream: false,
        input: [{ type: "message", role: "user", content: [{ type: "text", text: "hi" }] }],
      },
      { idempotencyKey: "22222222-2222-4222-8222-222222222222" },
    );
    expect(run.run.id).toBe("r1");

    await client.getRun("ws", "agent-1", "r1");
    await client.cancelRun("ws", "agent-1", "r1", {
      idempotencyKey: "33333333-3333-4333-8333-333333333333",
      ifMatch: '"run:1"',
    });
    await client.decideInteraction(
      "ws",
      "agent-1",
      "r1",
      "i1",
      { decision: "approve" },
      { idempotencyKey: "44444444-4444-4444-8444-444444444444", ifMatch: '"1"' },
    );

    for (const call of calls) {
      expect(call.headers.get("Authorization")).toBe("Bearer secret-token");
      expect(call.url).not.toMatch(/access_token|token=/);
    }
    expect(calls.some((c) => c.headers.get("Idempotency-Key"))).toBe(true);
  });

  it("rejects base URLs that embed access tokens in the query string", () => {
    expect(
      () =>
        new AgentAccessClient({
          baseUrl: "https://example.test/api?access_token=abc",
          tokenProvider: new StaticTokenProvider("t"),
        }),
    ).toThrow(/token/i);
  });

  it("follows a golden SSE stream and reduces to the final snapshot", async () => {
    const events = loadJSONL("text");
    const sse = toSSE(events);
    const fetchMock: typeof fetch = vi.fn(async (input, init) => {
      assertNoAccessTokenInURL(input);
      const headers = new Headers(init?.headers);
      expect(headers.get("Authorization")).toBe("Bearer tok-1");
      expect(headers.get("Accept")).toBe("text/event-stream");
      return new Response(streamBody(sse), {
        status: 200,
        headers: { "content-type": "text/event-stream" },
      });
    });

    const client = new AgentAccessClient({
      baseUrl: "https://example.test/api/agent-access/v1",
      tokenProvider: new StaticTokenProvider("tok-1"),
      fetch: fetchMock,
      reconnectBackoffMs: 1,
    });

    let finalSnapshot = null;
    for await (const step of client.followRun("ws", "ag", "run-1")) {
      finalSnapshot = step.snapshot;
    }
    expect(finalSnapshot?.lastSequence).toBe(9);
    expect(finalSnapshot?.run?.status).toBe("completed");
    expect(finalSnapshot?.items).toHaveLength(1);
    expect(finalSnapshot?.items[0]).toMatchObject({
      content: [{ type: "text", text: "你好，欢迎使用 ActWeave。" }],
    });
  });

  it("refreshes token on TOKEN_EXPIRED and resumes with Last-Event-ID", async () => {
    const events = loadJSONL("text");
    const firstBatch = events.slice(0, 3);
    const secondBatch = events.slice(3);
    let tokensIssued = 0;
    const provider = new MemoryTokenProvider({
      refresh: async () => {
        tokensIssued += 1;
        return { accessToken: `tok-${tokensIssued}`, expiresIn: 600 };
      },
    });

    let streamCalls = 0;
    const seenAuth: string[] = [];
    const seenLastEventId: Array<string | null> = [];

    const fetchMock: typeof fetch = vi.fn(async (input, init) => {
      assertNoAccessTokenInURL(input);
      const headers = new Headers(init?.headers);
      seenAuth.push(headers.get("Authorization") ?? "");
      seenLastEventId.push(headers.get("Last-Event-ID"));
      streamCalls += 1;

      if (streamCalls === 1) {
        const body =
          toSSE(firstBatch) +
          `event: stream.error\ndata: ${JSON.stringify({
            specVersion: "1.0",
            type: "stream.error",
            occurredAt: "2026-07-20T01:00:04Z",
            error: {
              code: "TOKEN_EXPIRED",
              message: "access token expired",
              retryable: true,
            },
          })}\n\n`;
        return new Response(streamBody(body), {
          status: 200,
          headers: { "content-type": "text/event-stream" },
        });
      }

      // Resume must carry Last-Event-ID = 3
      expect(headers.get("Last-Event-ID")).toBe("3");
      expect(headers.get("Authorization")).toBe("Bearer tok-2");
      return new Response(streamBody(toSSE(secondBatch)), {
        status: 200,
        headers: { "content-type": "text/event-stream" },
      });
    });

    const client = new AgentAccessClient({
      baseUrl: "https://example.test/api/agent-access/v1",
      tokenProvider: provider,
      fetch: fetchMock,
      reconnectBackoffMs: 1,
    });

    const messages = [];
    for await (const message of client.streamRunEvents("ws", "ag", "run-1")) {
      messages.push(message);
    }

    expect(streamCalls).toBe(2);
    expect(tokensIssued).toBe(2);
    expect(seenAuth[0]).toBe("Bearer tok-1");
    expect(seenAuth[1]).toBe("Bearer tok-2");
    expect(seenLastEventId[0]).toBeNull();
    expect(seenLastEventId[1]).toBe("3");
    expect(messages.filter((m) => m.kind === "protocol_event")).toHaveLength(events.length);
    expect(messages.some((m) => m.kind === "transport_signal")).toBe(true);
  });

  it("reconnects after sequence_gap with Last-Event-ID", async () => {
    const events = loadJSONL("tool_success");
    // First connection: seq 1..2 then jump to 5 → gap after last=2
    const gapSSE =
      toSSE(events.slice(0, 2)) +
      toSSE([events[4]!]); // sequence 5 while last is 2

    let streamCalls = 0;
    const fetchMock: typeof fetch = vi.fn(async (_input, init) => {
      const headers = new Headers(init?.headers);
      streamCalls += 1;
      if (streamCalls === 1) {
        return new Response(streamBody(gapSSE), {
          status: 200,
          headers: { "content-type": "text/event-stream" },
        });
      }
      expect(headers.get("Last-Event-ID")).toBe("2");
      return new Response(streamBody(toSSE(events.slice(2))), {
        status: 200,
        headers: { "content-type": "text/event-stream" },
      });
    });

    const client = new AgentAccessClient({
      baseUrl: "https://example.test/api/agent-access/v1",
      tokenProvider: new StaticTokenProvider("tok"),
      fetch: fetchMock,
      reconnectBackoffMs: 1,
    });

    const protocol = [];
    for await (const message of client.streamRunEvents("ws", "ag", "run-1")) {
      if (message.kind === "protocol_event") {
        protocol.push(message.event.sequence);
      }
    }
    expect(streamCalls).toBe(2);
    // session de-duplicates eventIds on resume (seq 3+ already includes any overlap)
    expect(protocol[0]).toBe(1);
    expect(protocol[1]).toBe(2);
    expect(protocol).toContain(3);
    expect(protocol[protocol.length - 1]).toBe(10);
  });

  it("retries JSON API once after HTTP 401 with force-refresh", async () => {
    let n = 0;
    const provider = new MemoryTokenProvider({
      refresh: async () => {
        n += 1;
        return { accessToken: `tok-${n}`, expiresIn: 600 };
      },
    });
    let calls = 0;
    const fetchMock: typeof fetch = vi.fn(async (_input, init) => {
      calls += 1;
      const headers = new Headers(init?.headers);
      if (calls === 1) {
        expect(headers.get("Authorization")).toBe("Bearer tok-1");
        return new Response(
          JSON.stringify({ error: { code: "UNAUTHENTICATED", message: "expired", retryable: true } }),
          { status: 401, headers: { "content-type": "application/json" } },
        );
      }
      expect(headers.get("Authorization")).toBe("Bearer tok-2");
      return new Response(
        JSON.stringify({
          object: "agent_profile",
          id: "a",
          name: "n",
          description: "d",
          version: "1",
          supportedContent: [],
          capabilities: [],
          interactionRequirements: {},
        }),
        { status: 200, headers: { "content-type": "application/json" } },
      );
    });

    const client = new AgentAccessClient({
      baseUrl: "https://example.test/api/agent-access/v1",
      tokenProvider: provider,
      fetch: fetchMock,
    });
    const profile = await client.getAgentProfile("ws", "ag");
    expect(profile.id).toBe("a");
    expect(calls).toBe(2);
    expect(n).toBe(2);
  });
});
