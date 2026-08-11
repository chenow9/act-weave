import { describe, expect, it } from "vitest";

import {
  findA2UIPart,
  isA2UIContentPart,
  isInputFileContentPart,
  isTextContentPart,
  joinTextParts,
  type A2UIContentPart,
  type ProtocolItem,
  type TextContentPart,
} from "../src/index.js";

const surface = {
  root: "form",
  components: {
    form: { type: "Column", children: ["title"] },
    title: { type: "Text", text: "Hello" },
  },
};

const a2uiPart: A2UIContentPart = {
  type: "a2ui",
  version: "a2ui-surface.v0",
  catalogId: "standard",
  surface,
};

const textPart: TextContentPart = {
  type: "text",
  text: "Please confirm:",
};

describe("content part guards", () => {
  it("narrows text / a2ui / input_file parts", () => {
    expect(isTextContentPart(textPart)).toBe(true);
    expect(isTextContentPart({ type: "text", text: "" })).toBe(true);
    expect(isTextContentPart({ type: "text" })).toBe(false);
    expect(isTextContentPart(a2uiPart)).toBe(false);

    expect(isA2UIContentPart(a2uiPart)).toBe(true);
    expect(isA2UIContentPart({ type: "a2ui", surface: "nope" })).toBe(false);
    expect(isA2UIContentPart({ type: "a2ui" })).toBe(false);

    expect(isInputFileContentPart({ type: "input_file", fileId: "f1" })).toBe(true);
    expect(isInputFileContentPart({ type: "input_file" })).toBe(false);
  });
});

describe("joinTextParts", () => {
  it("joins only type=text parts from a content array", () => {
    const content = [
      { type: "text", text: "Hello, " },
      a2uiPart,
      { type: "text", text: "world" },
      { type: "input_file", fileId: "00000000-0000-4000-8000-000000000001" },
      { type: "future_part", payload: 1 },
    ];
    expect(joinTextParts(content)).toBe("Hello, world");
  });

  it("allows empty text when a2ui is present", () => {
    expect(joinTextParts([{ type: "text", text: "" }, a2uiPart])).toBe("");
  });

  it("accepts a ProtocolItem and ignores non-message content shapes", () => {
    const item: ProtocolItem = {
      id: "i1",
      type: "message",
      status: "completed",
      role: "assistant",
      content: [textPart, a2uiPart],
    };
    expect(joinTextParts(item)).toBe("Please confirm:");
    expect(joinTextParts({ id: "x", type: "tool_call", status: "completed" })).toBe("");
    expect(joinTextParts(null)).toBe("");
    expect(joinTextParts("plain")).toBe("");
  });
});

describe("findA2UIPart", () => {
  it("returns the first a2ui part from content or item", () => {
    const content = [textPart, a2uiPart, { type: "a2ui", surface: { other: true } }];
    const found = findA2UIPart(content);
    expect(found).toBeDefined();
    expect(found?.catalogId).toBe("standard");
    expect(found?.surface).toEqual(surface);

    const item: ProtocolItem = {
      id: "i2",
      type: "message",
      status: "completed",
      content,
    };
    expect(findA2UIPart(item)?.version).toBe("a2ui-surface.v0");
  });

  it("returns undefined when no a2ui part is present", () => {
    expect(findA2UIPart([{ type: "text", text: "only text" }])).toBeUndefined();
    expect(findA2UIPart({ type: "a2ui", surface: null })).toBeUndefined();
    expect(findA2UIPart(undefined)).toBeUndefined();
  });
});
