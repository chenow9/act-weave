import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { pathToFileURL } from "node:url";
import { describe, expect, it } from "vitest";

import {
  AgentAccessClient,
  MemoryTokenProvider,
} from "../../src/index.js";
import { loadGoldenJSONL, startMockAAP } from "./mock-aap-server.js";

/**
 * Dynamic import of M9-T6 examples so the SDK package does not hard-depend on them
 * at publish time; monorepo e2e still exercises both modes against a live mock AAP.
 */
async function loadExamples() {
  const examplesRoot = join(
    dirname(fileURLToPath(import.meta.url)),
    "../../../../examples/agent-access",
  );
  const bffServer = await import(pathToFileURL(join(examplesRoot, "bff/server.ts")).href);
  const mintServer = await import(pathToFileURL(join(examplesRoot, "direct/mint-server.ts")).href);
  const browserClient = await import(
    pathToFileURL(join(examplesRoot, "direct/browser-client.ts")).href
  );
  const env = await import(pathToFileURL(join(examplesRoot, "shared/env.ts")).href);
  return { bffServer, mintServer, browserClient, env };
}

describe("M9-T7 BFF and direct-token modes e2e", () => {
  it("BFF mode: browser talks only to BFF; BFF uses client_credentials against AAP", async () => {
    const events = loadGoldenJSONL("tool_success");
    const first = events[0]!;
    const mock = await startMockAAP({
      runs: { [first.runId]: events },
      clientId: "bff-client",
      clientSecret: "bff-secret",
    });
    const { bffServer, env } = await loadExamples();
    const started = await bffServer.startBFFServer({
      env: env.testEnv({
        aapBaseUrl: mock.baseUrl,
        clientId: "bff-client",
        clientSecret: "bff-secret",
        workspaceId: first.workspaceId,
        agentId: first.agentId,
        bffPort: 0,
        bffHost: "127.0.0.1",
      }),
    });
    try {
      // Without business session → denied.
      const denied = await fetch(`${started.baseUrl}/api/aap/runs/${first.runId}/events`);
      expect(denied.status).toBe(401);

      const ok = await fetch(`${started.baseUrl}/api/aap/runs/${first.runId}/events`, {
        headers: {
          Authorization: "Bearer business-session",
          Accept: "text/event-stream",
        },
      });
      expect(ok.status).toBe(200);
      expect(ok.headers.get("set-cookie")).toBeNull();
      const text = await ok.text();
      expect(text).toContain("run.completed");
      // AAP mock saw client_credentials-issued token (Bearer issued-token-N).
      expect(mock.eventAuthTokens.some((t) => t.startsWith("issued-token-"))).toBe(true);
      // Browser never hit AAP base URL with secrets — only BFF.
      expect(mock.issuedTokens.length).toBeGreaterThanOrEqual(1);
    } finally {
      await started.close();
      await mock.close();
    }
  });

  it("direct mode: mint Token Exchange → MemoryTokenProvider → AgentAccessClient followRun", async () => {
    const events = loadGoldenJSONL("text");
    const first = events[0]!;
    const mock = await startMockAAP({
      runs: { [first.runId]: events },
      clientId: "mint-client",
      clientSecret: "mint-secret",
    });
    const { mintServer, browserClient, env } = await loadExamples();
    const mint = await mintServer.startMintServer({
      env: env.testEnv({
        aapBaseUrl: mock.baseUrl,
        clientId: "mint-client",
        clientSecret: "mint-secret",
        workspaceId: first.workspaceId,
        agentId: first.agentId,
        mintPort: 0,
        mintHost: "127.0.0.1",
      }),
      mintSubjectToken: async (session: { subjectId: string }) => `subject-jwt:${session.subjectId}`,
    });
    try {
      const browser = browserClient.createBrowserDirectClient({
        aapBaseUrl: mock.baseUrl,
        mintUrl: `${mint.baseUrl}/api/aap/mint-token`,
        getBusinessAuthorization: () => "Bearer business-session",
      });

      const result = await browserClient.followRunInBrowser(
        browser,
        first.workspaceId,
        first.agentId,
        first.runId,
      );
      expect(result.terminalStatus).toBe("completed");
      expect(result.lastSequence).toBe(events.length);
      // Short token came from exchange (issued by mock oauth).
      expect(mock.issuedTokens.length).toBeGreaterThanOrEqual(1);
      expect(mock.eventAuthTokens[0]?.startsWith("issued-token-")).toBe(true);

      browser.clearAAPToken();
    } finally {
      await mint.close();
      await mock.close();
    }
  });

  it("direct mode TokenProvider can be constructed with MemoryTokenProvider alone against AAP", async () => {
    // Minimal direct path without full mint server — still exercises SDK TokenProvider contract.
    const events = loadGoldenJSONL("workflow_tool");
    const first = events[0]!;
    const mock = await startMockAAP({
      runs: { [first.runId]: events },
      clientId: "cid",
      clientSecret: "sec",
    });
    try {
      const provider = new MemoryTokenProvider({
        refresh: async () => {
          const res = await fetch(`${mock.baseUrl}/oauth/token`, {
            method: "POST",
            headers: {
              Authorization: "Basic " + Buffer.from("cid:sec").toString("base64"),
              "Content-Type": "application/x-www-form-urlencoded",
            },
            body: new URLSearchParams({
              grant_type: "urn:ietf:params:oauth:grant-type:token-exchange",
              agent_id: first.agentId,
              scope: "event:read",
              subject_token: "subject-jwt",
              subject_token_type: "urn:ietf:params:oauth:token-type:jwt",
            }),
          });
          const body = (await res.json()) as { access_token: string; expires_in: number };
          return { accessToken: body.access_token, expiresIn: body.expires_in };
        },
      });
      const client = new AgentAccessClient({
        baseUrl: mock.baseUrl,
        tokenProvider: provider,
        reconnectBackoffMs: 1,
      });
      let lastSeq = 0;
      for await (const step of client.followRun(first.workspaceId, first.agentId, first.runId)) {
        lastSeq = step.snapshot.lastSequence;
      }
      expect(lastSeq).toBe(events.length);
    } finally {
      await mock.close();
    }
  });
});
