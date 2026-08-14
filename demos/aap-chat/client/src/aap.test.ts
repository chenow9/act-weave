import { describe, expect, it, vi } from "vitest";
import type { OutputFileContentPart, ProtocolItem } from "@actweave/agent-client";

import {
  extractOutputFileParts,
  placeholderAttachment,
  reconcileAssistantAttachments,
  type AttachmentCard,
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
