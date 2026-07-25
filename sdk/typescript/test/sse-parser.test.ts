import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

import {
  AAPSESession,
  assertNoAccessTokenInURL,
  openAAPSEStream,
  SSEFrameParser,
} from "../src/index.js";

const fixtureDir = join(dirname(fileURLToPath(import.meta.url)), "fixtures");

function readFixture(name: string): string {
  return readFileSync(join(fixtureDir, name), "utf8");
}

function encodeChunks(text: string, sizes: number[]): Uint8Array[] {
  const bytes = new TextEncoder().encode(text);
  const chunks: Uint8Array[] = [];
  let offset = 0;
  let sizeIndex = 0;
  while (offset < bytes.length) {
    const size = sizes[sizeIndex % sizes.length] ?? 1;
    sizeIndex += 1;
    chunks.push(bytes.subarray(offset, Math.min(bytes.length, offset + size)));
    offset += size;
  }
  return chunks;
}

function streamFromChunks(chunks: Uint8Array[]): ReadableStream<Uint8Array> {
  let index = 0;
  return new ReadableStream({
    pull(controller) {
      if (index >= chunks.length) {
        controller.close();
        return;
      }
      controller.enqueue(chunks[index]!);
      index += 1;
    },
  });
}

describe("sse-parser", () => {
  it("parses single-line protocol frames and multi-chunk UTF-8", () => {
    const parser = new SSEFrameParser();
    const text =
      "id: 4\nevent: item.delta\ndata: {\"type\":\"item.delta\",\"eventId\":\"e1\",\"sequence\":4,\"data\":{\"delta\":{\"text\":\"你好，世界\"}}}\n\n";
    const chunks = encodeChunks(text, [1, 2, 3, 5, 8, 13]);
    const frames = [];
    for (const chunk of chunks) {
      frames.push(...parser.push(chunk));
    }
    frames.push(...parser.flush());
    expect(frames).toHaveLength(1);
    expect(frames[0]?.id).toBe("4");
    expect(frames[0]?.event).toBe("item.delta");
    expect(frames[0]?.data).toContain("你好，世界");
  });

  it("joins multi-line data fields with newlines", () => {
    const parser = new SSEFrameParser();
    const frames = parser.push("event: custom\ndata: line1\ndata: line2\n\n");
    expect(frames).toHaveLength(1);
    expect(frames[0]?.data).toBe("line1\nline2");
  });

  it("emits heartbeat comments without treating them as protocol events", () => {
    const session = new AAPSESession();
    const parser = new SSEFrameParser();
    const frames = parser.push(": ping 2026-07-20T01:00:00Z\n\n");
    expect(frames).toHaveLength(1);
    const messages = session.pushFrame(frames[0]!);
    expect(messages).toEqual([{ kind: "heartbeat", comment: "ping 2026-07-20T01:00:00Z" }]);
    expect(session.getLastSequence()).toBeNull();
    expect(session.getLastEventId()).toBeUndefined();
  });

  it("does not advance cursor for stream.error transport signals", () => {
    const session = new AAPSESession({ initialLastSequence: 3 });
    const frame = {
      event: "stream.error",
      data: JSON.stringify({
        specVersion: "1.0",
        type: "stream.error",
        occurredAt: "2026-07-20T01:00:00Z",
        error: { code: "TOKEN_EXPIRED", message: "token expired", retryable: true },
      }),
      comments: [] as string[],
    };
    const messages = session.pushFrame(frame);
    expect(messages).toHaveLength(1);
    expect(messages[0]?.kind).toBe("transport_signal");
    expect(session.getLastSequence()).toBe(3);
    expect(session.getLastEventId()).toBe("3");
  });

  it("de-duplicates by eventId and detects sequence gaps", () => {
    const session = new AAPSESession();
    const base = {
      specVersion: "1.0",
      streamId: "run:r1",
      occurredAt: "2026-07-20T01:00:00Z",
      workspaceId: "w",
      agentId: "a",
      conversationId: "c",
      runId: "r1",
      traceId: "t",
      data: {},
    };

    const first = session.pushFrame({
      id: "1",
      event: "run.accepted",
      data: JSON.stringify({ ...base, type: "run.accepted", eventId: "e1", sequence: 1 }),
      comments: [],
    });
    expect(first[0]?.kind).toBe("protocol_event");

    const duplicate = session.pushFrame({
      id: "1",
      event: "run.accepted",
      data: JSON.stringify({ ...base, type: "run.accepted", eventId: "e1", sequence: 1 }),
      comments: [],
    });
    expect(duplicate[0]?.kind).toBe("duplicate");

    const gap = session.pushFrame({
      id: "4",
      event: "item.delta",
      data: JSON.stringify({ ...base, type: "item.delta", eventId: "e4", sequence: 4 }),
      comments: [],
    });
    expect(gap).toEqual([
      {
        kind: "sequence_gap",
        expected: 2,
        actual: 4,
        lastEventId: "1",
      },
    ]);
    expect(session.getLastEventId()).toBe("1");
  });

  it("advances cursor for unknown event types", () => {
    const session = new AAPSESession({ initialLastSequence: 4 });
    const messages = session.pushFrame({
      id: "5",
      event: "future.annotation",
      data: JSON.stringify({
        specVersion: "1.0",
        type: "future.annotation",
        eventId: "e5",
        streamId: "run:r1",
        sequence: 5,
        occurredAt: "2026-07-20T01:00:04Z",
        workspaceId: "w",
        agentId: "a",
        conversationId: "c",
        runId: "r1",
        traceId: "t",
        data: { annotation: { kind: "additive" } },
      }),
      comments: [],
    });
    expect(messages[0]).toMatchObject({
      kind: "protocol_event",
      unknownType: true,
      sseId: 5,
    });
    expect(session.getLastSequence()).toBe(5);
  });

  it("supports disconnect then resume with Last-Event-ID semantics", async () => {
    const firstBody = streamFromChunks([new TextEncoder().encode(readFixture("disconnect-resume.sse"))]);
    const first = openAAPSEStream(firstBody);
    const applied: number[] = [];
    for await (const message of first.messages) {
      if (message.kind === "protocol_event") {
        applied.push(message.sseId);
      }
    }
    expect(applied).toEqual([1, 2, 3]);
    expect(first.session.getLastEventId()).toBe("3");
    expect(first.session.resumeHeaders().get("Last-Event-ID")).toBe("3");

    // Server re-sends sequence 3 (at-least-once) then continues.
    const resumeBody = streamFromChunks([new TextEncoder().encode(readFixture("resume-after-3.sse"))]);
    const resumed = openAAPSEStream(resumeBody, { session: first.session });
    const resumedSequences: number[] = [];
    const kinds: string[] = [];
    for await (const message of resumed.messages) {
      kinds.push(message.kind);
      if (message.kind === "protocol_event") {
        resumedSequences.push(message.sseId);
      }
    }
    expect(kinds).toContain("duplicate");
    expect(resumedSequences).toEqual([4, 5, 6]);
    expect(resumed.session.getLastEventId()).toBe("6");
  });

  it("aborts ReadableStream consumption", async () => {
    const controller = new AbortController();
    let pullCount = 0;
    const body = new ReadableStream<Uint8Array>({
      pull(c) {
        pullCount += 1;
        if (pullCount === 1) {
          c.enqueue(new TextEncoder().encode("id: 1\nevent: run.accepted\ndata: {"));
          controller.abort();
          return;
        }
        c.enqueue(
          new TextEncoder().encode(
            '"specVersion":"1.0","type":"run.accepted","eventId":"e1","streamId":"run:r","sequence":1,"occurredAt":"2026-07-20T01:00:00Z","workspaceId":"w","agentId":"a","conversationId":"c","runId":"r","traceId":"t","data":{}}\n\n',
          ),
        );
      },
    });
    const { messages } = openAAPSEStream(body, { signal: controller.signal });
    await expect(async () => {
      for await (const _ of messages) {
        // drain
      }
    }).rejects.toMatchObject({ name: "AbortError" });
  });

  it("rejects access tokens in query strings", () => {
    expect(() =>
      assertNoAccessTokenInURL("https://api.example.test/events?access_token=secret"),
    ).toThrow(/must not carry credentials/);
    expect(() =>
      assertNoAccessTokenInURL("https://api.example.test/events?foo=eyJhbGciOiJIUzI1NiJ9.abc.def"),
    ).toThrow(/credential-like/);
    expect(() => assertNoAccessTokenInURL("https://api.example.test/events?cursor=42")).not.toThrow();
  });

  it("reads a multi-chunk stream end-to-end", async () => {
    const text = readFixture("disconnect-resume.sse");
    const chunks = encodeChunks(text, [7, 11, 17, 23]);
    const { messages, session } = openAAPSEStream(streamFromChunks(chunks));
    const types: string[] = [];
    for await (const message of messages) {
      if (message.kind === "protocol_event") {
        types.push(message.event.type);
      }
      if (message.kind === "heartbeat") {
        types.push("heartbeat");
      }
    }
    expect(types).toEqual(["run.accepted", "run.started", "heartbeat", "item.started"]);
    expect(session.getLastSequence()).toBe(3);
  });
});
