import { describe, expect, it } from "vitest";

import { resolveToolCapabilityBadge } from "./model-tool-disclosure";

describe("resolveToolCapabilityBadge", () => {
  it("maps toolDisclosureUI tokens and ignores modelName", () => {
    expect(resolveToolCapabilityBadge({ toolDisclosureUI: "hidden" })).toBe("native");
    expect(resolveToolCapabilityBadge({ toolDisclosureUI: "binary" })).toBe("function_calling");
    expect(resolveToolCapabilityBadge({ toolDisclosureUI: "unavailable" })).toBe("none");
    expect(resolveToolCapabilityBadge({ toolDisclosureUI: "unverified" })).toBe("unverified");
    expect(
      resolveToolCapabilityBadge({
        toolDisclosureUI: "unverified",
        agenticCapabilities: { toolCalling: "native_client_search" },
      }),
    ).toBe("unverified");
  });

  it("fails closed when UI and caps are empty or unknown", () => {
    expect(resolveToolCapabilityBadge({})).toBe("unverified");
    expect(resolveToolCapabilityBadge({ toolDisclosureUI: undefined, agenticCapabilities: {} })).toBe("unverified");
    expect(resolveToolCapabilityBadge({ toolDisclosureUI: "carry_all" as never })).toBe("unverified");
    expect(resolveToolCapabilityBadge({ agenticCapabilities: { toolSearchModes: [] } })).toBe("unverified");
    expect(
      resolveToolCapabilityBadge({ status: "ERROR", agenticCapabilities: { toolCalling: "function_calling" } }),
    ).toBe("unverified");
  });

  it("falls back to caps only when UI is absent", () => {
    expect(
      resolveToolCapabilityBadge({
        agenticCapabilities: { schemaVersion: "agentic-model.v1", toolSearchModes: ["client"] },
      }),
    ).toBe("native");
    expect(resolveToolCapabilityBadge({ agenticCapabilities: { toolCalling: "function_calling" } })).toBe(
      "function_calling",
    );
    expect(resolveToolCapabilityBadge({ agenticCapabilities: { toolCalling: "none" } })).toBe("none");
    expect(resolveToolCapabilityBadge({ agenticCapabilities: { toolCalling: "native_client_search" } })).toBe("native");
  });
});
