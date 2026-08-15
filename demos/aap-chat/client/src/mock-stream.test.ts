import { describe, expect, it } from "vitest";

import { DEMO_STORIES } from "./demo-stories";
import { mockAssistantStream, mockAttachmentsForStory } from "./mock-stream";

describe("mock outbound attachments", () => {
  it("materializes the export-csv story as a csv card", () => {
    const story = DEMO_STORIES.find((entry) => entry.id === "export-csv");
    const cards = mockAttachmentsForStory(story);
    expect(cards).toHaveLength(1);
    expect(cards[0]).toMatchObject({
      name: "invoice-2026-08.csv",
      mediaType: "text/csv",
      status: "ready",
    });
    expect(cards[0]?.sizeBytes).toBeGreaterThan(0);
  });

  it("yields assistant_done.attachments for 生成本月对账单", async () => {
    let done:
      | { kind: "assistant_done"; attachments?: Array<{ mediaType: string; name: string }> }
      | undefined;
    for await (const chunk of mockAssistantStream("生成本月对账单")) {
      if (chunk.kind === "assistant_done") done = chunk;
    }
    expect(done?.attachments?.map((a) => a.mediaType)).toEqual(["text/csv"]);
    expect(done?.attachments?.[0]?.name).toBe("invoice-2026-08.csv");
  });

  it("yields four image attachments for 看看这几张现场图", async () => {
    let done:
      | { kind: "assistant_done"; attachments?: Array<{ mediaType: string; name: string }> }
      | undefined;
    for await (const chunk of mockAssistantStream("看看这几张现场图")) {
      if (chunk.kind === "assistant_done") done = chunk;
    }
    expect(done?.attachments).toHaveLength(4);
    expect(done?.attachments?.every((a) => a.mediaType === "image/png")).toBe(true);
    expect(done?.attachments?.map((a) => a.name)).toEqual([
      "storefront.png",
      "aisle.png",
      "counter.png",
      "parking.png",
    ]);
  });

  it("yields markdown, mixed files, images, and a2ui for 出一份巡检复盘包", async () => {
    let done:
      | {
          kind: "assistant_done";
          a2ui?: { surface?: unknown };
          attachments?: Array<{ mediaType: string; name: string }>;
        }
      | undefined;
    let text = "";
    for await (const chunk of mockAssistantStream("出一份巡检复盘包")) {
      if (chunk.kind === "assistant_delta") text = chunk.text;
      if (chunk.kind === "assistant_done") done = chunk;
    }
    expect(text).toContain("## 星云便利 · 湖滨店巡检复盘");
    expect(done?.a2ui?.surface).toBeTruthy();
    expect(done?.attachments?.map((a) => a.name)).toEqual([
      "aisle.png",
      "counter.png",
      "sku-gaps.csv",
      "inspection-2026-08-15.json",
    ]);
    expect(done?.attachments?.map((a) => a.mediaType)).toEqual([
      "image/png",
      "image/png",
      "text/csv",
      "application/json",
    ]);
  });
});
