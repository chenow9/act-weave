import { afterEach, describe, expect, it, vi } from "vitest";
import type {
  Conversation,
  InputFileContentPart,
  OutputFileContentPart,
  ProtocolItem,
  ReducedRunSnapshot,
} from "@actweave/agent-client";

import {
  chronologicalRuns,
  clearStoredConversationId,
  conversationStorageKey,
  extractInputFileParts,
  extractOutputFileParts,
  filePartAsOutputRef,
  httpStatusOf,
  placeholderAttachment,
  readStoredConversationId,
  reconcileAssistantAttachments,
  replayMessagesFromItems,
  restoreConversationReplay,
  runItemsNeedCatchUp,
  shouldDropStoredConversation,
  unwrapRunResponse,
  writeStoredConversationId,
  type AttachmentCard,
  type ConversationReplayClient,
} from "./aap";

const csv: OutputFileContentPart = {
  type: "output_file",
  fileId: "019f0000-0000-7000-8000-00000000f001",
  filename: "invoice-2026-08.csv",
  mediaType: "text/csv",
  sizeBytes: 128,
};

const png: OutputFileContentPart = {
  type: "output_file",
  fileId: "019f0000-0000-7000-8000-00000000f002",
  filename: "preview.png",
  mediaType: "image/png",
  sizeBytes: 89,
};

function assistantItem(content: unknown[]): ProtocolItem {
  return {
    id: "msg-assistant",
    type: "message",
    status: "completed",
    role: "assistant",
    content,
  };
}

describe("extractOutputFileParts", () => {
  it("collects assistant output_file parts in order and dedupes fileId", () => {
    const items: ProtocolItem[] = [
      {
        id: "msg-user",
        type: "message",
        status: "completed",
        role: "user",
        content: [{ type: "output_file", fileId: "should-ignore", filename: "nope.csv" }],
      },
      assistantItem([
        { type: "text", text: "这是本月对账单" },
        csv,
        { type: "output_file", fileId: csv.fileId, filename: "dup.csv" },
        png,
      ]),
      { id: "tool", type: "tool_call", status: "completed", name: "actweave.publish_attachment" },
    ];
    expect(extractOutputFileParts(items).map((part) => part.fileId)).toEqual([csv.fileId, png.fileId]);
  });

  it("returns an empty list when the snapshot has no assistant files", () => {
    expect(
      extractOutputFileParts([
        assistantItem([{ type: "text", text: "hello" }]),
        { id: "u", type: "message", status: "completed", role: "user", content: [] },
      ]),
    ).toEqual([]);
  });
});

describe("reconcileAssistantAttachments", () => {
  it("places only unseen ids into toHydrate and keeps ready cards", () => {
    const ready: AttachmentCard = {
      ...placeholderAttachment(csv),
      status: "ready",
      previewUrl: "blob:ready-csv",
    };
    const first = reconcileAssistantAttachments([ready], [csv, png]);
    expect(first.next.map((a) => a.fileId)).toEqual([csv.fileId, png.fileId]);
    expect(first.next[0]).toBe(ready);
    expect(first.next[0]?.previewUrl).toBe("blob:ready-csv");
    expect(first.toHydrate.map((p) => p.fileId)).toEqual([png.fileId]);
    expect(first.next[1]?.status).toBe("uploading");

    const second = reconcileAssistantAttachments(first.next, [csv, png]);
    expect(second.toHydrate).toEqual([]);
    expect(second.next[0]).toBe(ready);
    expect(second.next[1]).toBe(first.next[1]);
  });

  it("keeps uploading and error rows instead of resetting them", () => {
    const uploading: AttachmentCard = { ...placeholderAttachment(csv), status: "uploading" };
    const failed: AttachmentCard = {
      ...placeholderAttachment(png),
      status: "error",
      error: "gone",
    };
    const { next, toHydrate } = reconcileAssistantAttachments([uploading, failed], [csv, png]);
    expect(toHydrate).toEqual([]);
    expect(next[0]).toBe(uploading);
    expect(next[1]).toBe(failed);
  });

  it("revokes and drops cards whose fileId left the snapshot", () => {
    const revoke = vi.fn();
    const orig = URL.revokeObjectURL;
    URL.revokeObjectURL = revoke;
    try {
      const gone: AttachmentCard = {
        ...placeholderAttachment(png),
        status: "ready",
        previewUrl: "blob:old-png",
      };
      const kept: AttachmentCard = { ...placeholderAttachment(csv), status: "ready" };
      const { next, toHydrate } = reconcileAssistantAttachments([kept, gone], [csv]);
      expect(next).toEqual([kept]);
      expect(toHydrate).toEqual([]);
      expect(revoke).toHaveBeenCalledWith("blob:old-png");
    } finally {
      URL.revokeObjectURL = orig;
    }
  });
});

const userPng: InputFileContentPart = {
  type: "input_file",
  fileId: "019f0000-0000-7000-8000-00000000f010",
  mediaType: "image/png",
};

function memorySessionStorage() {
  const data = new Map<string, string>();
  return {
    getItem(key: string) {
      return data.has(key) ? data.get(key)! : null;
    },
    setItem(key: string, value: string) {
      data.set(key, String(value));
    },
    removeItem(key: string) {
      data.delete(key);
    },
    clear() {
      data.clear();
    },
  };
}

describe("conversation sessionStorage", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("namespaces the key by workspace and agent", () => {
    expect(conversationStorageKey("ws-1", "ag-1")).toBe("aap-chat:ws-1:ag-1:conversationId");
    expect(conversationStorageKey("ws-1", "ag-2")).not.toBe(conversationStorageKey("ws-1", "ag-1"));
  });

  it("reads, writes, and clears only the matching workspace+agent key", () => {
    vi.stubGlobal("sessionStorage", memorySessionStorage());
    writeStoredConversationId("ws-a", "ag-a", "conv-a");
    writeStoredConversationId("ws-b", "ag-b", "conv-b");
    expect(readStoredConversationId("ws-a", "ag-a")).toBe("conv-a");
    expect(readStoredConversationId("ws-b", "ag-b")).toBe("conv-b");
    clearStoredConversationId("ws-a", "ag-a");
    expect(readStoredConversationId("ws-a", "ag-a")).toBe("");
    expect(readStoredConversationId("ws-b", "ag-b")).toBe("conv-b");
  });

  it("stays empty when sessionStorage is unavailable", () => {
    vi.stubGlobal("sessionStorage", undefined);
    expect(readStoredConversationId("ws", "ag")).toBe("");
    writeStoredConversationId("ws", "ag", "conv");
    expect(readStoredConversationId("ws", "ag")).toBe("");
  });
});

describe("extractInputFileParts", () => {
  it("collects user input_file parts and ignores assistant output_file", () => {
    const items: ProtocolItem[] = [
      {
        id: "msg-user",
        type: "message",
        status: "completed",
        role: "user",
        content: [{ type: "text", text: "看这张图" }, userPng, { type: "input_file", fileId: userPng.fileId }],
      },
      assistantItem([csv, { type: "text", text: "对账单" }]),
    ];
    expect(extractInputFileParts(items).map((part) => part.fileId)).toEqual([userPng.fileId]);
  });
});

describe("run snapshot helpers", () => {
  it("unwraps both {run} and bare GET bodies", () => {
    const bare = {
      object: "run" as const,
      id: "r1",
      conversationId: "c1",
      agentId: "a1",
      status: "completed" as const,
      version: 1,
      startedAt: "2026-08-01T00:00:00Z",
      items: [{ id: "m1", type: "message", status: "completed" }],
      links: { events: "/events" },
    };
    expect(unwrapRunResponse({ run: bare })?.id).toBe("r1");
    expect(unwrapRunResponse(bare)?.id).toBe("r1");
    expect(unwrapRunResponse(null)).toBeNull();
  });

  it("needs catch-up when items are missing or message content was not projected", () => {
    const base = {
      object: "run" as const,
      id: "r1",
      conversationId: "c1",
      agentId: "a1",
      status: "completed" as const,
      version: 1,
      startedAt: "2026-08-01T00:00:00Z",
      links: { events: "/events" },
    };
    expect(runItemsNeedCatchUp({ ...base, items: [] })).toBe(true);
    expect(
      runItemsNeedCatchUp({
        ...base,
        items: [{ id: "m1", type: "message", status: "completed" }],
      }),
    ).toBe(true);
    expect(
      runItemsNeedCatchUp({
        ...base,
        items: [assistantItem([{ type: "text", text: "ok" }])],
      }),
    ).toBe(false);
  });

  it("reverses newest-first conversation runs", () => {
    const conversation: Conversation = {
      object: "conversation",
      id: "c1",
      agentId: "a1",
      title: "t",
      status: "active",
      version: 1,
      createdAt: "2026-08-01T00:00:00Z",
      updatedAt: "2026-08-01T00:02:00Z",
      runs: [
        { object: "run", id: "r-new", status: "completed", version: 1, startedAt: "2026-08-01T00:02:00Z" },
        { object: "run", id: "r-old", status: "completed", version: 1, startedAt: "2026-08-01T00:01:00Z" },
      ],
    };
    expect(chronologicalRuns(conversation).map((r) => r.id)).toEqual(["r-old", "r-new"]);
  });

  it("drops stored ids only for gone/invalid conversations", () => {
    expect(shouldDropStoredConversation({ status: 404 })).toBe(true);
    expect(shouldDropStoredConversation({ status: 422 })).toBe(true);
    expect(shouldDropStoredConversation({ status: 401 })).toBe(false);
    expect(httpStatusOf(new Error("nope"))).toBe(0);
  });
});

describe("replayMessagesFromItems", () => {
  it("hydrates user input_file and assistant output_file on the matching bubbles", () => {
    const items: ProtocolItem[] = [
      {
        id: "u1",
        type: "message",
        status: "completed",
        role: "user",
        content: [{ type: "text", text: "导出对账单" }, userPng],
      },
      { id: "tool", type: "tool_call", status: "completed", name: "actweave.publish_attachment" },
      assistantItem([{ type: "text", text: "这是本月对账单" }, csv]),
    ];
    const replayed = replayMessagesFromItems(items, "2026-08-01T00:00:00Z");
    expect(replayed).toHaveLength(2);
    expect(replayed[0]).toMatchObject({
      role: "user",
      text: "导出对账单",
      files: [filePartAsOutputRef(userPng)],
    });
    expect(replayed[1]?.role).toBe("assistant");
    expect(replayed[1]?.files.map((f) => f.fileId)).toEqual([csv.fileId]);
    expect(replayed[1]?.tools?.[0]?.name).toBe("actweave.publish_attachment");
  });
});

describe("restoreConversationReplay", () => {
  const csvItem = assistantItem([{ type: "text", text: "bill" }, csv]);

  function conversation(runs: Conversation["runs"]): Conversation {
    return {
      object: "conversation",
      id: "c1",
      agentId: "a1",
      title: "t",
      status: "active",
      version: 1,
      createdAt: "2026-08-01T00:00:00Z",
      updatedAt: "2026-08-01T00:02:00Z",
      runs,
    };
  }

  it("loads getConversation then each getRun and skips followRun when items are complete", async () => {
    const followRun = vi.fn(async function* () {
      yield { snapshot: emptySnapshot([]) };
    });
    const client: ConversationReplayClient = {
      getConversation: vi.fn(async () =>
        conversation([
          { object: "run", id: "r-new", status: "completed", version: 1, startedAt: "2026-08-01T00:02:00Z" },
          { object: "run", id: "r-old", status: "completed", version: 1, startedAt: "2026-08-01T00:01:00Z" },
        ]),
      ),
      getRun: vi.fn(async (_ws, _ag, runId) => ({
        run: apiRun(runId, [
          {
            id: `u-${runId}`,
            type: "message",
            status: "completed",
            role: "user",
            content: [{ type: "text", text: runId }, userPng],
          },
          csvItem,
        ]),
      })),
      followRun,
    };

    const result = await restoreConversationReplay({
      aapBaseUrl: "https://aap.example/v1",
      workspaceId: "ws",
      agentId: "a1",
      conversationId: "c1",
      client,
    });
    expect(result.conversationId).toBe("c1");
    expect(result.messages.map((m) => m.text)).toEqual(["r-old", "bill", "r-new", "bill"]);
    expect(result.messages.filter((m) => m.role === "user").every((m) => m.files[0]?.fileId === userPng.fileId)).toBe(
      true,
    );
    expect(result.messages.filter((m) => m.role === "assistant").every((m) => m.files[0]?.fileId === csv.fileId)).toBe(
      true,
    );
    expect(followRun).not.toHaveBeenCalled();
  });

  it("catch-up from sequence 0 when run.items are empty, then extracts parts", async () => {
    const followOpts: unknown[] = [];
    const client: ConversationReplayClient = {
      getConversation: vi.fn(async () =>
        conversation([{ object: "run", id: "r1", status: "completed", version: 1, startedAt: "2026-08-01T00:00:00Z" }]),
      ),
      getRun: vi.fn(async () => ({ run: apiRun("r1", []) })),
      followRun: vi.fn(async function* (_ws, _ag, _runId, options) {
        followOpts.push(options);
        yield {
          snapshot: emptySnapshot([
            {
              id: "u1",
              type: "message",
              status: "completed",
              role: "user",
              content: [{ type: "input_file", fileId: userPng.fileId, mediaType: "image/png" }],
            },
            csvItem,
          ]),
        };
      }),
    };

    const result = await restoreConversationReplay({
      aapBaseUrl: "https://aap.example/v1",
      workspaceId: "ws",
      agentId: "a1",
      conversationId: "c1",
      client,
    });
    expect(followOpts).toEqual([expect.objectContaining({ initialLastSequence: 0 })]);
    expect(result.messages).toHaveLength(2);
    expect(result.messages[0]?.files.map((f) => f.fileId)).toEqual([userPng.fileId]);
    expect(result.messages[1]?.files.map((f) => f.fileId)).toEqual([csv.fileId]);
  });

  it("keeps getRun items when followRun catch-up fails", async () => {
    const partial: ProtocolItem[] = [
      {
        id: "u1",
        type: "message",
        status: "completed",
        role: "user",
        content: [{ type: "text", text: "hi" }],
      },
    ];
    const client: ConversationReplayClient = {
      getConversation: vi.fn(async () =>
        conversation([{ object: "run", id: "r1", status: "completed", version: 1, startedAt: "2026-08-01T00:00:00Z" }]),
      ),
      getRun: vi.fn(async () => apiRun("r1", partial)),
      followRun: vi.fn(async function* () {
        throw new Error("event:read denied");
      }),
    };

    // items have content → no catch-up
    const complete = await restoreConversationReplay({
      aapBaseUrl: "https://aap.example/v1",
      workspaceId: "ws",
      agentId: "a1",
      conversationId: "c1",
      client,
    });
    expect(complete.messages.map((m) => m.text)).toEqual(["hi"]);

    const incompleteClient: ConversationReplayClient = {
      ...client,
      getRun: vi.fn(async () => apiRun("r1", [])),
    };
    const fallback = await restoreConversationReplay({
      aapBaseUrl: "https://aap.example/v1",
      workspaceId: "ws",
      agentId: "a1",
      conversationId: "c1",
      client: incompleteClient,
    });
    expect(fallback.messages).toEqual([]);
  });

  it("propagates getConversation failures so boot can empty the session", async () => {
    const err = Object.assign(new Error("gone"), { status: 404 });
    const client: ConversationReplayClient = {
      getConversation: vi.fn(async () => {
        throw err;
      }),
      getRun: vi.fn(),
      followRun: vi.fn(async function* () {}),
    };
    await expect(
      restoreConversationReplay({
        aapBaseUrl: "https://aap.example/v1",
        workspaceId: "ws",
        agentId: "a1",
        conversationId: "c1",
        client,
      }),
    ).rejects.toBe(err);
    expect(client.getRun).not.toHaveBeenCalled();
  });
});

function apiRun(id: string, items: ProtocolItem[]) {
  return {
    object: "run" as const,
    id,
    conversationId: "c1",
    agentId: "a1",
    status: "completed" as const,
    version: 1,
    startedAt: "2026-08-01T00:00:00Z",
    items,
    links: { events: "/events" },
  };
}

function emptySnapshot(items: ProtocolItem[]): ReducedRunSnapshot {
  return {
    run: {
      id: "r1",
      conversationId: "c1",
      agentId: "a1",
      status: "completed",
      trigger: "message",
      startedAt: "2026-08-01T00:00:00Z",
    },
    items,
    interactions: [],
    usage: null,
    lastSequence: 3,
  };
}
