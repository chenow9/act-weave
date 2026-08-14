import { describe, expect, it } from "vitest";

import { DEMO_STORIES } from "./demo-stories";
import { mockAssistantStream, mockAttachmentsForStory } from "./mock-stream";

describe("mock outbound attachments", () => {
  it("materializes the export-csv story as csv + png cards", () => {
    const story = DEMO_STORIES.find((entry) => entry.id === "export-csv");
    const cards = mockAttachmentsForStory(story);
    expect(cards).toHaveLength(2);
    expect(cards[0]).toMatchObject({
      name: "invoice-2026-08.csv",
      mediaType: "text/csv",
      status: "ready",
    });
    expect(cards[0]?.sizeBytes).toBeGreaterThan(0);
    expect(cards[1]).toMatchObject({
      name: "invoice-preview.png",
      mediaType: "image/png",
      status: "ready",
    });
    expect(cards[1]?.sizeBytes).toBeGreaterThan(0);
  });

  it("yields assistant_done.attachments for 生成本月对账单", async () => {
    let done:
      | { kind: "assistant_done"; attachments?: Array<{ mediaType: string; name: string }> }
      | undefined;
    for await (const chunk of mockAssistantStream("生成本月对账单")) {
      if (chunk.kind === "assistant_done") done = chunk;
    }
    expect(done?.attachments?.map((a) => a.mediaType)).toEqual(["text/csv", "image/png"]);
    expect(done?.attachments?.[0]?.name).toBe("invoice-2026-08.csv");
  });
});
