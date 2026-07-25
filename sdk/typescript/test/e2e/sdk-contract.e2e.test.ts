import { describe, expect, it } from "vitest";

import {
  AgentAccessClient,
  MemoryTokenProvider,
  RunReducer,
  type ProtocolEventEnvelope,
} from "../../src/index.js";
import {
  loadGoldenJSONL,
  loadGoldenSnapshot,
  startMockAAP,
  type GoldenEnvelope,
} from "./mock-aap-server.js";

function normalize(value: unknown): unknown {
  return JSON.parse(JSON.stringify(value));
}

describe("M9-T7 SDK contract e2e (golden + recovery)", () => {
  const traces = ["text", "tool_success", "workflow_tool", "approval_resume"] as const;

  for (const name of traces) {
    it(`followRun reduces golden ${name} to schema snapshot`, async () => {
      const events = loadGoldenJSONL(name);
      const expected = loadGoldenSnapshot(name);
      const first = events[0]!;
      const mock = await startMockAAP({
        runs: { [first.runId]: events },
      });
      try {
        const client = new AgentAccessClient({
          baseUrl: mock.baseUrl,
          tokenProvider: new MemoryTokenProvider({
            refresh: async () => ({ accessToken: "issued-token-static", expiresIn: 600 }),
          }),
          reconnectBackoffMs: 1,
        });
        // Seed provider without going through oauth for pure stream contract.
        // (Token Exchange / client_credentials covered in separate cases.)
        let lastSnapshot = null as ReturnType<RunReducer["snapshot"]> | null;
        for await (const step of client.followRun(first.workspaceId, first.agentId, first.runId)) {
          lastSnapshot = step.snapshot;
        }
        expect(lastSnapshot).not.toBeNull();
        expect(normalize(lastSnapshot!.run)).toEqual(normalize((expected as { run: unknown }).run));
        expect(normalize(lastSnapshot!.items)).toEqual(
          normalize((expected as { items: unknown }).items),
        );
        expect(normalize(lastSnapshot!.interactions)).toEqual(
          normalize((expected as { interactions: unknown }).interactions),
        );
        expect(normalize(lastSnapshot!.usage)).toEqual(
          normalize((expected as { usage: unknown }).usage),
        );
        expect(lastSnapshot!.lastSequence).toBe((expected as { lastSequence: number }).lastSequence);
        // Unknown additive events must not hide final items (text has future.annotation).
        if (name === "text") {
          expect(events.some((e) => e.type === "future.annotation")).toBe(true);
          expect(lastSnapshot!.items).toHaveLength(1);
        }
      } finally {
        await mock.close();
      }
    });
  }

  it("TOKEN_EXPIRED refreshes token and resumes from Last-Event-ID without duplicate apply", async () => {
    const events = loadGoldenJSONL("text");
    const first = events[0]!;
    const mock = await startMockAAP({
      runs: { [first.runId]: events },
      expireAfterSequence: 3,
    });
    try {
      let n = 0;
      const provider = new MemoryTokenProvider({
        refresh: async () => {
          n += 1;
          return { accessToken: `issued-token-${n}`, expiresIn: 600 };
        },
      });
      const client = new AgentAccessClient({
        baseUrl: mock.baseUrl,
        tokenProvider: provider,
        reconnectBackoffMs: 1,
      });

      const appliedEventIds: string[] = [];
      const reducer = new RunReducer();
      for await (const message of client.streamRunEvents(first.workspaceId, first.agentId, first.runId)) {
        if (message.kind === "protocol_event") {
          // Side-effect simulation: applying twice would throw REDUCE_SEQUENCE.
          reducer.apply(message.event as ProtocolEventEnvelope);
          appliedEventIds.push(message.event.eventId);
        }
      }

      expect(n).toBeGreaterThanOrEqual(2);
      expect(mock.eventRequestCount).toBeGreaterThanOrEqual(2);
      // First connection used issued-token-1; resume used a refreshed token.
      expect(mock.eventAuthTokens[0]).toBe("issued-token-1");
      expect(mock.eventAuthTokens.some((t) => t !== mock.eventAuthTokens[0])).toBe(true);
      // No duplicate eventIds applied (at-least-once de-dup + contiguous reduce).
      expect(new Set(appliedEventIds).size).toBe(appliedEventIds.length);
      expect(reducer.snapshot().lastSequence).toBe(events.length);
      expect(reducer.snapshot().run?.status).toBe("completed");
    } finally {
      await mock.close();
    }
  });

  it("rejects token query on the mock AAP (SDK never places tokens in URL)", async () => {
    const events = loadGoldenJSONL("text");
    const first = events[0]!;
    const mock = await startMockAAP({ runs: { [first.runId]: events } });
    try {
      const res = await fetch(
        `${mock.baseUrl}/workspaces/${first.workspaceId}/agents/${first.agentId}/runs/${first.runId}/events?access_token=leaked`,
        { headers: { Accept: "text/event-stream" } },
      );
      expect(res.status).toBe(401);
    } finally {
      await mock.close();
    }
  });

  it("client_credentials and token-exchange mint short tokens for TokenProvider", async () => {
    const mock = await startMockAAP({
      runs: {},
      clientId: "cid",
      clientSecret: "csec",
    });
    try {
      const cc = await fetch(`${mock.baseUrl}/oauth/token`, {
        method: "POST",
        headers: {
          Authorization: "Basic " + Buffer.from("cid:csec").toString("base64"),
          "Content-Type": "application/x-www-form-urlencoded",
        },
        body: new URLSearchParams({
          grant_type: "client_credentials",
          agent_id: "ag",
          scope: "event:read",
        }),
      });
      expect(cc.status).toBe(200);
      expect(cc.headers.get("cache-control")).toMatch(/no-store/i);
      const ccBody = (await cc.json()) as { access_token: string; expires_in: number };
      expect(ccBody.access_token.startsWith("issued-token-")).toBe(true);
      expect(ccBody.expires_in).toBe(600);

      const ex = await fetch(`${mock.baseUrl}/oauth/token`, {
        method: "POST",
        headers: {
          Authorization: "Basic " + Buffer.from("cid:csec").toString("base64"),
          "Content-Type": "application/x-www-form-urlencoded",
        },
        body: new URLSearchParams({
          grant_type: "urn:ietf:params:oauth:grant-type:token-exchange",
          agent_id: "ag",
          scope: "event:read",
          subject_token: "subject-jwt",
          subject_token_type: "urn:ietf:params:oauth:token-type:jwt",
        }),
      });
      expect(ex.status).toBe(200);
      const exBody = (await ex.json()) as {
        access_token: string;
        issued_token_type: string;
      };
      expect(exBody.issued_token_type).toContain("access_token");
      expect(JSON.stringify(exBody)).not.toContain("subject-jwt");
    } finally {
      await mock.close();
    }
  });
});

// Re-export type usage guard
export type { GoldenEnvelope };
