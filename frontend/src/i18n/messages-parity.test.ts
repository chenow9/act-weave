import { describe, expect, it } from "vitest";

import { messages } from "./messages";

function leafPaths(value: unknown, prefix = ""): string[] {
  if (value !== null && typeof value === "object" && !Array.isArray(value)) {
    return Object.entries(value as Record<string, unknown>).flatMap(([key, child]) =>
      leafPaths(child, prefix ? `${prefix}.${key}` : key),
    );
  }
  return prefix ? [prefix] : [];
}

describe("i18n message parity", () => {
  it("has identical leaf keys for en and zh-CN", () => {
    const zhKeys = leafPaths(messages["zh-CN"]).sort();
    const enKeys = leafPaths(messages.en).sort();
    expect(enKeys).toEqual(zhKeys);
  });

  it("ships frozen glossary nav labels in English", () => {
    expect(messages.en.nav.item.chat).toBe("Run Console");
    expect(messages.en.common.appTitle).toBe("ActWeave");
    expect(messages.en.nav.section.space).toBe("Space");
    expect(messages["zh-CN"].common.appTitle).toContain("织行");
  });

  it("ships generate-dock English strings", () => {
    expect(messages.en.workflow.generateFromIntent).toBe("Generate from a sentence");
    expect(messages.en.workflow.generateDockTitle).toBe("Generate");
    expect(messages.en.workflow.generateSubmit).toBe("Generate draft");
    expect(messages.en.workflow.reviseDraftTitle).toMatch(/revise on this page/i);
    expect(messages["zh-CN"].workflow.reviseDraftTitle).toContain("在本页修订");
  });
});
