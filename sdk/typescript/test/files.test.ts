import { describe, expect, it, vi } from "vitest";

import {
  AgentAccessClient,
  AgentClientError,
  assertNoAccessTokenInURL,
  SDK_PREFER_DOWNLOAD_TOKEN_BYTES,
  StaticTokenProvider,
  type AAPFile,
} from "../src/index.js";

function fileResource(overrides: Partial<AAPFile> = {}): AAPFile {
  return {
    object: "file",
    id: "file-1",
    agentId: "agent-1",
    status: "pending_upload",
    mediaType: "image/png",
    sizeBytes: 1024,
    purpose: "GENERAL",
    processing: { version: 1, stages: [] },
    artifacts: [],
    links: { content: "/workspaces/ws/agents/agent-1/files/file-1/content" },
    createdAt: "2026-07-31T00:00:00Z",
    updatedAt: "2026-07-31T00:00:00Z",
    ...overrides,
  };
}

describe("AgentAccessClient files", () => {
  it("createFile → putFileUpload → completeFile → waitUntilReady flow", async () => {
    const calls: Array<{ url: string; method: string; headers: Headers; body?: string }> = [];
    let getCount = 0;

    const fetchMock: typeof fetch = vi.fn(async (input, init) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      const headers = new Headers(init?.headers);
      const body = typeof init?.body === "string" ? init.body : undefined;
      calls.push({ url, method, headers, body });
      assertNoAccessTokenInURL(url);

      if (method === "POST" && url.endsWith("/files")) {
        expect(headers.get("Authorization")).toBe("Bearer tok");
        expect(headers.get("Idempotency-Key")).toBe("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa");
        expect(JSON.parse(body ?? "{}")).toMatchObject({
          mediaType: "image/png",
          sizeBytes: 4,
        });
        return new Response(
          JSON.stringify({
            file: fileResource({ status: "pending_upload", sizeBytes: 4 }),
            upload: {
              method: "PUT",
              url: "https://minio.example.test/staging/file-1?X-Amz-Signature=abc",
              headers: {
                "Content-Type": "image/png",
                "Content-Length": "4",
              },
              expiresAt: "2026-07-31T00:15:00Z",
            },
            idempotent: false,
          }),
          { status: 201, headers: { "content-type": "application/json" } },
        );
      }

      if (method === "PUT" && url.startsWith("https://minio.example.test/")) {
        expect(headers.get("Authorization")).toBeNull();
        expect(headers.get("Content-Type")).toBe("image/png");
        expect(headers.get("Content-Length")).toBe("4");
        const putBody = init?.body;
        expect(putBody).toBeTruthy();
        return new Response(null, { status: 200 });
      }

      if (method === "POST" && url.includes(":complete")) {
        expect(headers.get("Idempotency-Key")).toBe("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb");
        return new Response(
          JSON.stringify({
            file: fileResource({ status: "uploaded", sizeBytes: 4 }),
            idempotent: false,
          }),
          { status: 200, headers: { "content-type": "application/json" } },
        );
      }

      if (method === "GET" && url.endsWith("/files/file-1")) {
        getCount += 1;
        const status = getCount < 3 ? "processing" : "ready";
        const partial: Partial<AAPFile> = { status, sizeBytes: 4 };
        if (status === "ready") {
          partial.readyAt = "2026-07-31T00:01:00Z";
        }
        return new Response(
          JSON.stringify({
            file: fileResource(partial),
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
      tokenProvider: new StaticTokenProvider("tok"),
      fetch: fetchMock,
    });

    const created = await client.createFile(
      "ws",
      "agent-1",
      { mediaType: "image/png", sizeBytes: 4, filename: "a.png" },
      { idempotencyKey: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" },
    );
    expect(created.file.id).toBe("file-1");
    expect(created.upload?.headers["Content-Length"]).toBe("4");

    const bytes = new Uint8Array([1, 2, 3, 4]);
    await client.putFileUpload(created.upload!, bytes);

    const completed = await client.completeFile("ws", "agent-1", "file-1", undefined, {
      idempotencyKey: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
    });
    expect(completed.file.status).toBe("uploaded");

    const ready = await client.waitUntilReady("ws", "agent-1", "file-1", {
      pollIntervalMs: 1,
      timeoutMs: 5_000,
    });
    expect(ready.status).toBe("ready");
    expect(getCount).toBeGreaterThanOrEqual(3);

    // createRun with input_file part is typed / accepted by client JSON body
    const runCalls: string[] = [];
    const runFetch: typeof fetch = vi.fn(async (input, init) => {
      runCalls.push(String(input));
      return new Response(
        JSON.stringify({
          run: {
            object: "run",
            id: "r1",
            conversationId: "c1",
            agentId: "agent-1",
            status: "accepted",
            version: 1,
            startedAt: "2026-07-31T00:00:00Z",
            items: [],
            links: { events: "/events" },
          },
        }),
        { status: 202, headers: { "content-type": "application/json" } },
      );
    });
    const runClient = new AgentAccessClient({
      baseUrl: "https://example.test/api/agent-access/v1",
      tokenProvider: new StaticTokenProvider("tok"),
      fetch: runFetch,
    });
    await runClient.createRun(
      "ws",
      "agent-1",
      {
        conversationId: "c1",
        stream: false,
        input: [
          {
            type: "message",
            role: "user",
            content: [
              { type: "text", text: "describe" },
              { type: "input_file", fileId: "file-1" },
            ],
          },
        ],
      },
      { idempotencyKey: "cccccccc-cccc-4ccc-8ccc-cccccccccccc" },
    );
    expect(runCalls[0]).toContain("/runs");
  });

  it("putFileUpload rejects missing Content-Length/Type from create headers", async () => {
    const client = new AgentAccessClient({
      baseUrl: "https://example.test/api/agent-access/v1",
      tokenProvider: new StaticTokenProvider("tok"),
      fetch: vi.fn(),
    });
    await expect(
      client.putFileUpload(
        {
          method: "PUT",
          url: "https://minio.example.test/x",
          headers: { "Content-Type": "image/png" },
          expiresAt: "2026-07-31T00:15:00Z",
        },
        new Uint8Array([1]),
      ),
    ).rejects.toMatchObject({ code: "INVALID_UPLOAD_HEADERS" });
  });

  it("waitUntilReady throws on failed terminal status", async () => {
    const fetchMock: typeof fetch = vi.fn(async () => {
      return new Response(
        JSON.stringify({
          file: fileResource({
            status: "failed",
            error: { code: "FILE_PROCESSING_FAILED", message: "promote failed", retryable: false },
          }),
        }),
        { status: 200, headers: { "content-type": "application/json" } },
      );
    });
    const client = new AgentAccessClient({
      baseUrl: "https://example.test/api/agent-access/v1",
      tokenProvider: new StaticTokenProvider("tok"),
      fetch: fetchMock,
    });
    await expect(client.waitUntilReady("ws", "a", "file-1", { pollIntervalMs: 1, timeoutMs: 1000 })).rejects.toBeInstanceOf(
      AgentClientError,
    );
  });

  it("getFileContent uses Bearer content for small files", async () => {
    const calls: string[] = [];
    const fetchMock: typeof fetch = vi.fn(async (input, init) => {
      const url = String(input);
      calls.push(`${init?.method ?? "GET"} ${url}`);
      assertNoAccessTokenInURL(url);
      if (url.endsWith("/content")) {
        expect(new Headers(init?.headers).get("Authorization")).toBe("Bearer tok");
        return new Response(new Uint8Array([9, 8, 7]), {
          status: 200,
          headers: { "content-type": "image/png" },
        });
      }
      return new Response("nope", { status: 404 });
    });
    const client = new AgentAccessClient({
      baseUrl: "https://example.test/api/agent-access/v1",
      tokenProvider: new StaticTokenProvider("tok"),
      fetch: fetchMock,
    });
    const result = await client.getFileContent("ws", "agent-1", "file-1", {
      sizeBytes: 3,
    });
    expect(result.via).toBe("content");
    expect(result.contentType).toBe("image/png");
    expect(new Uint8Array(result.body)).toEqual(new Uint8Array([9, 8, 7]));
    expect(calls.some((c) => c.includes("/content"))).toBe(true);
    expect(calls.some((c) => c.includes(":download"))).toBe(false);
  });

  it("getFileContent prefers :download when sizeBytes > 4MiB", async () => {
    const calls: Array<{ url: string; auth: string | null }> = [];
    const fetchMock: typeof fetch = vi.fn(async (input, init) => {
      const url = String(input);
      const headers = new Headers(init?.headers);
      calls.push({ url, auth: headers.get("Authorization") });
      assertNoAccessTokenInURL(url);

      if (url.includes(":download")) {
        expect(headers.get("Authorization")).toBe("Bearer tok");
        return new Response(
          JSON.stringify({
            token: "tok-dl-1",
            expiresAt: "2026-07-31T00:10:00Z",
            url: "/api/agent-access/v1/files/downloads/tok-dl-1",
          }),
          { status: 200, headers: { "content-type": "application/json" } },
        );
      }
      if (url.includes("/files/downloads/tok-dl-1")) {
        // Path B: no Bearer
        expect(headers.get("Authorization")).toBeNull();
        return new Response(new Uint8Array([1, 1, 1]), {
          status: 200,
          headers: { "content-type": "application/pdf" },
        });
      }
      return new Response("nope", { status: 404 });
    });

    const client = new AgentAccessClient({
      baseUrl: "https://example.test/api/agent-access/v1",
      tokenProvider: new StaticTokenProvider("tok"),
      fetch: fetchMock,
    });

    const result = await client.getFileContent("ws", "agent-1", "file-1", {
      sizeBytes: SDK_PREFER_DOWNLOAD_TOKEN_BYTES + 1,
    });
    expect(result.via).toBe("download");
    expect(result.contentType).toBe("application/pdf");
    expect(calls.some((c) => c.url.includes(":download") && c.auth === "Bearer tok")).toBe(true);
    expect(calls.some((c) => c.url.includes("/files/downloads/") && c.auth === null)).toBe(true);
  });
});
