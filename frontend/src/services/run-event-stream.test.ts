import { describe, expect, it } from "vitest";

import {
  applyStreamFrame,
  createProjectionState,
  isTerminalRunStatus,
  mapProtocolRunStatus,
  NOT_READY_MAX_ATTEMPTS,
  notReadyDelayMs,
  parseSSEBlock,
  type StreamFrame,
} from "./run-event-stream";

const runId = "01955555-5555-7555-8555-555555555555";
const itemId = "81000000-0000-4000-8000-000000000001";
const workspaceId = "11000000-0000-4000-8000-000000000001";
const agentId = "22000000-0000-4000-8000-000000000001";
const conversationId = "31000000-0000-4000-8000-000000000001";

function protocolEnvelope(type: string, sequence: number, data: Record<string, unknown>) {
  return {
    specVersion: "1.0",
    type,
    eventId: `a1000000-0000-4000-8000-${String(sequence).padStart(12, "0")}`,
    streamId: `run:${runId}`,
    sequence,
    occurredAt: "2026-07-20T01:00:00Z",
    workspaceId,
    agentId,
    conversationId,
    runId,
    traceId: "test-trace",
    data,
  };
}

function sseBlock(type: string, sequence: number, data: Record<string, unknown>) {
  return `id: ${sequence}\nevent: ${type}\ndata: ${JSON.stringify(protocolEnvelope(type, sequence, data))}\n\n`.trimEnd();
}

describe("run-event-stream parseSSEBlock", () => {
  it("parses protocol envelope frames (item.delta / run.completed)", () => {
    const block = sseBlock("item.delta", 4, {
      itemId,
      delta: { type: "text_delta", index: 0, text: "你好" },
    });
    const frame = parseSSEBlock(block, runId);
    expect(frame).toMatchObject({
      type: "item.delta",
      sequenceNo: 4,
      runId,
      kind: "protocol",
    });
    expect(frame?.data).toMatchObject({ itemId, delta: { type: "text_delta", text: "你好" } });
  });

  it("returns unknown frames without dropping the sequence cursor payload", () => {
    const block = sseBlock("future.annotation", 5, { annotation: { kind: "additive" } });
    const frame = parseSSEBlock(block, runId);
    expect(frame).toMatchObject({ type: "future.annotation", sequenceNo: 5, kind: "unknown" });
  });

  it("parses thin secondary legacy RUN_* frames (not sole live whitelist)", () => {
    const block = `id: 2\nevent: RUN_COMPLETED\ndata: ${JSON.stringify({ status: "SUCCEEDED" })}`;
    const frame = parseSSEBlock(block, runId);
    expect(frame).toMatchObject({ type: "RUN_COMPLETED", sequenceNo: 2, kind: "legacy" });
  });

  it("drops heartbeat / invalid blocks", () => {
    expect(parseSSEBlock(": ping\n", runId)).toBeUndefined();
    expect(parseSSEBlock("id: 1\nevent: run.started\ndata: not-json", runId)).toBeUndefined();
  });
});

describe("run-event-stream pure projection", () => {
  it("accumulates assistant text from item.delta and finalizes on run.completed", () => {
    let state = createProjectionState();
    const frames: StreamFrame[] = [
      parseSSEBlock(
        sseBlock("run.started", 2, {
          run: {
            id: runId,
            conversationId,
            agentId,
            status: "running",
            trigger: "message",
            startedAt: "2026-07-20T01:00:00Z",
          },
        }),
        runId,
      )!,
      parseSSEBlock(
        sseBlock("item.started", 3, {
          item: {
            id: itemId,
            type: "message",
            status: "in_progress",
            role: "assistant",
            content: [{ type: "text", text: "" }],
          },
        }),
        runId,
      )!,
      parseSSEBlock(
        sseBlock("item.delta", 4, { itemId, delta: { type: "text_delta", index: 0, text: "你好，" } }),
        runId,
      )!,
      parseSSEBlock(
        sseBlock("item.delta", 6, { itemId, delta: { type: "text_delta", index: 0, text: "欢迎使用 ActWeave。" } }),
        runId,
      )!,
      parseSSEBlock(
        sseBlock("item.completed", 7, {
          item: {
            id: itemId,
            type: "message",
            status: "completed",
            role: "assistant",
            content: [{ type: "text", text: "你好，欢迎使用 ActWeave。" }],
          },
        }),
        runId,
      )!,
      parseSSEBlock(
        sseBlock("run.completed", 9, {
          run: {
            id: runId,
            conversationId,
            agentId,
            status: "completed",
            trigger: "message",
            startedAt: "2026-07-20T01:00:00Z",
            completedAt: "2026-07-20T01:00:08Z",
          },
        }),
        runId,
      )!,
    ];

    const texts: string[] = [];
    let lastStatus: string | undefined;
    let terminal = false;
    for (const frame of frames) {
      const result = applyStreamFrame(state, frame);
      state = result.state;
      if (result.effects.assistantMessages[0]) {
        texts.push(result.effects.assistantMessages[0].content);
      }
      if (result.effects.runStatus) lastStatus = result.effects.runStatus;
      if (result.effects.terminal) terminal = true;
    }

    expect(texts).toContain("你好，");
    expect(texts).toContain("你好，欢迎使用 ActWeave。");
    expect(lastStatus).toBe("SUCCEEDED");
    expect(terminal).toBe(true);
    expect(state.assistantByItemId[itemId]?.content).toBe("你好，欢迎使用 ActWeave。");
    expect(isTerminalRunStatus(state.runStatus)).toBe(true);
  });

  // A chart must appear as the turn ends, not only after a reload, so the
  // completed item is where surfaces enter the Console projection.
  it("carries a2ui surfaces on item.completed and only for the current version", () => {
    const surface = { surfaceId: "srf_1", components: [{ id: "root", component: "Text", text: "hi" }] };
    const completed = (version: string) =>
      parseSSEBlock(
        sseBlock("item.completed", 7, {
          item: {
            id: itemId,
            type: "message",
            status: "completed",
            role: "assistant",
            content: [
              { type: "text", text: "看图。" },
              { type: "a2ui", version, catalogId: "c", surface },
            ],
          },
        }),
        runId,
      )!;

    const current = applyStreamFrame(createProjectionState(), completed("a2ui-surface.v1"));
    const patch = current.effects.assistantMessages[0];
    expect(patch?.content).toBe("看图。");
    expect(patch?.a2ui).toEqual([surface]);

    // An older surface version has no renderer in this build.
    const older = applyStreamFrame(createProjectionState(), completed("a2ui-surface.v0"));
    expect(older.effects.assistantMessages[0]?.content).toBe("看图。");
    expect(older.effects.assistantMessages[0]?.a2ui).toBeUndefined();
  });

  it("carries attachments on item.completed only", () => {
    const completed = parseSSEBlock(
      sseBlock("item.completed", 7, {
        item: {
          id: itemId,
          type: "message",
          status: "completed",
          role: "assistant",
          content: [
            { type: "text", text: "对账单已生成。" },
            {
              type: "output_file",
              fileId: "019f0000-0000-7000-8000-00000000f001",
              mediaType: "text/csv",
              filename: "invoice-2026-08.csv",
              sizeBytes: 4096,
            },
          ],
        },
      }),
      runId,
    )!;
    const patch = applyStreamFrame(createProjectionState(), completed).effects.assistantMessages[0];
    expect(patch?.content).toBe("对账单已生成。");
    expect(patch?.attachments).toEqual([
      {
        fileId: "019f0000-0000-7000-8000-00000000f001",
        mediaType: "text/csv",
        filename: "invoice-2026-08.csv",
        sizeBytes: 4096,
      },
    ]);

    const delta = parseSSEBlock(
      sseBlock("item.delta", 4, { itemId, delta: { type: "text_delta", index: 0, text: "…" } }),
      runId,
    )!;
    expect(applyStreamFrame(createProjectionState(), delta).effects.assistantMessages[0]?.attachments).toBeUndefined();
  });

  it("leaves a2ui absent while text is still streaming", () => {
    const state = createProjectionState();
    const delta = parseSSEBlock(
      sseBlock("item.delta", 4, { itemId, delta: { type: "text_delta", index: 0, text: "…" } }),
      runId,
    )!;
    expect(applyStreamFrame(state, delta).effects.assistantMessages[0]?.a2ui).toBeUndefined();
  });

  it("ignores unknown event types without aborting projection", () => {
    let state = createProjectionState();
    const started = parseSSEBlock(
      sseBlock("item.started", 1, {
        item: {
          id: itemId,
          type: "message",
          status: "in_progress",
          role: "assistant",
          content: [{ type: "text", text: "" }],
        },
      }),
      runId,
    )!;
    const unknown = parseSSEBlock(sseBlock("future.annotation", 2, { annotation: { kind: "x" } }), runId)!;
    const delta = parseSSEBlock(
      sseBlock("item.delta", 3, { itemId, delta: { type: "text_delta", index: 0, text: "ok" } }),
      runId,
    )!;

    state = applyStreamFrame(state, started).state;
    const unknownResult = applyStreamFrame(state, unknown);
    expect(unknownResult.effects.assistantMessages).toEqual([]);
    expect(unknownResult.effects.runStatus).toBeUndefined();
    state = unknownResult.state;
    expect(state.lastSequence).toBe(2);

    const after = applyStreamFrame(state, delta);
    expect(after.effects.assistantMessages[0]?.content).toBe("ok");
  });

  it("creates assistant bubble on item.delta even if item.started was missed", () => {
    const state = createProjectionState();
    const delta = parseSSEBlock(
      sseBlock("item.delta", 4, { itemId, delta: { type: "text_delta", index: 0, text: "late" } }),
      runId,
    )!;
    const result = applyStreamFrame(state, delta);
    expect(result.effects.assistantMessages[0]).toMatchObject({ id: itemId, content: "late", status: "PROCESSING" });
  });

  it("maps protocol run statuses", () => {
    expect(mapProtocolRunStatus("accepted")).toBe("PENDING");
    expect(mapProtocolRunStatus("running")).toBe("RUNNING");
    expect(mapProtocolRunStatus("waiting_interaction")).toBe("WAITING_CONFIRMATION");
    expect(mapProtocolRunStatus("completed")).toBe("SUCCEEDED");
    expect(mapProtocolRunStatus("failed")).toBe("FAILED");
    expect(mapProtocolRunStatus("cancelled")).toBe("CANCELLED");
  });

  it("maps run.waiting and run.resumed", () => {
    let state = createProjectionState();
    const waiting = parseSSEBlock(
      sseBlock("run.waiting", 5, {
        run: {
          id: runId,
          conversationId,
          agentId,
          status: "waiting_interaction",
          trigger: "api",
          startedAt: "2026-07-20T01:00:00Z",
        },
      }),
      runId,
    )!;
    let result = applyStreamFrame(state, waiting);
    expect(result.effects.runStatus).toBe("WAITING_CONFIRMATION");
    state = result.state;

    const resumed = parseSSEBlock(
      sseBlock("run.resumed", 6, {
        run: {
          id: runId,
          conversationId,
          agentId,
          status: "running",
          trigger: "api",
          startedAt: "2026-07-20T01:00:00Z",
        },
      }),
      runId,
    )!;
    result = applyStreamFrame(state, resumed);
    expect(result.effects.runStatus).toBe("RUNNING");
    expect(result.effects.clearPendingConfirmation).toBe(true);
  });
});

describe("run-event-stream 404 not-ready backoff", () => {
  it("uses 200ms → 500ms → 1s with a bounded attempt count", () => {
    expect(notReadyDelayMs(1)).toBe(200);
    expect(notReadyDelayMs(2)).toBe(500);
    expect(notReadyDelayMs(3)).toBe(1000);
    expect(notReadyDelayMs(15)).toBe(1000);
    expect(NOT_READY_MAX_ATTEMPTS).toBe(15);
  });
});
