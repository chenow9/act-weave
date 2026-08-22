import { describe, expect, it } from "vitest";

import type { SessionContextPolicy } from "../types/domain";
import {
  aapFlagBag,
  anyAapFlagTrue,
  buildContextPolicyPayload,
  mergeAapFlags,
  needsSessionContextV2,
  normalizeContextPolicy,
  readAapFlags,
} from "./session-context-config";

/** Flag matrix: includeCompactionSummary × enableA2UI → 00 / 01 / 10 / 11. */
const FLAG_MATRIX: Array<{
  label: string;
  includeCompactionSummary: boolean;
  enableA2UI: boolean;
}> = [
  { label: "00", includeCompactionSummary: false, enableA2UI: false },
  { label: "01", includeCompactionSummary: false, enableA2UI: true },
  { label: "10", includeCompactionSummary: true, enableA2UI: false },
  { label: "11", includeCompactionSummary: true, enableA2UI: true },
];

function policyWithFlags(
  includeCompactionSummary: boolean,
  enableA2UI: boolean,
  extra: Partial<SessionContextPolicy> = {},
): SessionContextPolicy {
  return {
    schemaVersion: "session-context-policy.v2",
    mode: "token_window",
    maxInputTokens: 8000,
    aap: { includeCompactionSummary, enableA2UI },
    ...extra,
  };
}

describe("aap flag bag helpers", () => {
  it("readAapFlags defaults missing keys to false", () => {
    expect(readAapFlags(undefined)).toEqual({
      includeCompactionSummary: false,
      enableA2UI: false,
      enableOutboundAttachments: false,
      enableInboundRead: false,
    });
    expect(readAapFlags({})).toEqual({
      includeCompactionSummary: false,
      enableA2UI: false,
      enableOutboundAttachments: false,
      enableInboundRead: false,
    });
    expect(readAapFlags({ includeCompactionSummary: true })).toMatchObject({
      includeCompactionSummary: true,
      enableA2UI: false,
      enableInboundRead: false,
    });
    expect(readAapFlags({ enableA2UI: true })).toEqual({
      includeCompactionSummary: false,
      enableA2UI: true,
      enableOutboundAttachments: false,
      enableInboundRead: false,
    });
  });

  it("mergeAapFlags does not clobber the sibling flag", () => {
    const withInclude = mergeAapFlags({ includeCompactionSummary: true, enableA2UI: false }, { enableA2UI: true });
    expect(withInclude).toMatchObject({
      includeCompactionSummary: true,
      enableA2UI: true,
    });

    const withA2UI = mergeAapFlags(
      { includeCompactionSummary: false, enableA2UI: true },
      { includeCompactionSummary: true },
    );
    expect(withA2UI).toMatchObject({
      includeCompactionSummary: true,
      enableA2UI: true,
    });

    const turnOffIncludeKeepsA2UI = mergeAapFlags(
      { includeCompactionSummary: true, enableA2UI: true },
      { includeCompactionSummary: false },
    );
    expect(turnOffIncludeKeepsA2UI).toMatchObject({
      includeCompactionSummary: false,
      enableA2UI: true,
    });

    const turnOffA2UIKeepsInclude = mergeAapFlags(
      { includeCompactionSummary: true, enableA2UI: true },
      { enableA2UI: false },
    );
    expect(turnOffA2UIKeepsInclude).toMatchObject({
      includeCompactionSummary: true,
      enableA2UI: false,
    });
  });

  it("preserves enableInboundRead across A2UI toggles", () => {
    let aap = aapFlagBag({
      includeCompactionSummary: false,
      enableA2UI: false,
      enableInboundRead: true,
    });
    expect(aap.enableInboundRead).toBe(true);
    aap = aapFlagBag(mergeAapFlags(aap, { enableA2UI: true }));
    expect(aap.enableInboundRead).toBe(true);
    expect(aap.enableA2UI).toBe(true);
  });

  it("anyAapFlagTrue / needsSessionContextV2 never allow v1+aap", () => {
    expect(anyAapFlagTrue({ includeCompactionSummary: false, enableA2UI: false })).toBe(false);
    expect(anyAapFlagTrue({ includeCompactionSummary: false, enableA2UI: true })).toBe(true);
    expect(
      needsSessionContextV2({
        schemaVersion: "session-context-policy.v1",
        aap: { enableA2UI: true },
      }),
    ).toBe(true);
    expect(
      needsSessionContextV2({
        schemaVersion: "session-context-policy.v1",
        aap: {},
      }),
    ).toBe(true);
  });
});

describe("buildContextPolicyPayload aap flag matrix", () => {
  it.each(FLAG_MATRIX)(
    "matrix $label: include=$includeCompactionSummary enableA2UI=$enableA2UI",
    ({ includeCompactionSummary, enableA2UI }) => {
      const built = buildContextPolicyPayload(
        policyWithFlags(includeCompactionSummary, enableA2UI),
      ) as SessionContextPolicy;

      if (!includeCompactionSummary && !enableA2UI) {
        // Both false with explicit v2+aap → still v2, both flags explicit false
        expect(built.schemaVersion).toBe("session-context-policy.v2");
        expect(built.aap).toEqual({
          includeCompactionSummary: false,
          enableA2UI: false,
        });
      } else {
        expect(built.schemaVersion).toBe("session-context-policy.v2");
        expect(built.aap).toEqual({
          includeCompactionSummary,
          enableA2UI,
        });
      }
      // Never v1+aap
      if (built.aap) {
        expect(built.schemaVersion).toBe("session-context-policy.v2");
      }
    },
  );

  it("omits aap for plain v1 token_window with no flags (00 without aap object)", () => {
    const built = buildContextPolicyPayload({
      schemaVersion: "session-context-policy.v1",
      mode: "token_window",
      maxInputTokens: 4096,
    }) as SessionContextPolicy;
    expect(built.schemaVersion).toBe("session-context-policy.v1");
    expect(built.aap).toBeUndefined();
    expect(built.mode).toBe("token_window");
  });

  it("enableA2UI alone forces v2 even when mode is inherit/empty", () => {
    const built = buildContextPolicyPayload({
      aap: { enableA2UI: true },
    }) as SessionContextPolicy;
    expect(built).toEqual({
      schemaVersion: "session-context-policy.v2",
      aap: { includeCompactionSummary: false, enableA2UI: true },
    });
  });

  it("includeCompactionSummary alone forces v2 on disabled mode", () => {
    const built = buildContextPolicyPayload({
      mode: "disabled",
      aap: { includeCompactionSummary: true },
    }) as SessionContextPolicy;
    expect(built.schemaVersion).toBe("session-context-policy.v2");
    expect(built.mode).toBe("disabled");
    expect(built.aap).toEqual({
      includeCompactionSummary: true,
      enableA2UI: false,
    });
  });

  it("never emits v1 with aap keys present", () => {
    for (const row of FLAG_MATRIX) {
      if (!row.includeCompactionSummary && !row.enableA2UI) continue;
      const built = buildContextPolicyPayload(
        policyWithFlags(row.includeCompactionSummary, row.enableA2UI, {
          schemaVersion: "session-context-policy.v1",
        }),
      ) as SessionContextPolicy;
      expect(built.schemaVersion).toBe("session-context-policy.v2");
      expect(built.aap).toBeDefined();
    }
  });
});

describe("normalizeContextPolicy aap flag matrix", () => {
  it.each(FLAG_MATRIX)("matrix $label round-trip via normalize", ({ includeCompactionSummary, enableA2UI }) => {
    const normalized = normalizeContextPolicy(policyWithFlags(includeCompactionSummary, enableA2UI));
    expect(normalized.schemaVersion).toBe("session-context-policy.v2");
    expect(normalized.aap).toEqual({
      includeCompactionSummary,
      enableA2UI,
    });
  });

  it("defaults both flags false when v2 has empty aap", () => {
    const normalized = normalizeContextPolicy({
      schemaVersion: "session-context-policy.v2",
      mode: "token_window",
      aap: {},
    });
    expect(normalized.aap).toEqual({
      includeCompactionSummary: false,
      enableA2UI: false,
    });
  });

  it("legacy single-flag aap still normalizes enableA2UI=false", () => {
    const normalized = normalizeContextPolicy({
      schemaVersion: "session-context-policy.v2",
      mode: "rolling_summary",
      aap: { includeCompactionSummary: true },
    });
    expect(normalized.aap).toEqual({
      includeCompactionSummary: true,
      enableA2UI: false,
    });
  });
});

describe("build + normalize matrix 00/01/10/11", () => {
  it.each(FLAG_MATRIX)(
    "build(normalize(input)) preserves flags for $label",
    ({ includeCompactionSummary, enableA2UI }) => {
      const input = policyWithFlags(includeCompactionSummary, enableA2UI, {
        mode: "rolling_summary",
        maxRecentTurns: 20,
      });
      const normalized = normalizeContextPolicy(input);
      const built = buildContextPolicyPayload(normalized) as SessionContextPolicy;
      expect(built.schemaVersion).toBe("session-context-policy.v2");
      expect(built.aap).toEqual({
        includeCompactionSummary,
        enableA2UI,
      });
      // sibling keys both present on emitted aap bag
      expect(Object.keys(built.aap || {}).sort()).toEqual(["enableA2UI", "includeCompactionSummary"]);
    },
  );
});

describe("mergeAapFlags setter simulation (does not clobber sibling)", () => {
  it("toggling enableA2UI keeps includeCompactionSummary", () => {
    let aap = aapFlagBag({ includeCompactionSummary: true, enableA2UI: false });
    aap = aapFlagBag(mergeAapFlags(aap, { enableA2UI: true }));
    expect(aap).toEqual({ includeCompactionSummary: true, enableA2UI: true });
    aap = aapFlagBag(mergeAapFlags(aap, { enableA2UI: false }));
    expect(aap).toEqual({ includeCompactionSummary: true, enableA2UI: false });
  });

  it("toggling includeCompactionSummary keeps enableA2UI", () => {
    let aap = aapFlagBag({ includeCompactionSummary: false, enableA2UI: true });
    aap = aapFlagBag(mergeAapFlags(aap, { includeCompactionSummary: true }));
    expect(aap).toEqual({ includeCompactionSummary: true, enableA2UI: true });
    aap = aapFlagBag(mergeAapFlags(aap, { includeCompactionSummary: false }));
    expect(aap).toEqual({ includeCompactionSummary: false, enableA2UI: true });
  });

  it("mode/token field rebuild via buildContextPolicyPayload preserves both flags", () => {
    const current: SessionContextPolicy = {
      schemaVersion: "session-context-policy.v2",
      mode: "token_window",
      maxInputTokens: 1000,
      aap: { includeCompactionSummary: true, enableA2UI: true },
    };
    // Simulate setAgentContextMaxInput-style update then build for API
    const draft = {
      ...current,
      maxInputTokens: 2048,
    };
    const payload = buildContextPolicyPayload(draft) as SessionContextPolicy;
    expect(payload.maxInputTokens).toBe(2048);
    expect(payload.aap).toEqual({
      includeCompactionSummary: true,
      enableA2UI: true,
    });
    expect(payload.schemaVersion).toBe("session-context-policy.v2");
  });
});
